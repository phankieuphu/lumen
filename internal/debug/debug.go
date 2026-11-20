// Package debug helps find out why a query or script does what it does. It
// splits a SELECT into the parts it is built from and counts the rows of
// each, so you can see where rows appear or vanish; it lists a script's
// statements for stepping through one at a time; and it dry-runs a script
// inside a transaction that is always rolled back, with a time limit.
//
// Safety: part counts run in read-only transactions. A dry run takes only
// statements the database can undo, refusing transaction control, built-in
// functions that act outside the transaction, and, on MySQL, schema
// changes, which MySQL commits at once. Sequence and AUTO_INCREMENT values
// are not given back, and on MySQL a change to a table without transactions
// (MyISAM) is kept: the run reports it with ErrKept.
package debug

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/query"
	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// Errors returned by Parts and DryRun.
var (
	ErrUnsupported = errors.New("debug: this database is not supported")
	ErrEmpty       = errors.New("debug: no statement to run")
	ErrStatement   = errors.New("debug: only a SELECT can be split into parts")
	ErrNotUndoable = errors.New("debug: a dry run cannot undo this statement")
	ErrTooMany     = errors.New("debug: too many statements for one dry run")
	ErrRollback    = errors.New("debug: the dry run could not be rolled back")
	ErrKept        = errors.New("debug: some changes were kept: a table they touched has no transactions")
)

// Step is one statement of a script.
type Step struct {
	// Start and End are byte offsets of SQL in the script.
	Start int    `json:"start"`
	End   int    `json:"end"`
	SQL   string `json:"sql"`
	Kind  string `json:"kind"`
	// Control is set for a statement that starts or ends a transaction;
	// stepping through a script skips these, as it runs in its own.
	Control bool `json:"control"`
	// Undoable is set when a dry run can take the statement; Why says why
	// it cannot.
	Undoable bool   `json:"undoable"`
	Why      string `json:"why,omitempty"`
}

// Steps returns the statements of script, in order.
func Steps(script string, d driver.Dialect) []Step {
	stmts := sqlscan.Split(script, sqlscan.Dialect(d))
	out := make([]Step, len(stmts))
	for i, st := range stmts {
		why := undoable(st.Tokens, d)
		out[i] = Step{
			Start: st.Start, End: st.End, SQL: st.Text,
			Kind:     string(query.Classify(st.Text, d).Kind),
			Control:  query.IsTxControl(st.Text, d),
			Undoable: why == "", Why: why,
		}
	}
	return out
}

// Statements a dry run takes, by leading keyword. Everything else is
// refused: it may commit, act outside the transaction, or be unknown.
var (
	dataKeywords = set("SELECT", "WITH", "VALUES", "TABLE", "INSERT", "UPDATE", "DELETE")
	undoKeywords = map[driver.Dialect]map[string]bool{
		// PostgreSQL schema changes are transactional; the few that are not
		// (CREATE DATABASE, CREATE INDEX CONCURRENTLY) refuse to run in a
		// transaction.
		driver.Postgres: set("MERGE", "CREATE", "ALTER", "DROP", "TRUNCATE", "COMMENT"),
		driver.MySQL:    set("REPLACE"),
		driver.SQLite:   set("REPLACE", "CREATE", "ALTER", "DROP"),
	}
	controlKeywords = set("BEGIN", "START", "COMMIT", "END", "ROLLBACK", "ABORT", "SAVEPOINT", "RELEASE",
		"PREPARE", "XA", "LOCK", "UNLOCK")
)

// txScoped are side-effect functions whose effect ends with the
// transaction, or, for nextval, that every INSERT with a serial key calls.
var txScoped = set("PG_ADVISORY_XACT_LOCK", "PG_TRY_ADVISORY_XACT_LOCK", "NEXTVAL")

// undoable says why a dry run cannot take the statement, or "".
func undoable(toks []sqlscan.Token, d driver.Dialect) string {
	if len(toks) == 0 {
		return "it is empty"
	}
	kw := toks[0].Upper()
	switch {
	case controlKeywords[kw]:
		return "it controls the transaction the dry run happens in"
	case d == driver.MySQL && schemaKeyword(kw):
		return "MySQL saves schema changes at once, so a dry run cannot undo them"
	case !dataKeywords[kw] && !undoKeywords[d][kw]:
		return kw + " statements cannot be undone by a dry run"
	}
	if fn := query.SideEffectCall(toks); fn != "" && !txScoped[fn] {
		return strings.ToLower(fn) + "() acts outside the transaction, so a dry run cannot undo it"
	}
	if d == driver.MySQL {
		for _, t := range toks {
			if t.Is("OUTFILE") || t.Is("DUMPFILE") {
				return "it writes a file on the server"
			}
		}
	}
	return ""
}

func schemaKeyword(kw string) bool {
	switch kw {
	case "CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME":
		return true
	}
	return false
}

func set(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// count reads a count(*) value, which drivers return as a number or text.
func count(v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case uint64:
		return int64(x), nil //nolint:gosec // a row count fits
	case float64:
		return int64(x), nil
	case string:
		return parseCount(x)
	case []byte:
		return parseCount(string(x))
	}
	return 0, fmt.Errorf("debug: read the count: unexpected %T", v)
}

func parseCount(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("debug: read the count: %w", err)
	}
	return n, nil
}

// trim shortens SQL to one line of at most n bytes.
func trim(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		cut := n
		for cut > 0 && !utf8Start(s[cut]) {
			cut--
		}
		return s[:cut] + "…"
	}
	return s
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
