package query

import (
	"testing"

	"github.com/patrick-rontech/kiemdb/internal/driver"
)

func TestClassify(t *testing.T) {
	pg, my, lite := driver.Postgres, driver.MySQL, driver.SQLite
	tests := []struct {
		name    string
		dialect driver.Dialect
		sql     string
		kind    Kind
		rows    bool
	}{
		// Reads
		{"select", pg, "SELECT * FROM t", KindRead, true},
		{"lowercase_with_comment", pg, "-- hi\nselect 1", KindRead, true},
		{"values", pg, "VALUES (1), (2)", KindRead, true},
		{"show", my, "SHOW TABLES", KindRead, true},
		{"describe", my, "DESC t", KindRead, true},
		{"parenthesized_union", pg, "(SELECT 1) UNION (SELECT 2)", KindRead, true},
		{"cte_select", pg, "WITH x AS (SELECT 1) SELECT * FROM x", KindRead, true},
		{"explain", pg, "EXPLAIN SELECT * FROM t", KindRead, true},
		{"explain_delete_without_analyze", pg, "EXPLAIN DELETE FROM t", KindRead, true},
		{"mysql_explain_table", my, "EXPLAIN t", KindRead, true},
		{"write_keyword_in_string", pg, "SELECT 'DELETE FROM t'", KindRead, true},
		{"write_keyword_as_quoted_column", pg, `SELECT "delete" FROM t`, KindRead, true},
		{"replace_function", my, "SELECT REPLACE(name, 'a', 'b') FROM t", KindRead, true},
		{"pragma_query", lite, "PRAGMA user_version", KindRead, true},
		{"pragma_table_info", lite, "PRAGMA table_info(t)", KindRead, true},
		{"pragma_schema_qualified", lite, "PRAGMA main.table_info(t)", KindRead, true},

		// Writes
		{"insert", pg, "INSERT INTO t VALUES (1)", KindWrite, false},
		{"insert_returning", pg, "INSERT INTO t VALUES (1) RETURNING id", KindWrite, true},
		{"update", my, "UPDATE t SET a = 1", KindWrite, false},
		{"delete", lite, "DELETE FROM t", KindWrite, false},
		{"merge", pg, "MERGE INTO t USING s ON true WHEN MATCHED THEN DELETE", KindWrite, false},
		{"replace_into", my, "REPLACE INTO t VALUES (1)", KindWrite, false},
		{"cte_delete", pg, "WITH d AS (DELETE FROM t RETURNING *) SELECT * FROM d", KindWrite, true},
		{"cte_main_delete", pg, "WITH x AS (SELECT 1) DELETE FROM t", KindWrite, false},
		{"select_into_table", pg, "SELECT * INTO backup FROM t", KindWrite, false},
		{"select_into_outfile", my, "SELECT * FROM t INTO OUTFILE '/tmp/x'", KindWrite, false},
		{"select_for_update", pg, "SELECT * FROM t FOR UPDATE", KindWrite, true},
		{"select_for_share", pg, "SELECT * FROM t FOR NO KEY UPDATE", KindWrite, true},
		{"mysql_lock_in_share_mode", my, "SELECT * FROM t LOCK IN SHARE MODE", KindWrite, true},
		{"explain_analyze_delete", pg, "EXPLAIN ANALYZE DELETE FROM t", KindWrite, true},
		{"explain_options_analyze_update", pg, "EXPLAIN (ANALYZE, BUFFERS) UPDATE t SET a = 1", KindWrite, true},
		{"explain_analyze_select", pg, "EXPLAIN ANALYZE SELECT 1", KindRead, true},
		{"set_config", pg, "SELECT set_config('default_transaction_read_only', 'off', false)", KindWrite, true},
		{"terminate_backend", pg, "SELECT pg_terminate_backend(123)", KindWrite, true},
		{"nextval", pg, "SELECT nextval('s')", KindWrite, true},
		{"mysql_get_lock", my, "SELECT GET_LOCK('x', 10)", KindWrite, true},
		{"call", my, "CALL p()", KindWrite, false},
		{"do_block", pg, "DO $$ BEGIN DELETE FROM t; END $$", KindWrite, false},
		{"copy", pg, "COPY t FROM '/tmp/x'", KindWrite, false},
		{"vacuum", pg, "VACUUM t", KindWrite, false},
		{"pragma_assign", lite, "PRAGMA query_only = 0", KindWrite, true},
		{"pragma_call_unknown", lite, "PRAGMA journal_mode(WAL)", KindWrite, true},
		{"attach", lite, "ATTACH DATABASE '/tmp/x.db' AS x", KindWrite, false},
		{"unknown_keyword", pg, "FROBNICATE t", KindWrite, false},
		{"mysql_exec_comment", my, "SELECT 1 /*!50000 , (DELETE FROM t) */", KindWrite, true},

		// Schema
		{"create", pg, "CREATE TABLE t (id int)", KindSchema, false},
		{"drop", my, "DROP TABLE t", KindSchema, false},
		{"truncate", pg, "TRUNCATE t", KindSchema, false},
		{"grant", pg, "GRANT SELECT ON t TO u", KindSchema, false},

		// Session
		{"begin", pg, "BEGIN", KindSession, false},
		{"commit", my, "COMMIT", KindSession, false},
		{"set_search_path", pg, "SET search_path = app", KindSession, false},
		{"use", my, "USE shop", KindSession, false},

		// Session escalation → write
		{"set_read_only_off", pg, "SET default_transaction_read_only = off", KindWrite, false},
		{"set_quoted_read_only", pg, `SET "default_transaction_read_only" = off`, KindWrite, false},
		{"set_session_characteristics", pg, "SET SESSION CHARACTERISTICS AS TRANSACTION READ WRITE", KindWrite, false},
		{"begin_read_write", pg, "BEGIN READ WRITE", KindWrite, false},
		{"start_transaction_read_write", my, "START TRANSACTION READ WRITE", KindWrite, false},
		{"mysql_tx_read_only", my, "SET SESSION transaction_read_only = 0", KindWrite, false},
		{"mysql_sysvar_read_only", my, "SET @@transaction_read_only = 0", KindWrite, false},
		{"set_role", pg, "SET ROLE admin", KindWrite, false},
		{"set_session_authorization", pg, "SET SESSION AUTHORIZATION admin", KindWrite, false},
		{"set_global", my, "SET GLOBAL max_connections = 10", KindWrite, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Classify(tt.sql, tt.dialect)
			if c.Kind != tt.kind || c.ReturnsRows != tt.rows {
				t.Errorf("Classify(%q) = %s rows=%v, want %s rows=%v", tt.sql, c.Kind, c.ReturnsRows, tt.kind, tt.rows)
			}
		})
	}
}

func TestClassifyMultipleStatementsTakesWorstKind(t *testing.T) {
	c := Classify("SELECT 1; DELETE FROM t; SELECT 2", driver.Postgres)
	if c.Statements != 3 || c.Kind != KindWrite || c.Keyword != "SELECT" {
		t.Fatalf("got %+v", c)
	}
}

func TestClassifyEmpty(t *testing.T) {
	c := Classify("  -- nothing\n", driver.Postgres)
	if c.Statements != 0 {
		t.Fatalf("got %+v", c)
	}
}

func BenchmarkClassify(b *testing.B) {
	q := "WITH recent AS (SELECT id, total FROM orders WHERE created_at > now() - interval '1 day') " +
		"SELECT c.name, SUM(r.total) FROM recent r JOIN customers c ON c.id = r.id GROUP BY c.name ORDER BY 2 DESC LIMIT 50"
	for b.Loop() {
		Classify(q, driver.Postgres)
	}
}
