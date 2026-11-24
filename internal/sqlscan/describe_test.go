package sqlscan

import (
	"slices"
	"testing"
)

func TestDescribe(t *testing.T) {
	tests := []struct {
		name    string
		dialect Dialect
		sql     string
		verb    string
		tables  []string
	}{
		{"select with join and aliases", Postgres, `SELECT c.id FROM orders o JOIN customers c ON c.id = o.customer_id WHERE o.total > $1`, "SELECT", []string{"orders", "customers"}},
		{"comma separated from list", Postgres, `SELECT * FROM a x, b AS y, c WHERE x.id IN ($1, $2)`, "SELECT", []string{"a", "b", "c"}},
		{"schema qualified and quoted", Postgres, `SELECT 1 FROM public."Order Lines" l`, "SELECT", []string{"public.Order Lines"}},
		{"backtick names on mysql", MySQL, "SELECT * FROM `shop`.`orders` WHERE `id` = ?", "SELECT", []string{"shop.orders"}},
		{"subquery in from", Postgres, `SELECT * FROM (SELECT id FROM inner_t) s JOIN outer_t o ON o.id = s.id`, "SELECT", []string{"inner_t", "outer_t"}},
		{"from inside a function call is not a table", Postgres, `SELECT EXTRACT(year FROM created_at) FROM events`, "SELECT", []string{"events"}},
		{"set returning function is not a table", Postgres, `SELECT * FROM generate_series($1, $2) g, t2`, "SELECT", []string{"t2"}},
		{"insert with column list", Postgres, `INSERT INTO audit (a, b) VALUES ($1, $2)`, "INSERT", []string{"audit"}},
		{"insert from select", Postgres, `INSERT INTO archive SELECT * FROM live WHERE at < $1`, "INSERT", []string{"archive", "live"}},
		{"update with from", Postgres, `UPDATE orders SET status = $1 FROM customers c WHERE c.id = orders.customer_id`, "UPDATE", []string{"orders", "customers"}},
		{"delete", Postgres, `DELETE FROM ONLY sessions WHERE expires < now()`, "DELETE", []string{"sessions"}},
		{"for update names no table", Postgres, `SELECT * FROM jobs WHERE id = $1 FOR UPDATE SKIP LOCKED`, "SELECT", []string{"jobs"}},
		{"on duplicate key update names no table", MySQL, "INSERT INTO hits (k, n) VALUES (?, ?) ON DUPLICATE KEY UPDATE n = n + ?", "INSERT", []string{"hits"}},
		{"same table twice is listed once", Postgres, `SELECT * FROM t a JOIN t b ON a.id = b.parent`, "SELECT", []string{"t"}},
		{"keyword in a string is ignored", Postgres, `SELECT 'from nowhere' FROM real_t`, "SELECT", []string{"real_t"}},
		{"no table", Postgres, `SELECT $1 + $2`, "SELECT", []string{}},
		{"utility statement", Postgres, `VACUUM ANALYZE`, "VACUUM", []string{}},
		{"empty", Postgres, ``, "", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verb, tables := Describe(tt.sql, tt.dialect)
			if verb != tt.verb || !slices.Equal(tables, tt.tables) {
				t.Fatalf("Describe = %q %q, want %q %q", verb, tables, tt.verb, tt.tables)
			}
		})
	}
}
