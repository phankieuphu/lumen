// Package shell hosts the desktop windows. It is the only package allowed to
// import Wails: it serves the built frontend, binds app.API to it, and
// implements app.Emitter, app.Windows, and app.FilePicker with Wails.
package shell

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

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

// Run opens the first window and blocks until the last one closes or ctx is
// cancelled.
func Run(ctx context.Context, a *app.App, log *slog.Logger) error {
	assets, err := fs.Sub(dist, "dist")
	if err != nil {
		return fmt.Errorf("shell: frontend assets: %w", err)
	}

	g := &guard{lumen: a}
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
		// Runs on the main thread, where a dialog cannot be waited for: the
		// quit is held while the guard asks, and repeated if the user agrees.
		ShouldQuit: func() bool {
			if g.quitSettled() {
				return true
			}
			go func() {
				if g.confirmQuit() {
					g.app.Quit()
				}
			}()
			return false
		},
		// Matches assets/linux/<id>.desktop, which gives the window its icon:
		// GTK 4 has no API to set one from Icon.
		Linux: application.LinuxOptions{ApplicationID: appID},
	})
	a.SetEmitter(emitter{w})
	a.SetFilePicker(picker{app: w})
	g.app = w
	ws := &windows{app: w, lumen: a, g: g}
	a.SetWindows(ws)
	ws.open()

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

// callerWindow returns the window whose bound call carries ctx, or nil.
func callerWindow(ctx context.Context) application.Window {
	w, _ := ctx.Value(application.WindowKey).(application.Window)
	return w
}

// windows opens UI windows and tells the app which one a call came from.
// Each window is a separate UI over the one app.App.
type windows struct {
	app   *application.App
	lumen *app.App
	g     *guard
}

func (ws *windows) Caller(ctx context.Context) app.WindowID {
	if w := callerWindow(ctx); w != nil {
		return app.WindowID(w.ID())
	}
	return 0
}

func (ws *windows) Open(context.Context) error {
	ws.open()
	return nil
}

// open opens a window with the full UI, which asks before closing while its
// SQL file has unsaved changes.
func (ws *windows) open() {
	win := ws.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "lumen",
		Width:            width,
		Height:           height,
		MinWidth:         minWidth,
		MinHeight:        minHeight,
		BackgroundColour: background,
		URL:              "/",
	})
	id := app.WindowID(win.ID())
	// Window hooks run off the main thread, so the dialog can be waited for.
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if !ws.g.confirmClose(win) {
			e.Cancel()
		}
	})
	// Listeners run only once no hook cancelled the close.
	win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) { ws.lumen.WindowClosed(id) })
}

// picker shows native file dialogs, as sheets on the window that asked on
// macOS.
type picker struct{ app *application.App }

// window is the window a dialog for ctx's call attaches to: the caller, or
// the focused window. It may be nil, and the dialog then stands alone.
func (p picker) window(ctx context.Context) application.Window {
	if w := callerWindow(ctx); w != nil {
		return w
	}
	return p.app.Window.Current()
}

func (p picker) SaveFile(ctx context.Context, title, filename string) (string, error) {
	path, err := p.app.Dialog.SaveFile().
		SetMessage(title).
		SetFilename(filename).
		AddFilter("SQL files", "*.sql").
		CanCreateDirectories(true).
		AttachToWindow(p.window(ctx)).
		PromptForSingleSelection()
	if err != nil {
		return "", fmt.Errorf("shell: save dialog: %w", err)
	}
	return path, nil
}

func (p picker) OpenFile(ctx context.Context, title string) (string, error) {
	path, err := p.app.Dialog.OpenFile().
		SetTitle(title).
		CanChooseFiles(true).
		AddFilter("SQL files", "*.sql").
		AddFilter("All files", "*").
		AttachToWindow(p.window(ctx)).
		PromptForSingleSelection()
	if err != nil {
		return "", fmt.Errorf("shell: open dialog: %w", err)
	}
	return path, nil
}

// unsavedState is what the guard reads: app.App's per-window record of
// unsaved SQL files.
type unsavedState interface {
	Unsaved(w app.WindowID) bool
	AnyUnsaved() bool
	ForgetWindow(w app.WindowID)
}

// guard asks before a window closes while its SQL file has unsaved changes,
// and before the app quits while any window's has. Discarding one window's
// changes does not discard another's; once the user agrees to quit, nothing
// asks again.
type guard struct {
	app      *application.App
	lumen    unsavedState
	quitting atomic.Bool
}

// closeSettled reports whether closing window w needs no question.
func (g *guard) closeSettled(w app.WindowID) bool { return g.quitting.Load() || !g.lumen.Unsaved(w) }

// quitSettled reports whether quitting needs no question.
func (g *guard) quitSettled() bool { return g.quitting.Load() || !g.lumen.AnyUnsaved() }

// confirmClose asks, unless settled, whether to close win and lose its
// changes, and blocks until the user answers. Never call it on the main
// thread: the dialog runs there.
func (g *guard) confirmClose(win application.Window) bool {
	id := app.WindowID(win.ID())
	if g.closeSettled(id) {
		return true
	}
	if !g.ask(win, "Your changes to the SQL file in this window will be lost if you don't save them.") {
		return false
	}
	g.lumen.ForgetWindow(id)
	return true
}

// confirmQuit asks, unless settled, whether to quit and lose every window's
// changes. Like confirmClose, never call it on the main thread.
func (g *guard) confirmQuit() bool {
	if g.quitSettled() {
		return true
	}
	if !g.ask(nil, "A window has changes to its SQL file that will be lost if you don't save them.") {
		return false
	}
	g.quitting.Store(true)
	return true
}

// ask shows the warning, on win when it is not nil, and reports whether the
// user chose to discard.
func (g *guard) ask(win application.Window, message string) bool {
	answer := make(chan bool, 1)
	d := g.app.Dialog.Warning().
		SetTitle("Unsaved SQL changes").
		SetMessage(message)
	if win != nil {
		d.AttachToWindow(win)
	}
	d.AddButton("Discard changes").OnClick(func() { answer <- true })
	keep := d.AddButton("Cancel").OnClick(func() { answer <- false })
	d.SetDefaultButton(keep)
	d.SetCancelButton(keep)
	d.Show()
	return <-answer
}

// emitter sends app events to the windows as Wails events.
type emitter struct{ w *application.App }

func (e emitter) Emit(event string, data any) { e.w.Event.Emit(event, data) }

// EmitTo sends to one window only, so other windows do not decode its query
// pages. An event for a window that has closed is dropped.
func (e emitter) EmitTo(w app.WindowID, event string, data any) {
	if w == 0 {
		e.Emit(event, data)
		return
	}
	if win, ok := e.w.Window.GetByID(uint(w)); ok {
		win.DispatchWailsEvent(&application.CustomEvent{Name: event, Data: data})
	}
}
