package schema_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/phankieuphu/lumen/internal/driver"
	_ "github.com/phankieuphu/lumen/internal/driver/sqlite"
	"github.com/phankieuphu/lumen/internal/schema"
)

// shopDDL builds a small store: a composite FK, an implicit-PK reference,
// a self reference, and a view.
var shopDDL = []string{
	`CREATE TABLE customers (id INTEGER PRIMARY KEY, email TEXT NOT NULL, referred_by INTEGER REFERENCES customers(id))`,
	`CREATE TABLE products (sku TEXT PRIMARY KEY, name TEXT)`,
	`CREATE TABLE orders (id INTEGER PRIMARY KEY, customer_id INTEGER NOT NULL REFERENCES customers)`,
	`CREATE TABLE order_lines (order_id INTEGER, line INTEGER, sku TEXT REFERENCES products(sku), PRIMARY KEY (order_id, line), FOREIGN KEY (order_id) REFERENCES orders(id))`,
	`CREATE TABLE shipments (id INTEGER PRIMARY KEY, order_id INTEGER, line INTEGER, FOREIGN KEY (order_id, line) REFERENCES order_lines(order_id, line))`,
	`CREATE VIEW big_orders AS SELECT * FROM orders`,
}

func openSQLite(t *testing.T, ddl []string) driver.Conn {
	t.Helper()
	c, err := driver.Open(t.Context(), driver.SQLite, driver.Config{Database: filepath.Join(t.TempDir(), "s.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	exec(t.Context(), t, c, ddl)
	return c
}

func exec(ctx context.Context, t *testing.T, c driver.Conn, ddl []string) {
	t.Helper()
	s, err := c.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, q := range ddl {
		if _, err := s.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func TestReadSQLite(t *testing.T) {
	c := openSQLite(t, shopDDL)
	got, err := schema.Read(t.Context(), c, schema.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Dialect != driver.SQLite || got.Truncated {
		t.Fatalf("dialect=%q truncated=%v", got.Dialect, got.Truncated)
	}

	t.Run("tables_and_kinds", func(t *testing.T) {
		kinds := map[string]string{}
		for _, tb := range got.Tables {
			kinds[tb.Name] = tb.Kind
		}
		want := map[string]string{
			"big_orders": "view", "customers": "table", "order_lines": "table",
			"orders": "table", "products": "table", "shipments": "table",
		}
		if !reflect.DeepEqual(kinds, want) {
			t.Fatalf("tables = %v, want %v", kinds, want)
		}
	})

	t.Run("columns_keep_order_pk_and_nullability", func(t *testing.T) {
		tb := find(t, got, "order_lines")
		want := []schema.Column{
			{Name: "order_id", Type: "INTEGER", Nullable: true, PrimaryKey: true},
			{Name: "line", Type: "INTEGER", Nullable: true, PrimaryKey: true},
			{Name: "sku", Type: "TEXT", Nullable: true},
		}
		if !reflect.DeepEqual(tb.Columns, want) {
			t.Fatalf("columns = %+v", tb.Columns)
		}
		if c := find(t, got, "customers").Columns[1]; c.Nullable || c.PrimaryKey {
			t.Fatalf("email = %+v, want NOT NULL, not PK", c)
		}
	})

	t.Run("foreign_keys", func(t *testing.T) {
		type edge struct{ from, fromCols, to, toCols string }
		edges := make([]edge, 0, len(got.ForeignKeys))
		for _, fk := range got.ForeignKeys {
			edges = append(edges, edge{fk.FromTable, join(fk.FromColumns), fk.ToTable, join(fk.ToColumns)})
		}
		want := []edge{
			{"customers", "referred_by", "customers", "id"},
			{"order_lines", "order_id", "orders", "id"},
			{"order_lines", "sku", "products", "sku"},
			{"orders", "customer_id", "customers", "id"}, // implicit PK resolved
			{"shipments", "order_id,line", "order_lines", "order_id,line"},
		}
		if !reflect.DeepEqual(sortEdges(edges, func(e edge) string { return e.from + e.fromCols }), want) {
			t.Fatalf("edges = %+v\nwant   %+v", edges, want)
		}
	})
}

func TestReadLimits(t *testing.T) {
	c := openSQLite(t, shopDDL)
	tests := []struct {
		name       string
		lim        schema.Limits
		wantTables int
	}{
		{"max_tables", schema.Limits{MaxTables: 2}, 2},
		{"max_columns", schema.Limits{MaxColumns: 4}, 2}, // big_orders(3) + customers(1 of 3)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := schema.Read(t.Context(), c, tt.lim)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Truncated || len(got.Tables) != tt.wantTables {
				t.Fatalf("truncated=%v tables=%d, want true, %d", got.Truncated, len(got.Tables), tt.wantTables)
			}
			names := map[string]bool{}
			for _, tb := range got.Tables {
				names[tb.Name] = true
			}
			for _, fk := range got.ForeignKeys {
				if !names[fk.FromTable] || !names[fk.ToTable] {
					t.Fatalf("edge to a table outside the result: %+v", fk)
				}
			}
		})
	}
}

func TestReadEmptyDatabase(t *testing.T) {
	got, err := schema.Read(t.Context(), openSQLite(t, nil), schema.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Tables == nil || got.ForeignKeys == nil || len(got.Tables) != 0 {
		t.Fatalf("got %+v, want empty non-nil slices", got)
	}
}

func TestReadReadOnlyConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ro.db")
	rw, err := driver.Open(t.Context(), driver.SQLite, driver.Config{Database: path})
	if err != nil {
		t.Fatal(err)
	}
	exec(t.Context(), t, rw, shopDDL)
	_ = rw.Close()

	ro, err := driver.Open(t.Context(), driver.SQLite, driver.Config{Database: path, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	got, err := schema.Read(t.Context(), ro, schema.Limits{})
	if err != nil || len(got.ForeignKeys) != 5 {
		t.Fatalf("read-only read: fks=%d err=%v", len(got.ForeignKeys), err)
	}
}

func TestReadCancelled(t *testing.T) {
	c := openSQLite(t, shopDDL)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := schema.Read(ctx, c, schema.Limits{}); err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func find(t *testing.T, s schema.Schema, name string) schema.Table {
	t.Helper()
	for _, tb := range s.Tables {
		if tb.Name == name {
			return tb
		}
	}
	t.Fatalf("table %q not found", name)
	return schema.Table{}
}
