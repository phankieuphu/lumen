package sqlparse

import (
	"context"
	"regexp"
	"strings"

	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// sqliteNear matches SQLite's syntax errors, e.g. `near "FORM": syntax error`.
var sqliteNear = regexp.MustCompile(`near "(.*)": syntax error`)

// checkSQLite prepares stmt on the empty in-memory database: SQLite parses
// the whole statement before it resolves names, so syntax errors come first.
// Other errors, such as "no such table", are about the schema and ignored.
// The statement is never run.
func (c *Checker) checkSQLite(ctx context.Context, stmt string) *failure {
	ps, err := c.sqlite.PrepareContext(ctx, stmt)
	if err == nil {
		_ = ps.Close()
		return nil
	}
	msg := err.Error()
	if m := sqliteNear.FindStringSubmatch(msg); m != nil {
		return &failure{at: tokenOffset(stmt, m[1]), msg: near(m[1])}
	}
	if strings.Contains(msg, "incomplete input") {
		return &failure{at: len(stmt), msg: near("")}
	}
	return nil
}

// tokenOffset is the byte offset of the first token of stmt with text tok,
// or -1. SQLite names the token but not its position.
func tokenOffset(stmt, tok string) int {
	sc := sqlscan.NewScanner(stmt, sqlscan.SQLite)
	for {
		t, ok := sc.Next()
		if !ok {
			return -1
		}
		if t.Text == tok {
			return t.Pos
		}
	}
}
