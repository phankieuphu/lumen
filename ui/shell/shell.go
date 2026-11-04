// Package shell hosts the desktop window. It is the only package allowed to
// import Wails: it serves the built frontend, binds app.API to it, and
// implements app.Emitter with Wails events.
package shell

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/phankieuphu/lumen/internal/app"
)

// dist is the frontend build (`npm run build` in ui/frontend writes here).
// A fresh checkout holds only a placeholder, so Go builds and tests work
// without Node; the window then explains how to build the UI.
//
//go:embed all:dist
var dist embed.FS

// icon is the window and taskbar icon (assets/icon/lumen-tile.svg at
// 512px). macOS takes the Dock icon from the app bundle instead.
//
//go:embed icon.png
var icon []byte

// Window size and colour; the colour matches the UI's --bg so the window
// does not flash white while the page loads.
const (
	width, height       = 1440, 900
	minWidth, minHeight = 960, 600
)

var background = application.NewRGB(0x12, 0x15, 0x1a)

// appID identifies the app to the desktop: the macOS bundle identifier and
// the Linux application id (scripts/package.sh, assets/linux).
const appID = "io.github.phankieuphu.lumen"

// Run opens the window and blocks until it closes or ctx is cancelled.
func Run(ctx context.Context, a *app.App, log *slog.Logger) error {
	assets, err := fs.Sub(dist, "dist")
	if err != nil {
		return fmt.Errorf("shell: frontend assets: %w", err)
	}

	w := application.New(application.Options{
		Name:        "lumen",
		Description: "Desktop SQL client",
		Icon:        icon,
		// Wails logs every bound call's arguments at debug level, and those
		// include connection passwords. Its logger never goes below Warn.
		Logger:   slog.New(minLevel{h: log.Handler(), min: slog.LevelWarn}),
		Services: []application.Service{application.NewService(app.NewAPI(a))},
		Assets:   application.AssetOptions{Handler: assetHandler(assets)},
		Mac:      application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		// Matches assets/linux/<id>.desktop, which gives the window its icon:
		// GTK 4 has no API to set one from Icon.
		Linux: application.LinuxOptions{ApplicationID: appID},
	})
	a.SetEmitter(emitter{w})

	w.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "lumen",
		Width:            width,
		Height:           height,
		MinWidth:         minWidth,
		MinHeight:        minHeight,
		BackgroundColour: background,
		URL:              "/",
	})

	// Quit on SIGINT/SIGTERM. The goroutine ends when Run returns, whichever
	// way the app stops.
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			w.Quit()
		case <-stopped:
		}
	}()

	if err := w.Run(); err != nil {
		return fmt.Errorf("shell: %w", err)
	}
	return nil
}

// assetHandler serves the frontend build, or a short help page when the
// binary was built without one.
func assetHandler(assets fs.FS) http.Handler {
	if _, err := fs.Stat(assets, "index.html"); err == nil {
		return application.AssetFileServerFS(assets)
	}
	return http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte(notBuiltPage))
	})
}

const notBuiltPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>lumen</title>
<style>body{margin:0;display:grid;place-items:center;height:100vh;background:#12151a;color:#e7eaf0;font:15px/1.5 system-ui,sans-serif}
code{background:#1f242c;padding:2px 6px;border-radius:4px}</style></head>
<body><div><h1>The UI is not built into this binary</h1>
<p>Build the frontend, then build lumen again:</p>
<p><code>make app</code></p></div></body></html>`

// minLevel drops records below min, whatever the wrapped handler allows.
type minLevel struct {
	h   slog.Handler
	min slog.Level
}

func (m minLevel) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= m.min && m.h.Enabled(ctx, l)
}

//nolint:wrapcheck // a pass-through handler adds no context
func (m minLevel) Handle(ctx context.Context, r slog.Record) error { return m.h.Handle(ctx, r) }

func (m minLevel) WithAttrs(as []slog.Attr) slog.Handler {
	return minLevel{h: m.h.WithAttrs(as), min: m.min}
}

func (m minLevel) WithGroup(name string) slog.Handler {
	return minLevel{h: m.h.WithGroup(name), min: m.min}
}

// emitter sends app events to the window as Wails events.
type emitter struct{ w *application.App }

func (e emitter) Emit(event string, data any) { e.w.Event.Emit(event, data) }
