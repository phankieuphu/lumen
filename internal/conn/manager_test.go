package conn

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/patrick-rontech/kiemdb/internal/driver"
	_ "github.com/patrick-rontech/kiemdb/internal/driver/sqlite"
	"github.com/patrick-rontech/kiemdb/internal/secret"
	"github.com/patrick-rontech/kiemdb/internal/store"
)

func newManager(t *testing.T) (*Manager, *secret.Memory) {
	t.Helper()
	s, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sec := &secret.Memory{}
	m := NewManager(s, sec)
	t.Cleanup(func() {
		_ = m.CloseAll()
		_ = s.Close()
	})
	return m, sec
}

func sqliteProfile(t *testing.T, name string) Profile {
	return Profile{Name: name, Dialect: driver.SQLite, Database: filepath.Join(t.TempDir(), name+".db")}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		p    Profile
		ok   bool
	}{
		{"sqlite_ok", Profile{Name: "a", Dialect: driver.SQLite, Database: "/x.db"}, true},
		{"postgres_ok", Profile{Name: "a", Dialect: driver.Postgres, Host: "h"}, true},
		{"missing_name", Profile{Dialect: driver.SQLite, Database: "/x.db"}, false},
		{"postgres_needs_host", Profile{Name: "a", Dialect: driver.Postgres}, false},
		{"unknown_dialect", Profile{Name: "a", Dialect: "oracle", Host: "h"}, false},
		{"bad_port", Profile{Name: "a", Dialect: driver.MySQL, Host: "h", Port: 70000}, false},
		{"bad_env", Profile{Name: "a", Dialect: driver.MySQL, Host: "h", Env: "qa"}, false},
		{"pool_too_big", Profile{Name: "a", Dialect: driver.MySQL, Host: "h", MaxConns: 1000}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.p.Validate()
			if (err == nil) != tt.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatalf("error does not wrap ErrInvalid: %v", err)
			}
		})
	}
}

func TestCRUDKeepsPasswordOutOfStore(t *testing.T) {
	m, sec := newManager(t)
	ctx := t.Context()
	p, err := m.Create(ctx, Profile{Name: "prod-db", Dialect: driver.Postgres, Host: "db", Env: EnvProd}, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" || p.TLS != driver.TLSPrefer {
		t.Fatalf("created = %+v", p)
	}
	if pw, _ := sec.Get(p.ID); pw != "s3cret" {
		t.Fatal("password not in secret store")
	}
	var leaked int
	if err := m.repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM connections
		WHERE id || name || host || username || database || tls || env LIKE '%s3cret%'`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked > 0 {
		t.Fatal("password stored in connections table")
	}

	if _, err := m.Create(ctx, Profile{Name: "prod-db", Dialect: driver.Postgres, Host: "db2"}, ""); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate name: err = %v", err)
	}

	p.Host = "db.internal"
	if _, err := m.Update(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	got, err := m.Profile(ctx, p.ID)
	if err != nil || got.Host != "db.internal" || !got.CreatedAt.Equal(p.CreatedAt) {
		t.Fatalf("after update = %+v, %v", got, err)
	}
	if pw, _ := sec.Get(p.ID); pw != "s3cret" {
		t.Fatal("nil password should keep the stored one")
	}

	list, err := m.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}

	if err := m.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sec.Get(p.ID); !errors.Is(err, secret.ErrNotFound) {
		t.Fatal("password not deleted with profile")
	}
	if _, err := m.Profile(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted: %v", err)
	}
}

func TestGetOpensOncePerProfile(t *testing.T) {
	m, _ := newManager(t)
	p, err := m.Create(t.Context(), sqliteProfile(t, "local"), "")
	if err != nil {
		t.Fatal(err)
	}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		conns = map[driver.Conn]bool{}
	)
	for range 16 {
		wg.Go(func() {
			c, _, err := m.Get(t.Context(), p.ID)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			conns[c] = true
			mu.Unlock()
		})
	}
	wg.Wait()
	if len(conns) != 1 {
		t.Fatalf("opened %d pools, want 1", len(conns))
	}
	if _, ok := m.Stats()[p.ID]; !ok {
		t.Fatal("stats missing open pool")
	}
}

func TestUpdateReopensPool(t *testing.T) {
	m, _ := newManager(t)
	ctx := t.Context()
	p, err := m.Create(ctx, sqliteProfile(t, "local"), "")
	if err != nil {
		t.Fatal(err)
	}
	c1, _, err := m.Get(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	p.ReadOnly = true
	if _, err := m.Update(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	c2, prof, err := m.Get(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c1 == c2 || !prof.ReadOnly {
		t.Fatal("pool not reopened with new settings")
	}
}

func TestGetFailureIsRetried(t *testing.T) {
	m, _ := newManager(t)
	ctx := t.Context()
	p, err := m.Create(ctx, Profile{Name: "missing", Dialect: driver.SQLite,
		Database: filepath.Join(t.TempDir(), "nope", "x.db"), ReadOnly: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Get(ctx, p.ID); err == nil {
		t.Fatal("expected open failure for missing read-only file")
	}
	if len(m.Stats()) != 0 || len(m.pools) != 0 {
		t.Fatal("failed open was cached")
	}
	if _, _, err := m.Get(ctx, "no-such-id"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}
