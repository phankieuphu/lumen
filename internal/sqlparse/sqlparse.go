// Package sqlparse checks SQL syntax offline with each dialect's own parser,
// so the editor can mark errors while typing without touching a database.
// It reports syntax only: unknown tables and columns need the real schema.
package sqlparse

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // registers "sqlite"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// Limits that keep a check fast on any input.
const (
	// MaxBytes is the longest SQL checked; longer text is not checked.
	MaxBytes = 256 << 10
	// MaxIssues caps the issues returned for one check.
	MaxIssues = 50
)

// Issue is a syntax error at byte offsets [Start, End) of the checked SQL.
type Issue struct {
	Start, End int
	Message    string
}

// Checker checks SQL syntax. It is safe for concurrent use. Close releases
// the in-memory SQLite engine it parses SQLite statements with.
type Checker struct {
	sqlite *sql.DB
}

// New returns a Checker.
func New() (*Checker, error) {
	// Statements are only prepared, never run, so the database stays empty.
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		return nil, fmt.Errorf("sqlparse: open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	return &Checker{sqlite: db}, nil
}

// Close releases the Checker's resources.
func (c *Checker) Close() error {
	if err := c.sqlite.Close(); err != nil {
		return fmt.Errorf("sqlparse: close sqlite: %w", err)
	}
	return nil
}

// failure is a parser's verdict on one statement: the byte offset of the
// error in the statement (len for end of input, -1 if unknown) and a message.
type failure struct {
	at  int
	msg string
}

// Check returns the syntax errors in src, at most one per statement and at
// most MaxIssues, in order. Each statement is parsed on its own, so an error
// in one does not hide errors in the next. Unknown dialects, and SQL longer
// than MaxBytes, are not checked.
func (c *Checker) Check(ctx context.Context, src string, d driver.Dialect) []Issue {
	if len(src) > MaxBytes {
		return nil
	}
	var check func(stmt string) *failure
	switch d {
	case driver.Postgres:
		check = checkPostgres
	case driver.MySQL:
		check = checkMySQL
	case driver.SQLite:
		check = func(stmt string) *failure { return c.checkSQLite(ctx, stmt) }
	default:
		return nil
	}
	var out []Issue
	for _, st := range sqlscan.Split(src, sqlscan.Dialect(d)) {
		if ctx.Err() != nil {
			return out
		}
		if f := check(st.Text); f != nil {
			out = append(out, locate(st, f))
			if len(out) == MaxIssues {
				break
			}
		}
	}
	return out
}

// locate widens a failure to the token it points at, in source offsets. An
// error at the end of input marks the last token; an unknown position marks
// the whole statement.
func locate(st sqlscan.Statement, f *failure) Issue {
	is := Issue{Start: st.Start, End: st.End, Message: f.msg}
	if f.at < 0 || len(st.Tokens) == 0 {
		return is
	}
	at := st.Start + f.at
	t := st.Tokens[len(st.Tokens)-1]
	for _, tok := range st.Tokens {
		if at < tok.Pos+len(tok.Text) {
			t = tok
			break
		}
	}
	is.Start, is.End = t.Pos, t.Pos+len(t.Text)
	return is
}

// near formats the usual message for an error at a token.
func near(tok string) string {
	if tok == "" {
		return "syntax error at end of input"
	}
	return fmt.Sprintf("syntax error at or near %q", tok)
}
