package sqlparse

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/pingcap/tidb/pkg/parser"
	// The parser needs a value-expression driver; this is the light one.
	_ "github.com/pingcap/tidb/pkg/parser/test_driver"
)

// mysqlError matches the TiDB parser's syntax errors, e.g.
// `line 1 column 13 near "FORM users"`.
var mysqlError = regexp.MustCompile(`line (\d+) column (\d+) near "(.*)"`)

// mysqlSkipped are leading keywords of statements the TiDB parser does not
// know although MySQL does (stored programs). They are not checked, rather
// than marked wrong.
var mysqlSkipped = map[string]bool{"DELIMITER": true}

// mysqlSkippedCreate are object kinds after CREATE that the parser lacks.
var mysqlSkippedCreate = map[string]bool{
	"PROCEDURE": true, "FUNCTION": true, "TRIGGER": true, "EVENT": true, "DEFINER": true,
}

// checkMySQL parses stmt with the TiDB parser, which accepts MySQL syntax.
func checkMySQL(stmt string) *failure {
	if skipMySQL(stmt) {
		return nil
	}
	// A parser is not safe for concurrent use and is cheap to make.
	_, _, err := parser.New().ParseSQL(stmt)
	if err == nil {
		return nil
	}
	m := mysqlError.FindStringSubmatch(err.Error())
	if m == nil {
		return &failure{at: -1, msg: "syntax error"}
	}
	rest := m[3]
	var at int
	if strings.HasSuffix(stmt, rest) {
		at = len(stmt) - len(rest)
	} else {
		line, _ := strconv.Atoi(m[1])
		col, _ := strconv.Atoi(m[2])
		at = lineColOffset(stmt, line, col)
	}
	return &failure{at: at, msg: near(firstToken(rest))}
}

func skipMySQL(stmt string) bool {
	words := strings.Fields(strings.ToUpper(stmt))
	if len(words) == 0 {
		return false
	}
	if mysqlSkipped[words[0]] {
		return true
	}
	if words[0] != "CREATE" {
		return false
	}
	for _, w := range words[1:] {
		if mysqlSkippedCreate[w] || strings.HasPrefix(w, "DEFINER=") {
			return true
		}
		if w != "OR" && w != "REPLACE" && w != "AGGREGATE" {
			return false
		}
	}
	return false
}

// firstToken is the first word of the text after an error, for the message.
func firstToken(rest string) string {
	f := strings.Fields(rest)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// lineColOffset is the byte offset of a 1-based line and 0-based column.
func lineColOffset(s string, line, col int) int {
	off := 0
	for l := 1; l < line; l++ {
		i := strings.IndexByte(s[off:], '\n')
		if i < 0 {
			return -1
		}
		off += i + 1
	}
	return min(off+col, len(s))
}
