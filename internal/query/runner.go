package query

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/phankieuphu/lumen/internal/audit"
	"github.com/phankieuphu/lumen/internal/conn"
	"github.com/phankieuphu/lumen/internal/driver"
)

// Errors returned by Run. Policy errors are returned before anything reaches
// the server.
var (
	ErrEmpty            = errors.New("query: no statement to run")
	ErrReadOnly         = errors.New("query: connection is read-only; only reads are allowed")
	ErrConfirmRequired  = errors.New("query: writes on a production connection need confirmation")
	ErrMultiple         = errors.New("query: run one statement at a time")
	ErrDuplicateRequest = errors.New("query: request ID already running")
	ErrNotRunning       = errors.New("query: no running request with this ID")
	ErrBadRequest       = errors.New("query: invalid request")
	// ErrAudit means the statement ran but its audit entry could not be
	// written. The summary is still returned.
	ErrAudit = errors.New("query: statement ran but the audit log write failed")
)

// Limits and defaults for Request.
const (
	DefaultPageSize = 500
	MaxPageSize     = 5000
	DefaultMaxRows  = 100_000
	MaxMaxRows      = 1_000_000
	cancelTimeout   = 5 * time.Second
)

// Request is one statement to run.
type Request struct {
	// ID is chosen by the caller and used to Cancel.
	ID     string
	ConnID string
	SQL    string
	// PageSize is rows per page; defaults to DefaultPageSize.
	PageSize int
	// MaxRows stops reading after this many rows; defaults to DefaultMaxRows.
	MaxRows int
	// ConfirmWrite must be set to run a write or schema statement on a
	// production connection.
	ConfirmWrite bool
	// Timeout bounds the statement; 0 means no limit.
	Timeout time.Duration
}

// Page is a batch of rows. Columns is set on the first page only.
type Page struct {
	RequestID string
	Seq       int
	Columns   []driver.Column
	Rows      [][]any
}

// Sink receives pages in order. It is called synchronously, so a slow sink
// slows reading instead of buffering rows. The sink must not keep p.Rows
// after it returns: the backing memory is reused for the next page.
type Sink func(ctx context.Context, p Page) error

// Status of a finished request.
type Status = audit.Status

// Summary describes a finished request.
type Summary struct {
	RequestID    string
	Kind         Kind
	Status       Status
	Columns      []driver.Column
	RowCount     int64 // rows returned
	RowsAffected int64 // rows changed, or -1 if unknown
	Truncated    bool  // stopped at MaxRows
	Duration     time.Duration
	Error        string
}

// ConnSource provides open connections; *conn.Manager implements it.
type ConnSource interface {
	Get(ctx context.Context, id string) (driver.Conn, conn.Profile, error)
}

// Auditor records non-read statements; *audit.Log implements it.
type Auditor interface {
	Append(ctx context.Context, e audit.Entry) (audit.Entry, error)
}

// Runner runs requests. It is safe for concurrent use; each request uses its
// own session from the pool.
type Runner struct {
	conns   ConnSource
	audit   Auditor
	history *History
	log     *slog.Logger

	mu     sync.Mutex
	active map[string]*running
}

type running struct {
	cancel    context.CancelFunc
	cancelled atomic.Bool
	mu        sync.Mutex
	session   driver.Session
}

// NewRunner returns a Runner. history and log may be nil.
func NewRunner(conns ConnSource, auditor Auditor, history *History, log *slog.Logger) *Runner {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Runner{conns: conns, audit: auditor, history: history, log: log, active: map[string]*running{}}
}

// Run executes req and streams rows to sink (nil discards them). It returns a summary for every
// request that got as far as classification, even when it also returns an
// error.
func (r *Runner) Run(ctx context.Context, req Request, sink Sink) (Summary, error) {
	if err := normalize(&req); err != nil {
		return Summary{RequestID: req.ID}, err
	}
	if sink == nil {
		sink = func(context.Context, Page) error { return nil }
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if req.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	run := &running{cancel: cancel}
	if err := r.register(req.ID, run); err != nil {
		return Summary{RequestID: req.ID}, err
	}
	defer r.unregister(req.ID)

	c, prof, err := r.conns.Get(ctx, req.ConnID)
	if err != nil {
		return Summary{RequestID: req.ID, Status: audit.StatusError, Error: err.Error()}, err
	}

	start := time.Now()
	cls := Classify(req.SQL, prof.Dialect)
	sum := Summary{RequestID: req.ID, Kind: cls.Kind, RowsAffected: -1}

	if err := checkPolicy(cls, prof, req); err != nil {
		sum.Status, sum.Error = audit.StatusBlocked, err.Error()
		// Confirmation prompts are part of normal flow, not events to audit.
		if !errors.Is(err, ErrConfirmRequired) {
			err = r.finish(ctx, req, prof, &sum, start, err)
		}
		return sum, err
	}

	err = r.execute(ctx, c, run, req, cls, sink, &sum)
	switch {
	case err == nil:
		sum.Status = audit.StatusOK
	case run.cancelled.Load() || errors.Is(ctx.Err(), context.Canceled):
		sum.Status, sum.Error = audit.StatusCancelled, "cancelled"
		err = fmt.Errorf("query: cancelled: %w", context.Canceled)
	default:
		sum.Status, sum.Error = audit.StatusError, err.Error()
	}
	return sum, r.finish(ctx, req, prof, &sum, start, err)
}

func normalize(req *Request) error {
	if req.ID == "" || req.ConnID == "" {
		return fmt.Errorf("%w: ID and ConnID are required", ErrBadRequest)
	}
	if req.PageSize <= 0 {
		req.PageSize = DefaultPageSize
	}
	req.PageSize = min(req.PageSize, MaxPageSize)
	if req.MaxRows <= 0 {
		req.MaxRows = DefaultMaxRows
	}
	req.MaxRows = min(req.MaxRows, MaxMaxRows)
	return nil
}

func checkPolicy(cls Classification, prof conn.Profile, req Request) error {
	switch {
	case cls.Statements == 0:
		return ErrEmpty
	case cls.Statements > 1:
		return ErrMultiple
	case prof.ReadOnly && (cls.Kind == KindWrite || cls.Kind == KindSchema):
		return ErrReadOnly
	case prof.Env == conn.EnvProd && (cls.Kind == KindWrite || cls.Kind == KindSchema) && !req.ConfirmWrite:
		return ErrConfirmRequired
	}
	return nil
}

func (r *Runner) execute(ctx context.Context, c driver.Conn, run *running, req Request,
	cls Classification, sink Sink, sum *Summary,
) error {
	s, err := c.Session(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	run.mu.Lock()
	run.session = s
	run.mu.Unlock()
	defer func() {
		run.mu.Lock()
		run.session = nil
		run.mu.Unlock()
	}()

	if !cls.ReturnsRows {
		res, err := s.Exec(ctx, req.SQL)
		if err != nil {
			return err
		}
		sum.RowsAffected = res.RowsAffected
		return nil
	}
	rows, err := s.Query(ctx, req.SQL)
	if err != nil {
		return err
	}
	// Only a read may be stopped early: cancelling a write such as
	// INSERT ... RETURNING would roll it back.
	stopped, err := stream(ctx, s, rows, req, cls.Kind == KindRead, sink, sum)
	if cerr := rows.Close(); err == nil && !stopped {
		err = cerr
	}
	if n := rows.RowsAffected(); n >= 0 {
		sum.RowsAffected = n
	}
	return err
}

// stream reads rows into pages backed by one reused buffer and hands each
// page to sink. Past MaxRows it either cancels the statement (stopEarly) or
// drains the remaining rows without keeping them; it reports whether it
// cancelled.
func stream(ctx context.Context, s driver.Session, rows driver.Rows, req Request, stopEarly bool,
	sink Sink, sum *Summary,
) (stopped bool, err error) {
	cols := rows.Columns()
	sum.Columns = cols
	ncol := len(cols)
	flat := make([]any, req.PageSize*ncol)
	page := make([][]any, 0, req.PageSize)
	seq := 0
	emit := func() error {
		p := Page{RequestID: req.ID, Seq: seq, Rows: page}
		if seq == 0 {
			p.Columns = cols
		}
		seq++
		err := sink(ctx, p)
		page = page[:0]
		clear(flat) // drop references so values can be collected
		return err
	}
	for rows.Next() {
		if sum.RowCount >= int64(req.MaxRows) {
			sum.Truncated = true
			if !stopEarly {
				continue // drain
			}
			// Stop the server producing rows nobody will read; closing an
			// unfinished cursor would otherwise drain it.
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cancelTimeout)
			_ = s.Cancel(cctx)
			cancel()
			stopped = true
			break
		}
		i := len(page)
		row := flat[i*ncol : (i+1)*ncol : (i+1)*ncol]
		if err := rows.Values(row); err != nil {
			return stopped, err
		}
		page = append(page, row)
		sum.RowCount++
		if len(page) == req.PageSize {
			if err := emit(); err != nil {
				return stopped, err
			}
		}
	}
	if err := rows.Err(); err != nil && !stopped {
		return stopped, err
	}
	if len(page) > 0 || seq == 0 { // always send columns, even for an empty result
		return stopped, emit()
	}
	return stopped, nil
}

// finish writes the audit entry (for non-reads) and history row.
func (r *Runner) finish(ctx context.Context, req Request, prof conn.Profile, sum *Summary, start time.Time, runErr error) error {
	sum.Duration = time.Since(start)
	// Bookkeeping must happen even when the request was cancelled.
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	var auditErr error
	if sum.Kind == KindWrite || sum.Kind == KindSchema {
		affected := sum.RowsAffected
		if affected < 0 {
			affected = 0
		}
		_, err := r.audit.Append(bctx, audit.Entry{
			ConnID: prof.ID, ConnName: prof.Name, Dialect: string(prof.Dialect), Env: string(prof.Env),
			Kind: string(sum.Kind), Status: sum.Status, RowsAffected: affected, Duration: sum.Duration,
			SQL: req.SQL, Error: sum.Error,
		})
		if err != nil {
			r.log.Error("audit append failed", "request", req.ID, "conn", prof.ID, "err", err)
			auditErr = fmt.Errorf("%w: %w", ErrAudit, err)
		}
	}
	if r.history != nil {
		rowCount := sum.RowCount
		if sum.RowsAffected > 0 && !sum.Kind.returnsData() {
			rowCount = sum.RowsAffected
		}
		if err := r.history.Add(bctx, HistoryEntry{
			ConnID: prof.ID, Dialect: prof.Dialect, StartedAt: start, Duration: sum.Duration,
			Kind: sum.Kind, Status: sum.Status, RowCount: rowCount, SQL: req.SQL, Error: sum.Error,
		}); err != nil {
			r.log.Warn("history append failed", "request", req.ID, "err", err)
		}
	}
	return errors.Join(runErr, auditErr)
}

func (k Kind) returnsData() bool { return k == KindRead }

// Cancel stops a running request: it asks the server to cancel the
// statement and cancels the request's context.
func (r *Runner) Cancel(ctx context.Context, requestID string) error {
	r.mu.Lock()
	run, ok := r.active[requestID]
	r.mu.Unlock()
	if !ok {
		return ErrNotRunning
	}
	run.cancelled.Store(true)
	run.mu.Lock()
	s := run.session
	run.mu.Unlock()
	var err error
	if s != nil {
		cctx, cancel := context.WithTimeout(ctx, cancelTimeout)
		err = s.Cancel(cctx)
		cancel()
	}
	run.cancel()
	if err != nil {
		return fmt.Errorf("query: cancel: %w", err)
	}
	return nil
}

// Running returns the IDs of requests in flight.
func (r *Runner) Running() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.active))
	for id := range r.active {
		ids = append(ids, id)
	}
	return ids
}

func (r *Runner) register(id string, run *running) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.active[id]; dup {
		return ErrDuplicateRequest
	}
	r.active[id] = run
	return nil
}

func (r *Runner) unregister(id string) {
	r.mu.Lock()
	delete(r.active, id)
	r.mu.Unlock()
}
