// Package drivertest is a conformance suite every engine runs against a real
// database, so all engines behave the same way behind driver.Conn.
package drivertest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/patrick-rontech/kiemdb/internal/driver"
)

// Engine describes how to reach and exercise one engine in tests.
type Engine struct {
	Dialect driver.Dialect
	Config  driver.Config
	// SleepSQL runs for at least several seconds, for cancel tests.
	SleepSQL string
	// Table is a unique scratch table name; the suite creates and drops it.
	Table string
}

// Run executes the conformance suite.
func Run(t *testing.T, e Engine) {
	t.Helper()
	ctx := t.Context()
	c, err := driver.Open(ctx, e.Dialect, e.Config)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	mustExec(t, c, "DROP TABLE IF EXISTS "+e.Table)
	mustExec(t, c, "CREATE TABLE "+e.Table+" (id INTEGER PRIMARY KEY, name VARCHAR(50), big BIGINT)")
	t.Cleanup(func() { mustExec(t, c, "DROP TABLE IF EXISTS "+e.Table) })

	t.Run("exec_reports_rows_affected", func(t *testing.T) {
		res := mustExec(t, c, "INSERT INTO "+e.Table+" (id, name, big) VALUES (1, 'a', 9007199254740993), (2, NULL, 2)")
		if res.RowsAffected != 2 {
			t.Fatalf("RowsAffected = %d, want 2", res.RowsAffected)
		}
	})

	t.Run("query_streams_normalized_rows", func(t *testing.T) {
		got := queryAll(t, c, "SELECT id, name, big FROM "+e.Table+" ORDER BY id")
		if len(got.cols) != 3 || !strings.EqualFold(got.cols[1].Name, "name") {
			t.Fatalf("columns = %+v", got.cols)
		}
		if len(got.rows) != 2 {
			t.Fatalf("rows = %d, want 2", len(got.rows))
		}
		if got.rows[0][1] != "a" || got.rows[1][1] != nil {
			t.Fatalf("name values = %#v, %#v", got.rows[0][1], got.rows[1][1])
		}
		if got.rows[0][2] != "9007199254740993" {
			t.Fatalf("big value = %#v, want string beyond 2^53", got.rows[0][2])
		}
	})

	t.Run("empty_result_has_columns", func(t *testing.T) {
		got := queryAll(t, c, "SELECT id, name FROM "+e.Table+" WHERE id < 0")
		if len(got.cols) != 2 || len(got.rows) != 0 {
			t.Fatalf("cols=%d rows=%d", len(got.cols), len(got.rows))
		}
	})

	t.Run("multiple_statements_are_rejected", func(t *testing.T) {
		s := session(t, c)
		_, err := s.Exec(ctx, "SELECT 1; DELETE FROM "+e.Table)
		if err == nil {
			t.Fatal("multi-statement exec succeeded")
		}
		if r, err := s.Query(ctx, "SELECT 1; DELETE FROM "+e.Table); err == nil {
			for r.Next() {
			}
			if r.Close() == nil {
				t.Fatal("multi-statement query succeeded")
			}
		}
		if n := countRows(t, c, e.Table); n != 2 {
			t.Fatalf("table has %d rows after rejected multi-statement, want 2", n)
		}
	})

	t.Run("cancel_stops_running_statement", func(t *testing.T) {
		s := session(t, c)
		done := make(chan error, 1)
		go func() {
			r, err := s.Query(ctx, e.SleepSQL)
			if err == nil {
				for r.Next() {
				}
				err = errors.Join(r.Err(), r.Close())
			}
			done <- err
		}()
		start := time.Now()
		// Retry until the statement is on the server; Cancel is a no-op before.
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		timeout := time.After(10 * time.Second)
		for {
			select {
			case <-done:
				// Engines differ on whether a cancelled statement errors
				// (MySQL's SLEEP returns 1 when killed). SleepSQL runs for
				// far longer than this bound, so finishing early proves the
				// cancel worked.
				if time.Since(start) > 4*time.Second {
					t.Fatalf("cancel took %v", time.Since(start))
				}
				// The connection must still be usable after a server-side cancel.
				if n := countRows(t, c, e.Table); n != 2 {
					t.Fatalf("count after cancel = %d", n)
				}
				return
			case <-tick.C:
				if err := s.Cancel(ctx); err != nil {
					t.Fatalf("cancel: %v", err)
				}
			case <-timeout:
				t.Fatal("statement did not stop")
			}
		}
	})

	t.Run("read_only_rejects_writes", func(t *testing.T) {
		cfg := e.Config
		cfg.ReadOnly = true
		ro, err := driver.Open(ctx, e.Dialect, cfg)
		if err != nil {
			t.Fatalf("open read-only: %v", err)
		}
		t.Cleanup(func() { _ = ro.Close() }) // after the sessions' cleanups
		s := session(t, ro)
		if _, err := s.Exec(ctx, "INSERT INTO "+e.Table+" (id, name) VALUES (99, 'x')"); err == nil {
			t.Fatal("insert succeeded on read-only connection")
		}
		if got := queryAll(t, ro, "SELECT COUNT(*) FROM "+e.Table); len(got.rows) != 1 {
			t.Fatal("read failed on read-only connection")
		}
	})

	t.Run("stats_reflect_pool", func(t *testing.T) {
		if st := c.Stats(); st.Max <= 0 {
			t.Fatalf("stats = %+v", st)
		}
	})
}

type result struct {
	cols []driver.Column
	rows [][]any
}

func session(t *testing.T, c driver.Conn) driver.Session {
	t.Helper()
	s, err := c.Session(t.Context())
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustExec(t *testing.T, c driver.Conn, sql string) driver.Result {
	t.Helper()
	// Cleanup may run after the test context is done.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := c.Session(ctx)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer s.Close()
	res, err := s.Exec(ctx, sql)
	if err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
	return res
}

func queryAll(t *testing.T, c driver.Conn, sql string) result {
	t.Helper()
	s := session(t, c)
	r, err := s.Query(t.Context(), sql)
	if err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	defer r.Close()
	out := result{cols: r.Columns()}
	for r.Next() {
		dst := make([]any, len(out.cols))
		if err := r.Values(dst); err != nil {
			t.Fatalf("values: %v", err)
		}
		out.rows = append(out.rows, dst)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func countRows(t *testing.T, c driver.Conn, table string) int {
	t.Helper()
	got := queryAll(t, c, "SELECT COUNT(*) FROM "+table)
	var n int
	if _, err := fmt.Sscan(fmt.Sprint(got.rows[0][0]), &n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
