// Package app is the façade the UI shell binds to. It wires the core
// services, owns their lifecycle, and maps domain types to JSON DTOs. It
// knows nothing about Wails; the shell supplies an Emitter for events.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/phankieuphu/lumen/internal/advisor"
	"github.com/phankieuphu/lumen/internal/audit"
	"github.com/phankieuphu/lumen/internal/conn"
	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/explain"
	"github.com/phankieuphu/lumen/internal/lint"
	"github.com/phankieuphu/lumen/internal/locks"
	"github.com/phankieuphu/lumen/internal/query"
	"github.com/phankieuphu/lumen/internal/schema"
	"github.com/phankieuphu/lumen/internal/secret"
	"github.com/phankieuphu/lumen/internal/sqlparse"
	"github.com/phankieuphu/lumen/internal/store"

	// Engines register themselves.
	_ "github.com/phankieuphu/lumen/internal/driver/mysql"
	_ "github.com/phankieuphu/lumen/internal/driver/postgres"
	_ "github.com/phankieuphu/lumen/internal/driver/sqlite"
)

// Event names emitted to the UI.
const (
	EventQueryPage = "query:page"
	// EventSchemaChanged carries a SchemaChangedDTO after DDL or the end of
	// a transaction, so the UI can fetch the connection's schema again.
	EventSchemaChanged = "schema:changed"
)

// Emitter sends events to the UI. The shell implements it.
type Emitter interface {
	Emit(event string, data any)
}

// Config configures App. Zero values select defaults.
type Config struct {
	// DataDir holds the local database. Defaults to <user config dir>/lumen.
	DataDir string
	Version string
	Logger  *slog.Logger
	// Secrets defaults to the OS keychain.
	Secrets secret.Store
}

// App holds the running services.
type App struct {
	log       *slog.Logger
	store     *store.Store
	conns     *conn.Manager
	audit     *audit.Log
	history   *query.History
	runner    *query.Runner
	syntax    *sqlparse.Checker
	schemas   *schema.Cache
	emitter   Emitter
	picker    FilePicker
	picked    pickedFiles
	unsaved   unsavedFlag
	sessionID int64
}

// Open starts the services. Call Close on shutdown.
func Open(ctx context.Context, cfg Config) (*App, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Secrets == nil {
		cfg.Secrets = secret.Keyring{}
	}
	if cfg.DataDir == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("app: find config dir: %w", err)
		}
		cfg.DataDir = filepath.Join(dir, "lumen")
	}
	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "lumen.db"))
	if err != nil {
		return nil, err
	}
	a := &App{
		log:     cfg.Logger,
		store:   st,
		conns:   conn.NewManager(st, cfg.Secrets),
		audit:   audit.New(st),
		history: query.NewHistory(st),
		emitter: nopEmitter{},
		picker:  noPicker{},
	}
	a.runner = query.NewRunner(a.conns, a.audit, a.history, cfg.Logger)
	a.schemas = schema.NewCache(a.loadSchema, 0)
	if a.syntax, err = sqlparse.New(); err != nil {
		_ = st.Close()
		return nil, err
	}
	if a.sessionID, err = st.StartAppSession(ctx, cfg.Version); err != nil {
		_ = a.syntax.Close()
		_ = st.Close()
		return nil, err
	}
	a.log.Info("app started", "data_dir", cfg.DataDir, "version", cfg.Version, "dialects", driver.Dialects())
	return a, nil
}

// SetEmitter installs the UI event sink. Call it before the UI starts
// issuing requests.
func (a *App) SetEmitter(e Emitter) { a.emitter = e }

// Close stops every service. It is safe to call once.
func (a *App) Close(ctx context.Context) error {
	err := errors.Join(
		a.runner.Close(ctx), // open transactions roll back before their pools close
		a.conns.CloseAll(),
		a.store.EndAppSession(ctx, a.sessionID),
		a.syntax.Close(),
	)
	return errors.Join(err, a.store.Close())
}

type nopEmitter struct{}

func (nopEmitter) Emit(string, any) {}

// RunQuery runs one statement. Rows arrive as EventQueryPage events with a
// PageDTO payload; the returned summary arrives after the last page.
func (a *App) RunQuery(ctx context.Context, in RunInput) (SummaryDTO, error) {
	sum, err := a.runner.Run(ctx, in.request(), func(_ context.Context, p query.Page) error {
		// Marshal now: the runner reuses the row buffer after we return.
		b, err := json.Marshal(pageDTO(p))
		if err != nil {
			return fmt.Errorf("app: encode page: %w", err)
		}
		a.emitter.Emit(EventQueryPage, json.RawMessage(b))
		return nil
	})
	if sum.Kind == query.KindSchema || sum.TxEnd != "" {
		a.schemaChanged(in.ConnID)
	}
	return summaryDTO(sum), err
}

// schemaChanged drops the cached schema of connID and tells the UI. DDL in
// an open transaction is not visible to the catalog read until it commits,
// so the end of a transaction counts too.
func (a *App) schemaChanged(connID string) {
	a.schemas.Invalidate(connID)
	a.emitter.Emit(EventSchemaChanged, SchemaChangedDTO{ConnID: connID})
}

// Statements returns where each statement of sql sits, so the editor can run
// the one under the cursor. Offsets are UTF-16 code units, as JavaScript
// strings index them.
func (a *App) Statements(dialect, sql string) []StatementDTO {
	return statementDTOs(sql, query.Spans(sql, driver.Dialect(dialect)))
}

// Lint returns what the lint rules (docs/lint-rules.md) find in sql, for
// the editor to mark. It reads the connection's settings but does not
// connect. Offsets are UTF-16 code units, as JavaScript strings index them.
func (a *App) Lint(ctx context.Context, connID, sql string) ([]LintIssueDTO, error) {
	p, err := a.conns.Profile(ctx, connID)
	if err != nil {
		return nil, err
	}
	fs := lint.Check(sql, lint.Context{Dialect: p.Dialect, Prod: p.Env == conn.EnvProd})
	return lintIssueDTOs(sql, fs), nil
}

// CheckSyntax returns the syntax errors in sql, parsed offline with the
// dialect's parser, for the editor to underline. Offsets are UTF-16 code
// units, as JavaScript strings index them.
func (a *App) CheckSyntax(ctx context.Context, dialect, sql string) []SyntaxIssueDTO {
	return syntaxIssueDTOs(sql, a.syntax.Check(ctx, sql, driver.Dialect(dialect)))
}

// editorColorsKey is the settings key holding the editor's syntax colours.
const editorColorsKey = "editor.colors"

var (
	colorToken = regexp.MustCompile(`^[a-z][a-zA-Z]{0,31}$`)
	colorValue = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// maxEditorColors bounds the stored map; the UI has about ten tokens.
const maxEditorColors = 32

// EditorColors returns the syntax colours the user changed, token → #rrggbb.
// The UI holds the defaults, so an empty map means none changed.
func (a *App) EditorColors(ctx context.Context) (map[string]string, error) {
	v, err := a.store.Setting(ctx, editorColorsKey)
	if errors.Is(err, store.ErrNotFound) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	colors := map[string]string{}
	if err := json.Unmarshal([]byte(v), &colors); err != nil {
		return nil, fmt.Errorf("app: read editor colors: %w", err)
	}
	return colors, nil
}

// SaveEditorColors replaces the changed syntax colours. Colours become CSS,
// so only #rrggbb values for simple token names are accepted.
func (a *App) SaveEditorColors(ctx context.Context, colors map[string]string) (map[string]string, error) {
	if len(colors) > maxEditorColors {
		return nil, fmt.Errorf("app: editor colors: at most %d", maxEditorColors)
	}
	for k, v := range colors {
		if !colorToken.MatchString(k) || !colorValue.MatchString(v) {
			return nil, fmt.Errorf("app: editor colors: %q: want a token name and a #rrggbb colour", k)
		}
	}
	if colors == nil {
		colors = map[string]string{}
	}
	b, err := json.Marshal(colors)
	if err != nil {
		return nil, fmt.Errorf("app: save editor colors: %w", err)
	}
	if err := a.store.SetSetting(ctx, editorColorsKey, string(b)); err != nil {
		return nil, err
	}
	return colors, nil
}

// CancelQuery cancels a running request.
func (a *App) CancelQuery(ctx context.Context, requestID string) error {
	return a.runner.Cancel(ctx, requestID)
}

// BeginTx opens a transaction on a connection. Pass its ID as RunInput.TxID
// to run statements inside it; label is an optional tag shown in history
// and the audit log.
func (a *App) BeginTx(ctx context.Context, connID, label string) (TxDTO, error) {
	t, err := a.runner.Begin(ctx, connID, label)
	return txDTO(t), err
}

// CommitTx commits an open transaction.
func (a *App) CommitTx(ctx context.Context, txID string) (SummaryDTO, error) {
	connID := a.txConn(txID)
	sum, err := a.runner.Commit(ctx, txID)
	if connID != "" {
		a.schemaChanged(connID)
	}
	return summaryDTO(sum), err
}

// RollbackTx rolls back an open transaction.
func (a *App) RollbackTx(ctx context.Context, txID string) (SummaryDTO, error) {
	connID := a.txConn(txID)
	sum, err := a.runner.Rollback(ctx, txID)
	if connID != "" {
		a.schemaChanged(connID)
	}
	return summaryDTO(sum), err
}

// Transactions returns the open transactions, so a reloaded UI can pick
// them up again.
func (a *App) Transactions(_ context.Context) ([]TxDTO, error) {
	ts := a.runner.Transactions()
	out := make([]TxDTO, len(ts))
	for i, t := range ts {
		out[i] = txDTO(t)
	}
	return out, nil
}

// TransactionHistory returns the statements of one transaction, oldest first.
func (a *App) TransactionHistory(ctx context.Context, txID string) ([]HistoryDTO, error) {
	hs, err := a.history.ListTx(ctx, txID)
	if err != nil {
		return nil, err
	}
	out := make([]HistoryDTO, len(hs))
	for i, h := range hs {
		out[i] = historyDTO(h)
	}
	return out, nil
}

// QueryHistory returns past requests, newest first.
func (a *App) QueryHistory(ctx context.Context, connID string, beforeID int64, limit int) ([]HistoryDTO, error) {
	hs, err := a.history.List(ctx, connID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]HistoryDTO, len(hs))
	for i, h := range hs {
		out[i] = historyDTO(h)
	}
	return out, nil
}

// schemaTimeout bounds a catalog read so a slow server cannot hang the
// diagram view.
const schemaTimeout = 30 * time.Second

// Schema returns the tables, columns, and foreign keys of a connection's
// database, for the relations diagram and autocomplete. It is cached until
// DDL, the end of a transaction, or RefreshSchema; Version tells copies
// apart. It runs fixed catalog reads only, so it is allowed on read-only and
// production connections.
func (a *App) Schema(ctx context.Context, connID string) (schema.Schema, error) {
	ctx, cancel := context.WithTimeout(ctx, schemaTimeout)
	defer cancel()
	return a.schemas.Get(ctx, connID)
}

// RefreshSchema reads a connection's schema again, e.g. after DDL run
// outside lumen.
func (a *App) RefreshSchema(ctx context.Context, connID string) (schema.Schema, error) {
	a.schemas.Invalidate(connID)
	return a.Schema(ctx, connID)
}

func (a *App) loadSchema(ctx context.Context, connID string) (schema.Schema, error) {
	c, _, err := a.conns.Get(ctx, connID)
	if err != nil {
		return schema.Schema{}, err
	}
	return schema.Read(ctx, c, schema.Limits{})
}

// txConn is the connection of an open transaction, or "".
func (a *App) txConn(txID string) string {
	for _, t := range a.runner.Transactions() {
		if t.ID == txID {
			return t.ConnID
		}
	}
	return ""
}

// Explain takes the plan of sql, one statement, on a connection. Reads run
// with EXPLAIN ANALYZE inside a read-only transaction that is rolled back;
// writes and anything else are planned but not run. The time limit is
// explain.DefaultTimeout.
func (a *App) Explain(ctx context.Context, connID, sql string) (explain.Plan, error) {
	c, p, err := a.conns.Get(ctx, connID)
	if err != nil {
		return explain.Plan{}, err
	}
	cls := query.Classify(sql, p.Dialect)
	switch {
	case cls.Statements == 0:
		return explain.Plan{}, query.ErrEmpty
	case cls.Statements > 1:
		return explain.Plan{}, fmt.Errorf("%w: select the statement to explain, or put the cursor in it", query.ErrMultiple)
	}
	return explain.Run(ctx, c, sql, explain.Options{Analyze: cls.IsRead()})
}

// locksTimeout bounds a read of a transaction's locks.
const locksTimeout = 10 * time.Second

// TxLocks returns the locks an open transaction holds or waits for, read
// from a separate session so the transaction itself is untouched.
func (a *App) TxLocks(ctx context.Context, txID string) (locks.Report, error) {
	connID, id, err := a.runner.TxSession(txID)
	if err != nil {
		return locks.Report{}, err
	}
	if id == 0 {
		return locks.Report{}, locks.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, locksTimeout)
	defer cancel()
	c, _, err := a.conns.Get(ctx, connID)
	if err != nil {
		return locks.Report{}, err
	}
	return locks.Held(ctx, c, id)
}

// advisorTimeout bounds a statistics or plan read.
const advisorTimeout = 30 * time.Second

// AdvisorList returns the statements that cost the connection's database
// the most time. It runs fixed reads of the engine's statement statistics,
// so it is allowed on read-only and production connections.
func (a *App) AdvisorList(ctx context.Context, connID string, f advisor.Filter) (advisor.List, error) {
	ctx, cancel := context.WithTimeout(ctx, advisorTimeout)
	defer cancel()
	c, _, err := a.conns.Get(ctx, connID)
	if err != nil {
		return advisor.List{}, err
	}
	return advisor.Top(ctx, c, f)
}

// AdvisorDetail returns one statement with its current plan. The plan comes
// from an EXPLAIN without ANALYZE, which never runs the statement.
func (a *App) AdvisorDetail(ctx context.Context, connID, id string) (advisor.Detail, error) {
	ctx, cancel := context.WithTimeout(ctx, advisorTimeout)
	defer cancel()
	c, _, err := a.conns.Get(ctx, connID)
	if err != nil {
		return advisor.Detail{}, err
	}
	return advisor.Explain(ctx, c, id)
}

// ListConnections returns all saved profiles.
func (a *App) ListConnections(ctx context.Context) ([]ConnectionDTO, error) {
	ps, err := a.conns.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ConnectionDTO, len(ps))
	for i, p := range ps {
		out[i] = connectionDTO(p)
	}
	return out, nil
}

// CreateConnection saves a new profile.
func (a *App) CreateConnection(ctx context.Context, in ConnectionInput) (ConnectionDTO, error) {
	p, err := a.conns.Create(ctx, in.profile(), in.passwordOrEmpty())
	return connectionDTO(p), err
}

// UpdateConnection saves changes; a nil Password keeps the stored one.
func (a *App) UpdateConnection(ctx context.Context, id string, in ConnectionInput) (ConnectionDTO, error) {
	// Saving reopens the pool, which would wait on a pinned transaction.
	if err := a.runner.RollbackConn(ctx, id); err != nil {
		a.log.Warn("rollback before update failed", "conn", id, "err", err)
	}
	p := in.profile()
	p.ID = id
	saved, err := a.conns.Update(ctx, p, in.Password)
	a.schemas.Forget(id) // it may point at another database now
	return connectionDTO(saved), err
}

// DeleteConnection removes a profile and its stored password.
func (a *App) DeleteConnection(ctx context.Context, id string) error {
	if err := a.runner.RollbackConn(ctx, id); err != nil {
		a.log.Warn("rollback before delete failed", "conn", id, "err", err)
	}
	a.schemas.Forget(id)
	return a.conns.Delete(ctx, id)
}

// TestConnection connects with unsaved settings.
func (a *App) TestConnection(ctx context.Context, in ConnectionInput) error {
	return a.conns.Test(ctx, in.profile(), in.passwordOrEmpty())
}

// AuditList returns a page of audit entries.
func (a *App) AuditList(ctx context.Context, f AuditFilter) ([]AuditDTO, error) {
	es, err := a.audit.List(ctx, f.filter())
	if err != nil {
		return nil, err
	}
	out := make([]AuditDTO, len(es))
	for i, e := range es {
		out[i] = auditDTO(e)
	}
	return out, nil
}

// AuditVerify checks the whole hash chain.
func (a *App) AuditVerify(ctx context.Context) (audit.VerifyResult, error) {
	return a.audit.Verify(ctx)
}

// AuditExport writes the log to path (chosen by the user in a save dialog)
// as "csv" or "json". The file is created 0600 and must not already exist.
func (a *App) AuditExport(ctx context.Context, path string, format string, f AuditFilter) (err error) {
	file, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("app: export: %w", err)
	}
	defer func() {
		if cerr := file.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("app: export: %w", cerr)
		}
		if err != nil {
			_ = os.Remove(file.Name())
		}
	}()
	return a.audit.Export(ctx, file, audit.Format(format), f.filter())
}
