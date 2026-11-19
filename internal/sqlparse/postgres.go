//go:build cgo && !windows

package sqlparse

import (
	"errors"

	"github.com/pganalyze/pg_query_go/v6/parser"
)

// postgresChecked reports whether PostgreSQL syntax is checked in this build.
const postgresChecked = true

// checkPostgres parses stmt with libpg_query, PostgreSQL's own parser.
func checkPostgres(stmt string) *failure {
	_, err := parser.ParseToProtobuf(stmt)
	if err == nil {
		return nil
	}
	var pe *parser.Error
	if !errors.As(err, &pe) {
		return &failure{at: -1, msg: err.Error()}
	}
	return &failure{at: runeOffset(stmt, pe.Cursorpos-1), msg: pe.Message}
}

// runeOffset is the byte offset of the n-th rune of s: PostgreSQL reports
// error positions in characters.
func runeOffset(s string, n int) int {
	if n < 0 {
		return -1
	}
	for i := range s {
		if n == 0 {
			return i
		}
		n--
	}
	return len(s)
}
