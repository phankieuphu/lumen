//go:build integration

package explain_test

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"

	"github.com/phankieuphu/lumen/internal/driver"
	_ "github.com/phankieuphu/lumen/internal/driver/mysql"
	_ "github.com/phankieuphu/lumen/internal/driver/postgres"
	"github.com/phankieuphu/lumen/internal/explain"
)

// engine is one database under test, with its own SQL for what differs.
type engine struct {
	dialect driver.Dialect
	cfg     driver.Config
	setup   []string
	sleep   string // a read that takes about five seconds
	// writable are writes that fail if the session is still in a read-only
	// transaction.
	writable []string
}

var tables = []string{
	`DROP TABLE IF EXISTS lumen_exp_orders`,
	`CREATE TABLE lumen_exp_orders (id INT PRIMARY KEY, status VARCHAR(10), total INT)`,
}

func postgres(t *testing.T) engine {
	dsn := os.Getenv("LUMEN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("LUMEN_TEST_POSTGRES_DSN not set")
	}
	pc, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	return engine{
		dialect: driver.Postgres,
		cfg: driver.Config{
			Host: pc.Host, Port: int(pc.Port), User: pc.User, Password: pc.Password,
			Database: pc.Database, TLS: driver.TLSDisable,
		},
		setup: append(tables,
			`INSERT INTO lumen_exp_orders SELECT g, CASE WHEN g % 100 = 0 THEN 'open' ELSE 'done' END, g % 500 FROM generate_series(1, 20000) g`,
			`ANALYZE lumen_exp_orders`),
		sleep:    `SELECT pg_sleep(5)`,
		writable: []string{`CREATE TEMP TABLE lumen_exp_probe (id INT)`},
	}
}

func mysql(t *testing.T) engine {
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
	return engine{
		dialect: driver.MySQL,
		cfg:     driver.Config{Host: host, Port: p, User: mc.User, Password: mc.Passwd, Database: mc.DBName, TLS: driver.TLSDisable},
		setup: append(tables,
			`SET SESSION cte_max_recursion_depth = 100000`,
			`INSERT INTO lumen_exp_orders WITH RECURSIVE g(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM g WHERE n < 20000)
			 SELECT n, IF(n % 100 = 0, 'open', 'done'), n % 500 FROM g`,
			`ANALYZE TABLE lumen_exp_orders`),
		sleep:    `SELECT SLEEP(5)`,
		writable: []string{`INSERT INTO lumen_exp_orders VALUES (99999, 'probe', 0)`, `DELETE FROM lumen_exp_orders WHERE id = 99999`},
	}
}

func TestPostgres(t *testing.T) { run(t, postgres(t)) }

func TestMySQL(t *testing.T) { run(t, mysql(t)) }

func run(t *testing.T, e engine) {
	ctx := t.Context()
	c := open(ctx, t, e.dialect, e.cfg)
	exec(ctx, t, c, e.setup)
	t.Cleanup(func() { exec(context.Background(), t, c, []string{`DROP TABLE IF EXISTS lumen_exp_orders`}) })
	read := `SELECT id, total FROM lumen_exp_orders WHERE status = 'open' ORDER BY total`

	t.Run("a read is analyzed", func(t *testing.T) {
		p, err := explain.Run(ctx, c, read, explain.Options{Analyze: true})
		if err != nil {
			t.Fatal(err)
		}
		if !p.Analyzed || !p.Estimated || p.Root == nil || p.Raw == "" {
			t.Fatalf("plan = %+v", p)
		}
		var scan *explain.Node
		walk(p.Root, func(n *explain.Node) {
			if n.Relation == "lumen_exp_orders" {
				scan = n
			}
		})
		// 200 rows match; the other 19,800 are read and thrown away.
		if scan == nil || scan.Kind != explain.KindScan || scan.Rows*scan.Loops != 200 || scan.RowsRemoved*scan.Loops != 19800 {
			t.Fatalf("scan = %+v", scan)
		}
		if !hasFinding(p, "Full scan of lumen_exp_orders") {
			t.Fatalf("findings = %+v", p.Findings)
		}
		if last := p.Steps[len(p.Steps)-1]; !strings.HasPrefix(last, "Finished in") {
			t.Fatalf("steps = %q", p.Steps)
		}
	})

	t.Run("a write is planned, not run", func(t *testing.T) {
		before := count(ctx, t, c, `SELECT count(*) FROM lumen_exp_orders WHERE status = 'gone'`)
		p, err := explain.Run(ctx, c, `UPDATE lumen_exp_orders SET status = 'gone'`, explain.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if p.Analyzed || p.Root == nil || !strings.Contains(p.Note, "estimates") {
			t.Fatalf("plan = %+v", p)
		}
		if after := count(ctx, t, c, `SELECT count(*) FROM lumen_exp_orders WHERE status = 'gone'`); after != before {
			t.Fatalf("the update ran: %d rows changed", after-before)
		}
	})

	t.Run("a write sent with Analyze fails in the read-only transaction", func(t *testing.T) {
		_, err := explain.Run(ctx, c, `UPDATE lumen_exp_orders SET status = 'gone'`, explain.Options{Analyze: true})
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "read") {
			t.Fatalf("err = %v, want a read-only error", err)
		}
		if n := count(ctx, t, c, `SELECT count(*) FROM lumen_exp_orders WHERE status = 'gone'`); n != 0 {
			t.Fatalf("%d rows changed", n)
		}
	})

	t.Run("the session leaves no transaction behind", func(t *testing.T) {
		one := e.cfg
		one.MaxConns = 1
		oc := open(ctx, t, e.dialect, one)
		for range 2 {
			if _, err := explain.Run(ctx, oc, read, explain.Options{Analyze: true}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := explain.Run(ctx, oc, `UPDATE lumen_exp_orders SET total = 0`, explain.Options{Analyze: true}); err == nil {
			t.Fatal("a write under ANALYZE did not fail")
		}
		// A pool of one hands back the same session: writing works only if
		// no read-only transaction was left open on it.
		exec(ctx, t, oc, e.writable)
	})

	t.Run("the time limit stops a slow read", func(t *testing.T) {
		start := time.Now()
		_, err := explain.Run(ctx, c, e.sleep, explain.Options{Analyze: true, Timeout: time.Second})
		if err == nil {
			t.Fatal("a five-second read finished within a one-second limit")
		}
		if took := time.Since(start); took > 4*time.Second {
			t.Fatalf("took %v", took)
		}
	})

	t.Run("statements EXPLAIN does not cover", func(t *testing.T) {
		if _, err := explain.Run(ctx, c, `DROP TABLE lumen_exp_orders`, explain.Options{}); !errors.Is(err, explain.ErrStatement) {
			t.Fatalf("err = %v, want ErrStatement", err)
		}
	})
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

func count(ctx context.Context, t *testing.T, c driver.Conn, sql string) int {
	t.Helper()
	s, err := c.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	rows, err := s.Query(ctx, sql)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	vals := make([]any, 1)
	if !rows.Next() || rows.Values(vals) != nil {
		t.Fatalf("%s: no row", sql)
	}
	n, err := strconv.Atoi(strings.TrimSpace(func() string {
		switch v := vals[0].(type) {
		case []byte:
			return string(v)
		default:
			return strconv.FormatInt(toInt(v), 10)
		}
	}()))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func toInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int32:
		return int64(x)
	case int:
		return int64(x)
	case uint64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return -1
}

func walk(n *explain.Node, fn func(*explain.Node)) {
	fn(n)
	for _, c := range n.Children {
		walk(c, fn)
	}
}

func hasFinding(p explain.Plan, title string) bool {
	for _, f := range p.Findings {
		if f.Title == title {
			return true
		}
	}
	return false
}
