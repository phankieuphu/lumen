//go:build integration

package debug_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"

	"github.com/phankieuphu/lumen/internal/debug"
	"github.com/phankieuphu/lumen/internal/driver"
	_ "github.com/phankieuphu/lumen/internal/driver/mysql"
	_ "github.com/phankieuphu/lumen/internal/driver/postgres"
	_ "github.com/phankieuphu/lumen/internal/driver/sqlite"
)

// engine is one database under test, with its own SQL for what differs.
type engine struct {
	dialect driver.Dialect
	cfg     driver.Config
	// sleep is an expression that takes about n seconds; "" if none.
	sleep func(n int) string
	// returning is a write that returns the rows it changed; "" if none.
	returning string
	// lock takes a session-level lock that a rollback keeps, and free
	// reports from another session whether it is free; "" if none.
	lock, free string
	// ddl is a schema change a dry run can undo; "" if none.
	ddl string
}

// Each test table has 10 users and 30 orders: 3 per user, totals 1..30.
var setup = []string{
	`DROP TABLE IF EXISTS lumen_dbg_orders`,
	`DROP TABLE IF EXISTS lumen_dbg_users`,
	`CREATE TABLE lumen_dbg_users (id INT PRIMARY KEY, name VARCHAR(20))`,
	`CREATE TABLE lumen_dbg_orders (id INT PRIMARY KEY, user_id INT, total INT)`,
	`INSERT INTO lumen_dbg_users VALUES (1,'u1'),(2,'u2'),(3,'u3'),(4,'u4'),(5,'u5'),(6,'u6'),(7,'u7'),(8,'u8'),(9,'u9'),(10,'u10')`,
}

func init() {
	var vals []string
	for i := 1; i <= 30; i++ {
		vals = append(vals, fmt.Sprintf("(%d,%d,%d)", i, (i-1)%10+1, i))
	}
	setup = append(setup, "INSERT INTO lumen_dbg_orders VALUES "+strings.Join(vals, ","))
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
		sleep:     func(n int) string { return fmt.Sprintf("pg_sleep(%d)", n) },
		returning: `UPDATE lumen_dbg_orders SET total = 0 WHERE user_id = 1 RETURNING id`,
		lock:      `SELECT pg_advisory_lock(4242)`,
		free:      `SELECT CASE WHEN pg_try_advisory_lock(4242) THEN 1 ELSE 0 END`,
		ddl:       `ALTER TABLE lumen_dbg_users ADD COLUMN lumen_probe INT`,
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
		sleep:   func(n int) string { return fmt.Sprintf("SLEEP(%d)", n) },
		lock:    `SELECT GET_LOCK('lumen_dbg', 1)`,
		free:    `SELECT IS_FREE_LOCK('lumen_dbg')`,
	}
}

func sqlite(t *testing.T) engine {
	return engine{
		dialect:   driver.SQLite,
		cfg:       driver.Config{Database: filepath.Join(t.TempDir(), "debug.db")},
		returning: `UPDATE lumen_dbg_orders SET total = 0 WHERE user_id = 1 RETURNING id`,
		ddl:       `ALTER TABLE lumen_dbg_users ADD COLUMN lumen_probe INT`,
	}
}

func TestPostgres(t *testing.T) { run(t, postgres(t)) }

func TestMySQL(t *testing.T) { run(t, mysql(t)) }

func TestSQLite(t *testing.T) { run(t, sqlite(t)) }

func run(t *testing.T, e engine) {
	ctx := context.Background()
	c, err := driver.Open(ctx, e.dialect, e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	exec(ctx, t, c, setup...)

	t.Run("parts", func(t *testing.T) {
		rep, err := debug.Parts(ctx, c, `SELECT u.name, count(*) AS n
			FROM lumen_dbg_users u JOIN lumen_dbg_orders o ON o.user_id = u.id
			WHERE o.total > 12 GROUP BY u.name HAVING count(*) > 1 ORDER BY n LIMIT 3`, debug.PartsOptions{})
		if err != nil {
			t.Fatal(err)
		}
		// totals 13..30 leave 18 orders: users 3..10 have two, 1 and 2 have one.
		want := map[debug.Clause]int64{
			debug.ClauseFrom: 10, debug.ClauseJoin: 30, debug.ClauseWhere: 18,
			debug.ClauseGroup: 10, debug.ClauseHaving: 8, debug.ClauseResult: 3,
		}
		if len(rep.Parts) != len(want) {
			t.Fatalf("parts: %+v", rep.Parts)
		}
		for _, p := range rep.Parts {
			if !p.Ran || p.Error != "" || p.Rows != want[p.Clause] {
				t.Errorf("%s: ran %v rows %d (want %d) error %q\n%s", p.Clause, p.Ran, p.Rows, want[p.Clause], p.Error, p.SQL)
			}
		}
		if rep.TimedOut || rep.Note == "" {
			t.Errorf("timed out %v, note %q", rep.TimedOut, rep.Note)
		}
	})

	t.Run("parts with a CTE and a union", func(t *testing.T) {
		rep, err := debug.Parts(ctx, c, `WITH big AS (SELECT * FROM lumen_dbg_orders WHERE total > 25)
			SELECT id FROM big UNION SELECT id FROM lumen_dbg_users`, debug.PartsOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(rep.Parts))
		for _, p := range rep.Parts {
			if p.Error != "" {
				t.Errorf("%s: %s\n%s", p.Clause, p.Error, p.SQL)
			}
			got = append(got, fmt.Sprintf("%s=%d", p.Clause, p.Rows))
		}
		// UNION drops duplicates: ids 26..30 and 1..10.
		if s := strings.Join(got, " "); s != "cte=5 branch=5 branch=10 result=15" {
			t.Errorf("got %s", s)
		}
	})

	t.Run("a part over its time limit does not stop the others", func(t *testing.T) {
		if e.dialect != driver.Postgres {
			t.Skip("only PostgreSQL turns a stopped sleep into an error")
		}
		rep, err := debug.Parts(ctx, c, `SELECT * FROM lumen_dbg_users u CROSS JOIN (SELECT `+e.sleep(3)+` AS s) z WHERE u.id > 5`,
			debug.PartsOptions{PartTimeout: 300 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		from, join := rep.Parts[0], rep.Parts[1]
		if !from.Ran || from.Rows != 10 {
			t.Errorf("from: %+v", from)
		}
		if join.Ran || !strings.Contains(join.Error, "time limit") || join.Ms > 2500 {
			t.Errorf("join: %+v", join)
		}
		if exec(ctx, t, c, "SELECT 1"); t.Failed() {
			t.Error("the connection is unusable after a stopped part")
		}
	})

	t.Run("parts refuse a write", func(t *testing.T) {
		if _, err := debug.Parts(ctx, c, `DELETE FROM lumen_dbg_users`, debug.PartsOptions{}); !errors.Is(err, debug.ErrStatement) {
			t.Errorf("got %v", err)
		}
	})

	t.Run("dry run keeps nothing", func(t *testing.T) {
		script := `INSERT INTO lumen_dbg_users VALUES (11, 'u11');
			UPDATE lumen_dbg_orders SET total = total + 1 WHERE user_id IN (1, 2);
			DELETE FROM lumen_dbg_orders WHERE total > 28;
			SELECT id, name FROM lumen_dbg_users WHERE id > 8 ORDER BY id`
		if e.returning != "" {
			script += ";\n" + e.returning
		}
		if e.ddl != "" {
			script += ";\n" + e.ddl
		}
		res, err := debug.DryRun(ctx, c, script, debug.DryRunOptions{PreviewRows: 2})
		if err != nil {
			t.Fatal(err)
		}
		if !res.RolledBack || res.TimedOut || res.Cancelled {
			t.Fatalf("result: %+v", res)
		}
		st := res.Statements
		for i, r := range st {
			if !r.Ran || r.Error != "" {
				t.Errorf("statement %d: ran %v error %q", i+1, r.Ran, r.Error)
			}
		}
		// Orders 1, 2, 11, 12, 21, 22 get +1; then 29 and 30 go.
		if st[0].RowsAffected != 1 || st[1].RowsAffected != 6 || st[2].RowsAffected != 2 {
			t.Errorf("rows affected: %d %d %d", st[0].RowsAffected, st[1].RowsAffected, st[2].RowsAffected)
		}
		if sel := st[3]; sel.RowCount != 3 || len(sel.Rows) != 2 || !sel.Truncated || len(sel.Columns) != 2 {
			t.Errorf("select: count %d rows %v truncated %v columns %v", sel.RowCount, sel.Rows, sel.Truncated, sel.Columns)
		}
		if e.returning != "" {
			if ret := st[4]; ret.RowCount != 3 || ret.RowsAffected != 3 {
				t.Errorf("returning: count %d affected %d", ret.RowCount, ret.RowsAffected)
			}
		}
		if n := scalar(ctx, t, c, `SELECT count(*) FROM lumen_dbg_users`); n != 10 {
			t.Errorf("users after the dry run: %d", n)
		}
		if n := scalar(ctx, t, c, `SELECT sum(total) FROM lumen_dbg_orders`); n != 465 {
			t.Errorf("order totals after the dry run: %d", n)
		}
		if e.ddl != "" {
			if err := tryExec(ctx, c, `SELECT lumen_probe FROM lumen_dbg_users`); err == nil {
				t.Error("the column added in the dry run is still there")
			}
		}
	})

	t.Run("dry run stops at the first error", func(t *testing.T) {
		res, err := debug.DryRun(ctx, c, `DELETE FROM lumen_dbg_orders;
			INSERT INTO lumen_dbg_users VALUES (1, 'duplicate');
			DELETE FROM lumen_dbg_users`, debug.DryRunOptions{})
		if err != nil {
			t.Fatal(err)
		}
		st := res.Statements
		if !st[0].Ran || st[0].RowsAffected != 30 || st[1].Ran || st[1].Error == "" || st[2].Ran {
			t.Errorf("statements: %+v", st)
		}
		if !res.RolledBack {
			t.Error("not rolled back")
		}
		if n := scalar(ctx, t, c, `SELECT count(*) FROM lumen_dbg_orders`); n != 30 {
			t.Errorf("orders after the dry run: %d", n)
		}
	})

	t.Run("dry run stops at its time limit", func(t *testing.T) {
		if e.sleep == nil {
			t.Skip("no sleep")
		}
		start := time.Now()
		res, err := debug.DryRun(ctx, c, `DELETE FROM lumen_dbg_orders; SELECT `+e.sleep(5)+`; DELETE FROM lumen_dbg_users`,
			debug.DryRunOptions{Timeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		if took := time.Since(start); took > 4*time.Second {
			t.Errorf("took %s", took)
		}
		if !res.TimedOut || !res.RolledBack || res.Statements[2].Ran {
			t.Errorf("result: %+v", res)
		}
		if n := scalar(ctx, t, c, `SELECT count(*) FROM lumen_dbg_orders`); n != 30 {
			t.Errorf("orders after the dry run: %d", n)
		}
	})

	t.Run("dry run refuses session locks", func(t *testing.T) {
		if e.lock == "" {
			t.Skip("no session locks")
		}
		if _, err := debug.DryRun(ctx, c, e.lock, debug.DryRunOptions{}); !errors.Is(err, debug.ErrNotUndoable) {
			t.Errorf("got %v", err)
		}
		if n := scalar(ctx, t, c, e.free); n != 1 {
			t.Error("the lock was taken")
		}
	})

	t.Run("parts of SELECT * over a join", func(t *testing.T) {
		// MySQL refuses the repeated id column in a derived table, so the
		// count falls back to counting rows.
		rep, err := debug.Parts(ctx, c, `SELECT * FROM lumen_dbg_users u JOIN lumen_dbg_orders o ON o.user_id = u.id`, debug.PartsOptions{})
		if err != nil {
			t.Fatal(err)
		}
		res := rep.Parts[len(rep.Parts)-1]
		if !res.Ran || res.Rows != 30 {
			t.Errorf("result: %+v", res)
		}
	})

	t.Run("dry run reports changes MySQL keeps", func(t *testing.T) {
		if e.dialect != driver.MySQL {
			t.Skip("only MySQL has tables without transactions")
		}
		exec(ctx, t, c, `DROP TABLE IF EXISTS lumen_dbg_myisam`,
			`CREATE TABLE lumen_dbg_myisam (id INT) ENGINE=MyISAM`, `INSERT INTO lumen_dbg_myisam VALUES (1), (2)`)
		res, err := debug.DryRun(ctx, c, `DELETE FROM lumen_dbg_myisam`, debug.DryRunOptions{})
		if !errors.Is(err, debug.ErrKept) || res.RolledBack {
			t.Errorf("err %v, rolled back %v", err, res.RolledBack)
		}
		if n := scalar(ctx, t, c, `SELECT count(*) FROM lumen_dbg_myisam`); n != 0 {
			t.Errorf("rows: %d; the test expects MyISAM to keep the delete", n)
		}
	})

	t.Run("dry run refuses what it cannot undo", func(t *testing.T) {
		for _, script := range []string{
			"DELETE FROM lumen_dbg_orders; COMMIT",
			"SET autocommit = 1; DELETE FROM lumen_dbg_orders",
		} {
			if _, err := debug.DryRun(ctx, c, script, debug.DryRunOptions{}); !errors.Is(err, debug.ErrNotUndoable) {
				t.Errorf("%s: got %v", script, err)
			}
		}
		if e.dialect == driver.MySQL {
			if _, err := debug.DryRun(ctx, c, "ALTER TABLE lumen_dbg_users ADD COLUMN x INT", debug.DryRunOptions{}); !errors.Is(err, debug.ErrNotUndoable) {
				t.Errorf("ALTER: got %v", err)
			}
		}
		if n := scalar(ctx, t, c, `SELECT count(*) FROM lumen_dbg_orders`); n != 30 {
			t.Errorf("orders: %d", n)
		}
	})
}

func exec(ctx context.Context, t *testing.T, c driver.Conn, stmts ...string) {
	t.Helper()
	for _, q := range stmts {
		if err := tryExec(ctx, c, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func tryExec(ctx context.Context, c driver.Conn, q string) error {
	s, err := c.Session(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	_, err = s.Exec(ctx, q)
	return err
}

func scalar(ctx context.Context, t *testing.T, c driver.Conn, sql string) int64 {
	t.Helper()
	s, err := c.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	rows, err := s.Query(ctx, sql)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	defer func() { _ = rows.Close() }()
	vals := make([]any, 1)
	if !rows.Next() || rows.Values(vals) != nil {
		t.Fatalf("%s: no row", sql)
	}
	switch v := vals[0].(type) {
	case int64:
		return v
	case int32:
		return int64(v)
	case float64:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(strings.SplitN(v, ".", 2)[0], 10, 64)
		return n
	case []byte:
		n, _ := strconv.ParseInt(strings.SplitN(string(v), ".", 2)[0], 10, 64)
		return n
	}
	t.Fatalf("%s: unexpected %T", sql, vals[0])
	return 0
}
