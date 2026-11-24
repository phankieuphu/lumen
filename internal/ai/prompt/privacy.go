package prompt

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// masked replaces a string literal or quoted value that is not sent.
const masked = "'?'"

// quotes are the characters that open a quoted run in comments and errors.
const quotes = `'"` + "`"

// rewriter copies or replaces stretches of src in order, carrying a cursor
// offset in src over to the same place in the output.
type rewriter struct {
	b   strings.Builder
	cur int // in src, or -1
	out int // in the output, -1 until placed
}

// copy writes src[from:to] as is.
func (w *rewriter) copy(src string, from, to int) {
	if w.out < 0 && w.cur >= from && w.cur < to {
		w.out = w.b.Len() + w.cur - from
	}
	w.b.WriteString(src[from:to])
}

// replace writes repl in place of src[from:to]. A cursor inside lands
// inside repl, before its last byte.
func (w *rewriter) replace(repl string, from, to int) {
	if w.out < 0 && w.cur >= from && w.cur < to {
		w.out = w.b.Len() + min(w.cur-from, max(len(repl)-1, 0))
	}
	w.b.WriteString(repl)
}

// sanitize returns src with password literals redacted and, unless keep is
// set, every other string literal (including PostgreSQL dollar-quoted
// bodies) replaced by masked, and quoted runs inside comments masked too.
// The rest of comments, and numbers, are kept. cur, a byte offset in src or
// -1, is mapped to the same place in the result.
func sanitize(src string, cur int, d sqlscan.Dialect, keep bool) (string, int) {
	// RedactSecrets decides which literals are secrets. It only swaps
	// literals for literals, so its tokens line up one to one with src's
	// and the redacted ones can be read off without re-deriving its rules.
	red := sqlscan.RedactSecrets(src, d)
	if keep && red == src {
		return src, cur
	}
	var rs *sqlscan.Scanner
	if red != src {
		rs = sqlscan.NewScanner(red, d)
	}
	w := rewriter{cur: cur, out: -1}
	w.b.Grow(len(src))
	var (
		last int // end of the previous token
		// lost is set if the token streams ever disagree; every literal
		// after that is treated as a secret.
		lost bool
		sc   = sqlscan.NewScanner(src, d)
	)
	for {
		t, ok := sc.Next()
		if !ok {
			break
		}
		var rt sqlscan.Token
		if rs != nil {
			rt, _ = rs.Next()
			lost = lost || rt.Kind != t.Kind
		}
		w.between(src, last, t.Pos, d, keep)
		end := t.Pos + len(t.Text)
		last = end
		switch {
		case t.Kind != sqlscan.String:
		case lost, rs != nil && rt.Text == sqlscan.Redacted && t.Text != sqlscan.Redacted:
			w.replace(sqlscan.Redacted, t.Pos, end)
			continue
		case !keep:
			w.replace(masked, t.Pos, end)
			continue
		}
		w.copy(src, t.Pos, end)
	}
	w.between(src, last, len(src), d, keep)
	if w.out < 0 && cur >= 0 {
		w.out = w.b.Len()
	}
	return w.b.String(), w.out
}

// between writes src[from:to], the text between two tokens, which holds
// only whitespace and comments. Unless keep is set, quoted runs inside the
// comments are values written there and are masked, each comment on its
// own so masking never moves where a comment ends.
func (w *rewriter) between(src string, from, to int, d sqlscan.Dialect, keep bool) {
	if keep || !strings.ContainsAny(src[from:to], quotes) {
		w.copy(src, from, to)
		return
	}
	for i := from; i < to; {
		rest := src[i:to]
		switch {
		case strings.HasPrefix(rest, "/*"):
			end, body := blockCommentEnd(src, i, to, d)
			w.maskQuoted(src, i, body)
			w.copy(src, body, end)
			i = end
		case rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\n' || rest[0] == '\r':
			w.copy(src, i, i+1)
			i++
		default:
			// A line comment, or anything else: up to the end of the line.
			end := to
			if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
				end = i + nl
			}
			w.maskQuoted(src, i, end)
			i = end
		}
	}
}

// blockCommentEnd returns the end of the block comment at src[i:] within
// to, and where its body ends (before the closing "*/", if any).
// PostgreSQL block comments nest.
func blockCommentEnd(src string, i, to int, d sqlscan.Dialect) (end, body int) {
	depth := 0
	for j := i; j+1 < to; {
		switch {
		case src[j] == '/' && src[j+1] == '*' && (depth == 0 || d == sqlscan.Postgres):
			depth++
			j += 2
		case src[j] == '*' && src[j+1] == '/':
			depth--
			j += 2
			if depth == 0 {
				return j, j - 2
			}
		default:
			j++
		}
	}
	return to, to
}

// maskQuoted writes src[from:to], replacing the inside of each quoted run
// with "?". An unclosed run is masked to to.
func (w *rewriter) maskQuoted(src string, from, to int) {
	for from < to {
		j := strings.IndexAny(src[from:to], quotes)
		if j < 0 {
			w.copy(src, from, to)
			return
		}
		open := from + j
		w.copy(src, from, open+1)
		end := to
		if k := strings.IndexByte(src[open+1:to], src[open]); k >= 0 {
			end = open + 1 + k
		}
		w.replace("?", open+1, end)
		from = end
		if end < to {
			w.copy(src, end, end+1)
			from = end + 1
		}
	}
}

// span is a stretch of error text to judge: s[open] is the opening quote
// or parenthesis and s[close] the closing one, or close is len(s) when it
// never closes.
type span struct {
	open, close int
	kind        spanKind
	// group links quoted values on one line with more than one pair of the
	// same quote; once one of them is masked, all later ones are too.
	group int
}

type spanKind uint8

const (
	spanQuote  spanKind = iota // a quoted value, kept if it is a known name
	spanForced                 // quotes that do not pair up: always masked
	spanRow                    // a list of row values: always masked
	spanNear                   // MySQL's near '...' echo of the statement
)

// rowValues introduce a parenthesized list of row values in PostgreSQL
// messages: Key (email)=(a@b.com) and Failing row contains (...).
var rowValues = [...]string{")=(", "Failing row contains ("}

// nearOpen and nearClose bracket MySQL's echo of the failing statement,
// which holds the statement's own quotes, so it is one span from the first
// quote to the last before " at line".
const (
	nearOpen  = "near '"
	nearClose = "' at line"
)

// errorSpans finds what may need masking in an error message. Everything
// that cannot be paired with certainty fails closed:
//
//   - MySQL's near '...' at line N is one span, greedy to the last quote
//     before " at line", or to the end.
//   - A row-value list runs to the last ")" on its line, or the last ")"
//     after it, or the end.
//   - For each quote kind, an apostrophe between two letters (can't,
//     O'Brien) is not a quote. If the rest do not pair up (an odd count, or
//     a pair spanning lines), everything from the first to the last is one
//     forced span, to the end if there is only one.
func errorSpans(s string) []span {
	var out []span
	for i := 0; i < len(s); {
		j := strings.Index(s[i:], nearOpen)
		if j < 0 {
			break
		}
		open := i + j + len(nearOpen) - 1
		end := len(s)
		if k := strings.LastIndex(s[open+1:], nearClose); k >= 0 {
			end = open + 1 + k
		}
		out = append(out, span{open: open, close: end, kind: spanNear})
		i = end + 1
	}
	inside := func(i int) bool {
		for _, sp := range out {
			if i >= sp.open && i <= sp.close {
				return true
			}
		}
		return false
	}

	for i := 0; i < len(s); i++ {
		if inside(i) {
			continue
		}
		for _, p := range rowValues {
			if strings.HasPrefix(s[i:], p) {
				open := i + len(p) - 1
				c := lastParen(s, open)
				out = append(out, span{open: open, close: c, kind: spanRow})
				i = c
				break
			}
		}
	}

	group := 0
	var pos []int
	for _, q := range []byte(quotes) {
		pos = pos[:0]
		for i := 0; i < len(s); i++ {
			if s[i] == q && !inside(i) && (q != '\'' || !apostrophe(s, i)) {
				pos = append(pos, i)
			}
		}
		if len(pos) == 0 {
			continue
		}
		paired := len(pos)%2 == 0
		for k := 0; paired && k < len(pos); k += 2 {
			paired = strings.IndexByte(s[pos[k]:pos[k+1]], '\n') < 0
		}
		if !paired {
			end := len(s)
			if len(pos) > 1 && len(pos)%2 == 0 {
				end = pos[len(pos)-1]
			}
			out = append(out, span{open: pos[0], close: end, kind: spanForced})
			continue
		}
		for k := 0; k < len(pos); k += 2 {
			g := 0
			sameLine := func(a, b int) bool { return strings.IndexByte(s[a:b], '\n') < 0 }
			if (k > 0 && sameLine(pos[k-1], pos[k])) || (k+2 < len(pos) && sameLine(pos[k+1], pos[k+2])) {
				if k == 0 || !sameLine(pos[k-1], pos[k]) {
					group++
				}
				g = group
			}
			out = append(out, span{open: pos[k], close: pos[k+1], kind: spanQuote, group: g})
		}
	}
	return out
}

// apostrophe reports whether the quote at s[i] sits between two letters.
func apostrophe(s string, i int) bool {
	if i == 0 || i+1 >= len(s) {
		return false
	}
	before, _ := utf8.DecodeLastRuneInString(s[:i])
	after, _ := utf8.DecodeRuneInString(s[i+1:])
	return unicode.IsLetter(before) && unicode.IsLetter(after)
}

// lastParen returns the last ")" after open on open's line, else the last
// one after open at all, else len(s): row values may hold ")" and newlines.
func lastParen(s string, open int) int {
	rest := s[open+1:]
	line := rest
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		line = rest[:nl]
	}
	if k := strings.LastIndexByte(line, ')'); k >= 0 {
		return open + 1 + k
	}
	if k := strings.LastIndexByte(rest, ')'); k >= 0 {
		return open + 1 + k
	}
	return len(s)
}

// maskedError returns the error text to send: password literals redacted
// and, unless Privacy.KeepLiterals, row values and quoted values masked.
// A quoted value survives only when it stands where messages put names,
// after a word such as relation or column, and names a known schema,
// table, column, or foreign key, or an identifier in the editor, as in
// relation "orderz" does not exist. A value that only looks like a name,
// as in Duplicate entry 'orders', is masked. MySQL's echo of the statement is
// always masked, or with KeepLiterals sanitized like editor SQL, so a
// password in it stays hidden.
func (b *builder) maskedError() string {
	s := strings.TrimSpace(b.in.Error)
	if s == "" {
		return ""
	}
	s = sqlscan.RedactSecrets(s, b.d)
	keep := b.in.Privacy.KeepLiterals
	spans := errorSpans(s)
	if len(spans) == 0 {
		return s
	}
	var known map[string]bool
	if !keep {
		known = b.knownNames(s, spans)
	}
	type cut struct {
		from, to int // s[from] and s[to] are kept; between them, repl
		repl     string
	}
	var cuts []cut
	for k := 0; k < len(spans); k++ {
		sp := spans[k]
		switch {
		case sp.kind == spanNear && keep:
			inner, _ := sanitize(s[sp.open+1:sp.close], -1, b.d, true)
			cuts = append(cuts, cut{sp.open, sp.close, inner})
		case keep:
		case sp.kind == spanQuote && namePosition(s, sp.open) && knownValue(s[sp.open+1:sp.close], known):
		case sp.kind == spanQuote && sp.group > 0:
			// Pairing several values on a line is a guess; mask from the
			// first unknown one through the line's last.
			end := sp.close
			for k+1 < len(spans) && spans[k+1].group == sp.group {
				k++
				end = spans[k].close
			}
			cuts = append(cuts, cut{sp.open, end, "?"})
		default:
			cuts = append(cuts, cut{sp.open, sp.close, "?"})
		}
	}
	if len(cuts) == 0 {
		return s
	}
	slices.SortFunc(cuts, func(x, y cut) int { return x.from - y.from })
	merged := cuts[:1]
	for _, c := range cuts[1:] {
		if m := &merged[len(merged)-1]; c.from <= m.to {
			m.to, m.repl = max(m.to, c.to), "?"
			continue
		}
		merged = append(merged, c)
	}
	var out strings.Builder
	out.Grow(len(s))
	last := 0
	for _, c := range merged {
		out.WriteString(s[last : c.from+1])
		out.WriteString(c.repl)
		last = min(c.to, len(s))
	}
	out.WriteString(s[last:])
	return out.String()
}

// nameWords are the words after which PostgreSQL and MySQL messages quote
// a name rather than a value: relation "t", column "c" of relation "t",
// constraint "k", Table 'db.t', for key 'k'.
var nameWords = map[string]bool{
	"relation": true, "table": true, "view": true, "column": true, "constraint": true,
	"index": true, "key": true, "schema": true, "database": true, "sequence": true,
	"function": true, "procedure": true, "trigger": true, "type": true,
}

// namePosition reports whether the quote at open follows one of nameWords.
func namePosition(s string, open int) bool {
	end := len(strings.TrimRight(s[:open], " "))
	start := end
	for start > 0 && isWordByte(s[start-1]) {
		start--
	}
	return start < end && nameWords[strings.ToLower(s[start:end])]
}

func isWordByte(c byte) bool {
	return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// knownNames returns the lower-cased dot parts of the quoted values in s,
// each mapped to whether it names something in the schema or editor.
func (b *builder) knownNames(s string, spans []span) map[string]bool {
	known := make(map[string]bool, len(spans))
	for _, sp := range spans {
		if sp.kind == spanQuote {
			for part := range strings.SplitSeq(strings.ToLower(s[sp.open+1:sp.close]), ".") {
				known[part] = false
			}
		}
	}
	see := func(name string) {
		if _, ok := known[name]; ok {
			known[name] = true
		} else if _, ok := known[strings.ToLower(name)]; ok {
			known[strings.ToLower(name)] = true
		}
	}
	for i := range b.tables {
		t := &b.tables[i]
		see(t.Schema)
		see(t.Name)
		for ci := range t.Columns {
			see(t.Columns[ci].Name)
		}
	}
	for i := range b.fks {
		see(b.fks[i].Name)
	}
	for i := range b.ed.stmts {
		for _, t := range b.ed.stmts[i].Tokens {
			switch t.Kind {
			case sqlscan.Word:
				see(t.Text)
			case sqlscan.QuotedIdent:
				see(unquoteIdent(t.Text))
			}
		}
	}
	delete(known, "")
	return known
}

// knownValue reports whether every dot part of v is a known name.
func knownValue(v string, known map[string]bool) bool {
	if v == "" {
		return false
	}
	for part := range strings.SplitSeq(strings.ToLower(v), ".") {
		if !known[part] {
			return false
		}
	}
	return true
}
