package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

// FilePicker asks the user for a file with a native dialog. The shell
// implements it; the UI never passes a path of its own choosing to a write.
type FilePicker interface {
	// SaveFile asks where to save, suggesting filename. It returns "" when
	// the user cancels.
	SaveFile(title, filename string) (string, error)
	// OpenFile asks for a file to open. It returns "" when the user cancels.
	OpenFile(title string) (string, error)
}

// ErrNotPicked is returned for a save to a file the user did not choose in
// the save dialog during this session.
var ErrNotPicked = errors.New("app: save: choose the file with Save As first")

// MaxSQLFileBytes bounds a saved SQL file.
const MaxSQLFileBytes = 32 << 20

// maxPicked bounds the remembered files; the oldest are forgotten first.
const maxPicked = 256

// pickedFiles are the files the user chose in a save dialog, the only ones
// SaveSQL may write. Safe for concurrent use.
type pickedFiles struct {
	mu    sync.Mutex
	order []string
	set   map[string]bool
}

func (p *pickedFiles) add(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.set == nil {
		p.set = map[string]bool{}
	}
	if p.set[path] {
		return
	}
	if len(p.order) == maxPicked {
		delete(p.set, p.order[0])
		p.order = p.order[1:]
	}
	p.order = append(p.order, path)
	p.set[path] = true
}

func (p *pickedFiles) has(path string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.set[path]
}

type noPicker struct{}

func (noPicker) SaveFile(string, string) (string, error) {
	return "", errors.New("app: no file dialog available")
}

func (noPicker) OpenFile(string) (string, error) {
	return "", errors.New("app: no file dialog available")
}

// SetFilePicker installs the native file dialog. Call it before the UI
// starts issuing requests.
func (a *App) SetFilePicker(p FilePicker) { a.picker = p }

// SaveSQLAs asks the user where to save sql, suggesting filename, and writes
// it there. A cancelled dialog returns an empty SavedFileDTO and no error.
func (a *App) SaveSQLAs(ctx context.Context, filename, sql string) (SavedFileDTO, error) {
	if len(sql) > MaxSQLFileBytes {
		return SavedFileDTO{}, fmt.Errorf("app: save: SQL is over %d MB", MaxSQLFileBytes>>20)
	}
	path, err := a.picker.SaveFile("Save SQL", filepath.Base(filename))
	if err != nil || path == "" {
		return SavedFileDTO{}, err
	}
	path = filepath.Clean(path)
	a.picked.add(path)
	return a.SaveSQL(ctx, path, sql)
}

// SaveSQL writes sql to path, a file the user chose with SaveSQLAs during
// this session, replacing it. The write is atomic: a crash leaves the old
// file or the new one, never half of it.
func (a *App) SaveSQL(_ context.Context, path, sql string) (SavedFileDTO, error) {
	path = filepath.Clean(path)
	if !a.picked.has(path) {
		return SavedFileDTO{}, ErrNotPicked
	}
	if len(sql) > MaxSQLFileBytes {
		return SavedFileDTO{}, fmt.Errorf("app: save: SQL is over %d MB", MaxSQLFileBytes>>20)
	}
	if err := writeAtomic(path, []byte(sql)); err != nil {
		return SavedFileDTO{}, fmt.Errorf("app: save: %w", err)
	}
	return SavedFileDTO{Path: path, Name: filepath.Base(path)}, nil
}

// OpenSQL asks the user for a SQL file and returns its text. The file may
// then be saved again with SaveSQL. A cancelled dialog returns an empty
// OpenedFileDTO and no error.
func (a *App) OpenSQL(_ context.Context) (OpenedFileDTO, error) {
	path, err := a.picker.OpenFile("Open SQL")
	if err != nil || path == "" {
		return OpenedFileDTO{}, err
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return OpenedFileDTO{}, fmt.Errorf("app: open: %w", err)
	}
	if info.Size() > MaxSQLFileBytes {
		return OpenedFileDTO{}, fmt.Errorf("app: open: %s is over %d MB", info.Name(), MaxSQLFileBytes>>20)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return OpenedFileDTO{}, fmt.Errorf("app: open: %w", err)
	}
	if !utf8.Valid(b) {
		return OpenedFileDTO{}, fmt.Errorf("app: open: %s is not UTF-8 text", info.Name())
	}
	a.picked.add(path)
	return OpenedFileDTO{Path: path, Name: filepath.Base(path), SQL: string(b)}, nil
}

// unsavedFlag is set while the editor has changes to a file not yet saved.
type unsavedFlag struct{ atomic.Bool }

// SetUnsaved records whether the editor's file has unsaved changes, so the
// shell can ask before the window closes or the app quits.
func (a *App) SetUnsaved(_ context.Context, unsaved bool) { a.unsaved.Store(unsaved) }

// Unsaved reports what the editor last passed to SetUnsaved.
func (a *App) Unsaved() bool { return a.unsaved.Load() }

// writeAtomic writes data to a temporary file next to path and renames it
// over path.
func writeAtomic(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Chmod(0o644); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
