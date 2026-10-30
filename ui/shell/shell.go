// Package shell hosts the desktop window. It is the only package allowed to
// import Wails: it binds *app.App methods to the frontend and implements
// app.Emitter with Wails events.
//
// Until the Wails v3 + React skeleton lands (phase 1, UI tasks), Run keeps
// the services alive headless so the backend can be exercised end to end.
package shell

import (
	"context"
	"log/slog"

	"github.com/phankieuphu/lumen/internal/app"
)

// Run blocks until ctx is cancelled.
func Run(ctx context.Context, a *app.App, log *slog.Logger) error {
	a.SetEmitter(logEmitter{log: log})
	log.Info("UI shell not built yet; running headless until interrupted")
	<-ctx.Done()
	return nil
}

type logEmitter struct{ log *slog.Logger }

func (e logEmitter) Emit(event string, _ any) { e.log.Debug("event", "name", event) }
