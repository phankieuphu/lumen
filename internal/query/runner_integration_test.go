//go:build integration

package query

import (
	"errors"
	"net"
	"os"
	"strconv"
	"testing"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"

	"github.com/phankieuphu/lumen/internal/audit"
	"github.com/phankieuphu/lumen/internal/conn"
	"github.com/phankieuphu/lumen/internal/driver"
	_ "github.com/phankieuphu/lumen/internal/driver/mysql"
	_ "github.com/phankieuphu/lumen/internal/driver/postgres"
)

// TestPhase1Gate checks the phase 1 gate on the network engines: query,
// cancel, and every write in the audit log. SQLite is covered by the unit
// tests in runner_test.go.
func TestPhase1Gate(t *testing.T) {
	engines := []struct {
		name     string
		env      string
		profile  func(t *testing.T, dsn string) (conn.Profile, string)
		sleepSQL string
	}{
		{"postgres", "LUMEN_TEST_POSTGRES_DSN", pgProfile, "SELECT pg_sleep(30)"},
		{"mysql", "LUMEN_TEST_MYSQL_DSN", mysqlProfile, "SELECT SLEEP(30)"},
	}
	for _, eng := range engines {
		t.Run(eng.name, func(t *testing.T) {
			dsn := os.Getenv(eng.env)
			if dsn == "" {
				t.Skip(eng.env + " not set")
			}
			e := newEnv(t)
			prof, pw := eng.profile(t, dsn)
			p, err := e.conns.Create(t.Context(), prof, pw)
			if err != nil {
				t.Fatal(err)
			}

			e.mustRun(t, p, "DROP TABLE IF EXISTS lumen_gate")
			e.mustRun(t, p, "CREATE TABLE lumen_gate (id INTEGER PRIMARY KEY, name VARCHAR(20))")
			t.Cleanup(func() { _, _, _ = e.run(t, p, "DROP TABLE IF EXISTS lumen_gate") })
			sum, _ := e.mustRun(t, p, "INSERT INTO lumen_gate VALUES (1, 'a'), (2, 'b'), (3, 'c')")
			if sum.RowsAffected != 3 {
				t.Fatalf("insert affected %d", sum.RowsAffected)
			}

			_, c := e.mustRun(t, p, "SELECT id, name FROM lumen_gate ORDER BY id", func(r *Request) { r.PageSize = 2 })
			if c.pages != 2 || len(c.rows) != 3 || c.rows[2][1] != "c" {
				t.Fatalf("pages=%d rows=%v", c.pages, c.rows)
			}

			testCancel(t, e, p, eng.sleepSQL)

			entries, err := e.audit.List(t.Context(), audit.Filter{ConnID: p.ID})
			if err != nil {
				t.Fatal(err)
			}
			// DROP, CREATE, INSERT — the SELECTs and the cancelled sleep are reads.
			if len(entries) != 3 || entries[2].RowsAffected != 3 {
				t.Fatalf("audit = %+v", entries)
			}
			if res, _ := e.audit.Verify(t.Context()); !res.OK {
				t.Fatalf("verify = %+v", res)
			}
		})
	}
}

func pgProfile(t *testing.T, dsn string) (conn.Profile, string) {
	pc, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	return conn.Profile{
		Name: "pg", Dialect: driver.Postgres, Host: pc.Host, Port: int(pc.Port),
		User: pc.User, Database: pc.Database, TLS: driver.TLSDisable,
	}, pc.Password
}

func mysqlProfile(t *testing.T, dsn string) (conn.Profile, string) {
	mc, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(mc.Addr)
	p, _ := strconv.Atoi(port)
	return conn.Profile{
		Name: "my", Dialect: driver.MySQL, Host: host, Port: p,
		User: mc.User, Database: mc.DBName, TLS: driver.TLSDisable,
	}, mc.Passwd
}

// TestTransactions checks Begin/Commit/Rollback on the network engines: a
// pinned session keeps its transaction across requests, other sessions do
// not see uncommitted rows, and typed COMMIT ends it.
func TestTransactions(t *testing.T) {
	engines := []struct {
		name    string
		env     string
		profile func(t *testing.T, dsn string) (conn.Profile, string)
	}{
		{"postgres", "LUMEN_TEST_POSTGRES_DSN", pgProfile},
		{"mysql", "LUMEN_TEST_MYSQL_DSN", mysqlProfile},
	}
	for _, eng := range engines {
		t.Run(eng.name, func(t *testing.T) {
			dsn := os.Getenv(eng.env)
			if dsn == "" {
				t.Skip(eng.env + " not set")
			}
			e := newEnv(t)
			prof, pw := eng.profile(t, dsn)
			p, err := e.conns.Create(t.Context(), prof, pw)
			if err != nil {
				t.Fatal(err)
			}
			e.mustRun(t, p, "DROP TABLE IF EXISTS lumen_tx")
			e.mustRun(t, p, "CREATE TABLE lumen_tx (id INTEGER PRIMARY KEY)")
			t.Cleanup(func() { _, _, _ = e.run(t, p, "DROP TABLE IF EXISTS lumen_tx") })
			rows := func(mut ...func(*Request)) any {
				_, c := e.mustRun(t, p, "SELECT COUNT(*) FROM lumen_tx", mut...)
				return c.rows[0][0]
			}

			tx, err := e.runner.Begin(t.Context(), p.ID, "it")
			if err != nil {
				t.Fatal(err)
			}
			e.mustRun(t, p, "INSERT INTO lumen_tx VALUES (1)", inTx(tx.ID))
			if got := rows(inTx(tx.ID)); got != int64(1) {
				t.Fatalf("count in tx = %v", got)
			}
			if got := rows(); got != int64(0) {
				t.Fatalf("uncommitted row visible outside: %v", got)
			}
			if _, err := e.runner.Rollback(t.Context(), tx.ID); err != nil {
				t.Fatal(err)
			}
			if got := rows(); got != int64(0) {
				t.Fatalf("count after rollback = %v", got)
			}

			tx, err = e.runner.Begin(t.Context(), p.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			e.mustRun(t, p, "INSERT INTO lumen_tx VALUES (2)", inTx(tx.ID))
			if sum, _ := e.mustRun(t, p, "COMMIT", inTx(tx.ID)); sum.TxEnd != TxCommit {
				t.Fatalf("typed COMMIT = %+v", sum)
			}
			if got := rows(); got != int64(1) {
				t.Fatalf("count after commit = %v", got)
			}

			// A failed statement poisons a PostgreSQL transaction; COMMIT must
			// not report success.
			if eng.name == "postgres" {
				tx, err = e.runner.Begin(t.Context(), p.ID, "")
				if err != nil {
					t.Fatal(err)
				}
				e.mustRun(t, p, "INSERT INTO lumen_tx VALUES (3)", inTx(tx.ID))
				if _, _, err := e.run(t, p, "INSERT INTO lumen_tx VALUES (3)", inTx(tx.ID)); err == nil {
					t.Fatal("duplicate key accepted")
				}
				sum, err := e.runner.Commit(t.Context(), tx.ID)
				if !errors.Is(err, ErrTxAborted) || sum.TxEnd != TxRollback {
					t.Fatalf("commit of aborted tx = %+v, %v", sum, err)
				}
				if got := rows(); got != int64(1) {
					t.Fatalf("count after aborted commit = %v", got)
				}
			}
		})
	}
}
