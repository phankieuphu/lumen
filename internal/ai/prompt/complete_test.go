package prompt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/phankieuphu/lumen/internal/ai"
	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/sqlparse"
)

type answerProvider struct {
	ai.Provider
	answers  []string
	requests []ai.Request
}

func (p *answerProvider) Complete(ctx context.Context, req ai.Request) (ai.Response, error) {
	if err := ctx.Err(); err != nil {
		return ai.Response{}, err
	}
	p.requests = append(p.requests, req)
	return ai.Response{Text: p.answers[len(p.requests)-1], InputTokens: 3, OutputTokens: 4}, nil
}

func TestSuggest(t *testing.T) {
	checker, err := sqlparse.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = checker.Close() })
	for _, tt := range []struct {
		name    string
		task    Task
		editor  string
		answers []string
		want    string
	}{
		{"retry invalid syntax", TextToSQL, "", []string{"```sql\nSELECT FROM\n```", "```sql\nSELECT 1;\n```\nA constant."}, "SELECT 1;"},
		{"reject two statements", TextToSQL, "", []string{"```sql\nSELECT 1; SELECT 2;\n```", "SELECT 1;"}, ""},
		{"validate inline in context", Inline, "SELECT", []string{" 1"}, " 1"},
		{"preserve inline token spacing", Inline, "SELECT ", []string{"1"}, "1"},
		{"reject inline extra statement", Inline, "SELECT", []string{" 1; DROP TABLE customers;", " 2"}, " 2"},
		{"accept empty inline", Inline, "SELECT", []string{""}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := Input{Task: tt.task, Dialect: driver.SQLite, Editor: tt.editor, Cursor: len(tt.editor)}
			c, err := Build(in)
			if err != nil {
				t.Fatal(err)
			}
			p := &answerProvider{answers: tt.answers}
			got, usage, err := Suggest(t.Context(), p, "model", c, in, checker)
			if got != tt.want || len(p.requests) != len(tt.answers) {
				t.Fatalf("suggestion %q, calls %d, error %v", got, len(p.requests), err)
			}
			if tt.name == "reject two statements" {
				if !errors.Is(err, ErrInvalidSQL) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if usage.InputTokens != 3*len(tt.answers) || usage.OutputTokens != 4*len(tt.answers) {
				t.Fatalf("usage %+v", usage)
			}
			if len(p.requests) == 2 && (!strings.Contains(p.requests[1].System, "previous answer was invalid") ||
				p.requests[1].Messages[0].Content != c.User) {
				t.Fatal("unexpected retry prompt")
			}
		})
	}
}
