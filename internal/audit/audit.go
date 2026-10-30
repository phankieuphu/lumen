// Package audit keeps an append-only, hash-chained log of every statement
// that is not a plain read. Each entry's hash covers the previous entry's
// hash and all of its own fields, so editing or removing an entry in the
// middle of the log breaks the chain and Verify reports where.
//
// The chain cannot detect removal of the newest entries on its own; Head
// returns the latest hash so it can be recorded elsewhere as an anchor.
package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/patrick-rontech/kiemdb/internal/sqlscan"
	"github.com/patrick-rontech/kiemdb/internal/store"
)

// Status of an audited statement.
type Status string

// Statuses.
const (
	StatusOK        Status = "ok"
	StatusError     Status = "error"
	StatusBlocked   Status = "blocked" // stopped by policy before reaching the server
	StatusCancelled Status = "cancelled"
)

// MaxSQLBytes caps the SQL text stored per entry; longer text is truncated
// with a marker so a bulk load cannot bloat the log.
const MaxSQLBytes = 64 << 10

// genesis is the previous-hash of the first entry.
var genesis = make([]byte, sha256.Size)

// Entry is one audit record. Export defines the serialized forms.
type Entry struct {
	Seq          int64
	At           time.Time
	ConnID       string
	ConnName     string
	Dialect      string
	Env          string
	Kind         string
	Status       Status
	RowsAffected int64
	Duration     time.Duration
	SQL          string
	Error        string
	PrevHash     []byte
	Hash         []byte
}

// Log is the audit log. It is safe for concurrent use.
type Log struct {
	db *sql.DB
	// mu serializes appends in this process; the IMMEDIATE transaction in
	// Append serializes them across processes.
	mu sync.Mutex
}

// New returns a Log over the store's database.
func New(s *store.Store) *Log { return &Log{db: s.DB()} }

// Append records e and returns it with Seq, At, and hashes set. SQL is
// redacted of password literals and truncated to MaxSQLBytes first.
func (l *Log) Append(ctx context.Context, e Entry) (Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	e.SQL = truncate(sqlscan.RedactSecrets(e.SQL, sqlscan.Dialect(e.Dialect)))
	e.At = time.Now().UTC().Truncate(time.Millisecond)

	tx, err := l.db.BeginTx(ctx, nil) // store opens with _txlock=immediate
	if err != nil {
		return Entry{}, fmt.Errorf("audit: append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var last int64
	var prev []byte
	err = tx.QueryRowContext(ctx, `SELECT seq, hash FROM audit_log ORDER BY seq DESC LIMIT 1`).Scan(&last, &prev)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		prev = genesis
	case err != nil:
		return Entry{}, fmt.Errorf("audit: read head: %w", err)
	}
	e.Seq = last + 1
	e.PrevHash = prev
	e.Hash = hash(e)

	_, err = tx.ExecContext(ctx, `INSERT INTO audit_log
		(seq, at, conn_id, conn_name, dialect, env, kind, status, rows_affected, duration_ms, sql_text, error, prev_hash, hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Seq, store.FormatTime(e.At), e.ConnID, e.ConnName, e.Dialect, e.Env, e.Kind, string(e.Status),
		e.RowsAffected, e.Duration.Milliseconds(), e.SQL, e.Error, e.PrevHash, e.Hash)
	if err != nil {
		return Entry{}, fmt.Errorf("audit: insert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Entry{}, fmt.Errorf("audit: commit: %w", err)
	}
	return e, nil
}

// hash computes SHA-256 over the previous hash and a length-prefixed
// encoding of every field, so no two different entries encode the same.
func hash(e Entry) []byte {
	h := sha256.New()
	h.Write(e.PrevHash)
	var n [8]byte
	writeInt := func(v int64) {
		binary.BigEndian.PutUint64(n[:], uint64(v)) //nolint:gosec // bit pattern only
		h.Write(n[:])
	}
	writeStr := func(s string) {
		writeInt(int64(len(s)))
		h.Write([]byte(s))
	}
	writeInt(e.Seq)
	writeStr(store.FormatTime(e.At))
	writeStr(e.ConnID)
	writeStr(e.ConnName)
	writeStr(e.Dialect)
	writeStr(e.Env)
	writeStr(e.Kind)
	writeStr(string(e.Status))
	writeInt(e.RowsAffected)
	writeInt(e.Duration.Milliseconds())
	writeStr(e.SQL)
	writeStr(e.Error)
	return h.Sum(nil)
}

func truncate(s string) string {
	if len(s) <= MaxSQLBytes {
		return s
	}
	cut := MaxSQLBytes
	for cut > 0 && s[cut]&0xC0 == 0x80 { // don't split a UTF-8 sequence
		cut--
	}
	return s[:cut] + "\n-- [truncated " + strconv.Itoa(len(s)-cut) + " bytes]"
}

// VerifyResult reports the outcome of Verify.
type VerifyResult struct {
	Entries int64 `json:"entries"`
	OK      bool  `json:"ok"`
	// BrokenAt is the first sequence number whose hash or link is wrong.
	BrokenAt int64  `json:"brokenAt,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// Verify walks the whole chain and checks every link and hash.
func (l *Log) Verify(ctx context.Context) (VerifyResult, error) {
	var res VerifyResult
	prev := genesis
	expect := int64(1)
	err := l.scan(ctx, Filter{}, func(e Entry) error {
		res.Entries++
		switch {
		case e.Seq != expect:
			res.BrokenAt, res.Reason = e.Seq, fmt.Sprintf("missing entries before seq %d", e.Seq)
		case !bytes.Equal(e.PrevHash, prev):
			res.BrokenAt, res.Reason = e.Seq, "previous-hash link does not match"
		case !bytes.Equal(hash(e), e.Hash):
			res.BrokenAt, res.Reason = e.Seq, "entry content does not match its hash"
		default:
			prev, expect = e.Hash, e.Seq+1
			return nil
		}
		return errStop
	})
	if err != nil && !errors.Is(err, errStop) {
		return VerifyResult{}, err
	}
	res.OK = res.BrokenAt == 0
	return res, nil
}

// Head returns the newest entry's sequence number and hash, for anchoring
// the chain outside this database. An empty log returns 0 and the genesis hash.
func (l *Log) Head(ctx context.Context) (int64, []byte, error) {
	var seq int64
	var h []byte
	err := l.db.QueryRowContext(ctx, `SELECT seq, hash FROM audit_log ORDER BY seq DESC LIMIT 1`).Scan(&seq, &h)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, genesis, nil
	}
	if err != nil {
		return 0, nil, fmt.Errorf("audit: head: %w", err)
	}
	return seq, h, nil
}

var errStop = errors.New("stop")
