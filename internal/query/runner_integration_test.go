//go:build integration

package query

import (
	"net"
	"os"
	"strconv"
	"testing"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"

	"github.com/patrick-rontech/kiemdb/internal/audit"
	"github.com/patrick-rontech/kiemdb/internal/conn"
	"github.com/patrick-rontech/kiemdb/internal/driver"
	_ "github.com/patrick-rontech/kiemdb/internal/driver/mysql"
	_ "github.com/patrick-rontech/kiemdb/internal/driver/postgres"
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
		{"postgres", "KIEMDB_TEST_POSTGRES_DSN", pgProfile, "SELECT pg_sleep(30)"},
		{"mysql", "KIEMDB_TEST_MYSQL_DSN", mysqlProfile, "SELECT SLEEP(30)"},
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

			e.mustRun(t, p, "DROP TABLE IF EXISTS kiemdb_gate")
			e.mustRun(t, p, "CREATE TABLE kiemdb_gate (id INTEGER PRIMARY KEY, name VARCHAR(20))")
			t.Cleanup(func() { _, _, _ = e.run(t, p, "DROP TABLE IF EXISTS kiemdb_gate") })
			sum, _ := e.mustRun(t, p, "INSERT INTO kiemdb_gate VALUES (1, 'a'), (2, 'b'), (3, 'c')")
			if sum.RowsAffected != 3 {
				t.Fatalf("insert affected %d", sum.RowsAffected)
			}

			_, c := e.mustRun(t, p, "SELECT id, name FROM kiemdb_gate ORDER BY id", func(r *Request) { r.PageSize = 2 })
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
	return conn.Profile{Name: "pg", Dialect: driver.Postgres, Host: pc.Host, Port: int(pc.Port),
		User: pc.User, Database: pc.Database, TLS: driver.TLSDisable}, pc.Password
}

func mysqlProfile(t *testing.T, dsn string) (conn.Profile, string) {
	mc, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(mc.Addr)
	p, _ := strconv.Atoi(port)
	return conn.Profile{Name: "my", Dialect: driver.MySQL, Host: host, Port: p,
		User: mc.User, Database: mc.DBName, TLS: driver.TLSDisable}, mc.Passwd
}
