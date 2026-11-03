//go:build integration

package schema_test

import (
	"context"
	"net"
	"os"
	"strconv"
	"testing"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"

	"github.com/phankieuphu/lumen/internal/driver"
	_ "github.com/phankieuphu/lumen/internal/driver/mysql"
	_ "github.com/phankieuphu/lumen/internal/driver/postgres"
	"github.com/phankieuphu/lumen/internal/schema"
)

// engineDDL creates the same shape on a network engine: a composite FK, a
// self reference, and a view. Names are prefixed so they do not collide
// with other suites sharing the database.
var engineDDL = []string{
	`CREATE TABLE lumen_er_customers (id INT PRIMARY KEY, email VARCHAR(100) NOT NULL, referred_by INT, FOREIGN KEY (referred_by) REFERENCES lumen_er_customers(id))`,
	`CREATE TABLE lumen_er_orders (id INT PRIMARY KEY, customer_id INT NOT NULL, FOREIGN KEY (customer_id) REFERENCES lumen_er_customers(id))`,
	`CREATE TABLE lumen_er_lines (order_id INT, line INT, PRIMARY KEY (order_id, line), FOREIGN KEY (order_id) REFERENCES lumen_er_orders(id))`,
	`CREATE TABLE lumen_er_shipments (id INT PRIMARY KEY, order_id INT, line INT, FOREIGN KEY (order_id, line) REFERENCES lumen_er_lines(order_id, line))`,
	`CREATE VIEW lumen_er_view AS SELECT id FROM lumen_er_orders`,
}

var engineDrop = []string{
	`DROP VIEW IF EXISTS lumen_er_view`,
	`DROP TABLE IF EXISTS lumen_er_shipments`,
	`DROP TABLE IF EXISTS lumen_er_lines`,
	`DROP TABLE IF EXISTS lumen_er_orders`,
	`DROP TABLE IF EXISTS lumen_er_customers`,
}

func TestReadPostgres(t *testing.T) {
	dsn := os.Getenv("LUMEN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("LUMEN_TEST_POSTGRES_DSN not set")
	}
	pc, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	checkEngine(t, driver.Postgres, driver.Config{
		Host: pc.Host, Port: int(pc.Port), User: pc.User, Password: pc.Password,
		Database: pc.Database, TLS: driver.TLSDisable,
	})
}

func TestReadMySQL(t *testing.T) {
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
	checkEngine(t, driver.MySQL, driver.Config{
		Host: host, Port: p, User: mc.User, Password: mc.Passwd, Database: mc.DBName, TLS: driver.TLSDisable,
	})
}

func checkEngine(t *testing.T, d driver.Dialect, cfg driver.Config) {
	c, err := driver.Open(t.Context(), d, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Cleanups run after t.Context is cancelled and in reverse order: drop
	// the tables first, then close the pool.
	t.Cleanup(func() { _ = c.Close() })
	t.Cleanup(func() { exec(context.Background(), t, c, engineDrop) })
	exec(t.Context(), t, c, engineDrop)
	exec(t.Context(), t, c, engineDDL)

	got, err := schema.Read(t.Context(), c, schema.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if v := find(t, got, "lumen_er_view"); v.Kind != "view" {
		t.Fatalf("view kind = %q", v.Kind)
	}
	lines := find(t, got, "lumen_er_lines")
	if len(lines.Columns) != 2 || !lines.Columns[0].PrimaryKey || !lines.Columns[1].PrimaryKey {
		t.Fatalf("lines columns = %+v", lines.Columns)
	}
	if email := find(t, got, "lumen_er_customers").Columns[1]; email.Nullable || email.Type == "" {
		t.Fatalf("email = %+v", email)
	}

	type edge struct{ from, fromCols, to, toCols string }
	var edges []edge
	for _, fk := range got.ForeignKeys {
		if len(fk.FromTable) > 9 && fk.FromTable[:9] == "lumen_er_" {
			edges = append(edges, edge{fk.FromTable, join(fk.FromColumns), fk.ToTable, join(fk.ToColumns)})
		}
	}
	want := []edge{
		{"lumen_er_customers", "referred_by", "lumen_er_customers", "id"},
		{"lumen_er_lines", "order_id", "lumen_er_orders", "id"},
		{"lumen_er_orders", "customer_id", "lumen_er_customers", "id"},
		{"lumen_er_shipments", "order_id,line", "lumen_er_lines", "order_id,line"},
	}
	edges = sortEdges(edges, func(e edge) string { return e.from })
	if len(edges) != len(want) {
		t.Fatalf("edges = %+v", edges)
	}
	for i := range want {
		if edges[i] != want[i] {
			t.Fatalf("edge %d = %+v, want %+v", i, edges[i], want[i])
		}
	}
}
