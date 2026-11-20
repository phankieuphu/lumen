package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/phankieuphu/lumen/internal/audit"
	"github.com/phankieuphu/lumen/internal/conn"
	"github.com/phankieuphu/lumen/internal/debug"
	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/query"
)

// DryRunInput is a dry run request from the UI.
type DryRunInput struct {
	// RequestID is chosen by the UI and used to CancelDebug.
	RequestID string `json:"requestId"`
	ConnID    string `json:"connId"`
	SQL       string `json:"sql"`
	// ConfirmWrite must be set for a dry run with writes on a production
	// connection: the writes do run, holding locks and firing triggers.
	ConfirmWrite bool  `json:"confirmWrite"`
	TimeoutMs    int64 `json:"timeoutMs"`
	PreviewRows  int   `json:"previewRows"`
}

// dryRunLabel tags a dry run's statements in the audit log.
const dryRunLabel = "dry run"

// DebugSteps returns the statements of sql, for stepping through a script
// and for checking what a dry run can take. Offsets are UTF-16 code units.
func (a *App) DebugSteps(dialect, sql string) []debug.Step {
	steps := debug.Steps(sql, driver.Dialect(dialect))
	at := utf16Offsets(sql)
	for i := range steps {
		steps[i].Start, steps[i].End = at(steps[i].Start), at(steps[i].End)
	}
	return steps
}

// DebugParts splits sql, one SELECT, into its parts and counts each one's
// rows in read-only transactions. Offsets are UTF-16 code units.
func (a *App) DebugParts(ctx context.Context, connID, sql string) (debug.Report, error) {
	c, p, err := a.conns.Get(ctx, connID)
	if err != nil {
		return debug.Report{}, err
	}
	switch cls := query.Classify(sql, p.Dialect); {
	case cls.Statements == 0:
		return debug.Report{}, query.ErrEmpty
	case cls.Statements > 1:
		return debug.Report{}, fmt.Errorf("%w: select the query to split, or put the cursor in it", query.ErrMultiple)
	case !cls.IsRead():
		return debug.Report{}, debug.ErrStatement
	}
	rep, err := debug.Parts(ctx, c, sql, debug.PartsOptions{})
	at := utf16Offsets(sql)
	for i := range rep.Parts {
		rep.Parts[i].Start, rep.Parts[i].End = at(rep.Parts[i].Start), at(rep.Parts[i].End)
	}
	return rep, err
}

// DryRun runs a script in a transaction that is always rolled back, with
// the connection's write rules: refused on a read-only connection, and on
// production only once confirmed. Each write that ran is audited as rolled
// back. Offsets are UTF-16 code units.
func (a *App) DryRun(ctx context.Context, in DryRunInput) (debug.DryRunResult, error) {
	if in.RequestID == "" || in.ConnID == "" {
		return debug.DryRunResult{}, fmt.Errorf("%w: requestId and connId are required", query.ErrBadRequest)
	}
	c, p, err := a.conns.Get(ctx, in.ConnID)
	if err != nil {
		return debug.DryRunResult{}, err
	}
	cls := query.Classify(in.SQL, p.Dialect)
	writes := cls.Kind != query.KindRead
	switch {
	case writes && p.ReadOnly:
		return debug.DryRunResult{}, a.auditBlocked(ctx, p, cls, in.SQL, query.ErrReadOnly)
	case writes && p.Env == conn.EnvProd && !in.ConfirmWrite:
		// Confirmation prompts are part of normal flow, not events to audit.
		return debug.DryRunResult{}, query.ErrConfirmRequired
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := a.debugRuns.add(in.RequestID, cancel); err != nil {
		return debug.DryRunResult{}, err
	}
	defer a.debugRuns.remove(in.RequestID)

	res, err := debug.DryRun(ctx, c, in.SQL, debug.DryRunOptions{
		Timeout: time.Duration(in.TimeoutMs) * time.Millisecond, PreviewRows: in.PreviewRows,
	})
	if errors.Is(err, debug.ErrNotUndoable) {
		return res, a.auditBlocked(ctx, p, cls, in.SQL, err)
	}
	err = errors.Join(err, a.auditDryRun(ctx, p, "dry-run-"+in.RequestID, res))
	at := utf16Offsets(in.SQL)
	for i := range res.Statements {
		st := &res.Statements[i].Step
		st.Start, st.End = at(st.Start), at(st.End)
	}
	return res, err
}

// auditDryRun records each write of a dry run that reached the server,
// tagged as one transaction.
func (a *App) auditDryRun(ctx context.Context, p conn.Profile, txID string, res debug.DryRunResult) error {
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var errs []error
	for _, r := range res.Statements {
		if (!r.Ran && r.Error == "") || r.Kind == string(query.KindRead) {
			continue
		}
		status := audit.StatusRolledBack
		switch {
		case r.Error != "" && (res.TimedOut || res.Cancelled):
			status = audit.StatusCancelled
		case r.Error != "":
			status = audit.StatusError
		case !res.RolledBack:
			status = audit.StatusOK // the rollback failed: it may have been kept
		}
		_, err := a.audit.Append(bctx, audit.Entry{
			ConnID: p.ID, ConnName: p.Name, Dialect: string(p.Dialect), Env: string(p.Env),
			Kind: r.Kind, Status: status, RowsAffected: max(r.RowsAffected, 0),
			Duration: time.Duration(r.Ms * float64(time.Millisecond)), SQL: r.SQL, Error: r.Error, TxID: txID, TxLabel: dryRunLabel,
		})
		if err != nil {
			a.log.Error("audit append failed", "conn", p.ID, "err", err)
			errs = append(errs, fmt.Errorf("%w: %w", query.ErrAudit, err))
		}
	}
	return errors.Join(errs...)
}

// auditBlocked records a dry run refused before it reached the server, as
// Runner does for a refused statement, and returns err.
func (a *App) auditBlocked(ctx context.Context, p conn.Profile, cls query.Classification, sql string, err error) error {
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if _, aerr := a.audit.Append(bctx, audit.Entry{
		ConnID: p.ID, ConnName: p.Name, Dialect: string(p.Dialect), Env: string(p.Env),
		Kind: string(cls.Kind), Status: audit.StatusBlocked, SQL: sql, Error: err.Error(),
	}); aerr != nil {
		a.log.Error("audit append failed", "conn", p.ID, "err", aerr)
		return errors.Join(err, fmt.Errorf("%w: %w", query.ErrAudit, aerr))
	}
	return err
}

// CancelDebug stops a running dry run.
func (a *App) CancelDebug(_ context.Context, requestID string) error {
	return a.debugRuns.cancel(requestID)
}

// runs holds the cancel funcs of running dry runs. The zero value is ready.
type runs struct {
	mu sync.Mutex
	m  map[string]context.CancelFunc
}

func (r *runs) add(id string, cancel context.CancelFunc) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.m[id]; dup {
		return query.ErrDuplicateRequest
	}
	if r.m == nil {
		r.m = map[string]context.CancelFunc{}
	}
	r.m[id] = cancel
	return nil
}

func (r *runs) remove(id string) {
	r.mu.Lock()
	delete(r.m, id)
	r.mu.Unlock()
}

func (r *runs) cancel(id string) error {
	r.mu.Lock()
	cancel, ok := r.m[id]
	r.mu.Unlock()
	if !ok {
		return query.ErrNotRunning
	}
	cancel()
	return nil
}
