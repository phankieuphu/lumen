package prompt

import (
	"strings"
	"unicode/utf8"

	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// maxWindow caps the editor text split and described around the cursor.
const maxWindow = 256 << 10

// editorView is the part of the editor around the cursor, split into
// statements.
type editorView struct {
	src   string // the window
	cur   int    // cursor offset in src
	stmts []sqlscan.Statement
	at    int // index of the statement at the cursor, or -1
	// mask is set when the statement at the cursor begins before the
	// window: without its start, the password redaction cannot see its
	// context, so every literal is masked whatever Privacy says.
	mask bool
	// skipped counts editor bytes outside the window.
	skipped int
}

// newEditor clamps cur to a rune boundary in src, cuts a window of at most
// maxWindow bytes around it, and finds the statement holding it.
//
// The window starts on a token boundary found by lexing from the top of
// src, so it never starts inside a string literal, where literal text would
// be read as SQL and escape masking. Lexing allocates nothing, so this
// costs one pass over the text before the window.
func newEditor(src string, cur int, d sqlscan.Dialect) editorView {
	cur = max(0, min(cur, len(src)))
	for cur > 0 && cur < len(src) && !utf8.RuneStart(src[cur]) {
		cur--
	}
	e := editorView{at: -1}
	start, end := 0, len(src)
	mid := false
	if len(src) > maxWindow {
		start = max(0, min(cur-maxWindow/2, len(src)-maxWindow))
		end = start + maxWindow
		for end > cur && end < len(src) && !utf8.RuneStart(src[end]) {
			end--
		}
		if start > 0 {
			start, mid = tokenBoundary(src, start, d)
		}
		if start > cur {
			// The cursor sits in one string or comment longer than half
			// the window. There is nothing safe to send.
			e.skipped = len(src)
			return e
		}
	}
	e.src, e.cur, e.skipped = src[start:end], cur-start, len(src)-(end-start)
	e.stmts = sqlscan.Split(e.src, d)
	for i := range e.stmts {
		if e.stmts[i].Start > e.cur {
			break
		}
		e.at = i
	}
	if e.at >= 0 {
		s := &e.stmts[e.at]
		if e.cur > s.End && hasSemicolon(e.src[s.End:e.cur], d) {
			e.at = -1 // the cursor is past this statement's semicolon
		}
	}
	e.mask = mid && e.at == 0
	return e
}

// tokenBoundary returns the offset of the first token at or after at, and
// whether that token continues a statement begun before it.
func tokenBoundary(src string, at int, d sqlscan.Dialect) (pos int, mid bool) {
	sc := sqlscan.NewScanner(src, d)
	prev := sqlscan.Semicolon
	for {
		t, ok := sc.Next()
		if !ok {
			return len(src), false
		}
		if t.Pos >= at {
			return t.Pos, prev != sqlscan.Semicolon
		}
		prev = t.Kind
	}
}

func hasSemicolon(s string, d sqlscan.Dialect) bool {
	sc := sqlscan.NewScanner(s, d)
	for {
		t, ok := sc.Next()
		if !ok {
			return false
		}
		if t.Kind == sqlscan.Semicolon {
			return true
		}
	}
}

// word returns the identifier characters just before the cursor.
func (e *editorView) word() string {
	i := e.cur
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(e.src[:i])
		if r == '$' || !isIdentRune(r) {
			break
		}
		i -= size
	}
	return e.src[i:e.cur]
}

// editorParts returns the editor sections within limit tokens: the
// statement at the cursor for TextToSQL and FixError, or the text before
// and after the cursor in it for Inline.
func (b *builder) editorParts(limit int) []Section {
	e := &b.ed
	if e.skipped > 0 {
		b.omit(Omission{Part: PartSQL, Reason: ReasonEditorTooLarge, Count: e.skipped})
	}
	others := len(e.stmts)
	if e.at >= 0 {
		others--
	}
	if others > 0 {
		b.omit(Omission{Part: PartSQL, Reason: ReasonOtherStatement, Count: others})
	}
	if e.at < 0 {
		return nil
	}
	s := &e.stmts[e.at]
	keep := b.in.Privacy.KeepLiterals && !e.mask
	if b.in.Task != Inline {
		text, _ := sanitize(s.Text, -1, b.d, keep)
		return b.textPart(PartSQL, text, limit)
	}

	end := max(s.End, e.cur)
	text, cur := sanitize(e.src[s.Start:end], e.cur-s.Start, b.d, keep)
	// Each side is neutralized on its own: a "</" split by the cursor is
	// harmless, as neither half closes a tag.
	out := make([]Section, 0, 2)
	if sec, ok := b.trimBefore(neutral(text[:cur]), limit*3/4); ok {
		out = append(out, sec)
		limit -= sec.Tokens + 1
	}
	if sec, ok := b.trimAfter(neutral(text[cur:]), limit); ok {
		out = append(out, sec)
	}
	return out
}

// trimBefore keeps the end of s, the text before the cursor, dropping
// whole lines from its start until it fits in limit tokens.
func (b *builder) trimBefore(s string, limit int) (Section, bool) {
	const p = PartSQLBeforeCursor
	if strings.TrimSpace(s) == "" {
		return Section{}, false
	}
	room := bodyBytes(p, limit)
	if len(s) <= room {
		return section(p, s), true
	}
	if room <= len(ellipsis) {
		b.omit(Omission{Part: p, Reason: ReasonBudget, Count: len(s)})
		return Section{}, false
	}
	from := len(s) - room
	if j := strings.IndexByte(s[from:], '\n'); j >= 0 && from+j+1 < len(s) {
		i := from + j + 1
		b.omit(Omission{Part: p, Reason: ReasonBudget, Count: i})
		return section(p, s[i:]), true
	}
	i := from + len(ellipsis)
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	b.omit(Omission{Part: p, Reason: ReasonBudget, Count: i})
	return section(p, ellipsis+s[i:]), true
}

// trimAfter keeps the start of s, the text after the cursor, dropping
// whole lines from its end until it fits in limit tokens.
func (b *builder) trimAfter(s string, limit int) (Section, bool) {
	const p = PartSQLAfterCursor
	if strings.TrimSpace(s) == "" {
		return Section{}, false
	}
	room := bodyBytes(p, limit)
	if len(s) <= room {
		return section(p, s), true
	}
	if room > len(ellipsis) {
		if j := strings.LastIndexByte(s[:room+1], '\n'); j > 0 {
			b.omit(Omission{Part: p, Reason: ReasonBudget, Count: len(s) - j})
			return section(p, s[:j]), true
		}
		i := room - len(ellipsis)
		for i > 0 && !utf8.RuneStart(s[i]) {
			i--
		}
		if i > 0 {
			b.omit(Omission{Part: p, Reason: ReasonBudget, Count: len(s) - i})
			return section(p, s[:i]+ellipsis), true
		}
	}
	b.omit(Omission{Part: p, Reason: ReasonBudget, Count: len(s)})
	return Section{}, false
}
