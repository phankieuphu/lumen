package lint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/sqlparse"
)

// hit is an expected finding: its rule and the text it marks.
type hit struct{ rule, mark string }

func (h hit) String() string { return h.rule + " " + h.mark }

// dialects are the dialects with a syntax tree in this build.
func dialects() []driver.Dialect {
	if _, err := sqlparse.Parse("SELECT 1", driver.Postgres); err == nil {
		return []driver.Dialect{driver.Postgres, driver.MySQL}
	}
	return []driver.Dialect{driver.MySQL}
}

func hits(src string, ctx Context) []hit {
	fs := Check(src, ctx)
	out := make([]hit, 0, len(fs))
	for _, f := range fs {
		out = append(out, hit{f.Rule, src[f.Start:f.End]})
	}
	return out
}

func TestRules(t *testing.T) {
	tests := []struct {
		name string
		src  string
		prod bool
		want []hit
		// only limits a case to one dialect's syntax.
		only driver.Dialect
	}{
		// L001 UPDATE or DELETE without WHERE
		{name: "L001 update", src: "UPDATE users SET active = false", want: []hit{{"L001", "UPDATE"}}},
		{name: "L001 delete", src: "DELETE FROM users", want: []hit{{"L001", "DELETE"}}},
		{name: "L001 with where", src: "DELETE FROM users WHERE id = 1"},

		// L002 = NULL
		{name: "L002 equals", src: "SELECT id FROM users WHERE email = NULL", want: []hit{{"L002", "NULL"}}},
		{name: "L002 not equals", src: "SELECT id FROM users WHERE NULL <> email", want: []hit{{"L002", "NULL"}}},
		{name: "L002 in update", src: "UPDATE users SET a = 1 WHERE b = NULL", want: []hit{{"L002", "NULL"}}},
		{name: "L002 in a case", src: "SELECT CASE WHEN a = NULL THEN 1 END AS x FROM t", want: []hit{{"L002", "NULL"}}},
		{name: "L002 is null", src: "SELECT id FROM users WHERE email IS NULL"},
		{name: "L002 assignment", src: "UPDATE users SET email = NULL WHERE id = 1"},

		// L003 NOT IN (subquery)
		{name: "L003", src: "SELECT id FROM a WHERE x NOT IN (SELECT y FROM b)", want: []hit{{"L003", "NOT IN"}}},
		{name: "L003 in list", src: "SELECT id FROM a WHERE x NOT IN (1, 2)"},
		{name: "L003 in subquery", src: "SELECT id FROM a WHERE x IN (SELECT y FROM b)"},

		// L004 comma join without a condition
		{name: "L004", src: "SELECT a.id FROM a, b", want: []hit{{"L004", "FROM"}}},
		{name: "L004 nested", src: "SELECT id FROM t WHERE id IN (SELECT a.id FROM a, b)", want: []hit{{"L004", "FROM"}}},
		{name: "L004 with where", src: "SELECT a.id FROM a, b WHERE a.id = b.aid"},
		{name: "L004 explicit join", src: "SELECT a.id FROM a JOIN b ON a.id = b.aid"},

		// L101 SELECT *
		{name: "L101", src: "SELECT * FROM users", want: []hit{{"L101", "*"}}},
		{name: "L101 qualified", src: "SELECT u.* FROM users u", want: []hit{{"L101", "u"}}},
		{name: "L101 count star", src: "SELECT count(*) FROM users"},
		{name: "L101 exists", src: "SELECT id FROM a WHERE EXISTS (SELECT * FROM b WHERE b.aid = a.id)"},

		// L102 leading wildcard
		{name: "L102", src: "SELECT id FROM users WHERE email LIKE '%@example.com'", want: []hit{{"L102", "'%@example.com'"}}},
		{name: "L102 anchored", src: "SELECT id FROM users WHERE email LIKE 'ann%'"},

		// L103 function on a column in WHERE
		{name: "L103", src: "SELECT id FROM users WHERE lower(email) = 'a@b.c'", want: []hit{{"L103", "lower"}}},
		{name: "L103 right side", src: "SELECT id FROM t WHERE 2024 = year(created_at)", want: []hit{{"L103", "year"}}},
		{name: "L103 on a literal", src: "SELECT id FROM users WHERE email = lower('A@B.C')"},
		{name: "L103 in select list", src: "SELECT lower(email) FROM users"},

		// L104 ORDER BY RAND()
		{name: "L104 rand", src: "SELECT id FROM t ORDER BY rand() LIMIT 1", want: []hit{{"L104", "rand"}}, only: driver.MySQL},
		{name: "L104 random", src: "SELECT id FROM t ORDER BY random() LIMIT 1", want: []hit{{"L104", "random"}}, only: driver.Postgres},
		{name: "L104 by column", src: "SELECT id FROM t ORDER BY id"},

		// L105 deep OFFSET
		{name: "L105", src: "SELECT id FROM t ORDER BY id LIMIT 50 OFFSET 20000", want: []hit{{"L105", "20000"}}},
		{name: "L105 small", src: "SELECT id FROM t ORDER BY id LIMIT 50 OFFSET 100"},

		// L106 INSERT without columns
		{name: "L106 values", src: "INSERT INTO t VALUES (1, 'a')", want: []hit{{"L106", "INSERT"}}},
		{name: "L106 select", src: "INSERT INTO t SELECT id, name FROM u", want: []hit{{"L106", "INSERT"}}},
		{name: "L106 named", src: "INSERT INTO t (id, name) VALUES (1, 'a')"},

		// L201 DDL on production
		{name: "L201 drop", src: "DROP TABLE old_orders", prod: true, want: []hit{{"L201", "DROP"}}},
		{name: "L201 alter", src: "ALTER TABLE t ADD COLUMN c int", prod: true, want: []hit{{"L201", "ALTER"}}},
		{name: "L201 not prod", src: "DROP TABLE old_orders"},
		{name: "L201 create", src: "CREATE TABLE t (id int)", prod: true},

		// Several findings, in order, across statements.
		{
			name: "several",
			src:  "SELECT * FROM users WHERE name = NULL;\nDELETE FROM sessions",
			want: []hit{{"L101", "*"}, {"L002", "NULL"}, {"L001", "DELETE"}},
		},
		// Statements that do not parse are left to the syntax check.
		{name: "syntax error", src: "SELEC * FORM users"},
	}
	for _, d := range dialects() {
		for _, tt := range tests {
			if tt.only != "" && tt.only != d {
				continue
			}
			t.Run(fmt.Sprintf("%s/%s", d, tt.name), func(t *testing.T) {
				got := hits(tt.src, Context{Dialect: d, Prod: tt.prod})
				if fmt.Sprint(got) != fmt.Sprint(tt.want) {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			})
		}
	}
}

func TestMessagesAndSeverities(t *testing.T) {
	src := "UPDATE t SET a = 1; SELECT * FROM t; DROP TABLE t"
	want := map[string]Severity{"L001": Error, "L101": Warning, "L201": Info}
	for _, f := range Check(src, Context{Dialect: driver.MySQL, Prod: true}) {
		if want[f.Rule] != f.Severity {
			t.Errorf("%s: severity %s, want %s", f.Rule, f.Severity, want[f.Rule])
		}
		if f.Message == "" || !strings.HasSuffix(f.Message, ".") {
			t.Errorf("%s: message %q", f.Rule, f.Message)
		}
	}
}

func TestFindingsAreCapped(t *testing.T) {
	src := strings.Repeat("DELETE FROM t;", MaxFindings+20)
	if got := len(Check(src, Context{Dialect: driver.MySQL})); got != MaxFindings {
		t.Fatalf("%d findings, want MaxFindings = %d", got, MaxFindings)
	}
}

func TestUnknownDialect(t *testing.T) {
	if got := Check("DELETE FROM t", Context{Dialect: "oracle"}); got != nil {
		t.Fatalf("got %v for a dialect without a syntax tree", got)
	}
}
