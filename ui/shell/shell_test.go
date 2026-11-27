package shell

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/phankieuphu/lumen/internal/app"
)

func TestMinLevelDropsDebugEvenWhenAppLogsDebug(t *testing.T) {
	var buf bytes.Buffer
	app := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	log := slog.New(minLevel{h: app, min: slog.LevelWarn}).With("component", "wails")

	log.Debug("Binding call complete", "args", `[{"password":"hunter2"}]`)
	log.Info("info")
	log.Warn("warned")

	out := buf.String()
	if strings.Contains(out, "hunter2") || strings.Contains(out, "info") {
		t.Fatalf("records below Warn were logged:\n%s", out)
	}
	if !strings.Contains(out, "warned") || !strings.Contains(out, "component=wails") {
		t.Fatalf("Warn record or attrs missing:\n%s", out)
	}
}

func TestAssetHandler(t *testing.T) {
	tests := []struct {
		name string
		fs   fstest.MapFS
		want string
	}{
		{"built UI is served", fstest.MapFS{"index.html": {Data: []byte("<p>app</p>")}}, "<p>app</p>"},
		{"missing build shows help", fstest.MapFS{".gitkeep": {}}, "make app"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			assetHandler(tt.fs).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
			if !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("body %q does not contain %q", rec.Body.String(), tt.want)
			}
		})
	}
}

// fakeWin is a window with only an ID; calling anything else panics.
type fakeWin struct {
	application.Window
	id uint
}

func (f fakeWin) ID() uint { return f.id }

func TestCaller(t *testing.T) {
	ws := &windows{}
	if got := ws.Caller(t.Context()); got != 0 {
		t.Fatalf("Caller without a window = %d, want 0", got)
	}
	ctx := context.WithValue(t.Context(), application.WindowKey, application.Window(fakeWin{id: 4}))
	if got := ws.Caller(ctx); got != 4 {
		t.Fatalf("Caller = %d, want 4", got)
	}
}

// unsaved is a fake unsavedState: the dirty windows.
type unsaved map[app.WindowID]bool

func (u unsaved) Unsaved(w app.WindowID) bool { return u[w] }

func (u unsaved) AnyUnsaved() bool { return len(u) > 0 }

func (u unsaved) ForgetWindow(w app.WindowID) { delete(u, w) }

func TestGuardSettled(t *testing.T) {
	tests := []struct {
		name      string
		dirty     unsaved
		quitting  bool
		closing   app.WindowID
		wantClose bool
		wantQuit  bool
	}{
		{name: "nothing unsaved", dirty: unsaved{}, closing: 1, wantClose: true, wantQuit: true},
		{name: "another window is dirty", dirty: unsaved{2: true}, closing: 1, wantClose: true},
		{name: "this window is dirty", dirty: unsaved{1: true}, closing: 1},
		{name: "agreed to quit", dirty: unsaved{1: true, 2: true}, quitting: true, closing: 1, wantClose: true, wantQuit: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &guard{lumen: tt.dirty}
			g.quitting.Store(tt.quitting)
			if got := g.closeSettled(tt.closing); got != tt.wantClose {
				t.Errorf("closeSettled(%d) = %v, want %v", tt.closing, got, tt.wantClose)
			}
			if got := g.quitSettled(); got != tt.wantQuit {
				t.Errorf("quitSettled() = %v, want %v", got, tt.wantQuit)
			}
		})
	}
}
