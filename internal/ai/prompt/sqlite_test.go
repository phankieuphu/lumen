package prompt

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/phankieuphu/lumen/internal/driver"
	_ "github.com/phankieuphu/lumen/internal/driver/sqlite"
	"github.com/phankieuphu/lumen/internal/schema"
)

// readSQLite reads the schema of a fresh SQLite file built from ddl, so
// Build is fed what schema.Read really returns.
func readSQLite(t *testing.T, ddl ...string) schema.Schema {
	t.Helper()
	ctx := t.Context()
	c, err := driver.Open(ctx, driver.SQLite, driver.Config{Database: filepath.Join(t.TempDir(), "p.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	s, err := c.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range ddl {
		if _, err := s.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	sch, err := schema.Read(ctx, c, schema.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

func TestSQLiteSchema(t *testing.T) {
	sch := readSQLite(t,
		`CREATE TABLE customers (id INTEGER PRIMARY KEY, email TEXT NOT NULL)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, customer_id INTEGER NOT NULL REFERENCES customers)`,
		`CREATE VIEW recent AS SELECT * FROM orders`,
	)
	in := editorInput(TextToSQL, sch, "SELECT * FROM orders WHERE note = 'private'|")
	in.Dialect = ""
	c := mustBuild(t, in)
	if c.Dialect != driver.SQLite {
		t.Fatalf("dialect = %q", c.Dialect)
	}
	if len(c.Tables) != 3 || c.Tables[0].Name != "orders" || c.Tables[1].Name != "customers" {
		t.Fatalf("tables = %+v", c.Tables)
	}
	s := c.Sections[1]
	want := "orders(id INTEGER PK, customer_id INTEGER NOT NULL)\n  FK (customer_id) -> customers(id)"
	if s.Part != PartSchema || !strings.Contains(s.Text, want) || !strings.Contains(s.Text, "view recent(") || strings.Contains(s.Text, "main.") {
		t.Fatalf("schema section:\n%s", s.Text)
	}
	if strings.Contains(c.User, "private") {
		t.Fatal("literal sent")
	}
}
