package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// WindowID identifies a UI window. The shell assigns IDs; 0 means no known
// window.
type WindowID uint

// Windows tells which window a UI call came from, and opens new ones. The
// shell implements it.
type Windows interface {
	// Caller returns the window whose bound call carries ctx, or 0.
	Caller(ctx context.Context) WindowID
	// Open opens another window with the full UI.
	Open(ctx context.Context) error
}

type noWindows struct{}

func (noWindows) Caller(context.Context) WindowID { return 0 }

func (noWindows) Open(context.Context) error { return errors.New("app: no window manager available") }

// SetWindows installs the window manager. Call it before the UI starts
// issuing requests.
func (a *App) SetWindows(w Windows) { a.windows = w }

// NewWindow opens another window with the full UI. Each window has its own
// tabs; connections, history, and open transactions are shared.
func (a *App) NewWindow(ctx context.Context) error {
	if err := a.windows.Open(ctx); err != nil {
		return fmt.Errorf("app: new window: %w", err)
	}
	return nil
}

// WindowClosed cancels the statements window w started and drops what it
// reported, so neither outlives it. The shell calls it once w has closed.
func (a *App) WindowClosed(w WindowID) { a.wins.closed(w) }

// windowScoped derives from ctx, a UI call's, a context cancelled when the
// calling window closes, so a statement it started does not hold a session
// or the connection's transaction after nobody can see or cancel it. Call
// done when the call returns.
func (a *App) windowScoped(ctx context.Context) (_ context.Context, _ WindowID, done func()) {
	w := a.windows.Caller(ctx)
	ctx, cancel := context.WithCancel(ctx)
	life := a.wins.life(w)
	if life == nil {
		return ctx, w, cancel
	}
	stop := context.AfterFunc(life, cancel)
	return ctx, w, func() {
		stop()
		cancel()
	}
}

// windowState tracks, per window, whether its editor has unsaved changes and
// a context its statements stop with when it closes. Window 0, the unknown
// window, gets no such context. Safe for concurrent use.
type windowState struct {
	mu    sync.Mutex
	dirty map[WindowID]struct{}
	lives map[WindowID]context.CancelFunc
	ctxs  map[WindowID]context.Context
	// gone holds closed windows, so a call still in flight from one cannot
	// mark it dirty or start a statement after it closed. Window IDs are
	// never reused.
	gone map[WindowID]struct{}
}

func (s *windowState) setUnsaved(w WindowID, unsaved bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.gone[w]; ok || !unsaved {
		delete(s.dirty, w)
		return
	}
	if s.dirty == nil {
		s.dirty = map[WindowID]struct{}{}
	}
	s.dirty[w] = struct{}{}
}

func (s *windowState) unsaved(w WindowID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.dirty[w]
	return ok
}

func (s *windowState) anyUnsaved() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.dirty) > 0
}

// life returns a context done once w closes (already done if it has), or
// nil for window 0.
func (s *windowState) life(w WindowID) context.Context {
	if w == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx, ok := s.ctxs[w]; ok {
		return ctx
	}
	ctx, cancel := context.WithCancel(context.Background())
	if _, ok := s.gone[w]; ok {
		cancel()
		return ctx
	}
	if s.ctxs == nil {
		s.ctxs, s.lives = map[WindowID]context.Context{}, map[WindowID]context.CancelFunc{}
	}
	s.ctxs[w], s.lives[w] = ctx, cancel
	return ctx
}

func (s *windowState) closed(w WindowID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel, ok := s.lives[w]; ok {
		cancel()
		delete(s.lives, w)
		delete(s.ctxs, w)
	}
	delete(s.dirty, w)
	if w == 0 {
		return
	}
	if s.gone == nil {
		s.gone = map[WindowID]struct{}{}
	}
	s.gone[w] = struct{}{}
}
