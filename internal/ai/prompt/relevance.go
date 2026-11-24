package prompt

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/schema"
	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// Points per signal. ColumnMatch scores per column, up to maxColumnPoints.
const (
	cursorStatementPoints = 100
	otherStatementPoints  = 40
	cursorPrefixPoints    = 50
	nameMatchPoints       = 60
	namePartPoints        = 25
	columnPoints          = 10
	maxColumnPoints       = 30
	errorPoints           = 60
	neighbourPoints       = 20
	// neighbourFrom is the first-pass score that makes a table's foreign-key
	// neighbours relevant too.
	neighbourFrom = 60
)

// Request words: at least minWordRunes long, at most maxWords of them.
const (
	minWordRunes = 3
	maxWords     = 64
)

// minPrefixRunes is the shortest word at the cursor matched as a prefix.
const minPrefixRunes = 2

// builder holds one Build call's working state. Nothing outlives the call.
type builder struct {
	in     Input
	d      sqlscan.Dialect
	tables []schema.Table
	fks    []schema.ForeignKey

	// lower holds lower-cased table names; byName maps one to its first
	// table index and next chains tables sharing it, -1 ending the chain.
	lower  []string
	byName map[string]int
	next   []int
	// fkFrom and fkTo are each foreign key's table indexes, -1 if absent;
	// fkOrder lists keys sorted by source table, then name.
	fkFrom, fkTo []int
	fkOrder      []int

	signals []Signal
	scores  []int
	order   []int // table indexes, most relevant first

	words words
	ed    editorView
	// home is the schema unqualified names resolve to; "" when unknown,
	// and then every name is shown qualified.
	home string

	sent    []TableRef
	omitted []Omission
	scratch []byte
}

func newBuilder(in Input, dialect driver.Dialect) *builder {
	d := sqlscan.Dialect(dialect)
	n := len(in.Schema.Tables)
	b := &builder{
		in:      in,
		d:       d,
		tables:  in.Schema.Tables,
		fks:     in.Schema.ForeignKeys,
		lower:   make([]string, n),
		byName:  make(map[string]int, n),
		next:    make([]int, n),
		signals: make([]Signal, n),
		scores:  make([]int, n),
		order:   make([]int, n),
	}
	for i := range b.tables {
		t := &b.tables[i]
		b.lower[i] = strings.ToLower(t.Name)
		b.next[i] = -1
		if head, ok := b.byName[b.lower[i]]; ok {
			b.next[i] = head
		}
		b.byName[b.lower[i]] = i
		b.order[i] = i
	}
	b.home = homeSchema(in.DefaultSchema, d)
	b.indexForeignKeys()
	b.ed = newEditor(in.Editor, in.Cursor, d)
	b.score()
	return b
}

// find returns the first table named name, ignoring case, or -1.
func (b *builder) find(name string) int {
	if i, ok := b.byName[strings.ToLower(name)]; ok {
		return i
	}
	return -1
}

// findExact returns the table schema.name, or -1.
func (b *builder) findExact(schemaName, name string) int {
	for i := b.find(name); i >= 0; i = b.next[i] {
		if t := &b.tables[i]; t.Name == name && t.Schema == schemaName {
			return i
		}
	}
	return -1
}

func (b *builder) indexForeignKeys() {
	b.fkFrom = make([]int, len(b.fks))
	b.fkTo = make([]int, len(b.fks))
	b.fkOrder = make([]int, 0, len(b.fks))
	for k := range b.fks {
		fk := &b.fks[k]
		b.fkFrom[k] = b.findExact(fk.FromSchema, fk.FromTable)
		b.fkTo[k] = b.findExact(fk.ToSchema, fk.ToTable)
		if b.fkFrom[k] >= 0 {
			b.fkOrder = append(b.fkOrder, k)
		}
	}
	slices.SortFunc(b.fkOrder, func(x, y int) int {
		if c := cmp.Compare(b.fkFrom[x], b.fkFrom[y]); c != 0 {
			return c
		}
		fx, fy := &b.fks[x], &b.fks[y]
		return cmp.Or(
			strings.Compare(fx.Name, fy.Name),
			strings.Compare(fx.ToSchema, fy.ToSchema),
			strings.Compare(fx.ToTable, fy.ToTable),
			slices.Compare(fx.FromColumns, fy.FromColumns),
			slices.Compare(fx.ToColumns, fy.ToColumns),
		)
	})
}

// foreignKeys returns the indexes of the foreign keys held by table i, in
// render order.
func (b *builder) foreignKeys(i int) []int {
	lo, _ := slices.BinarySearchFunc(b.fkOrder, i, func(k, t int) int { return cmp.Compare(b.fkFrom[k], t) })
	hi := lo
	for hi < len(b.fkOrder) && b.fkFrom[b.fkOrder[hi]] == i {
		hi++
	}
	return b.fkOrder[lo:hi]
}

// score sets every table's signals and score, then ranks them.
func (b *builder) score() {
	e := &b.ed
	for i := range e.stmts {
		sig := InOtherStatement
		if i == e.at {
			sig = InCursorStatement
		}
		b.markStatement(&e.stmts[i], sig)
	}
	if b.in.Task == Inline {
		if w := strings.ToLower(e.word()); utf8.RuneCountInString(w) >= minPrefixRunes {
			for i, name := range b.lower {
				if strings.HasPrefix(name, w) {
					b.signals[i] |= CursorPrefix
				}
			}
		}
	}
	b.words = newWords(b.in.Request)
	if !b.words.empty() {
		b.matchRequest()
	}
	if b.in.Error != "" {
		b.matchError(b.in.Error)
	}

	for i, s := range b.signals {
		b.scores[i] += points(s)
	}
	for k := range b.fks {
		f, t := b.fkFrom[k], b.fkTo[k]
		if f < 0 || t < 0 || f == t {
			continue
		}
		if b.scores[f] >= neighbourFrom {
			b.signals[t] |= Neighbour
		}
		if b.scores[t] >= neighbourFrom {
			b.signals[f] |= Neighbour
		}
	}
	for i, s := range b.signals {
		if s&Neighbour != 0 {
			b.scores[i] += neighbourPoints
		}
	}

	slices.SortFunc(b.order, func(x, y int) int {
		tx, ty := &b.tables[x], &b.tables[y]
		return cmp.Or(
			cmp.Compare(b.scores[y], b.scores[x]),
			strings.Compare(tx.Schema, ty.Schema),
			strings.Compare(tx.Name, ty.Name),
			strings.Compare(tx.Kind, ty.Kind),
			cmp.Compare(len(tx.Columns), len(ty.Columns)),
		)
	})
}

// points sums the fixed points of a signal set; column and neighbour
// points are added separately.
func points(s Signal) int {
	p := 0
	for _, sp := range [...]struct {
		s Signal
		p int
	}{
		{InCursorStatement, cursorStatementPoints},
		{InOtherStatement, otherStatementPoints},
		{CursorPrefix, cursorPrefixPoints},
		{NameMatch, nameMatchPoints},
		{NamePart, namePartPoints},
		{InError, errorPoints},
	} {
		if s&sp.s != 0 {
			p += sp.p
		}
	}
	return p
}

// markStatement marks the tables a statement names. Names of the
// statement's own WITH queries match nothing.
func (b *builder) markStatement(st *sqlscan.Statement, sig Signal) {
	_, refs := sqlscan.Describe(st.Text, b.d)
	if len(refs) == 0 {
		return
	}
	ctes := cteNames(st.Tokens)
	for _, ref := range refs {
		if !strings.Contains(ref, ".") && slices.ContainsFunc(ctes, func(c string) bool { return strings.EqualFold(c, ref) }) {
			continue
		}
		b.mark(ref, sig)
	}
}

// mark sets sig on the tables ref names. A qualified ref matches only its
// schema. An unqualified one matches the table in the home schema, as the
// database resolves it; with none there, or no known home, the name in any
// schema.
func (b *builder) mark(ref string, sig Signal) {
	schemaName, name := splitRef(ref)
	if schemaName == "" && b.home != "" {
		for i := b.find(name); i >= 0; i = b.next[i] {
			if strings.EqualFold(b.tables[i].Name, name) && b.tables[i].Schema == b.home {
				b.signals[i] |= sig
				return
			}
		}
	}
	for i := b.find(name); i >= 0; i = b.next[i] {
		if schemaName == "" || strings.EqualFold(b.tables[i].Schema, schemaName) {
			b.signals[i] |= sig
		}
	}
}

// homeSchema is the schema unqualified names resolve to: def if set, else
// public on PostgreSQL and main on SQLite. MySQL has no fixed default, so
// without def it is "" and every name is qualified.
func homeSchema(def string, d sqlscan.Dialect) string {
	switch {
	case def != "":
		return def
	case d == sqlscan.Postgres:
		return "public"
	case d == sqlscan.SQLite:
		return "main"
	}
	return ""
}

// splitRef splits db.schema.table or schema.table at its last dot.
func splitRef(ref string) (schemaName, name string) {
	i := strings.LastIndexByte(ref, '.')
	if i < 0 {
		return "", ref
	}
	schemaName, name = ref[:i], ref[i+1:]
	if j := strings.LastIndexByte(schemaName, '.'); j >= 0 {
		schemaName = schemaName[j+1:]
	}
	return schemaName, name
}

// cteNames returns the names a statement gives its WITH queries:
// WITH [RECURSIVE] name [(columns)] AS [[NOT] MATERIALIZED] (...), ...
func cteNames(toks []sqlscan.Token) []string {
	var out []string
	isPunct := func(j int, p string) bool {
		return j < len(toks) && toks[j].Kind == sqlscan.Punct && toks[j].Text == p
	}
	for i := range toks {
		if !toks[i].Is("WITH") {
			continue
		}
		j := i + 1
		if j < len(toks) && toks[j].Is("RECURSIVE") {
			j++
		}
		for j < len(toks) && (toks[j].Kind == sqlscan.Word || toks[j].Kind == sqlscan.QuotedIdent) {
			name := unquoteIdent(toks[j].Text)
			j++
			if isPunct(j, "(") {
				j = skipParens(toks, j)
			}
			if j >= len(toks) || !toks[j].Is("AS") {
				break
			}
			j++
			if j < len(toks) && toks[j].Is("NOT") {
				j++
			}
			if j < len(toks) && toks[j].Is("MATERIALIZED") {
				j++
			}
			if !isPunct(j, "(") {
				break
			}
			out = append(out, name)
			j = skipParens(toks, j)
			if !isPunct(j, ",") {
				break
			}
			j++
		}
	}
	return out
}

// skipParens returns the index after the parenthesis closing toks[j].
func skipParens(toks []sqlscan.Token, j int) int {
	depth := 0
	for ; j < len(toks); j++ {
		if toks[j].Kind != sqlscan.Punct {
			continue
		}
		switch toks[j].Text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return j + 1
			}
		}
	}
	return j
}

// unquoteIdent strips identifier quotes and undoubles quotes inside.
func unquoteIdent(s string) string {
	if len(s) < 2 {
		return s
	}
	switch q := s[0]; q {
	case '"', '`':
		return strings.ReplaceAll(s[1:len(s)-1], string([]byte{q, q}), string([]byte{q}))
	case '[':
		return s[1 : len(s)-1]
	}
	return s
}

// matchRequest scores tables whose name, name part, or columns are words
// of the request.
func (b *builder) matchRequest() {
	w := &b.words
	for i, name := range b.lower {
		if w.matchAny(name) {
			b.signals[i] |= NameMatch
		} else if strings.IndexByte(name, '_') >= 0 {
			for part := range strings.SplitSeq(name, "_") {
				if part != "" && w.matchAny(part) {
					b.signals[i] |= NamePart
					break
				}
			}
		}
		pts := 0
		cols := b.tables[i].Columns
		for ci := range cols {
			if w.matchColumn(cols[ci].Name) {
				b.signals[i] |= ColumnMatch
				if pts += columnPoints; pts >= maxColumnPoints {
					break
				}
			}
		}
		b.scores[i] += pts
	}
}

// matchError marks tables the error names, bare or quoted. For a dotted
// name, the part before the last dot also counts, as in table.column.
func (b *builder) matchError(s string) {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !isIdentRune(r) {
			i += size
			continue
		}
		j := i
		for j < len(s) {
			r, size := utf8.DecodeRuneInString(s[j:])
			if !isIdentRune(r) && r != '.' {
				break
			}
			j += size
		}
		b.markErrorRef(strings.Trim(s[i:j], "."))
		i = j
	}
	for _, sp := range errorSpans(s) {
		if sp.kind == spanQuote {
			b.markErrorRef(s[sp.open+1 : sp.close])
		}
	}
}

func (b *builder) markErrorRef(ref string) {
	if ref == "" {
		return
	}
	b.mark(ref, InError)
	if i := strings.LastIndexByte(ref, '.'); i > 0 {
		_, owner := splitRef(ref[:i])
		b.mark(owner, InError)
	}
}

func isIdentRune(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// form is a word reduced for matching: base, plus a trailing "y" when y is
// set, so "category" and the "categor"+"y" of "categories" compare equal
// without building a new string.
type form struct {
	base string
	y    bool
}

func canon(w string) form {
	if strings.HasSuffix(w, "y") {
		return form{w[:len(w)-1], true}
	}
	return form{w, false}
}

// eachForm calls f with w and its simple singulars (-ies to -y, -es, -s)
// until f returns true, and reports whether it did.
func eachForm(w string, f func(form) bool) bool {
	n := len(w)
	switch {
	case f(canon(w)):
		return true
	case n > 3 && strings.HasSuffix(w, "ies") && f(form{w[:n-3], true}):
		return true
	case n > 2 && strings.HasSuffix(w, "es") && f(canon(w[:n-2])):
		return true
	case n > 1 && w[n-1] == 's' && !strings.HasSuffix(w, "ss") && f(canon(w[:n-1])):
		return true
	}
	return false
}

// words is the set of request words and their singular forms. lens and
// first are cheap filters checked before the map, which keeps the
// per-column check fast on wide schemas.
type words struct {
	set   map[form]struct{}
	lens  uint64    // bit min(len(base), 63)
	first [4]uint64 // bit base[0]
}

func newWords(request string) words {
	var w words
	if request == "" {
		return w
	}
	lower := strings.ToLower(request)
	count := 0
	for i := 0; i < len(lower) && count < maxWords; {
		r, size := utf8.DecodeRuneInString(lower[i:])
		if !isWordRune(r) {
			i += size
			continue
		}
		j, runes := i, 0
		for j < len(lower) {
			r, size := utf8.DecodeRuneInString(lower[j:])
			if !isWordRune(r) {
				break
			}
			j += size
			runes++
		}
		if runes >= minWordRunes {
			if w.set == nil {
				w.set = make(map[form]struct{}, 4*maxWords)
			}
			eachForm(lower[i:j], func(f form) bool {
				if f.base != "" {
					w.set[f] = struct{}{}
					w.lens |= 1 << min(len(f.base), 63)
					w.first[f.base[0]/64] |= 1 << (f.base[0] % 64)
				}
				return false
			})
			count++
		}
		i = j
	}
	return w
}

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func (w *words) empty() bool { return len(w.set) == 0 }

func (w *words) has(f form) bool {
	if f.base == "" || w.lens&(1<<min(len(f.base), 63)) == 0 || w.first[f.base[0]/64]&(1<<(f.base[0]%64)) == 0 {
		return false
	}
	_, ok := w.set[f]
	return ok
}

// matchAny reports whether a lower-cased name, or a singular of it, is a
// request word or a singular of one.
func (w *words) matchAny(name string) bool { return eachForm(name, w.has) }

// matchColumn reports whether a column name is a request word or a
// singular of one. Only the word side is reduced, which keeps the check to
// one lookup per column.
func (w *words) matchColumn(name string) bool {
	if w.empty() || name == "" {
		return false
	}
	c := name[0]
	if 'A' <= c && c <= 'Z' {
		c += 'a' - 'A'
	}
	if w.first[c/64]&(1<<(c%64)) == 0 {
		return false
	}
	return w.has(canon(strings.ToLower(name)))
}
