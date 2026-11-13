//go:build integration

package advisor_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"

	"github.com/phankieuphu/lumen/internal/advisor"
	"github.com/phankieuphu/lumen/internal/driver"
	_ "github.com/phankieuphu/lumen/internal/driver/mysql"
	_ "github.com/phankieuphu/lumen/internal/driver/postgres"
)

// Names are prefixed so they do not collide with other suites sharing the
// database, and so a search finds only this suite's statements.
var (
	setup = []string{
		`DROP TABLE IF EXISTS lumen_adv_orders`,
		`DROP TABLE IF EXISTS lumen_adv_customers`,
		`CREATE TABLE lumen_adv_customers (id INT PRIMARY KEY, name VARCHAR(40))`,
		`CREATE TABLE lumen_adv_orders (id INT PRIMARY KEY, customer_id INT, total INT)`,
	}
	teardown = []string{
		`DROP TABLE IF EXISTS lumen_adv_orders`,
		`DROP TABLE IF EXISTS lumen_adv_customers`,
	}
)

const (
	joinCalls   = 5
	insertCalls = 2
	joinSQL     = `SELECT o.id FROM lumen_adv_orders o JOIN lumen_adv_customers c ON c.id = o.customer_id WHERE o.total > %d`
	insertSQL   = `INSERT INTO lumen_adv_customers (id, name) VALUES (%d, 'n')`
	// probeSQL holds "lumenxadv": a search for "lumen_adv" must not match
	// it, or the underscore was sent as a wildcard.
	probeSQL = `SELECT 1 AS lumenxadvprobe`
)

func TestPostgres(t *testing.T) {
	dsn := os.Getenv("LUMEN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("LUMEN_TEST_POSTGRES_DSN not set")
	}
	pc, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg := driver.Config{
		Host: pc.Host, Port: int(pc.Port), User: pc.User, Password: pc.Password,
		Database: pc.Database, TLS: driver.TLSDisable,
	}
	c := open(t, driver.Postgres, cfg)
	exec(t.Context(), t, c, []string{`CREATE EXTENSION IF NOT EXISTS pg_stat_statements`})
	if _, err := advisor.Top(t.Context(), c, advisor.Filter{}); errors.Is(err, advisor.ErrNotEnabled) {
		t.Skipf("server does not preload pg_stat_statements (use deploy/compose.yaml): %v", err)
	}
	workload(t, c)
	join := checkList(t, c)

	t.Run("statistics say when they were reset", func(t *testing.T) {
		l, err := advisor.Top(t.Context(), c, advisor.Filter{Search: "lumen_adv"})
		if err != nil {
			t.Fatal(err)
		}
		if l.StatsSince == "" || l.Windowed {
			t.Fatalf("StatsSince=%q Windowed=%v", l.StatsSince, l.Windowed)
		}
	})
	t.Run("plan of a statement with placeholders", func(t *testing.T) {
		d, err := advisor.Explain(t.Context(), c, join.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.PlanSource != "EXPLAIN (FORMAT JSON), generic plan" || len(d.Plan) < 2 {
			t.Fatalf("source=%q plan=%+v", d.PlanSource, d.Plan)
		}
		if !slices.ContainsFunc(d.Plan, func(n advisor.PlanNode) bool { return strings.Contains(n.Label, "lumen_adv_orders") }) {
			t.Fatalf("no scan of lumen_adv_orders in %+v", d.Plan)
		}
		if d.Plan[0].Depth != 0 || d.Plan[0].Cost <= 0 {
			t.Fatalf("root = %+v", d.Plan[0])
		}
		if d.Advice != nil || d.NoAdviceReason == "" || d.ID != join.ID || d.Calls < joinCalls {
			t.Fatalf("detail = %+v", d)
		}
	})
	t.Run("planning a write does not run it", func(t *testing.T) {
		l, err := advisor.Top(t.Context(), c, advisor.Filter{Search: "lumen_adv", Kind: advisor.KindInsert})
		if err != nil || len(l.Queries) == 0 {
			t.Fatalf("list = %+v, %v", l, err)
		}
		before := count(t, c, `SELECT count(*) FROM lumen_adv_customers`)
		d, err := advisor.Explain(t.Context(), c, l.Queries[0].ID)
		if err != nil || len(d.Plan) == 0 {
			t.Fatalf("detail = %+v, %v", d, err)
		}
		if after := count(t, c, `SELECT count(*) FROM lumen_adv_customers`); after != before {
			t.Fatalf("rows went from %d to %d", before, after)
		}
	})
	t.Run("session is not left in a transaction", func(t *testing.T) {
		// A pool of one hands back the session Explain used.
		one := cfg
		one.MaxConns = 1
		oc := open(t, driver.Postgres, one)
		// Twice: the second read fails if the first left its prepared
		// statement behind.
		for range 2 {
			if d, err := advisor.Explain(t.Context(), oc, join.ID); err != nil || len(d.Plan) == 0 {
				t.Fatalf("detail = %+v, %v", d, err)
			}
		}
		if n := count(t, oc, `SELECT count(*) FROM pg_stat_activity WHERE pid = pg_backend_pid() AND xact_start IS NOT NULL AND state = 'idle in transaction'`); n != 0 {
			t.Fatal("session is idle in transaction")
		}
		exec(t.Context(), t, oc, []string{`CREATE TEMP TABLE lumen_adv_writable (id INT)`})
	})
	t.Run("read-only connection can read statistics and plans", func(t *testing.T) {
		ro := cfg
		ro.ReadOnly = true
		rc := open(t, driver.Postgres, ro)
		if _, err := advisor.Top(t.Context(), rc, advisor.Filter{Search: "lumen_adv"}); err != nil {
			t.Fatal(err)
		}
		if d, err := advisor.Explain(t.Context(), rc, join.ID); err != nil || len(d.Plan) == 0 {
			t.Fatalf("detail = %+v, %v", d, err)
		}
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
	// application users do not.
	c := open(t, driver.MySQL, cfg)
	if _, err := advisor.Top(t.Context(), c, advisor.Filter{}); err != nil {
		if !errors.Is(err, advisor.ErrNoAccess) || !strings.Contains(err.Error(), "GRANT SELECT ON performance_schema") {
			t.Fatalf("err = %v, want ErrNoAccess with the GRANT to run", err)
		}
		// The test containers give root the same password.
		cfg.User = "root"
		rc, err := driver.Open(t.Context(), driver.MySQL, cfg)
		if err != nil {
			t.Skipf("no user that can read performance_schema: %v", err)
		}
		t.Cleanup(func() { _ = rc.Close() })
		c = rc
	}
	workload(t, c)
	join := checkList(t, c)

	t.Run("window keeps statements seen within it", func(t *testing.T) {
		l, err := advisor.Top(t.Context(), c, advisor.Filter{Search: "lumen_adv", WindowHours: 1})
		if err != nil {
			t.Fatal(err)
		}
		if !l.Windowed || !slices.ContainsFunc(l.Queries, func(q advisor.SlowQuery) bool { return q.ID == join.ID }) {
			t.Fatalf("list = %+v", l)
		}
	})
	t.Run("detail has statistics and says why there is no plan", func(t *testing.T) {
		d, err := advisor.Explain(t.Context(), c, join.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Plan) != 0 || !strings.HasPrefix(d.PlanSource, "No plan") || d.Calls < joinCalls || d.Advice != nil {
			t.Fatalf("detail = %+v", d)
		}
	})
}

// workload creates the tables and runs each statement a known number of
// times with different literals, which the server folds into one entry.
func workload(t *testing.T, c driver.Conn) {
	t.Helper()
	t.Cleanup(func() { exec(context.Background(), t, c, teardown) })
	exec(t.Context(), t, c, setup)
	stmts := make([]string, 0, insertCalls)
	for i := range insertCalls {
		stmts = append(stmts, fmt.Sprintf(insertSQL, i+1))
	}
	exec(t.Context(), t, c, stmts)
	for i := range joinCalls {
		count(t, c, fmt.Sprintf(joinSQL, i))
	}
	count(t, c, probeSQL)
}

// checkList covers what both engines share and returns the join statement.
func checkList(t *testing.T, c driver.Conn) advisor.SlowQuery {
	t.Helper()
	top := func(t *testing.T, f advisor.Filter) advisor.List {
		t.Helper()
		l, err := advisor.Top(t.Context(), c, f)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	isJoin := func(q advisor.SlowQuery) bool {
		return slices.Equal(q.Tables, []string{"lumen_adv_orders", "lumen_adv_customers"})
	}

	var join advisor.SlowQuery
	t.Run("search and minimum calls find the join", func(t *testing.T) {
		l := top(t, advisor.Filter{Search: "LUMEN_ADV_orders", MinCalls: joinCalls})
		i := slices.IndexFunc(l.Queries, isJoin)
		if i < 0 {
			t.Fatalf("join not in %+v", l.Queries)
		}
		join = l.Queries[i]
		if join.ID == "" || join.Calls < joinCalls || join.MeanMs <= 0 || join.SharePct < 0 || join.SharePct > 100 {
			t.Fatalf("join = %+v", join)
		}
		if join.Title != "SELECT on lumen_adv_orders, lumen_adv_customers" || join.State != advisor.StateNotAnalysed {
			t.Fatalf("join = %+v", join)
		}
		if strings.Contains(join.SQL, "> 3") {
			t.Fatalf("statement text keeps a literal: %q", join.SQL)
		}
	})
	if join.ID == "" {
		t.FailNow()
	}
	t.Run("search treats wildcards as text", func(t *testing.T) {
		if l := top(t, advisor.Filter{Search: "lumenxadvprobe"}); len(l.Queries) == 0 {
			t.Fatalf("probe not found: %+v", l.Queries)
		}
		if l := top(t, advisor.Filter{Search: "lumen_advprobe"}); len(l.Queries) != 0 {
			t.Fatalf("underscore matched as a wildcard: %+v", l.Queries)
		}
		if l := top(t, advisor.Filter{Search: "lumen%probe"}); len(l.Queries) != 0 {
			t.Fatalf("percent matched as a wildcard: %+v", l.Queries)
		}
	})
	t.Run("minimum mean time leaves fast statements out", func(t *testing.T) {
		if l := top(t, advisor.Filter{Search: "lumen_adv", MinMeanMs: 3_600_000}); len(l.Queries) != 0 {
			t.Fatalf("list = %+v", l.Queries)
		}
	})
	t.Run("minimum calls leaves rare statements out", func(t *testing.T) {
		if l := top(t, advisor.Filter{Search: "lumen_adv", MinCalls: 1 << 40}); len(l.Queries) != 0 {
			t.Fatalf("list = %+v", l.Queries)
		}
	})
	t.Run("kind keeps one statement type", func(t *testing.T) {
		// An earlier run's table had another OID, so PostgreSQL may keep
		// more than one entry for the same text.
		l := top(t, advisor.Filter{Search: "lumen_adv", Kind: advisor.KindInsert})
		if len(l.Queries) == 0 {
			t.Fatal("no insert found")
		}
		for _, q := range l.Queries {
			if q.Title != "INSERT on lumen_adv_customers" || q.Calls < insertCalls {
				t.Fatalf("insert filter returned %+v", q)
			}
		}
		for _, q := range top(t, advisor.Filter{Search: "lumen_adv", Kind: advisor.KindSelect}).Queries {
			if !strings.HasPrefix(q.Title, "SELECT") {
				t.Fatalf("select filter returned %+v", q)
			}
		}
	})
	t.Run("sort by calls is descending", func(t *testing.T) {
		l := top(t, advisor.Filter{Search: "lumen_adv", Sort: advisor.SortCalls})
		if len(l.Queries) < 2 {
			t.Fatalf("list = %+v", l.Queries)
		}
		if !slices.IsSortedFunc(l.Queries, func(a, b advisor.SlowQuery) int { return int(b.Calls - a.Calls) }) {
			t.Fatalf("not sorted by calls: %+v", l.Queries)
		}
	})
	t.Run("sort by mean time is descending", func(t *testing.T) {
		l := top(t, advisor.Filter{Search: "lumen_adv", Sort: advisor.SortMean})
		if !slices.IsSortedFunc(l.Queries, func(a, b advisor.SlowQuery) int {
			switch {
			case a.MeanMs > b.MeanMs:
				return -1
			case a.MeanMs < b.MeanMs:
				return 1
			}
			return 0
		}) {
			t.Fatalf("not sorted by mean: %+v", l.Queries)
		}
	})
	t.Run("limit cuts the list and says so", func(t *testing.T) {
		l := top(t, advisor.Filter{Search: "lumen_adv", Limit: 1})
		if len(l.Queries) != 1 || !l.Truncated {
			t.Fatalf("list = %+v", l)
		}
		if l := top(t, advisor.Filter{Search: "lumenxadvprobe", Limit: 1}); l.Truncated {
			t.Fatal("a full page of exactly Limit is not truncated")
		}
	})
	t.Run("the advisor's own reads are not listed", func(t *testing.T) {
		for _, s := range []string{"pg_stat_statements", "performance_schema"} {
			if l := top(t, advisor.Filter{Search: s}); len(l.Queries) != 0 {
				t.Fatalf("list = %+v", l.Queries)
			}
		}
	})
	t.Run("unknown id is not found", func(t *testing.T) {
		if _, err := advisor.Explain(t.Context(), c, "no-such-id"); !errors.Is(err, advisor.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	return join
}

func open(t *testing.T, d driver.Dialect, cfg driver.Config) driver.Conn {
	t.Helper()
	c, err := driver.Open(t.Context(), d, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func exec(ctx context.Context, t *testing.T, c driver.Conn, stmts []string) {
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

// count runs q, drains it, and returns the first field of the first row.
func count(t *testing.T, c driver.Conn, q string) int64 {
	t.Helper()
	s, err := c.Session(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	rows, err := s.Query(t.Context(), q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer func() { _ = rows.Close() }()
	var first int64
	vals := make([]any, len(rows.Columns()))
	for n := 0; rows.Next(); n++ {
		if err := rows.Values(vals); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			first, _ = strconv.ParseInt(fmt.Sprint(vals[0]), 10, 64)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return first
}
