package query

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/phankieuphu/lumen/internal/audit"
	"github.com/phankieuphu/lumen/internal/conn"
	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// Transaction errors.
var (
	ErrTxNotFound = errors.New("query: no open transaction with this ID")
	ErrTxBusy     = errors.New("query: the transaction is running another statement")
	ErrTxAborted  = errors.New("query: the transaction was aborted by an earlier error and has been rolled back")
	ErrTxOpen     = errors.New("query: a transaction is already open on this connection; commit or roll it back first")
	// ErrTxControl is returned for BEGIN, COMMIT, or ROLLBACK outside a
	// transaction: each request runs on a pooled session, so a transaction
	// typed there would end up on a session nobody owns.
	ErrTxControl = errors.New("query: start transactions with Begin; COMMIT and ROLLBACK run inside one")
)

// MaxTxLabel caps a transaction label, in runes.
const MaxTxLabel = 80

// endTimeout bounds COMMIT and ROLLBACK, which must run even when the
// caller's context is gone.
const endTimeout = 30 * time.Second

// TxEnd is how a transaction finished.
type TxEnd string

// Transaction outcomes.
const (
	TxCommit   TxEnd = "commit"
	TxRollback TxEnd = "rollback"
)

// TxInfo describes an open transaction.
type TxInfo struct {
	ID        string
	ConnID    string
	Label     string
	StartedAt time.Time
	// Statements counts statements run in the transaction, failed included.
	Statements int64
	// Writes counts write and schema statements that succeeded.
	Writes int64
}

// tx is an open transaction: one session pinned from the pool until COMMIT
// or ROLLBACK. mu is held while a statement or the end runs on the session,
// which is not safe for concurrent use.
type tx struct {
	id      string
	connID  string
	label   string
	prof    conn.Profile
	started time.Time
	session driver.Session

	mu         sync.Mutex
	done       bool // guarded by mu
	statements atomic.Int64
	writes     atomic.Int64
}

func (t *tx) info() TxInfo {
	return TxInfo{
		ID: t.id, ConnID: t.connID, Label: t.label, StartedAt: t.started,
		Statements: t.statements.Load(), Writes: t.writes.Load(),
	}
}

// txOp is a transaction-control statement.
type txOp int

const (
	txNone txOp = iota
	txBegin
	txCommit
	txRollback
)

// txControl reports whether sql is a single statement that starts or ends
// a transaction. Savepoints and two-phase COMMIT/ROLLBACK PREPARED are not:
// they run as ordinary session statements.
func txControl(sql string, d driver.Dialect) txOp {
	stmts := sqlscan.Split(sql, sqlscan.Dialect(d))
	if len(stmts) != 1 {
		return txNone
	}
	toks := stmts[0].Tokens
	word := func(i int) string {
		if i < len(toks) && toks[i].Kind == sqlscan.Word {
			return toks[i].Upper()
		}
		return ""
	}
	switch word(0) {
	case "BEGIN":
		return txBegin
	case "START":
		if word(1) == "TRANSACTION" {
			return txBegin
		}
	case "COMMIT", "END":
		if word(1) != "PREPARED" {
			return txCommit
		}
	case "ROLLBACK", "ABORT":
		if word(1) == "PREPARED" || slices.ContainsFunc(toks, func(t sqlscan.Token) bool { return t.Is("TO") }) {
			return txNone // ROLLBACK [WORK] TO SAVEPOINT x
		}
		return txRollback
	}
	return txNone
}

func beginSQL(d driver.Dialect) string {
	if d == driver.MySQL {
		return "START TRANSACTION"
	}
	return "BEGIN"
}

// cleanLabel makes a label one short line.
func cleanLabel(s string) string {
	s = strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
	if r := []rune(s); len(r) > MaxTxLabel {
		s = string(r[:MaxTxLabel])
	}
	return s
}

func newTxID() string {
	var b [6]byte
	_, _ = rand.Read(b[:]) // never fails; see crypto/rand
	return "tx-" + hex.EncodeToString(b[:])
}

// Begin opens a transaction on connID: it pins a session and starts a
// transaction on it. Requests with TxID set then run on that session until
// Commit or Rollback. A connection has at most one open transaction.
func (r *Runner) Begin(ctx context.Context, connID, label string) (TxInfo, error) {
	if connID == "" {
		return TxInfo{}, fmt.Errorf("%w: ConnID is required", ErrBadRequest)
	}
	if r.openOn(connID) != nil {
		return TxInfo{}, ErrTxOpen
	}
	c, prof, err := r.conns.Get(ctx, connID)
	if err != nil {
		return TxInfo{}, err
	}
	t := &tx{id: newTxID(), connID: connID, label: cleanLabel(label), prof: prof, started: time.Now()}
	sum := Summary{RequestID: t.id, Kind: KindSession, RowsAffected: -1, TxID: t.id, TxLabel: t.label}
	stmt := beginSQL(prof.Dialect)

	err = func() error {
		s, err := c.Session(ctx)
		if err != nil {
			return err
		}
		if _, err := s.Exec(ctx, stmt); err != nil {
			_ = s.Close()
			return err
		}
		t.session = s
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, o := range r.txs {
			if o.connID == connID {
				return ErrTxOpen
			}
		}
		r.txs[t.id] = t
		return nil
	}()
	if errors.Is(err, ErrTxOpen) { // lost a race with another Begin
		r.discard(ctx, t.session)
		return TxInfo{}, err
	}
	if err != nil {
		sum.Status, sum.Error = audit.StatusError, err.Error()
	} else {
		sum.Status = audit.StatusOK
	}
	err = errors.Join(err, r.finish(ctx, Request{ID: t.id, SQL: stmt}, prof, &sum, t.started, nil))
	if sum.Status != audit.StatusOK {
		return TxInfo{}, err
	}
	return t.info(), err
}

// Commit commits an open transaction and returns its session to the pool.
// If COMMIT fails the transaction is rolled back and the error returned.
func (r *Runner) Commit(ctx context.Context, txID string) (Summary, error) {
	return r.endByID(ctx, txID, TxCommit)
}

// Rollback rolls back an open transaction and returns its session to the
// pool.
func (r *Runner) Rollback(ctx context.Context, txID string) (Summary, error) {
	return r.endByID(ctx, txID, TxRollback)
}

func (r *Runner) endByID(ctx context.Context, txID string, how TxEnd) (Summary, error) {
	t, err := r.lockTx(txID, "")
	if err != nil {
		return Summary{RequestID: txID}, err
	}
	defer t.mu.Unlock()
	return r.endLocked(ctx, t, how, txID)
}

// Transactions returns the open transactions, oldest first.
func (r *Runner) Transactions() []TxInfo {
	r.mu.Lock()
	out := make([]TxInfo, 0, len(r.txs))
	for _, t := range r.txs {
		out = append(out, t.info())
	}
	r.mu.Unlock()
	slices.SortFunc(out, func(a, b TxInfo) int { return a.StartedAt.Compare(b.StartedAt) })
	return out
}

// RollbackConn cancels running requests on connID's open transaction and
// rolls it back. Call it before a connection's pool is closed: a pinned
// session would otherwise keep the pool from closing.
func (r *Runner) RollbackConn(ctx context.Context, connID string) error {
	t := r.openOn(connID)
	if t == nil {
		return nil
	}
	return r.forceRollback(ctx, t)
}

// Close rolls back every open transaction. Call it on shutdown, before the
// connections close.
func (r *Runner) Close(ctx context.Context) error {
	r.mu.Lock()
	open := make([]*tx, 0, len(r.txs))
	for _, t := range r.txs {
		open = append(open, t)
	}
	r.mu.Unlock()
	errs := make([]error, 0, len(open))
	for _, t := range open {
		errs = append(errs, r.forceRollback(ctx, t))
	}
	return errors.Join(errs...)
}

// forceRollback cancels whatever runs on t, waits for it, and rolls back.
func (r *Runner) forceRollback(ctx context.Context, t *tx) error {
	r.mu.Lock()
	var ids []string
	for id, run := range r.active {
		if run.txID == t.id {
			ids = append(ids, id)
		}
	}
	r.mu.Unlock()
	for _, id := range ids {
		_ = r.Cancel(ctx, id) // it may have finished meanwhile
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return nil
	}
	_, err := r.endLocked(ctx, t, TxRollback, t.id)
	return err
}

func (r *Runner) openOn(connID string) *tx {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.txs {
		if t.connID == connID {
			return t
		}
	}
	return nil
}

// lockTx finds an open transaction and takes its lock without waiting. A
// non-empty connID must match the transaction's connection.
func (r *Runner) lockTx(txID, connID string) (*tx, error) {
	r.mu.Lock()
	t, ok := r.txs[txID]
	r.mu.Unlock()
	if !ok || (connID != "" && t.connID != connID) {
		return nil, ErrTxNotFound
	}
	if !t.mu.TryLock() {
		return nil, ErrTxBusy
	}
	if t.done {
		t.mu.Unlock()
		return nil, ErrTxNotFound
	}
	return t, nil
}

// endLocked commits or rolls back t, whose lock the caller holds, and
// records the outcome in history. A transaction that wrote anything also
// gets an audit entry, so the log shows whether its writes were kept.
func (r *Runner) endLocked(ctx context.Context, t *tx, how TxEnd, requestID string) (Summary, error) {
	start := time.Now()
	stmt := "ROLLBACK"
	if how == TxCommit {
		stmt = "COMMIT"
	}
	ectx, cancel := context.WithTimeout(context.WithoutCancel(ctx), endTimeout)
	defer cancel()
	var err error
	if how == TxCommit && t.prof.Dialect == driver.Postgres {
		// After an error PostgreSQL answers COMMIT with a silent ROLLBACK.
		// Any statement fails in that state, so probe before claiming a commit.
		if _, perr := t.session.Exec(ectx, "SELECT 1"); perr != nil {
			err = ErrTxAborted
		}
	}
	if err == nil {
		_, err = t.session.Exec(ectx, stmt)
	}
	if err != nil && how == TxCommit {
		_, _ = t.session.Exec(ectx, "ROLLBACK")
	}
	_ = t.session.Close()
	t.done = true
	r.mu.Lock()
	delete(r.txs, t.id)
	r.mu.Unlock()

	sum := Summary{
		RequestID: requestID, Kind: KindSession, Status: audit.StatusOK, RowsAffected: -1,
		TxID: t.id, TxLabel: t.label, TxEnd: how,
	}
	if err != nil {
		sum.Status, sum.Error = audit.StatusError, err.Error()
		if how == TxCommit {
			sum.TxEnd = TxRollback
		}
	}
	req := Request{ID: requestID, ConnID: t.connID, SQL: stmt}
	ferr := r.finish(ctx, req, t.prof, &sum, start, err)
	if t.writes.Load() > 0 {
		ferr = errors.Join(ferr, r.auditEnd(ectx, t, &sum))
	}
	return sum, ferr
}

func (r *Runner) auditEnd(ctx context.Context, t *tx, sum *Summary) error {
	stmt := "ROLLBACK"
	if sum.TxEnd == TxCommit {
		stmt = "COMMIT"
	}
	_, err := r.audit.Append(ctx, audit.Entry{
		ConnID: t.prof.ID, ConnName: t.prof.Name, Dialect: string(t.prof.Dialect), Env: string(t.prof.Env),
		Kind: string(KindSession), Status: sum.Status, Duration: sum.Duration, SQL: stmt, Error: sum.Error,
		TxID: t.id, TxLabel: t.label,
	})
	if err != nil {
		r.log.Error("audit append failed", "tx", t.id, "conn", t.prof.ID, "err", err)
		return fmt.Errorf("%w: %w", ErrAudit, err)
	}
	return nil
}

// discard rolls back and releases a session that never became a tracked
// transaction.
func (r *Runner) discard(ctx context.Context, s driver.Session) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), endTimeout)
	defer cancel()
	_, _ = s.Exec(ctx, "ROLLBACK")
	_ = s.Close()
}
