package sqlscan

import "strings"

// Statement is one statement from a script.
type Statement struct {
	// Text is the statement source without the trailing semicolon,
	// trimmed of surrounding whitespace. Leading comments are kept.
	Text string
	// Tokens are the statement's tokens, comments excluded.
	Tokens []Token
}

// Split breaks src into statements on top-level semicolons. Statements made
// only of whitespace and comments are dropped.
//
// Semicolons inside BEGIN ... END blocks of CREATE statements (triggers,
// procedures, SQL-standard function bodies) do not split.
func Split(src string, d Dialect) []Statement {
	var (
		out   []Statement
		toks  []Token
		start = 0
		depth = 0
		sc    = NewScanner(src, d)
	)
	flush := func(end int) {
		if len(toks) > 0 {
			out = append(out, Statement{Text: strings.TrimSpace(src[start:end]), Tokens: toks})
		}
		toks = nil
	}
	for {
		t, ok := sc.Next()
		if !ok {
			break
		}
		if t.Kind == Semicolon && depth == 0 {
			flush(t.Pos)
			start = t.Pos + 1
			continue
		}
		if len(toks) == 0 {
			start = t.Pos
		}
		if len(toks) > 0 && toks[0].Is("CREATE") {
			depth = blockDepth(depth, toks[len(toks)-1], t)
		}
		toks = append(toks, t)
	}
	flush(len(src))
	return out
}

// blockDepth tracks BEGIN/CASE ... END nesting inside a CREATE statement.
// END IF / END LOOP / END WHILE / END REPEAT close blocks that never opened
// a level, so they are ignored.
func blockDepth(depth int, prev, t Token) int {
	switch {
	case t.Is("BEGIN"), t.Is("CASE") && !prev.Is("END"):
		return depth + 1
	case prev.Is("END") && (t.Is("IF") || t.Is("LOOP") || t.Is("WHILE") || t.Is("REPEAT")):
		return depth + 1 // undo the decrement applied when END was seen
	case t.Is("END") && depth > 0:
		return depth - 1
	}
	return depth
}

// Count returns the number of statements in src.
func Count(src string, d Dialect) int { return len(Split(src, d)) }
