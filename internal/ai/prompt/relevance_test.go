package prompt

import (
	"slices"
	"testing"

	"github.com/phankieuphu/lumen/internal/driver"
)

// ranked returns schema.name of every table in rank order, with its score
// and signals.
func ranked(t *testing.T, in Input) (names []string, score map[string]int, signals map[string]Signal) {
	t.Helper()
	b := newBuilder(in, in.Dialect)
	score, signals = map[string]int{}, map[string]Signal{}
	for _, i := range b.order {
		n := b.tables[i].Schema + "." + b.tables[i].Name
		names = append(names, n)
		score[n], signals[n] = b.scores[i], b.signals[i]
	}
	return names, score, signals
}

func TestRelevance(t *testing.T) {
	sch := shop(t)
	tests := []struct {
		name  string
		in    Input
		check func(t *testing.T, names []string, score map[string]int, sig map[string]Signal)
	}{
		{
			name: "cursor_from_beats_request_match",
			in: func() Input {
				in := editorInput(TextToSQL, sch, "SELECT * FROM categories c JOIN products p ON p.category_id = c.id|")
				in.Request = "orders"
				return in
			}(),
			check: func(t *testing.T, names []string, score map[string]int, sig map[string]Signal) {
				if names[0] != "public.categories" || names[1] != "public.products" {
					t.Fatalf("order = %q", names)
				}
				if score["public.categories"] <= score["public.orders"] || sig["public.orders"]&NameMatch == 0 {
					t.Fatalf("scores = %v", score)
				}
			},
		},
		{
			name: "qualified_name_matches_only_its_schema",
			in:   editorInput(TextToSQL, sch, "SELECT * FROM sales.orders|"),
			check: func(t *testing.T, names []string, _ map[string]int, sig map[string]Signal) {
				if sig["sales.orders"]&InCursorStatement == 0 || sig["public.orders"]&InCursorStatement != 0 {
					t.Fatalf("signals = %v", sig)
				}
			},
		},
		{
			name: "unqualified_name_matches_the_home_schema",
			in:   editorInput(TextToSQL, sch, "SELECT * FROM ORDERS|"),
			check: func(t *testing.T, _ []string, _ map[string]int, sig map[string]Signal) {
				if sig["public.orders"]&InCursorStatement == 0 || sig["sales.orders"]&InCursorStatement != 0 {
					t.Fatalf("signals = %v", sig)
				}
			},
		},
		{
			name: "unqualified_name_with_a_set_home_schema",
			in: func() Input {
				in := editorInput(TextToSQL, sch, "SELECT * FROM orders|")
				in.DefaultSchema = "sales"
				return in
			}(),
			check: func(t *testing.T, _ []string, _ map[string]int, sig map[string]Signal) {
				if sig["sales.orders"]&InCursorStatement == 0 || sig["public.orders"]&InCursorStatement != 0 {
					t.Fatalf("signals = %v", sig)
				}
			},
		},
		{
			name: "unqualified_name_not_in_the_home_schema",
			in:   editorInput(TextToSQL, sch, "SELECT * FROM invoices|"),
			check: func(t *testing.T, _ []string, _ map[string]int, sig map[string]Signal) {
				if sig["sales.invoices"]&InCursorStatement == 0 {
					t.Fatalf("signals = %v", sig)
				}
			},
		},
		{
			name: "unqualified_name_on_mysql_without_a_home",
			in: func() Input {
				in := editorInput(TextToSQL, sch, "SELECT * FROM orders|")
				in.Dialect = driver.MySQL
				return in
			}(),
			check: func(t *testing.T, _ []string, _ map[string]int, sig map[string]Signal) {
				if sig["sales.orders"]&InCursorStatement == 0 || sig["public.orders"]&InCursorStatement == 0 {
					t.Fatalf("signals = %v", sig)
				}
			},
		},
		{
			name: "other_statements_count_less",
			in:   editorInput(TextToSQL, sch, "SELECT * FROM products;\nSELECT * FROM customers|"),
			check: func(t *testing.T, _ []string, score map[string]int, sig map[string]Signal) {
				if sig["public.products"] != InOtherStatement || score["public.customers"] < cursorStatementPoints {
					t.Fatalf("signals = %v scores = %v", sig, score)
				}
			},
		},
		{
			name: "singular_plural_and_name_parts",
			in:   Input{Task: TextToSQL, Dialect: driver.Postgres, Schema: sch, Request: "each customer by category, and their invoice items"},
			check: func(t *testing.T, _ []string, _ map[string]int, sig map[string]Signal) {
				for _, n := range []string{"public.customers", "public.categories", "sales.invoices"} {
					if sig[n]&NameMatch == 0 {
						t.Errorf("%s not matched by name: %v", n, sig[n])
					}
				}
				if sig["public.order_items"]&NamePart == 0 || sig["public.order_items"]&NameMatch != 0 {
					t.Errorf("order_items = %v, want a name-part match", sig["public.order_items"])
				}
			},
		},
		{
			name: "column_matches_add_up_to_a_cap",
			in:   Input{Task: TextToSQL, Dialect: driver.Postgres, Schema: sch, Request: "sku name category_id email"},
			check: func(t *testing.T, _ []string, score map[string]int, sig map[string]Signal) {
				if sig["public.products"] != ColumnMatch || score["public.products"] != maxColumnPoints {
					t.Fatalf("products = %v %d", sig["public.products"], score["public.products"])
				}
				if score["public.customers"] != 2*columnPoints {
					t.Fatalf("customers = %d", score["public.customers"])
				}
			},
		},
		{
			name: "foreign_key_neighbours",
			in:   editorInput(TextToSQL, sch, "SELECT * FROM order_items|"),
			check: func(t *testing.T, _ []string, score map[string]int, sig map[string]Signal) {
				for _, n := range []string{"public.orders", "public.products"} {
					if sig[n] != Neighbour || score[n] != neighbourPoints {
						t.Errorf("%s = %v %d", n, sig[n], score[n])
					}
				}
				if sig["public.customers"] != 0 {
					t.Errorf("customers is two hops away: %v", sig["public.customers"])
				}
			},
		},
		{
			name: "cte_names_match_nothing",
			in:   editorInput(TextToSQL, sch, "WITH orders AS (SELECT * FROM customers), x(a) AS MATERIALIZED (SELECT 1) SELECT * FROM orders, x|"),
			check: func(t *testing.T, _ []string, _ map[string]int, sig map[string]Signal) {
				if sig["public.orders"]&InCursorStatement != 0 || sig["sales.orders"]&InCursorStatement != 0 {
					t.Fatalf("CTE matched a table: %v", sig)
				}
				if sig["public.customers"]&InCursorStatement == 0 {
					t.Fatalf("table inside the CTE not matched: %v", sig)
				}
			},
		},
		{
			name: "ties_by_schema_then_name",
			in:   Input{Task: TextToSQL, Dialect: driver.Postgres, Schema: sch},
			check: func(t *testing.T, names []string, _ map[string]int, _ map[string]Signal) {
				want := []string{
					"public.Order Notes", "public.audit_log", "public.big_orders", "public.categories", "public.customers",
					"public.order_items", "public.orders", "public.products", "sales.invoices", "sales.orders",
				}
				if !slices.Equal(names, want) {
					t.Fatalf("order = %q", names)
				}
			},
		},
		{
			name: "cursor_prefix",
			in:   editorInput(Inline, sch, "SELECT * FROM ord|"),
			check: func(t *testing.T, _ []string, _ map[string]int, sig map[string]Signal) {
				for _, n := range []string{"public.orders", "sales.orders", "public.order_items", "public.Order Notes"} {
					if sig[n]&CursorPrefix == 0 {
						t.Errorf("%s has no prefix match", n)
					}
				}
				if sig["public.products"]&CursorPrefix != 0 {
					t.Error("products matched prefix ord")
				}
			},
		},
		{
			name: "one_letter_prefix_matches_nothing",
			in:   editorInput(Inline, sch, "SELECT * FROM o|"),
			check: func(t *testing.T, _ []string, _ map[string]int, sig map[string]Signal) {
				if sig["public.orders"]&CursorPrefix != 0 {
					t.Error("one letter matched a prefix")
				}
			},
		},
		{
			name: "prefix_only_for_inline",
			in:   editorInput(TextToSQL, sch, "SELECT * FROM ord|"),
			check: func(t *testing.T, _ []string, _ map[string]int, sig map[string]Signal) {
				if sig["public.orders"]&CursorPrefix != 0 {
					t.Error("prefix matched outside inline")
				}
			},
		},
		{
			name: "error_names_tables_bare_quoted_and_dotted",
			in: Input{
				Task: FixError, Dialect: driver.Postgres, Schema: sch,
				Error: `column products.colour does not exist; see "Order Notes" and sales.invoices`,
			},
			check: func(t *testing.T, _ []string, _ map[string]int, sig map[string]Signal) {
				for _, n := range []string{"public.products", "public.Order Notes", "sales.invoices"} {
					if sig[n]&InError == 0 {
						t.Errorf("%s not matched by the error", n)
					}
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			names, score, sig := ranked(t, tt.in)
			tt.check(t, names, score, sig)
		})
	}
}

func TestCTENames(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want []string
	}{
		{"simple", "WITH a AS (SELECT 1) SELECT * FROM a", []string{"a"}},
		{"recursive_with_columns", `WITH RECURSIVE "T"(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM "T") SELECT * FROM "T"`, []string{"T"}},
		{"several", "WITH a AS (SELECT 1), b AS NOT MATERIALIZED (SELECT 2) SELECT 1", []string{"a", "b"}},
		{"time_zone_is_not_a_cte", "SELECT now()::timestamp WITH TIME ZONE", nil},
		{"ordinality_is_not_a_cte", "SELECT * FROM unnest(x) WITH ORDINALITY AS t(v, n)", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEditor(tt.sql, 0, "postgres")
			if got := cteNames(e.stmts[0].Tokens); !slices.Equal(got, tt.want) {
				t.Fatalf("cteNames = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWordForms(t *testing.T) {
	tests := []struct {
		word, name string
		want       bool
	}{
		{"order", "orders", true},
		{"orders", "order", true},
		{"category", "categories", true},
		{"categories", "category", true},
		{"box", "boxes", true},
		{"type", "types", true},
		{"address", "addresses", true},
		{"boss", "bos", false},
		{"order", "ordering", false},
	}
	for _, tt := range tests {
		t.Run(tt.word+"_"+tt.name, func(t *testing.T) {
			w := newWords(tt.word)
			if got := w.matchAny(tt.name); got != tt.want {
				t.Fatalf("match = %v, want %v", got, tt.want)
			}
		})
	}
	t.Run("short_words_ignored", func(t *testing.T) {
		if w := newWords("an id of ab"); !w.empty() {
			t.Fatal("words under three letters were kept")
		}
	})
	t.Run("word_cap", func(t *testing.T) {
		req := ""
		for i := range maxWords + 10 {
			req += "word" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + " "
		}
		if w := newWords(req); !w.matchAny("wordaa") || w.matchAny("wordoc") {
			t.Fatal("words after the first maxWords were kept")
		}
	})
}
