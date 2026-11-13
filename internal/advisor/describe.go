package advisor

import (
	"strings"

	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// maxTables caps the table names kept per statement.
const maxTables = 20

// fromEnd lists keywords that end a FROM list.
var fromEnd = map[string]bool{
	"WHERE": true, "GROUP": true, "ORDER": true, "HAVING": true, "LIMIT": true, "OFFSET": true,
	"FETCH": true, "FOR": true, "WINDOW": true, "UNION": true, "INTERSECT": true, "EXCEPT": true,
	"ON": true, "USING": true, "SET": true, "VALUES": true, "SELECT": true, "RETURNING": true,
	"INNER": true, "LEFT": true, "RIGHT": true, "FULL": true, "CROSS": true, "NATURAL": true,
	"JOIN": true, "STRAIGHT_JOIN": true,
}

// tableSkip lists words that may sit between a keyword and its table name.
var tableSkip = map[string]bool{"ONLY": true, "LATERAL": true, "IGNORE": true, "LOW_PRIORITY": true}

// updateClause lists words that put UPDATE inside a clause, where no table
// follows: FOR UPDATE, FOR NO KEY UPDATE, ON DUPLICATE KEY UPDATE.
var updateClause = map[string]bool{"FOR": true, "KEY": true}

// Describe returns a statement's leading keyword, upper-cased, and the
// tables it names, in order of first use. It reads tokens without parsing,
// so it is a display aid and never a security decision: common table
// expressions show up as tables, and unusual syntax may hide one.
func Describe(sql string, d sqlscan.Dialect) (verb string, tables []string) {
	tables = []string{}
	seen := map[string]bool{}
	sc := sqlscan.NewScanner(sql, d)

	// One entry per open paren. subquery says whether it opened with SELECT:
	// FROM inside a plain call, as in EXTRACT(year FROM ts), names no table.
	// outerFrom is the FROM-list state to return to at the closing paren.
	type paren struct{ subquery, outerFrom bool }
	var parens []paren
	var (
		expect   bool // the next name is a table
		inFrom   bool // inside a FROM list, where a comma brings another table
		opened   bool // the previous token was "("
		name     []string
		wantPart bool // the previous token was "." inside a name
		callable bool // the name follows FROM or JOIN, where f(...) is a function
		prev     string
	)
	flush := func() {
		if len(name) == 0 {
			return
		}
		full := strings.Join(name, ".")
		if !seen[full] && len(tables) < maxTables {
			seen[full] = true
			tables = append(tables, full)
		}
		name, wantPart = nil, false
	}

	for {
		t, ok := sc.Next()
		if !ok {
			flush()
			return verb, tables
		}
		wasOpened := opened
		opened = false
		isName := t.Kind == sqlscan.QuotedIdent || (t.Kind == sqlscan.Word && !fromEnd[t.Upper()])

		// Continue a dotted name: schema.table.
		if len(name) > 0 {
			switch {
			case t.Kind == sqlscan.Punct && t.Text == "." && !wantPart:
				wantPart = true
				continue
			case wantPart && (t.Kind == sqlscan.Word || t.Kind == sqlscan.QuotedIdent):
				name = append(name, unquote(t.Text))
				wantPart = false
				continue
			}
			// FROM generate_series(1, 10) names a function, not a table.
			if callable && t.Kind == sqlscan.Punct && t.Text == "(" {
				name = nil
			}
			flush()
		}

		switch {
		case t.Kind == sqlscan.Punct && t.Text == "(":
			parens = append(parens, paren{outerFrom: inFrom})
			opened, expect, inFrom = true, false, false
		case t.Kind == sqlscan.Punct && t.Text == ")":
			expect, inFrom = false, false
			if n := len(parens); n > 0 {
				inFrom = parens[n-1].outerFrom
				parens = parens[:n-1]
			}
		case t.Kind == sqlscan.Punct && t.Text == ",":
			expect, callable = inFrom, inFrom
		case t.Kind == sqlscan.Word:
			up := t.Upper()
			if verb == "" {
				verb = up
			}
			if wasOpened && (up == "SELECT" || up == "WITH") {
				parens[len(parens)-1].subquery = true
			}
			switch {
			case expect && tableSkip[up]:
			case expect && isName:
				name = []string{t.Text}
				expect = false
			case up == "FROM":
				ok := len(parens) == 0 || parens[len(parens)-1].subquery
				expect, inFrom, callable = ok, ok, ok
			case up == "JOIN" || up == "STRAIGHT_JOIN":
				expect, inFrom, callable = true, false, true
			case up == "INTO", up == "UPDATE" && !updateClause[prev]:
				expect, inFrom, callable = true, false, false
			case fromEnd[up]:
				expect, inFrom = false, false
			}
			prev = up
		case t.Kind == sqlscan.QuotedIdent && expect:
			name = []string{unquote(t.Text)}
			expect = false
		default:
			expect = false
		}
	}
}

// unquote strips one layer of identifier quotes.
func unquote(s string) string {
	if len(s) >= 2 {
		switch s[0] {
		case '"', '`', '[':
			return s[1 : len(s)-1]
		}
	}
	return s
}
