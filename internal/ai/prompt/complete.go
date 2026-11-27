package prompt

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/phankieuphu/lumen/internal/ai"
	"github.com/phankieuphu/lumen/internal/sqlparse"
	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// ErrInvalidSQL indicates that the model did not produce a valid SQL suggestion.
var ErrInvalidSQL = errors.New("ai: the model did not return a valid SQL suggestion")

// ErrUnsupportedSQL indicates that the current build cannot validate the dialect.
var ErrUnsupportedSQL = errors.New("ai: this build cannot validate suggestions for the connection's SQL dialect")

// Suggest asks for SQL and retries invalid output once without echoing it.
func Suggest(ctx context.Context, provider ai.Provider, model string, c Context, in Input, checker *sqlparse.Checker) (string, ai.Response, error) {
	if !sqlparse.Supports(c.Dialect) {
		return "", ai.Response{}, ErrUnsupportedSQL
	}
	req := ai.Request{Model: model, System: c.System, Messages: []ai.Message{{Role: ai.User, Content: c.User}}}
	if in.Task == Inline {
		req.MaxTokens = 128
	}
	var total ai.Response
	for attempt := 0; attempt < 2; attempt++ {
		res, err := provider.Complete(ctx, req)
		total.InputTokens += res.InputTokens
		total.OutputTokens += res.OutputTokens
		if err != nil && !errors.Is(err, ai.ErrTruncated) {
			return "", total, err
		}
		if err := ctx.Err(); err != nil {
			return "", total, fmt.Errorf("ai: suggestion canceled: %w", err)
		}
		if err == nil {
			if sql, ok := validated(ctx, c, in, checker, res.Text); ok {
				return sql, total, nil
			}
		}
		req.System = c.System + "\nThe previous answer was invalid. Follow the response format and return syntactically valid SQL."
	}
	return "", total, ErrInvalidSQL
}

func validated(ctx context.Context, c Context, in Input, checker *sqlparse.Checker, answer string) (string, bool) {
	d := sqlscan.Dialect(c.Dialect)
	var sql string
	if in.Task == Inline {
		if strings.TrimSpace(answer) == "" {
			return "", true
		}
		e := newEditor(in.Editor, in.Cursor, d)
		if e.skipped != 0 || strings.Contains(answer, "```") {
			return "", false
		}
		start, end := e.cur, e.cur
		if e.at >= 0 {
			start, end = e.stmts[e.at].Start, e.stmts[e.at].End
		}
		start, end = min(start, e.cur), max(end, e.cur)
		sql = e.src[start:e.cur] + answer + e.src[e.cur:end]
	} else {
		fence, body, ok := strings.Cut(strings.TrimSpace(answer), "\n")
		if !ok || !strings.EqualFold(strings.TrimSpace(fence), "```sql") {
			return "", false
		}
		sql, _, ok = strings.Cut(body, "\n```")
		if !ok {
			return "", false
		}
		sql = strings.TrimSpace(sql)
		answer = sql
	}
	if len(sql) > sqlparse.MaxBytes || len(sqlscan.Split(sql, d)) != 1 ||
		sqlscan.RedactSecrets(sql, d) != sql || len(checker.Check(ctx, sql, c.Dialect)) != 0 || ctx.Err() != nil {
		return "", false
	}
	return answer, true
}

// ValidSuggestion rechecks cached suggestions against the current editor.
func ValidSuggestion(ctx context.Context, c Context, in Input, checker *sqlparse.Checker, answer string) bool {
	if !sqlparse.Supports(c.Dialect) {
		return false
	}
	if in.Task != Inline {
		if strings.Contains(answer, "\n```") {
			return false
		}
		answer = "```sql\n" + answer + "\n```"
	}
	_, ok := validated(ctx, c, in, checker, answer)
	return ok
}
