package query

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/phankieuphu/lumen/internal/audit"
	"github.com/phankieuphu/lumen/internal/conn"
	"github.com/phankieuphu/lumen/internal/driver"
	_ "github.com/phankieuphu/lumen/internal/driver/sqlite"
	"github.com/phankieuphu/lumen/internal/secret"
	"github.com/phankieuphu/lumen/internal/store"
)

type env struct {
	runner  *Runner
	conns   *conn.Manager
	audit   *audit.Log
	history *History
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "lumen.db"))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{conns: conn.NewManager(st, &secret.Memory{}), audit: audit.New(st), history: NewHistory(st)}
	e.runner = NewRunner(e.conns, e.audit, e.history, nil)
	t.Cleanup(func() {
		_ = e.conns.CloseAll()
		_ = st.Close()
	})
	return e
}

func (e *env) profile(t *testing.T, p conn.Profile) conn.Profile {
	t.Helper()
	p, err := e.conns.Create(t.Context(), p, "")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *env) sqlite(t *testing.T, mut func(*conn.Profile)) conn.Profile {
	t.Helper()
	p := conn.Profile{Name: t.Name(), Dialect: driver.SQLite, Database: filepath.Join(t.TempDir(), "data.db")}
	if mut != nil {
		mut(&p)
	}
	return e.profile(t, p)
}

// collect is a Sink that copies rows, as the UI would by serializing them.
type collect struct {
	pages   int
	columns []driver.Column
	rows    [][]any
}

func (c *collect) sink(_ context.Context, p Page) error {
	if p.Seq != c.pages {
		return fmt.Errorf("page seq %d, want %d", p.Seq, c.pages)
	}
	if p.Seq == 0 {
		c.columns = p.Columns
	} else if p.Columns != nil {
		return errors.New("columns repeated after first page")
	}
	c.pages++
	for _, r := range p.Rows {
		c.rows = append(c.rows, slices.Clone(r))
	}
	return nil
}

var reqSeq int

func (e *env) run(t *testing.T, p conn.Profile, sql string, mut ...func(*Request)) (Summary, *collect, error) {
	t.Helper()
	reqSeq++
	req := Request{ID: fmt.Sprintf("r%d", reqSeq), ConnID: p.ID, SQL: sql}
	for _, m := range mut {
		m(&req)
	}
	c := &collect{}
	sum, err := e.runner.Run(t.Context(), req, c.sink)
	return sum, c, err
}

func (e *env) mustRun(t *testing.T, p conn.Profile, sql string, mut ...func(*Request)) (Summary, *collect) {
	t.Helper()
	sum, c, err := e.run(t, p, sql, mut...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return sum, c
}

func seed(t *testing.T, e *env, p conn.Profile, n int) {
	t.Helper()
	e.mustRun(t, p, "CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)")
	e.mustRun(t, p, fmt.Sprintf(`INSERT INTO t (id, name)
		WITH RECURSIVE s(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM s WHERE i < %d)
		SELECT i, 'n' || i FROM s`, n))
}

func TestRunStreamsPages(t *testing.T) {
	e := newEnv(t)
	p := e.sqlite(t, nil)
	seed(t, e, p, 1234)

	sum, c := e.mustRun(t, p, "SELECT id, name FROM t ORDER BY id", func(r *Request) { r.PageSize = 500 })
	if sum.Status != audit.StatusOK || sum.Kind != KindRead || sum.RowCount != 1234 || sum.Truncated {
		t.Fatalf("summary = %+v", sum)
	}
	if c.pages != 3 || len(c.rows) != 1234 || len(c.columns) != 2 {
		t.Fatalf("pages=%d rows=%d cols=%d", c.pages, len(c.rows), len(c.columns))
	}
	if c.rows[0][0] != int64(1) || c.rows[1233][1] != "n1234" {
		t.Fatalf("first/last rows = %v / %v", c.rows[0], c.rows[1233])
	}
}

func TestRunEmptyResultSendsColumns(t *testing.T) {
	e := newEnv(t)
	p := e.sqlite(t, nil)
	seed(t, e, p, 1)
	_, c := e.mustRun(t, p, "SELECT id, name FROM t WHERE id < 0")
	if c.pages != 1 || len(c.columns) != 2 || len(c.rows) != 0 {
		t.Fatalf("pages=%d cols=%d rows=%d", c.pages, len(c.columns), len(c.rows))
	}
}

func TestRunTruncatesAtMaxRows(t *testing.T) {
	e := newEnv(t)
	p := e.sqlite(t, nil)
	seed(t, e, p, 300)
	sum, c := e.mustRun(t, p, "SELECT * FROM t", func(r *Request) { r.MaxRows = 120; r.PageSize = 50 })
	if !sum.Truncated || sum.RowCount != 120 || len(c.rows) != 120 {
		t.Fatalf("summary = %+v, rows = %d", sum, len(c.rows))
	}
}

func TestRunAuditsWrites(t *testing.T) {
	e := newEnv(t)
	p := e.sqlite(t, nil)
	seed(t, e, p, 10)
	sum, _ := e.mustRun(t, p, "DELETE FROM t WHERE id <= 3")
	if sum.Kind != KindWrite || sum.RowsAffected != 3 {
		t.Fatalf("summary = %+v", sum)
	}
	e.mustRun(t, p, "SELECT * FROM t") // reads are not audited

	entries, err := e.audit.List(t.Context(), audit.Filter{ConnID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	// CREATE TABLE, INSERT (seed), DELETE
	if len(entries) != 3 {
		t.Fatalf("audit entries = %d, want 3", len(entries))
	}
	last := entries[2]
	if last.SQL != "DELETE FROM t WHERE id <= 3" || last.RowsAffected != 3 || last.Status != audit.StatusOK {
		t.Fatalf("last entry = %+v", last)
	}
	if res, _ := e.audit.Verify(t.Context()); !res.OK {
		t.Fatalf("verify = %+v", res)
	}
}

func TestRunAuditsFailedWrites(t *testing.T) {
	e := newEnv(t)
	p := e.sqlite(t, nil)
	if _, _, err := e.run(t, p, "INSERT INTO missing VALUES (1)"); err == nil {
		t.Fatal("expected error")
	}
	entries, _ := e.audit.List(t.Context(), audit.Filter{})
	if len(entries) != 1 || entries[0].Status != audit.StatusError || entries[0].Error == "" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestRunReadOnlyBlocksAndAudits(t *testing.T) {
	e := newEnv(t)
	rw := e.sqlite(t, nil)
	seed(t, e, rw, 5)
	ro := e.profile(t, conn.Profile{Name: "ro", Dialect: driver.SQLite, Database: rw.Database, ReadOnly: true})

	sum, _, err := e.run(t, ro, "DELETE FROM t")
	if !errors.Is(err, ErrReadOnly) || sum.Status != audit.StatusBlocked {
		t.Fatalf("err = %v, summary = %+v", err, sum)
	}
	entries, _ := e.audit.List(t.Context(), audit.Filter{ConnID: ro.ID})
	if len(entries) != 1 || entries[0].Status != audit.StatusBlocked {
		t.Fatalf("blocked write not audited: %+v", entries)
	}
	if _, c := e.mustRun(t, ro, "SELECT COUNT(*) FROM t"); c.rows[0][0] != int64(5) {
		t.Fatalf("rows deleted despite read-only: %v", c.rows)
	}
	// Engine-level enforcement catches what the classifier lets through as
	// a read; here a PRAGMA the classifier knows is a write is blocked too.
	if _, _, err := e.run(t, ro, "PRAGMA query_only = 0"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("pragma escalation: %v", err)
	}
}

func TestRunProdNeedsConfirmation(t *testing.T) {
	e := newEnv(t)
	p := e.sqlite(t, func(p *conn.Profile) { p.Env = conn.EnvProd })
	_, _, err := e.run(t, p, "CREATE TABLE t (id INTEGER)")
	if !errors.Is(err, ErrConfirmRequired) {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := e.audit.List(t.Context(), audit.Filter{}); len(entries) != 0 {
		t.Fatal("confirmation prompt should not be audited")
	}
	e.mustRun(t, p, "CREATE TABLE t (id INTEGER)", func(r *Request) { r.ConfirmWrite = true })
	e.mustRun(t, p, "SELECT * FROM t") // reads need no confirmation
}

func TestRunRejectsMultipleAndEmpty(t *testing.T) {
	e := newEnv(t)
	p := e.sqlite(t, nil)
	if _, _, err := e.run(t, p, "SELECT 1; SELECT 2"); !errors.Is(err, ErrMultiple) {
		t.Fatalf("multi: %v", err)
	}
	if _, _, err := e.run(t, p, " -- nothing"); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := e.runner.Run(t.Context(), Request{SQL: "SELECT 1"}, nil); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("bad request: %v", err)
	}
}

func TestRunSinkErrorStops(t *testing.T) {
	e := newEnv(t)
	p := e.sqlite(t, nil)
	seed(t, e, p, 100)
	boom := errors.New("ui gone")
	sum, err := e.runner.Run(t.Context(), Request{ID: "x", ConnID: p.ID, SQL: "SELECT * FROM t", PageSize: 10},
		func(context.Context, Page) error { return boom })
	if !errors.Is(err, boom) || sum.Status != audit.StatusError {
		t.Fatalf("err = %v, summary = %+v", err, sum)
	}
}

const longSQLite = `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c) SELECT MAX(x) FROM c`

func TestCancelByRequestID(t *testing.T) {
	e := newEnv(t)
	p := e.sqlite(t, nil)
	testCancel(t, e, p, longSQLite)
}

func testCancel(t *testing.T, e *env, p conn.Profile, sleepSQL string) {
	t.Helper()
	type result struct {
		sum Summary
		err error
	}
	done := make(chan result, 1)
	go func() {
		sum, err := e.runner.Run(context.Background(), Request{ID: "long", ConnID: p.ID, SQL: sleepSQL},
			func(context.Context, Page) error { return nil })
		done <- result{sum, err}
	}()

	waitRunning(t, e.runner, "long")
	if _, err := e.runner.Run(t.Context(), Request{ID: "long", ConnID: p.ID, SQL: "SELECT 1"}, nil); !errors.Is(err, ErrDuplicateRequest) {
		t.Fatalf("duplicate id: %v", err)
	}

	start := time.Now()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case r := <-done:
			if r.sum.Status != audit.StatusCancelled && r.sum.Status != audit.StatusOK {
				t.Fatalf("status = %s, err = %v", r.sum.Status, r.err)
			}
			if time.Since(start) > 5*time.Second {
				t.Fatalf("cancel took %v", time.Since(start))
			}
			if err := e.runner.Cancel(t.Context(), "long"); !errors.Is(err, ErrNotRunning) {
				t.Fatalf("cancel after finish: %v", err)
			}
			// The pool still works.
			e.mustRun(t, p, "SELECT 1")
			return
		case <-tick.C:
			_ = e.runner.Cancel(t.Context(), "long")
		case <-timeout:
			t.Fatal("request did not stop after cancel")
		}
	}
}

func waitRunning(t *testing.T, r *Runner, id string) {
	t.Helper()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	deadline := time.After(5 * time.Second)
	for !slices.Contains(r.Running(), id) {
		select {
		case <-tick.C:
		case <-deadline:
			t.Fatalf("request %q never started", id)
		}
	}
}

func TestHistoryRecordsRequests(t *testing.T) {
	e := newEnv(t)
	p := e.sqlite(t, nil)
	seed(t, e, p, 3)
	e.mustRun(t, p, "SELECT * FROM t")
	_, _, _ = e.run(t, p, "SELECT * FROM nope")

	got, err := e.history.List(t.Context(), p.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("history = %d entries", len(got))
	}
	if got[0].Status != audit.StatusError || got[1].RowCount != 3 || got[2].RowCount != 3 {
		t.Fatalf("history = %+v", got)
	}
	page2, err := e.history.List(t.Context(), p.ID, got[1].ID, 10)
	if err != nil || len(page2) != 2 {
		t.Fatalf("page 2 = %d, %v", len(page2), err)
	}
}

func BenchmarkRunStream(b *testing.B) {
	st, err := store.OpenMemory(b.Context())
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	m := conn.NewManager(st, &secret.Memory{})
	defer m.CloseAll()
	p, err := m.Create(b.Context(), conn.Profile{
		Name: "b", Dialect: driver.SQLite,
		Database: filepath.Join(b.TempDir(), "b.db"),
	}, "")
	if err != nil {
		b.Fatal(err)
	}
	r := NewRunner(m, audit.New(st), nil, nil)
	for i, q := range []string{
		"CREATE TABLE t (id INTEGER, name TEXT, v REAL)",
		`INSERT INTO t WITH RECURSIVE s(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM s WHERE i < 10000)
		 SELECT i, 'name' || i, i * 1.5 FROM s`,
	} {
		if _, err := r.Run(b.Context(), Request{ID: fmt.Sprint("seed", i), ConnID: p.ID, SQL: q}, nil); err != nil {
			b.Fatal(err)
		}
	}
	sink := func(context.Context, Page) error { return nil }
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if _, err := r.Run(b.Context(), Request{ID: fmt.Sprint(i), ConnID: p.ID, SQL: "SELECT * FROM t"}, sink); err != nil {
			b.Fatal(err)
		}
	}
}
