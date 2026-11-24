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

// isWrite stands in for the query classifier, which lint does not import.
func isWrite(stmt string) bool {
	verb, _, _ := strings.Cut(strings.ToUpper(strings.TrimSpace(stmt)), " ")
	return verb != "SELECT"
}

func TestRules(t *testing.T) {
	tests := []struct {
		name string
		src  string
		prod bool
		// readOnly lints for a read-only connection.
		readOnly bool
		want     []hit
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
		{name: "L002 bang equals", src: "SELECT id FROM users WHERE email != NULL", want: []hit{{"L002", "NULL"}}},

		// L003 NOT IN (subquery)
		{name: "L003", src: "SELECT id FROM a WHERE x NOT IN (SELECT y FROM b)", want: []hit{{"L003", "NOT IN"}}},
		{name: "L003 in list", src: "SELECT id FROM a WHERE x NOT IN (1, 2)"},
		{name: "L003 in subquery", src: "SELECT id FROM a WHERE x IN (SELECT y FROM b)"},

		// L005 NULL in a NOT IN list
		{name: "L005", src: "SELECT id FROM t WHERE x NOT IN (1, NULL)", want: []hit{{"L005", "NULL"}}},
		{name: "L005 in delete", src: "DELETE FROM t WHERE x NOT IN (NULL)", want: []hit{{"L005", "NULL"}}},
		{name: "L005 in list", src: "SELECT id FROM t WHERE x IN (1, NULL)"},
		{name: "L005 no null", src: "SELECT id FROM t WHERE x NOT IN (1, 2)"},

		// L006 OFFSET without ORDER BY
		{name: "L006", src: "SELECT id FROM t LIMIT 50 OFFSET 100", want: []hit{{"L006", "100"}}},
		{name: "L006 mysql comma", src: "SELECT id FROM t LIMIT 100, 50", want: []hit{{"L006", "100"}}, only: driver.MySQL},
		{name: "L006 ordered", src: "SELECT id FROM t ORDER BY id LIMIT 50 OFFSET 100"},
		{name: "L006 limit only", src: "SELECT id FROM t LIMIT 50"},
		{name: "L006 offset zero", src: "SELECT id FROM t LIMIT 50 OFFSET 0"},

		// L007 AND and OR mixed without parentheses in UPDATE or DELETE
		{name: "L007 delete", src: "DELETE FROM t WHERE a = 1 AND b = 2 OR c = 3", want: []hit{{"L007", "OR"}}},
		{name: "L007 or first", src: "UPDATE t SET x = 1 WHERE a = 1 OR b = 2 AND c = 3", want: []hit{{"L007", "OR"}}},
		{name: "L007 parenthesized", src: "DELETE FROM t WHERE a = 1 AND (b = 2 OR c = 3)"},
		{name: "L007 both parenthesized", src: "DELETE FROM t WHERE (a = 1 AND b = 2) OR c = 3"},
		{name: "L007 only and", src: "DELETE FROM t WHERE a = 1 AND b = 2"},
		{name: "L007 between", src: "DELETE FROM t WHERE a BETWEEN 1 AND 5 OR b = 2"},
		{name: "L007 case", src: "UPDATE t SET x = 1 WHERE CASE WHEN a = 1 AND b = 2 THEN 1 END = 1 OR c = 3"},
		{name: "L007 subquery", src: "DELETE FROM t WHERE id IN (SELECT id FROM u WHERE a = 1 AND b = 2 OR c = 3)"},
		{name: "L007 select", src: "SELECT id FROM t WHERE a = 1 AND b = 2 OR c = 3"},
		{name: "L007 first or", src: "DELETE FROM t WHERE a = 1 AND b = 2 OR c = 3 OR d = 4", want: []hit{{"L007", "OR"}}},
		{name: "L007 not between", src: "DELETE FROM t WHERE a NOT BETWEEN 1 AND 5 AND c = 1 OR b = 2", want: []hit{{"L007", "OR"}}},
		{name: "L007 with", src: "WITH x AS (SELECT 1) DELETE FROM t WHERE a = 1 AND b = 2 OR c = 3", want: []hit{{"L007", "OR"}}},
		{name: "L007 case in set", src: "UPDATE t SET x = CASE WHEN a = 1 OR b = 2 THEN 1 END WHERE id = 1 AND c = 2"},
		{name: "L007 ends at order by", src: "DELETE FROM t WHERE a = 1 AND b = 2 ORDER BY id LIMIT 1", only: driver.MySQL},
		{name: "L007 ends at returning", src: "DELETE FROM t WHERE a = 1 AND b = 2 RETURNING a OR b", only: driver.Postgres},
		{name: "L007 join on", src: "DELETE t FROM t JOIN u ON t.id = u.id AND u.a = 1 OR u.b = 2 WHERE t.c = 1", only: driver.MySQL},
		{name: "L007 multi-table", src: "DELETE t FROM t JOIN u ON t.id = u.id WHERE a = 1 AND b = 2 OR c = 3", want: []hit{{"L007", "OR"}}, only: driver.MySQL},
		{name: "L007 mysql symbols", src: "DELETE FROM t WHERE a = 1 && b = 2 || c = 3", want: []hit{{"L007", "||"}}, only: driver.MySQL},
		{name: "L007 mysql xor", src: "DELETE FROM t WHERE a = 1 AND b = 2 XOR c = 3", want: []hit{{"L007", "XOR"}}, only: driver.MySQL},
		{name: "L007 mysql bitwise", src: "DELETE FROM t WHERE a & 1 = 1 OR c = 3", only: driver.MySQL},
		{name: "L007 postgres concat", src: "DELETE FROM t WHERE a = 'x' || b AND c = 1", only: driver.Postgres},

		// L004 comma join without a condition
		{name: "L004", src: "SELECT a.id FROM a, b", want: []hit{{"L004", "FROM"}}},
		{name: "L004 nested", src: "SELECT id FROM t WHERE id IN (SELECT a.id FROM a, b)", want: []hit{{"L004", "FROM"}}},
		{name: "L004 with where", src: "SELECT a.id FROM a, b WHERE a.id = b.aid"},
		{name: "L004 explicit join", src: "SELECT a.id FROM a JOIN b ON a.id = b.aid"},
		{name: "L004 cross join", src: "SELECT a.id FROM a CROSS JOIN b", only: driver.Postgres},

		// L101 SELECT *
		{name: "L101", src: "SELECT * FROM users", want: []hit{{"L101", "*"}}},
		{name: "L101 qualified", src: "SELECT u.* FROM users u", want: []hit{{"L101", "u"}}},
		{name: "L101 count star", src: "SELECT count(*) FROM users"},
		{name: "L101 exists", src: "SELECT id FROM a WHERE EXISTS (SELECT * FROM b WHERE b.aid = a.id)"},
		{name: "L101 insert select", src: "INSERT INTO t (a, b) SELECT * FROM u", want: []hit{{"L101", "*"}}},
		{name: "L101 insert from derived", src: "INSERT INTO t (a) SELECT a FROM (SELECT * FROM u) s", want: []hit{{"L101", "*"}}},

		// L102 leading wildcard
		{name: "L102", src: "SELECT id FROM users WHERE email LIKE '%@example.com'", want: []hit{{"L102", "'%@example.com'"}}},
		{name: "L102 anchored", src: "SELECT id FROM users WHERE email LIKE 'ann%'"},
		{name: "L102 ilike", src: "SELECT id FROM users WHERE email ILIKE '%@example.com'", want: []hit{{"L102", "'%@example.com'"}}, only: driver.Postgres},

		// L103 function on a column in WHERE
		{name: "L103", src: "SELECT id FROM users WHERE lower(email) = 'a@b.c'", want: []hit{{"L103", "lower"}}},
		{name: "L103 right side", src: "SELECT id FROM t WHERE 2024 = year(created_at)", want: []hit{{"L103", "year"}}},
		{name: "L103 on a literal", src: "SELECT id FROM users WHERE email = lower('A@B.C')"},
		{name: "L103 in select list", src: "SELECT lower(email) FROM users"},
		{name: "L103 like", src: "SELECT id FROM users WHERE lower(email) LIKE 'ann%'", want: []hit{{"L103", "lower"}}},
		{name: "L103 in", src: "SELECT id FROM users WHERE upper(code) IN ('A', 'B')", want: []hit{{"L103", "upper"}}},

		// L104 ORDER BY RAND()
		{name: "L104 rand", src: "SELECT id FROM t ORDER BY rand() LIMIT 1", want: []hit{{"L104", "rand"}}, only: driver.MySQL},
		{name: "L104 random", src: "SELECT id FROM t ORDER BY random() LIMIT 1", want: []hit{{"L104", "random"}}, only: driver.Postgres},
		{name: "L104 by column", src: "SELECT id FROM t ORDER BY id"},

		// L105 deep OFFSET
		{name: "L105", src: "SELECT id FROM t ORDER BY id LIMIT 50 OFFSET 20000", want: []hit{{"L105", "20000"}}},
		{name: "L105 small", src: "SELECT id FROM t ORDER BY id LIMIT 50 OFFSET 100"},
		{name: "L105 at the limit", src: "SELECT id FROM t ORDER BY id LIMIT 50 OFFSET 10000"},

		// L106 INSERT without columns
		{name: "L106 values", src: "INSERT INTO t VALUES (1, 'a')", want: []hit{{"L106", "INSERT"}}},
		{name: "L106 select", src: "INSERT INTO t SELECT id, name FROM u", want: []hit{{"L106", "INSERT"}}},
		{name: "L106 named", src: "INSERT INTO t (id, name) VALUES (1, 'a')"},

		// L201 DDL on production
		{name: "L201 drop", src: "DROP TABLE old_orders", prod: true, want: []hit{{"L201", "DROP"}}},
		{name: "L201 alter", src: "ALTER TABLE t ADD COLUMN c int", prod: true, want: []hit{{"L201", "ALTER"}}},
		{name: "L201 truncate", src: "TRUNCATE TABLE old_orders", prod: true, want: []hit{{"L201", "TRUNCATE"}}},
		{name: "L201 not prod", src: "DROP TABLE old_orders"},
		{name: "L201 create", src: "CREATE TABLE t (id int)", prod: true},

		// L202 write on a read-only connection
		{name: "L202 update", src: "UPDATE t SET a = 1 WHERE id = 1", readOnly: true, want: []hit{{"L202", "UPDATE"}}},
		{name: "L202 drop", src: "DROP TABLE t", readOnly: true, want: []hit{{"L202", "DROP"}}},
		{name: "L202 read", src: "SELECT id FROM t", readOnly: true},
		{name: "L202 writable", src: "UPDATE t SET a = 1 WHERE id = 1"},
		{name: "L202 does not parse", src: "UPDATE OR IGNORE t SET a = 1", readOnly: true, want: []hit{{"L202", "UPDATE"}}},
		{name: "L202 not L201", src: "DROP TABLE t", readOnly: true, prod: true, want: []hit{{"L202", "DROP"}}},

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
				got := hits(tt.src, Context{Dialect: d, Prod: tt.prod, ReadOnly: tt.readOnly, IsWrite: isWrite})
				if fmt.Sprint(got) != fmt.Sprint(tt.want) {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			})
		}
	}
}

func TestMessagesAndSeverities(t *testing.T) {
	src := "UPDATE t SET a = 1; SELECT * FROM t; DROP TABLE t; " +
		"SELECT a FROM t WHERE a NOT IN (NULL) LIMIT 1, 1; DELETE FROM t WHERE a = 1 AND b = 2 OR c = 3; " +
		"INSERT INTO t (a) SELECT * FROM u; INSERT INTO t (a) SELECT a FROM (SELECT * FROM u) s"
	insert, derived := strings.Index(src, "INSERT"), strings.LastIndex(src, "*")
	want := map[string]Severity{"L001": Error, "L005": Error, "L006": Warning, "L007": Error, "L101": Info, "L202": Error}
	// L201 is left out: a read-only connection refuses DDL before any
	// confirmation; TestRules covers it.
	seen := map[string]bool{}
	for _, f := range Check(src, Context{Dialect: driver.MySQL, Prod: true, ReadOnly: true, IsWrite: isWrite}) {
		seen[f.Rule] = true
		sev := want[f.Rule]
		if f.Rule == "L101" && f.Start > insert && f.Start != derived {
			// INSERT ... SELECT * is a warning; a plain SELECT * only info.
			sev = Warning
		}
		if f.Severity != sev {
			t.Errorf("%s at %d: severity %s, want %s", f.Rule, f.Start, f.Severity, sev)
		}
		if f.Message == "" || !strings.HasSuffix(f.Message, ".") {
			t.Errorf("%s: message %q", f.Rule, f.Message)
		}
	}
	for rule := range want {
		if !seen[rule] {
			t.Errorf("%s: no finding", rule)
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
