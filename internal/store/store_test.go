package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenCreatesPrivateFileAndMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested ?#", "kiemdb.db")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Fatalf("file mode = %o, want 600", perm)
		}
	}
	var v int
	if err := s.DB().QueryRowContext(t.Context(), `SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Fatalf("version = %d", v)
	}
}

func TestReopenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kiemdb.db")
	for range 2 {
		s, err := Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRefusesNewerDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kiemdb.db")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(t.Context(), `INSERT INTO schema_migrations VALUES (999, 'x')`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(t.Context(), path); err == nil {
		t.Fatal("opened a database from a newer app version")
	}
}

func TestSettings(t *testing.T) {
	s, err := OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	if _, err := s.Setting(ctx, "theme"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	for _, v := range []string{"dark", "light"} {
		if err := s.SetSetting(ctx, "theme", v); err != nil {
			t.Fatal(err)
		}
		got, err := s.Setting(ctx, "theme")
		if err != nil || got != v {
			t.Fatalf("Setting = %q, %v; want %q", got, err, v)
		}
	}
}

func TestAppSessions(t *testing.T) {
	s, err := OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, err := s.StartAppSession(t.Context(), "v0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EndAppSession(t.Context(), id); err != nil {
		t.Fatal(err)
	}
}

func TestAuditLogIsAppendOnly(t *testing.T) {
	s, err := OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO audit_log
		(at, conn_id, conn_name, dialect, env, kind, status, rows_affected, duration_ms, sql_text, error, prev_hash, hash)
		VALUES ('t', 'c', 'n', 'postgres', 'dev', 'write', 'ok', 1, 1, 'DELETE', '', x'00', x'01')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE audit_log SET sql_text = 'SELECT 1'`); err == nil {
		t.Fatal("update succeeded on audit_log")
	}
	if _, err := s.DB().ExecContext(ctx, `DELETE FROM audit_log`); err == nil {
		t.Fatal("delete succeeded on audit_log")
	}
}
