package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/phankieuphu/lumen/internal/secret"
)

// fakePicker answers file dialogs with a fixed path ("" = cancelled), and
// records the window that asked.
type fakePicker struct {
	path      string
	suggested string
	win       WindowID
}

func (f *fakePicker) SaveFile(ctx context.Context, _, filename string) (string, error) {
	f.suggested = filename
	f.win = fakeWindows{}.Caller(ctx)
	return f.path, nil
}

func (f *fakePicker) OpenFile(ctx context.Context, _ string) (string, error) {
	f.win = fakeWindows{}.Caller(ctx)
	return f.path, nil
}

type windowKey struct{}

// inWindow marks ctx as a call from window w, as the shell's context does.
func inWindow(ctx context.Context, w WindowID) context.Context {
	return context.WithValue(ctx, windowKey{}, w)
}

// fakeWindows reads the caller from inWindow, and counts opened windows.
type fakeWindows struct{ opened *int }

func (fakeWindows) Caller(ctx context.Context) WindowID {
	w, _ := ctx.Value(windowKey{}).(WindowID)
	return w
}

func (f fakeWindows) Open(context.Context) error {
	*f.opened++
	return nil
}

func openTestApp(t *testing.T) *App {
	t.Helper()
	a, err := Open(t.Context(), Config{DataDir: t.TempDir(), Version: "test", Secrets: &secret.Memory{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(t.Context()) })
	return a
}

func TestSaveSQL(t *testing.T) {
	ctx := t.Context()
	a := openTestApp(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "orders.sql")
	p := &fakePicker{path: path}
	a.SetFilePicker(p)

	// Without a dialog choice, nothing may be written: the UI cannot pick paths.
	other := filepath.Join(dir, "other.sql")
	if _, err := a.SaveSQL(ctx, other, "SELECT 1"); !errors.Is(err, ErrNotPicked) {
		t.Fatalf("save to an unpicked file: err = %v, want ErrNotPicked", err)
	}
	if _, err := os.Stat(other); !os.IsNotExist(err) {
		t.Fatalf("unpicked file was written: %v", err)
	}

	got, err := a.SaveSQLAs(ctx, "../../evil/query.sql", "SELECT 1;\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != path || got.Name != "orders.sql" {
		t.Fatalf("saved = %+v, want %s", got, path)
	}
	if p.suggested != "query.sql" {
		t.Errorf("suggested name %q, want the base name only", p.suggested)
	}
	// Saving again replaces the file, with no dialog.
	p.path = "should-not-be-used.sql"
	if _, err := a.SaveSQL(ctx, path, "SELECT 2;\n"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "SELECT 2;\n" {
		t.Fatalf("file holds %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".*.tmp")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

func TestSaveSQLAsCancelled(t *testing.T) {
	a := openTestApp(t)
	a.SetFilePicker(&fakePicker{path: ""})
	got, err := a.SaveSQLAs(t.Context(), "query.sql", "SELECT 1")
	if err != nil || got != (SavedFileDTO{}) {
		t.Fatalf("cancelled save = %+v, %v; want empty and no error", got, err)
	}
}

func TestPickedFilesIsBounded(t *testing.T) {
	var p pickedFiles
	for i := range maxPicked + 10 {
		p.add(filepath.Join("/tmp", string(rune('a'+i%26)), string(rune('0'+i))))
	}
	if len(p.order) != maxPicked || len(p.set) != maxPicked {
		t.Fatalf("remembered %d/%d files, want %d", len(p.order), len(p.set), maxPicked)
	}
}

func TestOpenSQL(t *testing.T) {
	ctx := t.Context()
	a := openTestApp(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "report.sql")
	if err := os.WriteFile(path, []byte("SELECT 'é';\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.SetFilePicker(&fakePicker{path: path})

	got, err := a.OpenSQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.SQL != "SELECT 'é';\n" || got.Name != "report.sql" || got.Path != path {
		t.Fatalf("opened %+v", got)
	}
	// An opened file can be saved back without Save As.
	if _, err := a.SaveSQL(ctx, path, "SELECT 2;\n"); err != nil {
		t.Fatalf("save to the opened file: %v", err)
	}

	binary := filepath.Join(dir, "image.sql")
	if err := os.WriteFile(binary, []byte{0xff, 0xfe, 0x00}, 0o600); err != nil {
		t.Fatal(err)
	}
	a.SetFilePicker(&fakePicker{path: binary})
	if _, err := a.OpenSQL(ctx); err == nil {
		t.Fatal("opened a file that is not UTF-8 text")
	}
	if _, err := a.SaveSQL(ctx, binary, "x"); !errors.Is(err, ErrNotPicked) {
		t.Fatalf("a file that failed to open became saveable: %v", err)
	}

	a.SetFilePicker(&fakePicker{path: ""})
	if got, err := a.OpenSQL(ctx); err != nil || got != (OpenedFileDTO{}) {
		t.Fatalf("cancelled open = %+v, %v", got, err)
	}
}

func TestUnsavedPerWindow(t *testing.T) {
	type set struct {
		win     WindowID
		unsaved bool
	}
	tests := []struct {
		name    string
		sets    []set
		forget  []WindowID
		want1   bool
		want2   bool
		wantAny bool
	}{
		{name: "fresh app"},
		{name: "one dirty window", sets: []set{{1, true}}, want1: true, wantAny: true},
		{name: "saved again", sets: []set{{1, true}, {1, false}}},
		{
			name: "forgetting one of two keeps the other", sets: []set{{1, true}, {2, true}}, forget: []WindowID{1},
			want2: true, wantAny: true,
		},
		{name: "forgetting all", sets: []set{{1, true}, {2, true}}, forget: []WindowID{1, 2}},
		{name: "unknown window", sets: []set{{0, true}}, wantAny: true},
		{name: "closed window", sets: []set{{1, true}}, forget: []WindowID{1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := openTestApp(t)
			a.SetWindows(fakeWindows{})
			for _, s := range tt.sets {
				a.SetUnsaved(inWindow(t.Context(), s.win), s.unsaved)
			}
			for _, w := range tt.forget {
				a.ForgetWindow(w)
			}
			if a.Unsaved(1) != tt.want1 || a.Unsaved(2) != tt.want2 || a.AnyUnsaved() != tt.wantAny {
				t.Fatalf("Unsaved(1), Unsaved(2), AnyUnsaved = %v, %v, %v; want %v, %v, %v",
					a.Unsaved(1), a.Unsaved(2), a.AnyUnsaved(), tt.want1, tt.want2, tt.wantAny)
			}
		})
	}
}

func TestPickerOpensOnCallingWindow(t *testing.T) {
	a := openTestApp(t)
	p := &fakePicker{path: filepath.Join(t.TempDir(), "q.sql")}
	a.SetFilePicker(p)
	if _, err := a.SaveSQLAs(inWindow(t.Context(), 3), "q.sql", "SELECT 1"); err != nil || p.win != 3 {
		t.Fatalf("save dialog on window %d, %v; want 3", p.win, err)
	}
	if _, err := a.OpenSQL(inWindow(t.Context(), 4)); err != nil || p.win != 4 {
		t.Fatalf("open dialog on window %d, %v; want 4", p.win, err)
	}
}

func TestNewWindow(t *testing.T) {
	a := openTestApp(t)
	if err := a.NewWindow(t.Context()); err == nil {
		t.Fatal("NewWindow without a window manager succeeded")
	}
	opened := 0
	a.SetWindows(fakeWindows{opened: &opened})
	if err := a.NewWindow(t.Context()); err != nil || opened != 1 {
		t.Fatalf("opened %d windows, %v; want 1", opened, err)
	}
}
