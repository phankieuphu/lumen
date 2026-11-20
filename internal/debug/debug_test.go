package debug

import (
	"errors"
	"strings"
	"testing"

	"github.com/phankieuphu/lumen/internal/driver"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		d    driver.Dialect
		want []string // "clause|label|count SQL"
	}{
		{
			name: "no from",
			sql:  "SELECT 1",
			want: []string{"result|Result|SELECT count(*) FROM (SELECT 1) AS lumen_part"},
		},
		{
			name: "joins, where, group, having",
			sql: "SELECT u.name, count(*) AS n FROM users u JOIN orders o ON o.user_id = u.id " +
				"LEFT OUTER JOIN refunds r ON r.order_id = o.id WHERE o.total > 10 GROUP BY 1 HAVING count(*) > 2 ORDER BY n DESC LIMIT 5",
			want: []string{
				"from|users u|SELECT count(*) FROM users u",
				"join|JOIN orders o ON o.user_id = u.id|SELECT count(*) FROM users u JOIN orders o ON o.user_id = u.id",
				"join|LEFT OUTER JOIN refunds r ON r.order_id = o.id|SELECT count(*) FROM users u JOIN orders o ON o.user_id = u.id LEFT OUTER JOIN refunds r ON r.order_id = o.id",
				"where|WHERE o.total > 10|SELECT count(*) FROM users u JOIN orders o ON o.user_id = u.id LEFT OUTER JOIN refunds r ON r.order_id = o.id WHERE o.total > 10",
				"group|GROUP BY 1|SELECT count(*) FROM (SELECT u.name, count(*) AS n FROM users u JOIN orders o ON o.user_id = u.id LEFT OUTER JOIN refunds r ON r.order_id = o.id WHERE o.total > 10 GROUP BY 1) AS lumen_part",
				"having|HAVING count(*) > 2|SELECT count(*) FROM (SELECT u.name, count(*) AS n FROM users u JOIN orders o ON o.user_id = u.id LEFT OUTER JOIN refunds r ON r.order_id = o.id WHERE o.total > 10 GROUP BY 1 HAVING count(*) > 2) AS lumen_part",
				"result|Result|SELECT count(*) FROM (SELECT u.name, count(*) AS n FROM users u JOIN orders o ON o.user_id = u.id LEFT OUTER JOIN refunds r ON r.order_id = o.id WHERE o.total > 10 GROUP BY 1 HAVING count(*) > 2 ORDER BY n DESC LIMIT 5) AS lumen_part",
			},
		},
		{
			name: "comma join and nested parentheses",
			sql:  "SELECT * FROM a, (SELECT x FROM b WHERE y IN (1, 2)) AS bb WHERE a.x = bb.x",
			want: []string{
				"from|a|SELECT count(*) FROM a",
				"join|(SELECT x FROM b WHERE y IN (1, 2)) AS bb|SELECT count(*) FROM a, (SELECT x FROM b WHERE y IN (1, 2)) AS bb",
				"where|WHERE a.x = bb.x|SELECT count(*) FROM a, (SELECT x FROM b WHERE y IN (1, 2)) AS bb WHERE a.x = bb.x",
				"result|Result|SELECT count(*) FROM (SELECT * FROM a, (SELECT x FROM b WHERE y IN (1, 2)) AS bb WHERE a.x = bb.x) AS lumen_part",
			},
		},
		{
			name: "with list",
			sql:  "WITH RECURSIVE a(n) AS (SELECT 1), b AS MATERIALIZED (SELECT n FROM a) SELECT n FROM b",
			want: []string{
				"cte|a(n) AS (SELECT 1)|WITH RECURSIVE a(n) AS (SELECT 1) SELECT count(*) FROM a",
				"cte|b AS MATERIALIZED (SELECT n FROM a)|WITH RECURSIVE a(n) AS (SELECT 1), b AS MATERIALIZED (SELECT n FROM a) SELECT count(*) FROM b",
				"from|b|WITH RECURSIVE a(n) AS (SELECT 1), b AS MATERIALIZED (SELECT n FROM a) SELECT count(*) FROM b",
				"result|Result|WITH RECURSIVE a(n) AS (SELECT 1), b AS MATERIALIZED (SELECT n FROM a) SELECT count(*) FROM (SELECT n FROM b) AS lumen_part",
			},
		},
		{
			name: "union with trailing order",
			sql:  "(SELECT id FROM a) UNION ALL SELECT id FROM b ORDER BY id",
			want: []string{
				"branch|(SELECT id FROM a)|SELECT count(*) FROM (SELECT id FROM a) AS lumen_part",
				"branch|SELECT id FROM b|SELECT count(*) FROM (SELECT id FROM b) AS lumen_part",
				"result|Result|SELECT count(*) FROM ((SELECT id FROM a) UNION ALL SELECT id FROM b ORDER BY id) AS lumen_part",
			},
		},
		{
			name: "keywords that are not clauses",
			sql: "SELECT a IS DISTINCT FROM b, percentile_cont(0.5) WITHIN GROUP (ORDER BY x), extract(year FROM d) " +
				"FROM t LEFT JOIN u USING (id) WHERE left(t.s, 1) = 'x'",
			want: []string{
				"from|t|SELECT count(*) FROM t",
				"join|LEFT JOIN u USING (id)|SELECT count(*) FROM t LEFT JOIN u USING (id)",
				"where|WHERE left(t.s, 1) = 'x'|SELECT count(*) FROM t LEFT JOIN u USING (id) WHERE left(t.s, 1) = 'x'",
				"result|Result|SELECT count(*) FROM (SELECT a IS DISTINCT FROM b, percentile_cont(0.5) WITHIN GROUP (ORDER BY x), extract(year FROM d) FROM t LEFT JOIN u USING (id) WHERE left(t.s, 1) = 'x') AS lumen_part",
			},
		},
		{
			name: "a window named in the select list",
			sql:  "SELECT a, sum(b) OVER w FROM t GROUP BY a, b WINDOW w AS (ORDER BY a) ORDER BY a",
			want: []string{
				"from|t|SELECT count(*) FROM t",
				"group|GROUP BY a, b|SELECT count(*) FROM (SELECT a, sum(b) OVER w FROM t GROUP BY a, b WINDOW w AS (ORDER BY a)) AS lumen_part",
				"result|Result|SELECT count(*) FROM (SELECT a, sum(b) OVER w FROM t GROUP BY a, b WINDOW w AS (ORDER BY a) ORDER BY a) AS lumen_part",
			},
		},
		{
			name: "mysql column named offset, and qualify",
			sql:  "SELECT * FROM t WHERE offset = 1 AND qualify = 2 LIMIT 5 OFFSET 2",
			d:    driver.MySQL,
			want: []string{
				"from|t|SELECT count(*) FROM t",
				"where|WHERE offset = 1 AND qualify = 2|SELECT count(*) FROM t WHERE offset = 1 AND qualify = 2",
				"result|Result|SELECT count(*) FROM (SELECT * FROM t WHERE offset = 1 AND qualify = 2 LIMIT 5 OFFSET 2) AS lumen_part",
			},
		},
		{
			name: "mysql quoting and trailing semicolon",
			sql:  "SELECT `from` FROM `t` WHERE `where` = 'FROM';",
			d:    driver.MySQL,
			want: []string{
				"from|`t`|SELECT count(*) FROM `t`",
				"where|WHERE `where` = 'FROM'|SELECT count(*) FROM `t` WHERE `where` = 'FROM'",
				"result|Result|SELECT count(*) FROM (SELECT `from` FROM `t` WHERE `where` = 'FROM') AS lumen_part",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := tt.d
			if d == "" {
				d = driver.Postgres
			}
			parts, err := split(tt.sql, d)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, len(parts))
			for i, p := range parts {
				got[i] = string(p.Clause) + "|" + p.Label + "|" + p.SQL
				if !strings.HasPrefix(p.SQL[p.countAt:], "count(*)") {
					t.Errorf("part %d: countAt %d does not point at count(*): %q", i, p.countAt, p.SQL)
				}
				if p.Clause != ClauseResult && p.Clause != ClauseCTE {
					if span := tt.sql[p.Start:p.End]; trim(span, 120) != p.Label {
						t.Errorf("part %d: span %q, label %q", i, span, p.Label)
					}
				}
			}
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("parts:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

func TestSplitRefuses(t *testing.T) {
	for _, sql := range []string{
		"",
		"-- only a comment",
		"UPDATE t SET a = 1",
		"WITH x AS (SELECT 1) DELETE FROM t",
		"WITH x (SELECT 1) SELECT 1",
		"SELECT a INTO b FROM t",
	} {
		if _, err := split(sql, driver.Postgres); !errors.Is(err, ErrStatement) && !errors.Is(err, ErrEmpty) {
			t.Errorf("%q: got %v", sql, err)
		}
	}
}

func TestSteps(t *testing.T) {
	tests := []struct {
		d        driver.Dialect
		sql      string
		kind     string
		control  bool
		undoable bool
	}{
		{driver.Postgres, "UPDATE t SET a = 1", "write", false, true},
		{driver.Postgres, "SELECT 1", "read", false, true},
		{driver.Postgres, "ALTER TABLE t ADD c int", "schema", false, true},
		{driver.Postgres, "COMMIT", "session", true, false},
		{driver.Postgres, "SAVEPOINT a", "session", false, false},
		{driver.Postgres, "SET search_path = x", "session", false, false},
		{driver.Postgres, "COPY t FROM '/tmp/x'", "write", false, false},
		{driver.Postgres, "VACUUM t", "write", false, false},
		{driver.MySQL, "ALTER TABLE t ADD c int", "schema", false, false},
		{driver.MySQL, "TRUNCATE t", "schema", false, false},
		{driver.MySQL, "REPLACE INTO t VALUES (1)", "write", false, true},
		{driver.MySQL, "SELECT * FROM t INTO OUTFILE '/tmp/x'", "write", false, false},
		{driver.MySQL, "START TRANSACTION", "session", true, false},
		{driver.MySQL, "LOCK TABLES t WRITE", "write", false, false},
		{driver.Postgres, "SELECT dblink_exec('db', 'DELETE FROM t')", "write", false, false},
		{driver.Postgres, "SELECT setval('s', 1)", "write", false, false},
		{driver.Postgres, "SELECT pg_advisory_lock(1)", "write", false, false},
		{driver.Postgres, "SELECT pg_advisory_xact_lock(1)", "write", false, true},
		{driver.Postgres, "INSERT INTO t VALUES (nextval('s'))", "write", false, true},
		{driver.MySQL, "SELECT GET_LOCK('a', 1)", "write", false, false},
		{driver.SQLite, "SELECT writefile('/tmp/x', 'y')", "write", false, false},
		{driver.SQLite, "CREATE TABLE x (a)", "schema", false, true},
		{driver.SQLite, "PRAGMA foreign_keys = off", "write", false, false},
	}
	for _, tt := range tests {
		steps := Steps("  "+tt.sql+";", tt.d)
		if len(steps) != 1 {
			t.Fatalf("%s: %d steps", tt.sql, len(steps))
		}
		st := steps[0]
		if st.SQL != tt.sql || st.Start != 2 || st.End != 2+len(tt.sql) {
			t.Errorf("%s: span %d-%d %q", tt.sql, st.Start, st.End, st.SQL)
		}
		if st.Kind != tt.kind || st.Control != tt.control || st.Undoable != tt.undoable {
			t.Errorf("%s %s: kind %s control %v undoable %v (%s)", tt.d, tt.sql, st.Kind, st.Control, st.Undoable, st.Why)
		}
		if st.Undoable != (st.Why == "") {
			t.Errorf("%s: undoable %v with why %q", tt.sql, st.Undoable, st.Why)
		}
	}
}

func TestCount(t *testing.T) {
	for _, v := range []any{int64(7), 7, int32(7), uint64(7), 7.0, "7", []byte("7")} {
		if n, err := count(v); err != nil || n != 7 {
			t.Errorf("%T: %d %v", v, n, err)
		}
	}
	if _, err := count(nil); err == nil {
		t.Error("nil: want an error")
	}
}

func TestTrim(t *testing.T) {
	if got := trim("a\n  b\tc", 10); got != "a b c" {
		t.Errorf("got %q", got)
	}
	if got := trim("ééé", 3); got != "é…" {
		t.Errorf("got %q", got)
	}
}
