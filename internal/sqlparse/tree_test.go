package sqlparse

import (
	"errors"
	"testing"

	"github.com/phankieuphu/lumen/internal/driver"
)

// treeDialects are the dialects with an adapter in this build.
func treeDialects(t *testing.T) []driver.Dialect {
	t.Helper()
	if postgresTrees {
		return []driver.Dialect{driver.Postgres, driver.MySQL}
	}
	return []driver.Dialect{driver.MySQL}
}

func parseOne(t *testing.T, src string, d driver.Dialect) Stmt {
	t.Helper()
	got, err := Parse(src, d)
	if err != nil || len(got) != 1 {
		t.Fatalf("%s: Parse(%q) = %d statements, %v", d, src, len(got), err)
	}
	return got[0]
}

// at is the text of src at p, for checking positions.
func at(src string, p, n int) string {
	if p < 0 || p+n > len(src) {
		return "<out of range>"
	}
	return src[p : p+n]
}

func TestTreeStatements(t *testing.T) {
	tests := []struct {
		src     string
		kind    StmtKind
		verb    string
		where   bool
		columns bool
	}{
		{"SELECT 1", StmtSelect, "SELECT", false, false},
		{"UPDATE t SET a = 1", StmtUpdate, "UPDATE", false, false},
		{"UPDATE t SET a = 1 WHERE id = 2", StmtUpdate, "UPDATE", true, false},
		{"DELETE FROM t", StmtDelete, "DELETE", false, false},
		{"INSERT INTO t VALUES (1)", StmtInsert, "INSERT", false, false},
		{"INSERT INTO t (a) VALUES (1)", StmtInsert, "INSERT", false, true},
		{"DROP TABLE t", StmtDDL, "DROP", false, false},
		{"EXPLAIN SELECT 1", StmtSelect, "EXPLAIN", false, false},
	}
	for _, d := range treeDialects(t) {
		for _, tt := range tests {
			st := parseOne(t, tt.src, d)
			if st.Kind != tt.kind || st.Verb != tt.verb || (st.Where != nil) != tt.where || st.Columns != tt.columns {
				t.Errorf("%s %q: kind %d verb %q where %v columns %v", d, tt.src, st.Kind, st.Verb, st.Where != nil, st.Columns)
			}
		}
	}
}

func TestTreeSelect(t *testing.T) {
	src := "SELECT *, t.a FROM t, u WHERE t.b = NULL AND lower(c) LIKE '%x' AND d NOT IN (SELECT e FROM f) ORDER BY rand() LIMIT 5 OFFSET 20000"
	for _, d := range treeDialects(t) {
		// Statements start after leading text: positions are in the whole SQL.
		full := "SELECT 0;\n" + src
		got, err := Parse(full, d)
		if err != nil || len(got) != 2 {
			t.Fatalf("%s: %d statements, %v", d, len(got), err)
		}
		s := got[1].Select
		if len(s.Items) != 2 || !s.Items[0].Star || at(full, s.Items[0].Pos, 1) != "*" {
			t.Errorf("%s: items %+v", d, s.Items)
		}
		if len(s.From) != 2 {
			t.Errorf("%s: %d FROM items, want 2 (comma join)", d, len(s.From))
		}
		if s.Offset == nil || s.Offset.Value != "20000" || at(full, s.Offset.Pos, 5) != "20000" {
			t.Errorf("%s: offset %+v", d, s.Offset)
		}
		if len(s.OrderBy) != 1 {
			t.Fatalf("%s: order by %+v", d, s.OrderBy)
		}
		if f, ok := s.OrderBy[0].(*Func); !ok || f.Name != "rand" {
			t.Errorf("%s: order by %#v", d, s.OrderBy[0])
		}

		var nullCmp, like, notIn, fn bool
		WalkExpr(s.Where, func(e Expr) {
			switch e := e.(type) {
			case *Binary:
				if lit, ok := e.R.(*Literal); ok && e.Op == "=" && lit.Kind == LitNull {
					nullCmp = at(full, lit.Pos, 4) == "NULL"
				}
			case *Like:
				if lit, ok := e.Pattern.(*Literal); ok {
					like = lit.Value == "%x" && at(full, lit.Pos, 4) == "'%x'"
				}
			case *In:
				notIn = e.Not && e.Subquery != nil && len(e.Subquery.Items) == 1
			case *Func:
				fn = e.Name == "lower" && at(full, e.Pos, 5) == "lower"
			}
		})
		if !nullCmp || !like || !notIn || !fn {
			t.Errorf("%s: = NULL %v, LIKE %v, NOT IN subquery %v, lower() %v", d, nullCmp, like, notIn, fn)
		}

		var selects int
		got[1].Selects(func(*Select) { selects++ })
		if selects != 2 {
			t.Errorf("%s: Selects visited %d blocks, want 2", d, selects)
		}
	}
}

func TestTreeJoinsAreOneFromItem(t *testing.T) {
	for _, d := range treeDialects(t) {
		st := parseOne(t, "SELECT a FROM t JOIN u ON u.id = t.uid, (SELECT 1 AS x) s", d)
		if n := len(st.Select.From); n != 2 {
			t.Errorf("%s: %d FROM items, want 2", d, n)
		}
		if len(st.Select.From[0].Conds) != 1 {
			t.Errorf("%s: join condition missing: %+v", d, st.Select.From[0])
		}
	}
}

func TestTreeSkipsUnparsable(t *testing.T) {
	for _, d := range treeDialects(t) {
		got, err := Parse("SELEC 1; SELECT 2", d)
		if err != nil || len(got) != 1 || got[0].Start != 9 {
			t.Errorf("%s: got %+v, %v; want only the second statement", d, got, err)
		}
	}
	if _, err := Parse("SELECT 1", driver.Dialect("oracle")); !errors.Is(err, ErrNoAdapter) {
		t.Errorf("unknown dialect: err = %v, want ErrNoAdapter", err)
	}
}

func TestTreeMySQLLimitForms(t *testing.T) {
	for _, src := range []string{"SELECT a FROM t LIMIT 10 OFFSET 20000", "SELECT a FROM t LIMIT 20000, 10"} {
		st := parseOne(t, src, driver.MySQL)
		if o := st.Select.Offset; o == nil || o.Value != "20000" || at(src, o.Pos, 5) != "20000" {
			t.Errorf("%q: offset %+v", src, o)
		}
	}
}
