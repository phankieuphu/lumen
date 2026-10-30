// Package sqladapter adapts a database/sql pool to the driver interfaces, for
// engines whose Go drivers are database/sql drivers (MySQL, SQLite).
package sqladapter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/phankieuphu/lumen/internal/driver"
)

// Options customizes the adapter per engine. All fields are optional.
type Options struct {
	// NewCanceler is called once per session and returns a function that
	// stops the statement running on that session from the server side
	// (e.g. MySQL KILL QUERY). Without it, Cancel only cancels the context.
	NewCanceler func(ctx context.Context, c *sql.Conn) (func(context.Context) error, error)
	// Guard runs before every statement and may reject it.
	Guard func(sql string) error
	// OnClose runs after the pool is closed, to release engine resources.
	OnClose func() error
}

// Conn implements driver.Conn over a *sql.DB.
type Conn struct {
	db      *sql.DB
	dialect driver.Dialect
	max     int
	opts    Options
}

// New wraps db. The adapter takes ownership and closes db on Close.
func New(db *sql.DB, dialect driver.Dialect, maxConns int, opts Options) *Conn {
	return &Conn{db: db, dialect: dialect, max: maxConns, opts: opts}
}

// Dialect implements driver.Conn.
func (c *Conn) Dialect() driver.Dialect { return c.dialect }

// Session implements driver.Conn.
func (c *Conn) Session(ctx context.Context) (driver.Session, error) {
	sc, err := c.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: acquire: %w", c.dialect, err)
	}
	s := &session{c: c, sc: sc}
	if c.opts.NewCanceler != nil {
		if s.serverCancel, err = c.opts.NewCanceler(ctx, sc); err != nil {
			_ = sc.Close()
			return nil, fmt.Errorf("%s: session: %w", c.dialect, err)
		}
	}
	return s, nil
}

// Ping implements driver.Conn.
func (c *Conn) Ping(ctx context.Context) error {
	if err := c.db.PingContext(ctx); err != nil {
		return fmt.Errorf("%s: ping: %w", c.dialect, err)
	}
	return nil
}

// Stats implements driver.Conn.
func (c *Conn) Stats() driver.PoolStats {
	s := c.db.Stats()
	return driver.PoolStats{Open: s.OpenConnections, InUse: s.InUse, Idle: s.Idle, Max: c.max}
}

// Close implements driver.Conn.
func (c *Conn) Close() error {
	err := c.db.Close()
	if c.opts.OnClose != nil {
		err = errors.Join(err, c.opts.OnClose())
	}
	if err != nil {
		return fmt.Errorf("%s: close: %w", c.dialect, err)
	}
	return nil
}

type session struct {
	c            *Conn
	sc           *sql.Conn
	serverCancel func(context.Context) error

	mu         sync.Mutex
	stmtCancel context.CancelFunc // cancels the running statement's context
}

// begin derives a cancellable context for one statement.
func (s *session) begin(ctx context.Context) context.Context {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.stmtCancel = cancel
	s.mu.Unlock()
	return ctx
}

func (s *session) end() {
	s.mu.Lock()
	if s.stmtCancel != nil {
		s.stmtCancel()
		s.stmtCancel = nil
	}
	s.mu.Unlock()
}

func (s *session) guard(q string) error {
	if s.c.opts.Guard == nil {
		return nil
	}
	return s.c.opts.Guard(q)
}

func (s *session) Query(ctx context.Context, q string, args ...any) (driver.Rows, error) {
	if err := s.guard(q); err != nil {
		return nil, err
	}
	ctx = s.begin(ctx)
	r, err := s.sc.QueryContext(ctx, q, args...)
	if err != nil {
		s.end()
		return nil, fmt.Errorf("%s: query: %w", s.c.dialect, err)
	}
	cts, err := r.ColumnTypes()
	if err != nil {
		_ = r.Close() //nolint:sqlclosecheck // error path; on success rows close via driver.Rows
		s.end()
		return nil, fmt.Errorf("%s: columns: %w", s.c.dialect, err)
	}
	cols := make([]driver.Column, len(cts))
	for i, ct := range cts {
		nullable, ok := ct.Nullable()
		cols[i] = driver.Column{Name: ct.Name(), Type: ct.DatabaseTypeName(), Nullable: nullable || !ok}
	}
	return newRows(r, cols, s), nil
}

func (s *session) Exec(ctx context.Context, q string, args ...any) (driver.Result, error) {
	if err := s.guard(q); err != nil {
		return driver.Result{}, err
	}
	ctx = s.begin(ctx)
	defer s.end()
	res, err := s.sc.ExecContext(ctx, q, args...)
	if err != nil {
		return driver.Result{}, fmt.Errorf("%s: exec: %w", s.c.dialect, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		n = -1
	}
	return driver.Result{RowsAffected: n}, nil
}

// Cancel asks the server to stop the statement, then cancels its context so
// the client stops waiting even if the server-side cancel did not land.
func (s *session) Cancel(ctx context.Context) error {
	s.mu.Lock()
	cancel := s.stmtCancel
	s.mu.Unlock()
	if cancel == nil {
		return nil
	}
	var err error
	if s.serverCancel != nil {
		err = s.serverCancel(ctx)
	}
	cancel()
	if err != nil {
		return fmt.Errorf("%s: cancel: %w", s.c.dialect, err)
	}
	return nil
}

func (s *session) Close() error {
	s.end()
	if err := s.sc.Close(); err != nil {
		return fmt.Errorf("%s: release: %w", s.c.dialect, err)
	}
	return nil
}

type rows struct {
	r       *sql.Rows
	cols    []driver.Column
	s       *session
	holders []any // scan targets, reused across rows
	ptrs    []any
	closed  bool
}

func newRows(r *sql.Rows, cols []driver.Column, s *session) *rows {
	holders := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range holders {
		ptrs[i] = &holders[i]
	}
	return &rows{r: r, cols: cols, s: s, holders: holders, ptrs: ptrs}
}

func (r *rows) Columns() []driver.Column { return r.cols }
func (r *rows) Next() bool               { return r.r.Next() }

func (r *rows) Values(dst []any) error {
	if err := r.r.Scan(r.ptrs...); err != nil {
		return fmt.Errorf("%s: scan: %w", r.s.c.dialect, err)
	}
	for i, v := range r.holders {
		dst[i] = driver.Normalize(v)
		r.holders[i] = nil
	}
	return nil
}

func (r *rows) Err() error {
	if err := r.r.Err(); err != nil {
		return fmt.Errorf("%s: rows: %w", r.s.c.dialect, err)
	}
	return nil
}

func (r *rows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	err := errors.Join(r.r.Close(), r.r.Err())
	r.s.end()
	if err != nil {
		return fmt.Errorf("%s: rows: %w", r.s.c.dialect, err)
	}
	return nil
}

func (r *rows) RowsAffected() int64 { return -1 }
