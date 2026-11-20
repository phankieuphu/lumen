//go:build integration

package locks_test

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"

	"github.com/phankieuphu/lumen/internal/driver"
	_ "github.com/phankieuphu/lumen/internal/driver/mysql"
	_ "github.com/phankieuphu/lumen/internal/driver/postgres"
	"github.com/phankieuphu/lumen/internal/locks"
)

const update = `UPDATE lumen_lock_t SET v = v + 1 WHERE id = 1`

type engine struct {
	dialect driver.Dialect
	cfg     driver.Config
	begin   string
	// table and own are the locks a session that ran update holds.
	table, own func(l locks.Lock) bool
	// waiting is the lock a second session waits for on the same row.
	waiting func(l locks.Lock) bool
}

func TestPostgres(t *testing.T) {
	dsn := os.Getenv("LUMEN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("LUMEN_TEST_POSTGRES_DSN not set")
	}
	pc, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	run(t, engine{
		dialect: driver.Postgres,
		cfg:     driver.Config{Host: pc.Host, Port: int(pc.Port), User: pc.User, Password: pc.Password, Database: pc.Database, TLS: driver.TLSDisable},
		begin:   `BEGIN`,
		table: func(l locks.Lock) bool {
			return l.Type == locks.TypeTable && l.Object == "lumen_lock_t" && l.Mode == "RowExclusiveLock" && l.Granted
		},
		own: func(l locks.Lock) bool { return l.Type == locks.TypeTransaction && l.Granted },
		waiting: func(l locks.Lock) bool {
			return !l.Granted && (l.Type == locks.TypeTransaction || l.Type == locks.TypeRow)
		},
	})
}

func TestMySQL(t *testing.T) {
	dsn := os.Getenv("LUMEN_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("LUMEN_TEST_MYSQL_DSN not set")
	}
	mc, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(mc.Addr)
	p, _ := strconv.Atoi(port)
	cfg := driver.Config{Host: host, Port: p, User: mc.User, Password: mc.Passwd, Database: mc.DBName, TLS: driver.TLSDisable}

	// The test user has no grant on performance_schema, as most
	// application users do not: the error says what to run.
	c := open(t.Context(), t, driver.MySQL, cfg)
	if _, err := locks.Held(t.Context(), c, 1); !errors.Is(err, locks.ErrNoAccess) || !strings.Contains(err.Error(), "GRANT SELECT ON performance_schema.data_locks") {
		t.Fatalf("err = %v, want ErrNoAccess with the GRANT to run", err)
	}
	// The test containers give root the same password.
	cfg.User = "root"
	run(t, engine{
		dialect: driver.MySQL,
		cfg:     cfg,
		begin:   `START TRANSACTION`,
		table: func(l locks.Lock) bool {
			return l.Type == locks.TypeTable && strings.HasSuffix(l.Object, "lumen_lock_t") && l.Mode == "IX" && l.Granted
		},
		own: func(l locks.Lock) bool {
			return l.Type == locks.TypeRow && l.Granted && strings.Contains(l.Mode, "X") && strings.Contains(l.Detail, "PRIMARY")
		},
		waiting: func(l locks.Lock) bool { return l.Type == locks.TypeRow && !l.Granted },
	})
}

func run(t *testing.T, e engine) {
	ctx := t.Context()
	c := open(ctx, t, e.dialect, e.cfg)
	exec(ctx, t, c, `DROP TABLE IF EXISTS lumen_lock_t`, `CREATE TABLE lumen_lock_t (id INT PRIMARY KEY, v INT)`, `INSERT INTO lumen_lock_t VALUES (1, 0), (2, 0)`)
	t.Cleanup(func() { exec(context.WithoutCancel(ctx), t, c, `DROP TABLE IF EXISTS lumen_lock_t`) })

	// a holds a row lock until it rolls back.
	a, aid := session(ctx, t, c, e)
	defer func() { _, _ = a.Exec(context.WithoutCancel(ctx), "ROLLBACK"); _ = a.Close() }()
	if _, err := a.Exec(ctx, update); err != nil {
		t.Fatal(err)
	}

	t.Run("locks held after an update", func(t *testing.T) {
		r, err := locks.Held(ctx, c, aid)
		if err != nil {
			t.Fatal(err)
		}
		if r.Waiting || !has(r, e.table) || !has(r, e.own) || r.Note == "" {
			t.Fatalf("report = %+v", r)
		}
		for _, l := range r.Locks {
			if l.Meaning == "" {
				t.Errorf("no meaning for %+v", l)
			}
		}
	})

	t.Run("a session waiting on the same row", func(t *testing.T) {
		b, bid := session(ctx, t, c, e)
		var wg sync.WaitGroup
		wg.Go(func() {
			// Blocks until a rolls back below.
			_, _ = b.Exec(context.WithoutCancel(ctx), update)
		})
		defer func() {
			_, _ = a.Exec(context.WithoutCancel(ctx), "ROLLBACK")
			wg.Wait()
			_, _ = b.Exec(context.WithoutCancel(ctx), "ROLLBACK")
			_ = b.Close()
		}()
		deadline := time.Now().Add(10 * time.Second)
		for {
			r, err := locks.Held(ctx, c, bid)
			if err != nil {
				t.Fatal(err)
			}
			if r.Waiting {
				if !has(r, e.waiting) || !strings.HasPrefix(waitingMeaning(r), "Waiting") {
					t.Fatalf("waiting report = %+v", r)
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("never saw the wait: %+v", r)
			}
			time.Sleep(100 * time.Millisecond)
		}
	})

	t.Run("no locks for an idle session", func(t *testing.T) {
		s, id := session(ctx, t, c, e)
		defer func() { _, _ = s.Exec(context.WithoutCancel(ctx), "ROLLBACK"); _ = s.Close() }()
		r, err := locks.Held(ctx, c, id)
		if err != nil || r.Waiting || r.Locks == nil {
			t.Fatalf("report = %+v, %v", r, err)
		}
		for _, l := range r.Locks {
			if l.Type == locks.TypeTable || l.Type == locks.TypeRow {
				t.Fatalf("idle session holds %+v", l)
			}
		}
	})
}

// session opens a session in a transaction and returns its database id.
func session(ctx context.Context, t *testing.T, c driver.Conn, e engine) (driver.Session, int64) {
	t.Helper()
	s, err := c.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(ctx, e.begin); err != nil {
		t.Fatal(err)
	}
	id, err := locks.SessionID(ctx, s, e.dialect)
	if err != nil {
		t.Fatal(err)
	}
	return s, id
}

func has(r locks.Report, match func(locks.Lock) bool) bool {
	for _, l := range r.Locks {
		if match(l) {
			return true
		}
	}
	return false
}

func waitingMeaning(r locks.Report) string {
	for _, l := range r.Locks {
		if !l.Granted {
			return l.Meaning
		}
	}
	return ""
}

func open(ctx context.Context, t *testing.T, d driver.Dialect, cfg driver.Config) driver.Conn {
	t.Helper()
	c, err := driver.Open(ctx, d, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func exec(ctx context.Context, t *testing.T, c driver.Conn, stmts ...string) {
	t.Helper()
	s, err := c.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for _, q := range stmts {
		if _, err := s.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}
