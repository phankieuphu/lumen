// Package query runs user SQL: it classifies statements, enforces read-only
// and production policies, streams rows in pages, supports cancel by request
// ID, and writes every non-read statement to the audit log.
package query

import (
	"strings"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// Kind is what a statement does, from least to most dangerous.
type Kind string

// Statement kinds.
const (
	KindRead    Kind = "read"    // returns data, changes nothing
	KindSession Kind = "session" // transaction control and harmless session settings
	KindWrite   Kind = "write"   // changes data or server state; also anything unknown
	KindSchema  Kind = "schema"  // DDL and privileges
)

func (k Kind) rank() int {
	switch k {
	case KindRead:
		return 0
	case KindSession:
		return 1
	case KindWrite:
		return 2
	default:
		return 3
	}
}

// Classification is the result of Classify.
type Classification struct {
	// Kind is the most dangerous kind among all statements.
	Kind Kind
	// Statements is the number of statements found.
	Statements int
	// ReturnsRows reports whether the (first) statement produces a result
	// set, which decides between Session.Query and Session.Exec.
	ReturnsRows bool
	// Keyword is the leading keyword of the first statement, upper-cased.
	Keyword string
}

// IsRead reports whether every statement is a plain read.
func (c Classification) IsRead() bool { return c.Kind == KindRead }

// Classify inspects sql lexically. It fails closed: anything it cannot prove
// to be a read or a harmless session statement is a write.
//
// It cannot see inside functions; a SELECT that calls a user function with
// side effects classifies as a read. Read-only connections therefore also
// enforce read-only inside the engine.
func Classify(sql string, d driver.Dialect) Classification {
	stmts := sqlscan.Split(sql, sqlscan.Dialect(d))
	c := Classification{Kind: KindRead, Statements: len(stmts)}
	for i, st := range stmts {
		kind, rows := classifyTokens(st.Tokens)
		if kind.rank() > c.Kind.rank() {
			c.Kind = kind
		}
		if i == 0 {
			c.ReturnsRows = rows
			c.Keyword = st.Tokens[0].Upper()
		}
	}
	return c
}

var (
	readKeywords   = set("SELECT", "VALUES", "TABLE", "SHOW", "DESCRIBE", "DESC", "HELP", "CHECKSUM")
	schemaKeywords = set("CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME", "COMMENT", "GRANT", "REVOKE",
		"SECURITY", "REASSIGN", "INSTALL", "UNINSTALL", "IMPORT")
	sessionKeywords = set("BEGIN", "START", "COMMIT", "END", "ROLLBACK", "ABORT", "SAVEPOINT", "RELEASE",
		"SET", "RESET", "USE", "DISCARD", "DEALLOCATE")
	writeKeywords = set("INSERT", "UPDATE", "DELETE", "MERGE", "REPLACE", "UPSERT")

	// Session statements that touch any of these words can lift read-only
	// mode or change identity, so they count as writes.
	escalationWords = []string{"READ_ONLY", "QUERY_ONLY", "ROLE", "AUTHORIZATION", "GLOBAL", "PERSIST", "PASSWORD"}

	// Built-in functions with side effects. A read that calls one is a write.
	sideEffectFuncs = set(
		"SET_CONFIG", "SETVAL", "NEXTVAL", "PG_TERMINATE_BACKEND", "PG_CANCEL_BACKEND", "PG_RELOAD_CONF",
		"PG_ROTATE_LOGFILE", "PG_PROMOTE", "PG_SWITCH_WAL", "PG_CREATE_RESTORE_POINT",
		"PG_ADVISORY_LOCK", "PG_ADVISORY_XACT_LOCK", "PG_TRY_ADVISORY_LOCK", "PG_TRY_ADVISORY_XACT_LOCK",
		"LO_IMPORT", "LO_EXPORT", "LO_UNLINK", "LO_CREATE", "LO_FROM_BYTEA", "LO_PUT",
		"DBLINK", "DBLINK_EXEC", "PG_FILE_WRITE", "PG_LOGICAL_EMIT_MESSAGE",
		"PG_CREATE_LOGICAL_REPLICATION_SLOT", "PG_CREATE_PHYSICAL_REPLICATION_SLOT", "PG_DROP_REPLICATION_SLOT",
		"PG_STAT_RESET", "PG_STAT_STATEMENTS_RESET",
		"GET_LOCK", "RELEASE_LOCK", "RELEASE_ALL_LOCKS",
		"LOAD_EXTENSION", "WRITEFILE",
	)

	// PRAGMAs that only report information, even when given an argument.
	readPragmas = set("TABLE_INFO", "TABLE_XINFO", "TABLE_LIST", "INDEX_LIST", "INDEX_INFO", "INDEX_XINFO",
		"FOREIGN_KEY_LIST", "FOREIGN_KEY_CHECK", "DATABASE_LIST", "COLLATION_LIST", "FUNCTION_LIST",
		"MODULE_LIST", "PRAGMA_LIST", "COMPILE_OPTIONS", "INTEGRITY_CHECK", "QUICK_CHECK")
)

// classifyTokens classifies one statement and reports whether it returns rows.
func classifyTokens(toks []sqlscan.Token) (Kind, bool) {
	first := toks[0]
	if first.Kind != sqlscan.Word {
		if first.Kind == sqlscan.Punct && first.Text == "(" {
			// (SELECT ...) UNION (SELECT ...)
			return classifyRead(toks)
		}
		return KindWrite, false
	}
	kw := first.Upper()
	switch {
	case kw == "WITH":
		return classifyWith(toks)
	case kw == "EXPLAIN":
		return classifyExplain(toks)
	case kw == "PRAGMA":
		return classifyPragma(toks), true
	case readKeywords[kw]:
		return classifyRead(toks)
	case schemaKeywords[kw]:
		return KindSchema, false
	case writeKeywords[kw]:
		return KindWrite, hasWord(toks, "RETURNING")
	case sessionKeywords[kw]:
		if escalates(toks) {
			return KindWrite, false
		}
		return KindSession, false
	}
	return KindWrite, false // unknown: fail closed
}

// classifyRead checks a SELECT-like statement for clauses that make it more
// than a read: SELECT INTO (creates a table or writes a file), locking
// clauses, and side-effect functions.
func classifyRead(toks []sqlscan.Token) (Kind, bool) {
	for i, t := range toks {
		switch {
		case t.Is("INTO"):
			return KindWrite, false
		case t.Is("FOR") && i+1 < len(toks) &&
			(toks[i+1].Is("UPDATE") || toks[i+1].Is("SHARE") || toks[i+1].Is("NO") || toks[i+1].Is("KEY")):
			return KindWrite, true
		case t.Is("LOCK") && i+1 < len(toks) && toks[i+1].Is("IN"): // MySQL LOCK IN SHARE MODE
			return KindWrite, true
		case writeKeywords[t.Upper()] && t.Kind == sqlscan.Word && (!t.Is("REPLACE") || !isCall(toks, i)):
			return KindWrite, true
		case isSideEffectCall(toks, i):
			return KindWrite, true
		}
	}
	return KindRead, true
}

// classifyWith handles WITH ... : any data-modifying CTE or main statement
// makes it a write. It returns rows unless the main statement is a write
// without RETURNING.
func classifyWith(toks []sqlscan.Token) (Kind, bool) {
	kind := KindRead
	depth := 0
	main := ""
	for i, t := range toks {
		switch {
		case t.Kind == sqlscan.Punct && t.Text == "(":
			depth++
		case t.Kind == sqlscan.Punct && t.Text == ")":
			depth--
		case t.Kind != sqlscan.Word:
		case writeKeywords[t.Upper()] && (!t.Is("REPLACE") || !isCall(toks, i)):
			kind = KindWrite
		case t.Is("INTO") && depth == 0 && main == "SELECT":
			kind = KindWrite
		case isSideEffectCall(toks, i):
			kind = KindWrite
		}
		if depth == 0 && main == "" && i > 0 && t.Kind == sqlscan.Word {
			if u := t.Upper(); u == "SELECT" || u == "VALUES" || u == "TABLE" || writeKeywords[u] {
				main = u
			}
		}
	}
	if kind == KindRead {
		if k, _ := classifyRead(toks); k != KindRead {
			kind = k
		}
	}
	rows := !writeKeywords[main] || hasWord(toks, "RETURNING")
	return kind, rows
}

// classifyExplain: plain EXPLAIN only plans. EXPLAIN ANALYZE runs the
// statement, so it takes the inner statement's kind.
func classifyExplain(toks []sqlscan.Token) (Kind, bool) {
	analyze := false
	i := 1
	for ; i < len(toks); i++ {
		t := toks[i]
		if t.Is("ANALYZE") || t.Is("ANALYSE") {
			analyze = true
		}
		if t.Kind == sqlscan.Word && (readKeywords[t.Upper()] || writeKeywords[t.Upper()] ||
			t.Is("WITH") || schemaKeywords[t.Upper()]) && !t.Is("DESC") {
			break
		}
	}
	if i >= len(toks) {
		return KindRead, true // EXPLAIN <table> (MySQL) or options only
	}
	if !analyze {
		return KindRead, true
	}
	kind, _ := classifyTokens(toks[i:])
	return kind, true
}

func classifyPragma(toks []sqlscan.Token) Kind {
	if len(toks) < 2 {
		return KindRead
	}
	nameIdx := 1
	if len(toks) > 3 && toks[2].Text == "." { // PRAGMA schema.name
		nameIdx = 3
	}
	for _, t := range toks {
		if t.Kind == sqlscan.Punct && t.Text == "=" {
			return KindWrite
		}
	}
	if readPragmas[toks[nameIdx].Upper()] || nameIdx == len(toks)-1 {
		return KindRead
	}
	return KindWrite
}

func escalates(toks []sqlscan.Token) bool {
	for i, t := range toks {
		// Check every named thing: words, quoted names, strings, and
		// MySQL @@system_variables (lexed as parameters).
		if t.Kind == sqlscan.Punct || t.Kind == sqlscan.Number {
			continue
		}
		u := strings.ToUpper(t.Text)
		for _, w := range escalationWords {
			if strings.Contains(u, w) {
				return true
			}
		}
		if t.Is("READ") && i+1 < len(toks) && toks[i+1].Is("WRITE") {
			return true
		}
	}
	return false
}

func isCall(toks []sqlscan.Token, i int) bool {
	return i+1 < len(toks) && toks[i+1].Kind == sqlscan.Punct && toks[i+1].Text == "("
}

func isSideEffectCall(toks []sqlscan.Token, i int) bool {
	return toks[i].Kind == sqlscan.Word && sideEffectFuncs[toks[i].Upper()] && isCall(toks, i)
}

func hasWord(toks []sqlscan.Token, kw string) bool {
	for _, t := range toks {
		if t.Is(kw) {
			return true
		}
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
