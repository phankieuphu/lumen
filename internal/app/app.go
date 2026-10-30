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

	"github.com/phankieuphu/lumen/internal/audit"
	"github.com/phankieuphu/lumen/internal/conn"
	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/query"
	"github.com/phankieuphu/lumen/internal/secret"
	"github.com/phankieuphu/lumen/internal/store"

	// Engines register themselves.
	_ "github.com/phankieuphu/lumen/internal/driver/mysql"
	_ "github.com/phankieuphu/lumen/internal/driver/postgres"
	_ "github.com/phankieuphu/lumen/internal/driver/sqlite"
)

// Event names emitted to the UI.
const (
	EventQueryPage = "query:page"
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
	emitter   Emitter
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
	}
	a.runner = query.NewRunner(a.conns, a.audit, a.history, cfg.Logger)
	if a.sessionID, err = st.StartAppSession(ctx, cfg.Version); err != nil {
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
		a.conns.CloseAll(),
		a.store.EndAppSession(ctx, a.sessionID),
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
	return summaryDTO(sum), err
}

// CancelQuery cancels a running request.
func (a *App) CancelQuery(ctx context.Context, requestID string) error {
	return a.runner.Cancel(ctx, requestID)
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
	p := in.profile()
	p.ID = id
	saved, err := a.conns.Update(ctx, p, in.Password)
	return connectionDTO(saved), err
}

// DeleteConnection removes a profile and its stored password.
func (a *App) DeleteConnection(ctx context.Context, id string) error {
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
