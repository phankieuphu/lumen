package prompt

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/schema"
)

func instructionTokens(t *testing.T, task Task) int {
	t.Helper()
	s, ok := instructions(task, driver.Postgres)
	if !ok {
		t.Fatal("no instructions")
	}
	return EstimateTokens(s)
}

func findOmission(c Context, p Part, r Reason) (Omission, bool) {
	for _, o := range c.Omitted {
		if o.Part == p && o.Reason == r {
			return o, true
		}
	}
	return Omission{}, false
}

func findTable(c Context, name string) (TableRef, bool) {
	for _, r := range c.Tables {
		if r.Name == name {
			return r, true
		}
	}
	return TableRef{}, false
}

func checkInvariants(t *testing.T, c Context) {
	t.Helper()
	if c.Tokens > c.Budget {
		t.Fatalf("Tokens %d > Budget %d", c.Tokens, c.Budget)
	}
	if !utf8.ValidString(c.User) {
		t.Fatal("User is not valid UTF-8")
	}
	seenZero := false
	for _, r := range c.Tables {
		if r.Score == 0 {
			seenZero = true
		} else if seenZero {
			t.Fatalf("scored table %s sent after a zero-score one", r.Name)
		}
	}
}

// wide is one table with many columns and a foreign key, plus its parent.
func wide() schema.Schema {
	cols := make([]schema.Column, 0, 102)
	cols = append(cols, schema.Column{Name: "id", Type: "bigint", PrimaryKey: true}, schema.Column{Name: "parent_id", Type: "bigint", Nullable: true})
	for i := range 100 {
		cols = append(cols, schema.Column{Name: fmt.Sprintf("c%03d", i), Type: "text", Nullable: true})
	}
	return schema.Schema{
		Dialect: driver.Postgres,
		Tables: []schema.Table{
			{Schema: "public", Name: "parent", Kind: "table", Columns: []schema.Column{{Name: "id", Type: "bigint", PrimaryKey: true}}},
			{Schema: "public", Name: "wide", Kind: "table", Columns: cols},
		},
		ForeignKeys: []schema.ForeignKey{{
			Name: "wide_parent_fk", FromSchema: "public", FromTable: "wide", FromColumns: []string{"parent_id"},
			ToSchema: "public", ToTable: "parent", ToColumns: []string{"id"},
		}},
	}
}

func TestBudget(t *testing.T) {
	t.Run("instructions_plus_one", func(t *testing.T) {
		in := goldenCases(t)["text_to_sql"]
		in.Budget = instructionTokens(t, TextToSQL) + 1
		c := mustBuild(t, in)
		checkInvariants(t, c)
		if c.User != "" || len(c.Sections) != 1 || len(c.Tables) != 0 {
			t.Fatalf("sent more than the instructions:\n%s", dump(c))
		}
		if _, ok := findOmission(c, PartRequest, ReasonBudget); !ok {
			t.Fatal("request not reported as omitted")
		}
		if o, ok := findOmission(c, PartSchema, ReasonBudget); !ok || o.Count == 0 || len(o.Names) == 0 {
			t.Fatalf("relevant tables not reported: %+v", c.Omitted)
		}
	})

	t.Run("full_then_compact_then_omitted", func(t *testing.T) {
		in := editorInput(TextToSQL, wide(), "SELECT * FROM wide|")
		base := instructionTokens(t, TextToSQL)
		var forms []Form
		for budget := base + 1; budget < base+2000; budget += 5 {
			in.Budget = budget
			c := mustBuild(t, in)
			checkInvariants(t, c)
			r, ok := findTable(c, "wide")
			f := Form("omitted")
			if ok {
				f = r.Form
				if f == FormCompact && !strings.Contains(c.User, "wide(id bigint PK, parent_id bigint, +100 more)\n  FK (parent_id) -> parent(id)") {
					t.Fatalf("compact form:\n%s", c.User)
				}
			} else if _, ok := findOmission(c, PartSchema, ReasonBudget); !ok {
				t.Fatalf("wide neither sent nor omitted at budget %d", budget)
			}
			if len(forms) == 0 || forms[len(forms)-1] != f {
				forms = append(forms, f)
			}
		}
		if want := []Form{"omitted", FormCompact, FormFull}; fmt.Sprint(forms) != fmt.Sprint(want) {
			t.Fatalf("forms as the budget grows = %v, want %v", forms, want)
		}
	})

	t.Run("zero_score_tables_fill_what_is_left", func(t *testing.T) {
		in := editorInput(TextToSQL, shop(t), "SELECT * FROM audit_log|")
		base := instructionTokens(t, TextToSQL)
		sawLow := false
		for budget := base + 1; budget < base+800; budget += 3 {
			in.Budget = budget
			c := mustBuild(t, in)
			checkInvariants(t, c)
			if o, ok := findOmission(c, PartSchema, ReasonLowRelevance); ok {
				if _, sent := findTable(c, "audit_log"); sent && o.Count > 0 {
					sawLow = true
				}
			}
		}
		if !sawLow {
			t.Fatal("never sent the relevant table while leaving out low-relevance ones")
		}
		in.Budget = 0
		if c := mustBuild(t, in); len(c.Tables) != 10 || len(c.Omitted) != 0 {
			t.Fatalf("small schema not sent whole: %d tables, %+v", len(c.Tables), c.Omitted)
		}
	})

	t.Run("schema_truncated_reported", func(t *testing.T) {
		in := goldenCases(t)["text_to_sql"]
		in.Schema.Truncated = true
		if _, ok := findOmission(mustBuild(t, in), PartSchema, ReasonSchemaTruncated); !ok {
			t.Fatal("truncated schema not reported")
		}
	})

	t.Run("long_vietnamese_request_cut_at_rune_boundary", func(t *testing.T) {
		in := Input{
			Task: TextToSQL, Dialect: driver.Postgres, Schema: shop(t), Budget: 2000,
			Request: strings.Repeat("Tìm khách hàng đã đặt nhiều đơn hàng nhất trong năm nay. ", 200),
		}
		c := mustBuild(t, in)
		checkInvariants(t, c)
		req := c.Sections[len(c.Sections)-1]
		if req.Part != PartRequest || !strings.HasSuffix(req.Text, ellipsis+"\n</request>") {
			t.Fatalf("request section:\n%s", req.Text)
		}
		if limit := (2000 - instructionTokens(t, TextToSQL)) * requestShare / 100; req.Tokens+1 > limit {
			t.Fatalf("request took %d tokens, share is %d", req.Tokens, limit)
		}
		if o, ok := findOmission(c, PartRequest, ReasonBudget); !ok || o.Count <= 0 {
			t.Fatalf("cut not reported: %+v", c.Omitted)
		}
	})

	t.Run("unused_shares_go_to_the_schema", func(t *testing.T) {
		in := Input{Task: TextToSQL, Dialect: driver.Postgres, Schema: bigSchema(200, 20)}
		c := mustBuild(t, in)
		checkInvariants(t, c)
		if c.Tokens < c.Budget*9/10 {
			t.Fatalf("schema used only %d of %d tokens", c.Tokens, c.Budget)
		}
	})

	t.Run("tokens_never_exceed_budget", func(t *testing.T) {
		for name, in := range goldenCases(t) {
			base := instructionTokens(t, in.Task)
			for budget := base; budget < base+1200; budget += 7 {
				in.Budget = budget
				c, err := Build(in)
				if err != nil {
					t.Fatalf("%s at %d: %v", name, budget, err)
				}
				checkInvariants(t, c)
			}
		}
	})

	t.Run("inline_text_trimmed_on_line_boundaries", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString("SELECT\n")
		for i := range 300 {
			fmt.Fprintf(&sb, "  col_%03d,\n", i)
		}
		sb.WriteString("  x|\nFROM t\n")
		for i := range 300 {
			fmt.Fprintf(&sb, "  AND col_%03d > 0\n", i)
		}
		c := mustBuild(t, editorInput(Inline, shop(t), sb.String()))
		checkInvariants(t, c)
		var before, after Section
		for _, s := range c.Sections {
			switch s.Part {
			case PartSQLBeforeCursor:
				before = s
			case PartSQLAfterCursor:
				after = s
			}
		}
		if !strings.HasPrefix(before.Text, "<sql_before_cursor>  col_") || !strings.HasSuffix(before.Text, "  x</sql_before_cursor>") {
			t.Fatalf("before:\n%s", before.Text)
		}
		if !strings.HasPrefix(after.Text, "<sql_after_cursor>\nFROM t\n") || !strings.HasSuffix(after.Text, " > 0</sql_after_cursor>") {
			t.Fatalf("after:\n%s", after.Text)
		}
		if before.Tokens <= after.Tokens {
			t.Fatalf("before %d tokens, after %d: before should get the larger share", before.Tokens, after.Tokens)
		}
		for _, p := range []Part{PartSQLBeforeCursor, PartSQLAfterCursor} {
			if _, ok := findOmission(c, p, ReasonBudget); !ok {
				t.Errorf("%s cut not reported", p)
			}
		}
	})
}

func TestEditorWindow(t *testing.T) {
	filler := strings.Repeat("-- filler line\n", 20_000) // 300 KB

	t.Run("large_editor_windowed_around_the_cursor", func(t *testing.T) {
		in := editorInput(TextToSQL, shop(t), "SELECT * FROM products;\n"+filler+"SELECT * FROM orders WHERE id = 1|;\n"+filler)
		c := mustBuild(t, in)
		checkInvariants(t, c)
		o, ok := findOmission(c, PartSQL, ReasonEditorTooLarge)
		// The window starts at the first token in it, so the comment lines
		// before that are left out too.
		if !ok || o.Count < len(in.Editor)-maxWindow {
			t.Fatalf("omitted = %+v, want at least %d bytes", c.Omitted, len(in.Editor)-maxWindow)
		}
		if !strings.Contains(c.User, "<sql>\nSELECT * FROM orders WHERE id = 1\n</sql>") {
			t.Fatalf("statement not sent:\n%s", c.User)
		}
		if r, _ := findTable(c, "products"); r.Signals&InOtherStatement != 0 {
			t.Fatal("statement outside the window was read")
		}
	})

	t.Run("window_never_starts_inside_a_literal", func(t *testing.T) {
		secret := strings.Repeat("secret-data ", 30_000) // 360 KB
		in := editorInput(TextToSQL, shop(t), "SELECT '"+secret+"' AS x;\nSELECT * FROM customers WHERE id = 1|")
		in.Privacy.KeepLiterals = true
		c := mustBuild(t, in)
		if strings.Contains(dump(c), "secret-data") {
			t.Fatal("literal text read as SQL and sent")
		}
		if !strings.Contains(c.User, "SELECT * FROM customers WHERE id = 1") {
			t.Fatalf("statement at the cursor not sent:\n%s", c.User)
		}
	})

	t.Run("cursor_inside_a_huge_literal_sends_no_sql", func(t *testing.T) {
		secret := strings.Repeat("secret-data ", 30_000)
		in := editorInput(Inline, shop(t), "SELECT '"+secret+"|"+secret+"'")
		c := mustBuild(t, in)
		if strings.Contains(dump(c), "secret-data") {
			t.Fatal("literal sent")
		}
		if o, ok := findOmission(c, PartSQL, ReasonEditorTooLarge); !ok || o.Count != len(in.Editor) {
			t.Fatalf("omitted = %+v", c.Omitted)
		}
	})

	t.Run("statement_cut_by_the_window_is_always_masked", func(t *testing.T) {
		in := editorInput(TextToSQL, shop(t), "SELECT "+strings.Repeat("a, ", 100_000)+"'kept-literal' FROM t|")
		in.Privacy.KeepLiterals = true
		in.Budget = 1 << 20
		c := mustBuild(t, in)
		if strings.Contains(c.User, "kept-literal") || !strings.Contains(c.User, "'?' FROM t") {
			t.Fatal("literal sent from a statement whose start was cut off")
		}
	})
}

// bigSchema returns n tables of cols columns, each with a key to the one
// before it.
func bigSchema(n, cols int) schema.Schema {
	nouns := []string{"orders", "customers", "products", "invoices", "payments", "shipments", "accounts", "events"}
	s := schema.Schema{Dialect: driver.Postgres, Tables: make([]schema.Table, 0, n), ForeignKeys: make([]schema.ForeignKey, 0, n)}
	for i := range n {
		name := fmt.Sprintf("%s_%04d", nouns[i%len(nouns)], i)
		cs := make([]schema.Column, 0, cols)
		cs = append(cs, schema.Column{Name: "id", Type: "bigint", PrimaryKey: true}, schema.Column{Name: "parent_id", Type: "bigint", Nullable: true})
		for j := 2; j < cols; j++ {
			cs = append(cs, schema.Column{Name: fmt.Sprintf("field_%02d", j), Type: "character varying(255)", Nullable: j%3 != 0})
		}
		s.Tables = append(s.Tables, schema.Table{Schema: "public", Name: name, Kind: "table", Columns: cs})
		if i > 0 {
			s.ForeignKeys = append(s.ForeignKeys, schema.ForeignKey{
				Name: name + "_parent_fk", FromSchema: "public", FromTable: name, FromColumns: []string{"parent_id"},
				ToSchema: "public", ToTable: s.Tables[i-1].Name, ToColumns: []string{"id"},
			})
		}
	}
	return s
}

func BenchmarkBuild(b *testing.B) {
	sch := bigSchema(1000, 50)
	editor := "SELECT o.id, c.field_02\nFROM orders_0500 o\nJOIN customers_0501 c ON c.parent_id = o.id\nWHERE o.field_0"
	cases := []struct {
		name string
		in   Input
	}{
		{"inline", Input{Task: Inline, Dialect: driver.Postgres, Schema: sch, Editor: editor, Cursor: len(editor)}},
		{"textToSql", Input{
			Task: TextToSQL, Dialect: driver.Postgres, Schema: sch, Editor: editor, Cursor: len(editor),
			Request: "total payments per customer for shipments this month, with the account id",
		}},
	}
	for _, bc := range cases {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Build(bc.in); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
