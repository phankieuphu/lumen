package sqlparse

import (
	"strings"
	"testing"

	"github.com/phankieuphu/lumen/internal/driver"
)

func newChecker(t *testing.T) *Checker {
	t.Helper()
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// want is an expected issue: the marked text and a message fragment.
type want struct{ mark, msg string }

func TestCheck(t *testing.T) {
	tests := []struct {
		name    string
		dialect driver.Dialect
		src     string
		want    []want
	}{
		{"pg_typo", driver.Postgres, "SELECT * FORM users WHERE id = 1", []want{{"FORM", `near "FORM"`}}},
		{"pg_end_of_input", driver.Postgres, "SELECT 1 +", []want{{"+", "end of input"}}},
		// PostgreSQL counts characters; read as bytes the mark would land on FROM.
		{"pg_multibyte_before_error", driver.Postgres, "SELECT 'éééééééééé' FROM WHERE", []want{{"WHERE", "WHERE"}}},
		{"pg_valid", driver.Postgres, "SELECT 1; UPDATE t SET a = 2 WHERE id = $1", nil},
		{
			"pg_each_statement_checked", driver.Postgres,
			"SELEC 1;\nSELECT 2;\nDELETE t WHERE id = 1",
			[]want{{"SELEC", "SELEC"}, {"t", `near "t"`}},
		},
		{"mysql_typo", driver.MySQL, "SELECT * FORM users", []want{{"FORM", `near "FORM"`}}},
		{"mysql_end_of_input", driver.MySQL, "SELECT 1 +", []want{{"+", "end of input"}}},
		{"mysql_second_line", driver.MySQL, "SELECT a,\n  b FROM t WHERE", []want{{"WHERE", "end of input"}}},
		{"mysql_valid", driver.MySQL, "SELECT `a` FROM t LIMIT 3; UPDATE t SET a = 'x' WHERE id = 1", nil},
		{"sqlite_typo", driver.SQLite, "SELECT * FORM users", []want{{"FORM", `near "FORM"`}}},
		{"sqlite_end_of_input", driver.SQLite, "SELECT * FROM t WHERE", []want{{"WHERE", "end of input"}}},
		{"sqlite_unknown_table_is_not_syntax", driver.SQLite, "SELECT * FROM no_such_table", nil},
		{"unknown_dialect", driver.Dialect("oracle"), "SELEC", nil},
		{"empty", driver.Postgres, "  -- nothing\n", nil},
	}
	c := newChecker(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.dialect == driver.Postgres && !postgresChecked && tt.want != nil {
				t.Skip("PostgreSQL syntax is not checked without CGO or on Windows")
			}
			got := c.Check(t.Context(), tt.src, tt.dialect)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d issues %+v, want %d", len(got), got, len(tt.want))
			}
			for i, w := range tt.want {
				if mark := tt.src[got[i].Start:got[i].End]; mark != w.mark {
					t.Errorf("issue %d marks %q, want %q", i, mark, w.mark)
				}
				if !strings.Contains(got[i].Message, w.msg) {
					t.Errorf("issue %d message %q, want it to contain %q", i, got[i].Message, w.msg)
				}
			}
		})
	}
}

// TestNoFalseAlarms runs valid statements people really write: a wrong
// squiggle is worse than none.
func TestNoFalseAlarms(t *testing.T) {
	valid := map[driver.Dialect][]string{
		driver.Postgres: {
			"WITH RECURSIVE r AS (SELECT 1 n UNION ALL SELECT n + 1 FROM r WHERE n < 5) SELECT * FROM r",
			"INSERT INTO t (a) VALUES (1) ON CONFLICT (a) DO UPDATE SET a = EXCLUDED.a RETURNING *",
			"CREATE FUNCTION f() RETURNS int LANGUAGE sql AS $$ SELECT 1 $$",
			"SELECT data->>'name', count(*) OVER (PARTITION BY a ORDER BY b) FROM t",
			"EXPLAIN (ANALYZE, BUFFERS) SELECT 1",
			"SELECT pg_terminate_backend(42)",
			"ALTER TABLE t ADD COLUMN IF NOT EXISTS c jsonb DEFAULT '{}'::jsonb",
			"BEGIN", "COMMIT", "SET search_path TO public",
		},
		driver.MySQL: {
			"WITH RECURSIVE r AS (SELECT 1 n UNION ALL SELECT n + 1 FROM r WHERE n < 5) SELECT * FROM r",
			"INSERT INTO t (a) VALUES (1) ON DUPLICATE KEY UPDATE a = VALUES(a)",
			"REPLACE INTO t (a) VALUES (1)",
			"SELECT a, ROW_NUMBER() OVER (PARTITION BY b ORDER BY c) FROM t",
			"SELECT * FROM t WHERE id = 1 FOR UPDATE",
			"SELECT * FROM t LOCK IN SHARE MODE",
			"CREATE TABLE t (id BIGINT AUTO_INCREMENT PRIMARY KEY, v JSON) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
			"ALTER TABLE t ADD INDEX idx_a (a), ALGORITHM=INPLACE, LOCK=NONE",
			"SHOW FULL PROCESSLIST", "KILL 42", "SET GLOBAL max_connections = 200",
			"EXPLAIN ANALYZE SELECT 1", "SELECT * FROM t WHERE a = \"double quoted string\"",
			"CREATE PROCEDURE p() BEGIN SELECT 1; END",
			"CREATE DEFINER=`root`@`%` TRIGGER tr BEFORE INSERT ON t FOR EACH ROW SET NEW.a = 1",
		},
		driver.SQLite: {
			"CREATE TABLE IF NOT EXISTS t (id INTEGER PRIMARY KEY, v TEXT) STRICT",
			"INSERT INTO t (v) VALUES ('a') ON CONFLICT (id) DO NOTHING",
			"PRAGMA journal_mode = WAL",
			"CREATE TRIGGER tr AFTER INSERT ON t BEGIN UPDATE u SET n = n + 1; END",
			"SELECT json_extract(v, '$.a') FROM t", "VACUUM", "ATTACH DATABASE 'other.db' AS other",
		},
	}
	c := newChecker(t)
	for d, stmts := range valid {
		for _, s := range stmts {
			if got := c.Check(t.Context(), s, d); len(got) != 0 {
				t.Errorf("%s: %q flagged: %+v", d, s, got)
			}
		}
	}
}

func TestCheckLimits(t *testing.T) {
	c := newChecker(t)
	if got := c.Check(t.Context(), strings.Repeat("x", MaxBytes+1), driver.Postgres); got != nil {
		t.Errorf("SQL over MaxBytes was checked: %d issues", len(got))
	}
	// MySQL is checked in every build.
	if got := c.Check(t.Context(), strings.Repeat("SELEC 1;", MaxIssues+10), driver.MySQL); len(got) != MaxIssues {
		t.Errorf("got %d issues, want MaxIssues = %d", len(got), MaxIssues)
	}
}
