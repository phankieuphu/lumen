package sqlscan

import "strings"

// Redacted replaces secret literals with this placeholder.
const Redacted = "'***'"

// RedactSecrets returns src with password literals replaced by Redacted, so
// statements like ALTER USER ... PASSWORD '...' or CREATE USER ... IDENTIFIED
// BY '...' can be logged. Everything else is returned unchanged.
func RedactSecrets(src string, d Dialect) string {
	var (
		b       strings.Builder
		last    int
		prev    [3]Token // most recent tokens, prev[0] newest
		changed bool
		// sawPassword is set once PASSWORD appears in the current statement,
		// so SET PASSWORD FOR u = 'x' is caught too.
		sawPassword bool
		first       Token // first token of the current statement
		sc          = NewScanner(src, d)
	)
	for {
		t, ok := sc.Next()
		if !ok {
			break
		}
		switch {
		case t.Kind == Semicolon:
			sawPassword, first = false, Token{}
		case first.Kind == 0:
			first = t
		case t.Is("PASSWORD") && (first.Is("SET") || first.Is("ALTER") || first.Is("CREATE") || first.Is("GRANT")):
			sawPassword = true
		}
		afterAssign := sawPassword && prev[0].Kind == Punct && prev[0].Text == "="
		if t.Kind == String && (afterAssign || isSecretContext(prev)) {
			if !changed {
				b.Grow(len(src))
				changed = true
			}
			b.WriteString(src[last:t.Pos])
			b.WriteString(Redacted)
			last = t.Pos + len(t.Text)
		}
		prev[2], prev[1], prev[0] = prev[1], prev[0], t
	}
	if !changed {
		return src
	}
	b.WriteString(src[last:])
	return b.String()
}

// isSecretContext reports whether the next string literal is a password:
//
//	PASSWORD 'x'          (PostgreSQL)
//	PASSWORD = 'x'        (also SET PASSWORD FOR u = 'x', handled by the caller)
//	IDENTIFIED BY 'x'     (MySQL)
//	IDENTIFIED WITH plugin BY 'x'
//	ENCRYPTED PASSWORD 'x'
func isSecretContext(prev [3]Token) bool {
	switch {
	case prev[0].Is("PASSWORD"):
		return true
	case prev[0].Kind == Punct && prev[0].Text == "=" && prev[1].Is("PASSWORD"):
		return true
	case prev[0].Is("BY") && (prev[1].Is("IDENTIFIED") || prev[2].Is("WITH") || prev[2].Is("IDENTIFIED")):
		return true
	case prev[0].Is("AS") && prev[2].Is("IDENTIFIED"): // IDENTIFIED WITH plugin AS 'hash'
		return true
	}
	return false
}
