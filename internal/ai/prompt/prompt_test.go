package prompt

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/schema"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// shop loads testdata/shop.json: eight public tables (one view, one with a
// quoted name) and two in a second schema, sales.
func shop(t testing.TB) schema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "shop.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s schema.Schema
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// at returns editor text with the cursor marker "|" removed, and its offset.
func at(s string) (string, int) {
	i := strings.Index(s, "|")
	if i < 0 {
		return s, len(s)
	}
	return s[:i] + s[i+1:], i
}

func editorInput(task Task, sch schema.Schema, editor string) Input {
	text, cur := at(editor)
	return Input{Task: task, Dialect: driver.Postgres, Schema: sch, Editor: text, Cursor: cur}
}

func mustBuild(t testing.TB, in Input) Context {
	t.Helper()
	c, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// dump renders a Context for golden files and leak checks.
func dump(c Context) string {
	var b strings.Builder
	fmt.Fprintf(&b, "task=%s dialect=%s version=%d tokens=%d budget=%d\nkey=%s\n", c.Task, c.Dialect, c.SchemaVersion, c.Tokens, c.Budget, c.Key)
	b.WriteString("== tables\n")
	for _, r := range c.Tables {
		fmt.Fprintf(&b, "%s.%s %s score=%d signals=%s\n", r.Schema, r.Name, r.Form, r.Score, signalNames(r.Signals))
	}
	b.WriteString("== omitted\n")
	for _, o := range c.Omitted {
		fmt.Fprintf(&b, "%s %s count=%d names=%q\n", o.Part, o.Reason, o.Count, o.Names)
	}
	for _, s := range c.Sections {
		fmt.Fprintf(&b, "== %s (%s) tokens=%d\n%s\n", s.Part, s.Heading, s.Tokens, s.Text)
	}
	return b.String()
}

func signalNames(s Signal) string {
	names := []string{"cursorStatement", "otherStatement", "cursorPrefix", "nameMatch", "namePart", "columnMatch", "inError", "neighbour"}
	var out []string
	for i, n := range names {
		if s&(1<<i) != 0 {
			out = append(out, n)
		}
	}
	return strings.Join(out, ",")
}

// goldenCases are the inputs pinned by testdata/*.golden.
func goldenCases(t testing.TB) map[string]Input {
	sch := shop(t)
	inline := editorInput(Inline, sch, "SELECT o.id, c.email\nFROM orders o\nJOIN customers c ON c.id = o.customer_id\nWHERE o.st| AND c.email = 'a@b.com'\nORDER BY o.created_at DESC")
	multi := editorInput(Inline, sch, "SELECT * FROM products WHERE sku = 'X-1';\n\nSELECT * FROM ord|\n;\nDELETE FROM sales.invoices WHERE id = 3;")
	text := editorInput(TextToSQL, sch, "")
	text.Request = "Show the 10 customers with the highest order totals this year, with their email"
	fix := editorInput(FixError, sch, "SELECT id, email FROM customerz| WHERE email = 'a@b.com'")
	fix.Error = `ERROR: relation "customerz" does not exist (SQLSTATE 42P01)`
	empty := Input{Task: TextToSQL, Dialect: driver.Postgres, Schema: schema.Schema{Dialect: driver.Postgres}, Request: "list every table"}
	noEditor := editorInput(Inline, sch, "")
	return map[string]Input{
		"inline_mid_statement":   inline,
		"inline_multi_statement": multi,
		"text_to_sql":            text,
		"fix_error":              fix,
		"empty_schema":           empty,
		"empty_editor":           noEditor,
	}
}

func TestBuild(t *testing.T) {
	for name, in := range goldenCases(t) {
		t.Run(name, func(t *testing.T) {
			got := dump(mustBuild(t, in))
			path := filepath.Join("testdata", name+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if got != string(want) {
				t.Fatalf("output differs from %s (run with -update after checking):\n%s", path, got)
			}
		})
	}
}

func TestSectionsMatchSent(t *testing.T) {
	for name, in := range goldenCases(t) {
		t.Run(name, func(t *testing.T) {
			c := mustBuild(t, in)
			if c.Sections[0].Part != PartInstructions || c.System != c.Sections[0].Text {
				t.Fatalf("System is not Sections[0]")
			}
			texts := make([]string, 0, len(c.Sections)-1)
			for _, s := range c.Sections[1:] {
				texts = append(texts, s.Text)
				if s.Tokens != EstimateTokens(s.Text) {
					t.Fatalf("%s: Tokens = %d, want %d", s.Part, s.Tokens, EstimateTokens(s.Text))
				}
			}
			if c.User != strings.Join(texts, "\n\n") {
				t.Fatalf("User is not the joined sections:\n%s", c.User)
			}
			if c.Tokens != EstimateTokens(c.System)+EstimateTokens(c.User) || c.Tokens > c.Budget {
				t.Fatalf("Tokens = %d, Budget = %d", c.Tokens, c.Budget)
			}
		})
	}
}

func TestDeterministic(t *testing.T) {
	for name, in := range goldenCases(t) {
		t.Run(name, func(t *testing.T) {
			want := mustBuild(t, in)
			rng := rand.New(rand.NewPCG(1, 2))
			for range 50 {
				shuffled := in
				shuffled.Schema.Tables = slices.Clone(in.Schema.Tables)
				shuffled.Schema.ForeignKeys = slices.Clone(in.Schema.ForeignKeys)
				rng.Shuffle(len(shuffled.Schema.Tables), reflect.Swapper(shuffled.Schema.Tables))
				rng.Shuffle(len(shuffled.Schema.ForeignKeys), reflect.Swapper(shuffled.Schema.ForeignKeys))
				got := mustBuild(t, shuffled)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("shuffled schema changed the output:\n%s\nwant:\n%s", dump(got), dump(want))
				}
			}
		})
	}
	t.Run("key_ignores_schema_version", func(t *testing.T) {
		in := goldenCases(t)["text_to_sql"]
		a := mustBuild(t, in)
		in.Schema.Version++
		if b := mustBuild(t, in); a.Key != b.Key {
			t.Fatal("Key changed with the schema version")
		}
	})
	t.Run("key_follows_text", func(t *testing.T) {
		in := goldenCases(t)["text_to_sql"]
		a := mustBuild(t, in)
		in.Request += "!"
		if b := mustBuild(t, in); a.Key == b.Key {
			t.Fatal("Key did not change with the request")
		}
	})
}

func TestInputHasNoDataFields(t *testing.T) {
	// Adding a field here is a privacy decision: anything in Input can be
	// sent to a model. Row data, samples, DSNs, hosts, users, and keys must
	// never be among them.
	tests := []struct {
		typ  reflect.Type
		want []string
	}{
		{reflect.TypeFor[Input](), []string{"Task", "Dialect", "Schema", "Request", "Error", "Editor", "Cursor", "Budget", "Privacy"}},
		{reflect.TypeFor[Privacy](), []string{"KeepLiterals"}},
		{reflect.TypeFor[schema.Schema](), []string{"Dialect", "Tables", "ForeignKeys", "Truncated", "Version"}},
		{reflect.TypeFor[schema.Table](), []string{"Schema", "Name", "Kind", "Columns"}},
		{reflect.TypeFor[schema.Column](), []string{"Name", "Type", "Nullable", "PrimaryKey"}},
	}
	for _, tt := range tests {
		t.Run(tt.typ.Name(), func(t *testing.T) {
			var got []string
			for f := range tt.typ.Fields() {
				got = append(got, f.Name)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("fields = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name string
		in   Input
		want error
	}{
		{"unknown_task", Input{Task: "advise"}, ErrTask},
		{"empty_task", Input{}, ErrTask},
		{"budget_below_instructions", Input{Task: Inline, Dialect: driver.Postgres, Budget: 10}, ErrBudget},
		{"no_dialect", Input{Task: Inline}, ErrDialect},
		{"unknown_dialect", Input{Task: Inline, Dialect: "oracle"}, ErrDialect},
		{"unknown_schema_dialect", Input{Task: Inline, Schema: schema.Schema{Dialect: "mssql"}}, ErrDialect},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Build(tt.in); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestMaxBudget(t *testing.T) {
	for _, budget := range []int{MaxBudget, MaxBudget + 1, 1 << 62, int(^uint(0) >> 1)} {
		in := goldenCases(t)["text_to_sql"]
		in.Budget = budget
		c := mustBuild(t, in)
		if c.Budget != MaxBudget || len(c.Tables) != len(in.Schema.Tables) || !strings.Contains(c.User, "<request>") {
			t.Fatalf("budget %d: Budget=%d tables=%d", budget, c.Budget, len(c.Tables))
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want int
	}{
		{"empty", "", 0},
		{"one_byte", "a", 1},
		{"three_bytes", "abc", 1},
		{"four_bytes", "abcd", 2},
		{"ascii_sql", "SELECT * FROM t", 5},
		{"vietnamese", "khách hàng", 4}, // 12 bytes for 10 characters
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EstimateTokens(tt.s); got != tt.want {
				t.Fatalf("EstimateTokens(%q) = %d, want %d", tt.s, got, tt.want)
			}
		})
	}
}

func TestDefaultBudget(t *testing.T) {
	for task, want := range map[Task]int{Inline: 2000, TextToSQL: 8000, FixError: 8000, "x": 0} {
		if got := DefaultBudget(task); got != want {
			t.Errorf("DefaultBudget(%q) = %d, want %d", task, got, want)
		}
	}
}
