package sqlscan

import (
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name    string
		dialect Dialect
		src     string
		want    []string
	}{
		{"single", Postgres, "SELECT 1", []string{"SELECT 1"}},
		{"trailing_semicolon", Postgres, "SELECT 1;", []string{"SELECT 1"}},
		{"two", Postgres, "SELECT 1; DELETE FROM t", []string{"SELECT 1", "DELETE FROM t"}},
		{"empty_and_comment_only_dropped", Postgres, " ; -- hi\n ; /* x */", nil},
		{"semicolon_in_string", Postgres, "SELECT ';' ; SELECT 2", []string{"SELECT ';'", "SELECT 2"}},
		{"doubled_quote", Postgres, "SELECT 'it''s;'; SELECT 2", []string{"SELECT 'it''s;'", "SELECT 2"}},
		{"pg_backslash_is_literal", Postgres, `SELECT 'a\'; DELETE FROM t`, []string{`SELECT 'a\'`, "DELETE FROM t"}},
		{"pg_escape_string", Postgres, `SELECT E'a\';b'; SELECT 2`, []string{`SELECT E'a\';b'`, "SELECT 2"}},
		{"mysql_backslash_escapes", MySQL, `SELECT 'a\';b'; SELECT 2`, []string{`SELECT 'a\';b'`, "SELECT 2"}},
		{
			"dollar_quote", Postgres, "CREATE FUNCTION f() RETURNS int AS $$ SELECT 1; $$ LANGUAGE sql; SELECT 2",
			[]string{"CREATE FUNCTION f() RETURNS int AS $$ SELECT 1; $$ LANGUAGE sql", "SELECT 2"},
		},
		{"tagged_dollar_quote", Postgres, "SELECT $a$ ; $b$ ; $a$; SELECT 2", []string{"SELECT $a$ ; $b$ ; $a$", "SELECT 2"}},
		{"param_is_not_dollar_quote", Postgres, "SELECT $1; SELECT $2", []string{"SELECT $1", "SELECT $2"}},
		{"line_comment", Postgres, "SELECT 1 -- ; not a split\n; SELECT 2", []string{"SELECT 1 -- ; not a split", "SELECT 2"}},
		{"nested_block_comment_pg", Postgres, "SELECT /* a /* ; */ ; */ 1; SELECT 2", []string{"SELECT /* a /* ; */ ; */ 1", "SELECT 2"}},
		{"mysql_hash_comment", MySQL, "SELECT 1 # ;\n; SELECT 2", []string{"SELECT 1 # ;", "SELECT 2"}},
		{"quoted_ident", Postgres, `SELECT ";" FROM t; SELECT 2`, []string{`SELECT ";" FROM t`, "SELECT 2"}},
		{"backtick_ident", MySQL, "SELECT `;` FROM t; SELECT 2", []string{"SELECT `;` FROM t", "SELECT 2"}},
		{
			"sqlite_trigger_body", SQLite,
			"CREATE TRIGGER tr AFTER INSERT ON t BEGIN UPDATE u SET n = n + 1; DELETE FROM v; END; SELECT 1",
			[]string{"CREATE TRIGGER tr AFTER INSERT ON t BEGIN UPDATE u SET n = n + 1; DELETE FROM v; END", "SELECT 1"},
		},
		{
			"mysql_procedure_with_if", MySQL,
			"CREATE PROCEDURE p() BEGIN IF 1 THEN SELECT 1; END IF; SELECT CASE WHEN 1 THEN 2 END; END; SELECT 3",
			[]string{"CREATE PROCEDURE p() BEGIN IF 1 THEN SELECT 1; END IF; SELECT CASE WHEN 1 THEN 2 END; END", "SELECT 3"},
		},
		{"transaction_begin_splits", Postgres, "BEGIN; DELETE FROM t; COMMIT", []string{"BEGIN", "DELETE FROM t", "COMMIT"}},
		{"unterminated_string", Postgres, "SELECT 'abc; DELETE", []string{"SELECT 'abc; DELETE"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Split(tt.src, tt.dialect)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d statements %q, want %d", len(got), texts(got), len(tt.want))
			}
			for i := range got {
				if got[i].Text != tt.want[i] {
					t.Errorf("stmt %d = %q, want %q", i, got[i].Text, tt.want[i])
				}
			}
		})
	}
}

func TestMySQLExecutableCommentIsCode(t *testing.T) {
	got := Split("SELECT 1 /*!50000 ; DELETE FROM t */", MySQL)
	if len(got) != 2 || !got[1].Tokens[0].Is("DELETE") {
		t.Fatalf("executable comment body not scanned as code: %q", texts(got))
	}
	// Plain comments and optimizer hints stay comments.
	if n := Count("SELECT /*+ ; */ 1 /* ; */", MySQL); n != 1 {
		t.Fatalf("count = %d", n)
	}
}

func TestTokens(t *testing.T) {
	got := Split(`select "Weird"".Name", 'x', $1, 3.5e2 from t where a = ?`, Postgres)[0].Tokens
	kinds := []Kind{Word, QuotedIdent, Punct, String, Punct, Param, Punct, Number, Word, Word, Word, Word, Punct, Param}
	if len(got) != len(kinds) {
		t.Fatalf("got %d tokens: %+v", len(got), got)
	}
	for i, k := range kinds {
		if got[i].Kind != k {
			t.Errorf("token %d %q kind = %d, want %d", i, got[i].Text, got[i].Kind, k)
		}
	}
	if !got[0].Is("SELECT") {
		t.Error("Is should be case-insensitive")
	}
}

func texts(ss []Statement) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Text
	}
	return out
}

func BenchmarkSplit(b *testing.B) {
	src := strings.Repeat("SELECT a, 'str;ing', \"id\" FROM t WHERE x = $1 /* c */; ", 100)
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		Split(src, Postgres)
	}
}
