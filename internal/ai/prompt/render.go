package prompt

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// Caps on rendered names and types, in bytes.
const (
	maxNameBytes = 128
	maxTypeBytes = 64
)

// maxSchemaPrealloc bounds the schema buffer reserved up front.
const maxSchemaPrealloc = 64 << 10

// schemaPart renders tables in rank order within limit tokens: each in full
// if it fits, else in compact form, else left out. Tables that matched
// nothing come last, so a small database is sent whole.
func (b *builder) schemaPart(limit int) []Section {
	if b.in.Schema.Truncated {
		b.omit(Omission{Part: PartSchema, Reason: ReasonSchemaTruncated})
	}
	if len(b.tables) == 0 {
		return nil
	}
	room := bodyBytes(PartSchema, limit)
	var (
		out      strings.Builder
		size     int // bytes of out, plus the newline ending each line
		budget   = Omission{Part: PartSchema, Reason: ReasonBudget}
		lowScore = Omission{Part: PartSchema, Reason: ReasonLowRelevance}
	)
	out.Grow(max(0, min(room, maxSchemaPrealloc)))
	for _, i := range b.order {
		// The newline before a line is only paid for when one precedes it.
		left := room - size
		if size > 0 {
			left--
		}
		if f, line, ok := b.fit(i, left); ok {
			if size > 0 {
				out.WriteByte('\n')
				size++
			}
			out.Write(line)
			size += len(line)
			t := &b.tables[i]
			b.sent = append(b.sent, TableRef{
				Schema: t.Schema, Name: t.Name, Score: b.scores[i], Signals: b.signals[i], Form: f,
			})
			continue
		}
		if b.scores[i] > 0 {
			b.addName(&budget, i)
		} else {
			b.addName(&lowScore, i)
		}
	}
	if budget.Count > 0 {
		b.omit(budget)
	}
	if lowScore.Count > 0 {
		b.omit(lowScore)
	}
	if size == 0 {
		return nil
	}
	return []Section{section(PartSchema, out.String())}
}

// fit renders table i in the fullest form that fits in limit bytes. The
// returned line is valid until the next call.
func (b *builder) fit(i int, limit int) (Form, []byte, bool) {
	if limit <= 0 {
		return "", nil, false
	}
	for _, f := range [...]Form{FormFull, FormCompact} {
		line, ok := b.appendTable(b.scratch[:0], i, f == FormCompact, limit)
		b.scratch = line
		if ok {
			return f, line, true
		}
	}
	return "", nil, false
}

func (b *builder) addName(o *Omission, i int) {
	o.Count++
	if len(o.Names) < maxOmittedNames {
		t := &b.tables[i]
		name := t.Name
		if b.qualify(t.Schema) {
			name = t.Schema + "." + t.Name
		}
		o.Names = append(o.Names, name)
	}
}

// appendTable appends table i's lines, without a final newline, and
// reports whether they fit in limit bytes. It stops early once they do
// not. The compact form keeps key, foreign-key, and request-matched
// columns and counts the rest.
func (b *builder) appendTable(dst []byte, i int, compact bool, limit int) ([]byte, bool) {
	t := &b.tables[i]
	fks := b.foreignKeys(i)
	if t.Kind == "view" {
		dst = append(dst, "view "...)
	}
	dst = b.appendName(dst, t.Schema, t.Name)
	dst = append(dst, '(')
	first, hidden := true, 0
	for ci := range t.Columns {
		c := &t.Columns[ci]
		if compact && !c.PrimaryKey && !b.isForeignKeyColumn(fks, c.Name) && !b.words.matchColumn(c.Name) {
			hidden++
			continue
		}
		if !first {
			dst = append(dst, ", "...)
		}
		first = false
		dst = appendIdent(dst, c.Name, b.d)
		if c.Type != "" {
			dst = append(dst, ' ')
			dst = appendClean(dst, c.Type, maxTypeBytes)
		}
		switch {
		case c.PrimaryKey:
			dst = append(dst, " PK"...)
		case !c.Nullable:
			dst = append(dst, " NOT NULL"...)
		}
		if len(dst) > limit {
			return dst, false
		}
	}
	if hidden > 0 {
		if !first {
			dst = append(dst, ", "...)
		}
		dst = append(dst, '+')
		dst = strconv.AppendInt(dst, int64(hidden), 10)
		dst = append(dst, " more"...)
	}
	dst = append(dst, ')')
	for _, k := range fks {
		fk := &b.fks[k]
		dst = append(dst, "\n  FK ("...)
		dst = b.appendIdents(dst, fk.FromColumns)
		dst = append(dst, ") -> "...)
		dst = b.appendName(dst, fk.ToSchema, fk.ToTable)
		dst = append(dst, '(')
		dst = b.appendIdents(dst, fk.ToColumns)
		dst = append(dst, ')')
		if len(dst) > limit {
			return dst, false
		}
	}
	return dst, len(dst) <= limit
}

func (b *builder) isForeignKeyColumn(fks []int, name string) bool {
	for _, k := range fks {
		for _, c := range b.fks[k].FromColumns {
			if c == name {
				return true
			}
		}
	}
	return false
}

func (b *builder) appendIdents(dst []byte, names []string) []byte {
	for i, n := range names {
		if i > 0 {
			dst = append(dst, ", "...)
		}
		dst = appendIdent(dst, n, b.d)
	}
	return dst
}

// appendName appends a table name, qualified when its schema is needed.
func (b *builder) appendName(dst []byte, schemaName, name string) []byte {
	if b.qualify(schemaName) {
		dst = appendIdent(dst, schemaName, b.d)
		dst = append(dst, '.')
	}
	return appendIdent(dst, name, b.d)
}

// qualify reports whether names in schemaName are shown qualified: every
// schema but the home one, so SQL the model writes from the names finds
// the same tables, even when only one schema is in use.
func (b *builder) qualify(schemaName string) bool {
	return schemaName != "" && schemaName != b.home
}

// appendIdent appends s as an identifier: as is when it is a plain name,
// else quoted in the dialect's style with quotes doubled. Characters that
// could restyle or break the prompt (control, line and paragraph
// separators, bidi and other format characters) are dropped, "</" is
// written "< /", and the name is capped at maxNameBytes.
func appendIdent(dst []byte, s string, d sqlscan.Dialect) []byte {
	if plainIdent(s, d) {
		return append(dst, s[:min(len(s), maxNameBytes)]...)
	}
	q := byte('"')
	if d == sqlscan.MySQL {
		q = '`'
	}
	dst = append(dst, q)
	dst = appendSafe(dst, s, maxNameBytes, q)
	return append(dst, q)
}

// plainIdent reports whether s needs no quotes: [A-Za-z_][A-Za-z0-9_$]*,
// and on PostgreSQL no upper case, which an unquoted name would fold away.
func plainIdent(s string, d sqlscan.Dialect) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '_', 'a' <= c && c <= 'z':
		case 'A' <= c && c <= 'Z':
			if d == sqlscan.Postgres {
				return false
			}
		case i > 0 && (c == '$' || '0' <= c && c <= '9'):
		default:
			return false
		}
	}
	return true
}

// appendClean appends a type name made safe like a quoted identifier,
// capped at limit bytes.
func appendClean(dst []byte, s string, limit int) []byte {
	return appendSafe(dst, s, limit, 0)
}

// appendSafe appends s without unsafe characters, with "</" written
// "< /", and with q doubled unless it is 0, up to limit bytes of s's text.
func appendSafe(dst []byte, s string, limit int, q byte) []byte {
	n := 0
	prev := rune(0)
	for _, r := range s {
		if unsafeRune(r) {
			continue
		}
		size := utf8.RuneLen(r)
		if size < 0 {
			r, size = utf8.RuneError, 3
		}
		gap := prev == '<' && r == '/'
		if gap {
			size++
		}
		if n+size > limit {
			break
		}
		n += size
		if gap {
			dst = append(dst, ' ')
		}
		if q != 0 && r == rune(q) {
			dst = append(dst, q)
		}
		dst = utf8.AppendRune(dst, r)
		prev = r
	}
	return dst
}

// unsafeRune reports characters dropped from names and types: controls,
// line and paragraph separators, and format characters such as bidi
// overrides and zero-width joiners.
func unsafeRune(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp, unicode.Cf)
}
