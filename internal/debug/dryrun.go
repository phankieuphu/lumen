package debug

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/query"
)

// Limits for DryRun.
const (
	DefaultDryRunTimeout = 30 * time.Second
	MaxDryRunTimeout     = 10 * time.Minute
	DefaultPreviewRows   = 20
	MaxPreviewRows       = 200
	MaxStatements        = 100
	cancelTimeout        = 5 * time.Second
	// serverGrace lets the client's time limit fire before the server's.
	serverGrace = time.Second
)

// DryRunOptions bounds DryRun.
type DryRunOptions struct {
	// Timeout bounds the whole run; DefaultDryRunTimeout if zero, at most
	// MaxDryRunTimeout.
	Timeout time.Duration
	// PreviewRows is how many rows to keep from each statement that
	// returns rows; DefaultPreviewRows if zero, at most MaxPreviewRows.
	PreviewRows int
}

// Ran is what one statement of a dry run did.
type Ran struct {
	Step
	Ran bool `json:"ran"`
	// RowsAffected is the rows changed, or -1 if unknown.
	RowsAffected int64           `json:"rowsAffected"`
	Columns      []driver.Column `json:"columns,omitempty"`
	// Rows are the first rows returned; RowCount counts them all.
	Rows      [][]any `json:"rows,omitempty"`
	RowCount  int64   `json:"rowCount"`
	Truncated bool    `json:"truncated"`
	Ms        float64 `json:"ms"`
	Error     string  `json:"error,omitempty"`
}

// DryRunResult is a whole dry run. Statements after the first error did
// not run.
type DryRunResult struct {
	Statements []Ran `json:"statements"`
	// RolledBack is set once nothing the run did is kept.
	RolledBack bool    `json:"rolledBack"`
	TimedOut   bool    `json:"timedOut"`
	Cancelled  bool    `json:"cancelled"`
	Note       string  `json:"note"`
	Ms         float64 `json:"ms"`
}

// DryRun runs script in one transaction and rolls it back. Every statement
// must be one a dry run can undo (see Steps); otherwise nothing runs and
// the error wraps ErrNotUndoable. A statement that fails ends the run; its
// error is reported on it, not returned.
//
// The caller decides whether the connection allows the run: the writes do
// execute, so triggers fire and rows stay locked until the rollback.
func DryRun(ctx context.Context, c driver.Conn, script string, opt DryRunOptions) (DryRunResult, error) {
	d := c.Dialect()
	if _, ok := undoKeywords[d]; !ok {
		return DryRunResult{}, ErrUnsupported
	}
	steps := Steps(script, d)
	switch {
	case len(steps) == 0:
		return DryRunResult{}, ErrEmpty
	case len(steps) > MaxStatements:
		return DryRunResult{}, fmt.Errorf("%w: %d statements; the limit is %d", ErrTooMany, len(steps), MaxStatements)
	}
	for i, st := range steps {
		if !st.Undoable {
			return DryRunResult{}, fmt.Errorf("%w: statement %d: %s", ErrNotUndoable, i+1, st.Why)
		}
	}
	if opt.Timeout <= 0 {
		opt.Timeout = DefaultDryRunTimeout
	}
	if opt.PreviewRows <= 0 {
		opt.PreviewRows = DefaultPreviewRows
	}
	opt.Timeout = min(opt.Timeout, MaxDryRunTimeout)
	opt.PreviewRows = min(opt.PreviewRows, MaxPreviewRows)

	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()
	s, err := c.Session(runCtx)
	if err != nil {
		return DryRunResult{}, fmt.Errorf("debug: %w", err)
	}
	defer func() { _ = s.Close() }()

	res := DryRunResult{Statements: make([]Ran, len(steps))}
	for i, st := range steps {
		res.Statements[i] = Ran{Step: st, RowsAffected: -1}
	}
	if _, err := s.Exec(runCtx, beginSQL(d)); err != nil {
		return DryRunResult{}, fmt.Errorf("debug: start the transaction: %w", err)
	}
	stop := watch(runCtx, s)
	err = run(runCtx, s, d, &res, opt)
	stop()

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded), time.Since(start) >= opt.Timeout:
		res.TimedOut = true
	case errors.Is(ctx.Err(), context.Canceled):
		res.Cancelled = true
	}
	stopped := res.TimedOut || res.Cancelled
	kept, rerr := rollback(ctx, s, d)
	res.Ms = float64(time.Since(start).Microseconds()) / 1000
	switch {
	case rerr == nil && kept:
		res.Note = "The transaction was rolled back, but MySQL kept the changes to tables without transactions (such as MyISAM)."
		return res, errors.Join(err, ErrKept)
	case rerr == nil:
		res.RolledBack = true
		res.Note = "Ran in a transaction that was rolled back: nothing it changed was kept."
	case stopped && d == driver.SQLite && strings.Contains(rerr.Error(), "no transaction is active"):
		// An interrupted write makes SQLite roll back the transaction itself.
		res.RolledBack = true
		res.Note = "Stopping the run made SQLite roll back the transaction: nothing it changed was kept."
	case stopped && d != driver.SQLite:
		// Stopping a statement can close the connection, and the server
		// discards the open transaction of a closed connection.
		res.RolledBack = true
		res.Note = "Stopping the run closed its connection, so the database discarded the transaction: nothing it changed was kept."
	default:
		return res, fmt.Errorf("%w: %w", ErrRollback, rerr)
	}
	switch {
	case res.TimedOut:
		res.Note += fmt.Sprintf(" It stopped at the time limit of %s.", opt.Timeout)
	case res.Cancelled:
		res.Note += " It was stopped before the end."
	}
	res.Note += " Sequence and AUTO_INCREMENT values it used are not given back."
	return res, err
}

// run executes the statements in order until one fails. Only setup
// failures are returned.
func run(ctx context.Context, s driver.Session, d driver.Dialect, res *DryRunResult, opt DryRunOptions) error {
	if d == driver.Postgres {
		// The server stops a statement soon after the limit even if the
		// client's cancel does not land.
		left := time.Until(deadline(ctx)) + serverGrace
		if _, err := s.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", left.Milliseconds())); err != nil {
			return fmt.Errorf("debug: set a time limit: %w", err)
		}
	}
	for i := range res.Statements {
		r := &res.Statements[i]
		start := time.Now()
		err := runOne(ctx, s, d, r, opt.PreviewRows)
		r.Ms = float64(time.Since(start).Microseconds()) / 1000
		r.Ran = err == nil
		if !r.Ran {
			r.Error = err.Error()
			break // the statement's error is its result, not a failure of the run
		}
	}
	return nil
}

func runOne(ctx context.Context, s driver.Session, d driver.Dialect, r *Ran, preview int) (err error) {
	if !query.Classify(r.SQL, d).ReturnsRows {
		res, err := s.Exec(ctx, r.SQL)
		if err != nil {
			return err
		}
		r.RowsAffected = res.RowsAffected
		return nil
	}
	rows, err := s.Query(ctx, r.SQL)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := rows.Close(); err == nil {
			err = cerr
		}
		switch n := rows.RowsAffected(); {
		case n >= 0:
			r.RowsAffected = n
		case r.Kind != string(query.KindRead):
			// A write's RETURNING gives one row per row it changed.
			r.RowsAffected = r.RowCount
		}
	}()
	r.Columns = rows.Columns()
	vals := make([]any, len(r.Columns))
	for rows.Next() {
		r.RowCount++
		if len(r.Rows) >= preview {
			r.Truncated = true
			continue // the statement runs to its end either way
		}
		if err := rows.Values(vals); err != nil {
			return err
		}
		r.Rows = append(r.Rows, append([]any(nil), vals...))
	}
	return rows.Err()
}

// watch asks the server to stop the running statement once ctx is done.
// The returned func stops watching and waits for the watcher to exit.
func watch(ctx context.Context, s driver.Session) func() {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		select {
		case <-ctx.Done():
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cancelTimeout)
			_ = s.Cancel(cctx)
			cancel()
		case <-done:
		}
	})
	return func() {
		close(done)
		wg.Wait()
	}
}

// rollback ends the transaction. On MySQL it reports whether changes to
// tables without transactions were kept, which MySQL only warns about.
func rollback(ctx context.Context, s driver.Session, d driver.Dialect) (kept bool, err error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()
	if _, err := s.Exec(ctx, "ROLLBACK"); err != nil {
		return false, err
	}
	if d != driver.MySQL {
		return false, nil
	}
	return nonTxWarning(ctx, s)
}

// nonTxWarning reports whether the last statement raised MySQL warning
// 1196: "Some non-transactional changed tables couldn't be rolled back".
func nonTxWarning(ctx context.Context, s driver.Session) (_ bool, err error) {
	rows, err := s.Query(ctx, "SHOW WARNINGS")
	if err != nil {
		return false, err
	}
	defer func() {
		if cerr := rows.Close(); err == nil {
			err = cerr
		}
	}()
	vals := make([]any, len(rows.Columns()))
	found := false
	for rows.Next() {
		if err := rows.Values(vals); err != nil {
			return false, err
		}
		if len(vals) > 1 {
			if n, err := count(vals[1]); err == nil && n == 1196 {
				found = true
			}
		}
	}
	return found, rows.Err()
}

func beginSQL(d driver.Dialect) string {
	if d == driver.MySQL {
		return "START TRANSACTION"
	}
	return "BEGIN"
}

func deadline(ctx context.Context) time.Time {
	t, _ := ctx.Deadline()
	return t
}
