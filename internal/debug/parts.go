package debug

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// Time limits for Parts.
const (
	DefaultPartsTimeout = 60 * time.Second
	DefaultPartTimeout  = 15 * time.Second
)

// PartsOptions bounds Parts.
type PartsOptions struct {
	// Timeout bounds all the counts; DefaultPartsTimeout if zero.
	Timeout time.Duration
	// PartTimeout bounds one count; DefaultPartTimeout if zero.
	PartTimeout time.Duration
}

// Clause names the part of a query a Part counts.
type Clause string

// Clauses, in the order a query is built.
const (
	ClauseCTE    Clause = "cte"    // one WITH query
	ClauseFrom   Clause = "from"   // the first table
	ClauseJoin   Clause = "join"   // the tables so far, with one more joined
	ClauseWhere  Clause = "where"  // the joined tables, filtered
	ClauseGroup  Clause = "group"  // the groups
	ClauseHaving Clause = "having" // the groups that are kept
	ClauseBranch Clause = "branch" // one side of a UNION, INTERSECT, or EXCEPT
	ClauseResult Clause = "result" // the whole query
)

// Part is one step of a query with the rows it gives.
type Part struct {
	Clause Clause `json:"clause"`
	// Label is the part's own SQL, shortened to one line.
	Label string `json:"label"`
	// Start and End are byte offsets of the part in the query.
	Start int `json:"start"`
	End   int `json:"end"`
	// SQL is the count that was run.
	SQL   string  `json:"sql"`
	Rows  int64   `json:"rows"`
	Ran   bool    `json:"ran"`
	Ms    float64 `json:"ms"`
	Error string  `json:"error,omitempty"`

	countAt int    // where "count(*)" starts in SQL, for engine hints
	rowsSQL string // for a count over a derived table: the query it counts
}

// Report is a query's parts, counted.
type Report struct {
	Parts []Part `json:"parts"`
	// Note says how the counts were taken.
	Note string `json:"note"`
	// TimedOut is set when the time limit stopped the counts; parts not
	// reached have Ran unset.
	TimedOut bool `json:"timedOut"`
}

// Parts splits sql, one SELECT, into its parts and counts each one's rows.
// A count that fails is reported on its part; the rest still run.
func Parts(ctx context.Context, c driver.Conn, sql string, opt PartsOptions) (Report, error) {
	d := c.Dialect()
	if _, ok := undoKeywords[d]; !ok {
		return Report{}, ErrUnsupported
	}
	parts, err := split(sql, d)
	if err != nil {
		return Report{}, err
	}
	if opt.Timeout <= 0 {
		opt.Timeout = DefaultPartsTimeout
	}
	if opt.PartTimeout <= 0 {
		opt.PartTimeout = DefaultPartTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()

	rep := Report{Parts: parts}
	for i := range rep.Parts {
		if ctx.Err() != nil {
			rep.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
			break
		}
		countPart(ctx, c, &rep.Parts[i], opt.PartTimeout)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && !rep.Parts[i].Ran {
			rep.Parts[i].Error = fmt.Sprintf("stopped at the overall time limit of %s", opt.Timeout)
		}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		rep.TimedOut = true
	}
	if err := context.Cause(ctx); errors.Is(err, context.Canceled) {
		return rep, fmt.Errorf("debug: %w", err)
	}
	rep.Note = fmt.Sprintf("Each part was counted in a read-only transaction that was rolled back, "+
		"with a limit of %s per part and %s in all.", opt.PartTimeout, opt.Timeout)
	return rep, nil
}

// countPart runs one count on its own session, so a count stopped by its
// time limit cannot leave the next one a broken connection.
func countPart(ctx context.Context, c driver.Conn, p *Part, limit time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	start := time.Now()
	n, err := runCount(ctx, c, p, limit)
	p.Ms = float64(time.Since(start).Microseconds()) / 1000
	switch {
	case err == nil:
		p.Rows, p.Ran = n, true
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		p.Error = fmt.Sprintf("stopped at the time limit of %s", limit)
	default:
		p.Error = err.Error()
	}
}

func runCount(ctx context.Context, c driver.Conn, p *Part, limit time.Duration) (_ int64, err error) {
	s, err := c.Session(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, s.Close()) }()

	sql := p.SQL
	var begin []string
	switch c.Dialect() {
	case driver.Postgres:
		begin = []string{"BEGIN READ ONLY", fmt.Sprintf("SET LOCAL statement_timeout = %d", limit.Milliseconds())}
	case driver.MySQL:
		begin = []string{"START TRANSACTION READ ONLY"}
		// The server stops the count itself, so the connection survives.
		sql = sql[:p.countAt] + fmt.Sprintf("/*+ MAX_EXECUTION_TIME(%d) */ ", limit.Milliseconds()) + sql[p.countAt:]
	case driver.SQLite:
		// No read-only transactions; a count reads only.
	}
	if len(begin) > 0 {
		for _, b := range begin {
			if _, err := s.Exec(ctx, b); err != nil {
				return 0, err
			}
		}
		defer func() {
			rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
			defer cancel()
			if _, rerr := s.Exec(rctx, "ROLLBACK"); rerr != nil && err == nil {
				err = rerr
			}
		}()
	}
	n, err := scalar(ctx, s, sql)
	if err != nil && c.Dialect() == driver.MySQL && p.rowsSQL != "" && strings.Contains(err.Error(), "1060") {
		// A derived table needs unique column names on MySQL, and SELECT *
		// over a join repeats them: count the rows here instead.
		return rowCount(ctx, s, p.rowsSQL)
	}
	return n, err
}

// scalar reads a count from the first column of the first row.
func scalar(ctx context.Context, s driver.Session, sql string) (_ int64, err error) {
	rows, err := s.Query(ctx, sql)
	if err != nil {
		return 0, err
	}
	defer func() {
		if cerr := rows.Close(); err == nil {
			err = cerr
		}
	}()
	vals := make([]any, len(rows.Columns()))
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, err
		}
		return 0, errors.New("debug: the count returned no row")
	}
	if err := rows.Values(vals); err != nil {
		return 0, err
	}
	n, err := count(vals[0])
	if err != nil {
		return 0, err
	}
	for rows.Next() { //nolint:revive // drain so Close reports the statement's error
	}
	return n, rows.Err()
}

// rowCount runs sql and counts its rows without keeping them.
func rowCount(ctx context.Context, s driver.Session, sql string) (_ int64, err error) {
	rows, err := s.Query(ctx, sql)
	if err != nil {
		return 0, err
	}
	defer func() {
		if cerr := rows.Close(); err == nil {
			err = cerr
		}
	}()
	var n int64
	for rows.Next() {
		n++
	}
	return n, rows.Err()
}

// tok is a token with its parenthesis depth; a parenthesis has the depth
// outside it.
type tok struct {
	sqlscan.Token
	depth int
}

func (t tok) end() int { return t.Pos + len(t.Text) }

func (t tok) punct(p string) bool { return t.Kind == sqlscan.Punct && t.Text == p }

func tokens(sql string, d driver.Dialect) []tok {
	sc := sqlscan.NewScanner(sql, sqlscan.Dialect(d))
	var out []tok
	depth := 0
	for {
		t, ok := sc.Next()
		if !ok || t.Kind == sqlscan.Semicolon {
			return out
		}
		tk := tok{Token: t}
		if tk.punct(")") && depth > 0 {
			depth--
		}
		tk.depth = depth
		out = append(out, tk)
		if tk.punct("(") {
			depth++
		}
	}
}

// closing returns the index of the parenthesis that closes toks[open].
func closing(toks []tok, open int) int {
	for k := open + 1; k < len(toks); k++ {
		if toks[k].depth == toks[open].depth && toks[k].punct(")") {
			return k
		}
	}
	return -1
}

// splitter builds the counts of one query.
type splitter struct {
	dialect driver.Dialect
	sql     string
	toks    []tok
	with    string // the WITH clause, ending in a space, or ""
	out     []Part
}

// text is the source of toks[a:b].
func (s *splitter) text(a, b int) string { return s.sql[s.toks[a].Pos:s.toks[b-1].end()] }

// add appends a part over toks[a:b] whose count is
// "<WITH> SELECT count(*) <from>".
func (s *splitter) add(c Clause, a, b int, label, from string) {
	head := s.with + "SELECT "
	s.out = append(s.out, Part{
		Clause: c, Label: trim(label, 120),
		Start: s.toks[a].Pos, End: s.toks[b-1].end(),
		SQL: head + "count(*) " + from, countAt: len(head),
	})
}

// addWrapped appends a part over toks[a:b] that counts the rows of inner,
// a query, through a derived table.
func (s *splitter) addWrapped(c Clause, a, b int, label, inner string) {
	s.add(c, a, b, label, "FROM ("+inner+") AS lumen_part")
	s.out[len(s.out)-1].rowsSQL = s.with + inner
}

// split returns the parts of sql, each with its count but not yet run.
func split(sql string, d driver.Dialect) ([]Part, error) {
	s := &splitter{dialect: d, sql: sql, toks: tokens(sql, d)}
	if len(s.toks) == 0 {
		return nil, ErrEmpty
	}
	i := 0
	if s.toks[0].Is("WITH") {
		var err error
		if i, err = s.ctes(); err != nil {
			return nil, err
		}
	}
	end := len(s.toks)
	if i >= end {
		return nil, ErrStatement
	}
	switch first := s.toks[i]; {
	case first.Is("SELECT"), first.punct("("):
	case first.Is("VALUES"), first.Is("TABLE"):
	default:
		return nil, ErrStatement
	}
	if branches := setOps(s.toks, i, end); len(branches) > 1 {
		for _, b := range branches {
			a, z := b[0], b[1]
			if s.toks[a].punct("(") && closing(s.toks, a) == z-1 {
				a, z = a+1, z-1 // (SELECT ...): count the inside
			}
			s.addWrapped(ClauseBranch, b[0], b[1], s.text(b[0], b[1]), s.text(a, z))
		}
	} else if s.toks[i].Is("SELECT") {
		if err := s.selectParts(i, end); err != nil {
			return nil, err
		}
	}
	s.addWrapped(ClauseResult, i, end, "Result", s.text(i, end))
	return s.out, nil
}

// ctes adds a part per WITH query and returns where the main query starts.
func (s *splitter) ctes() (int, error) {
	toks := s.toks
	bad := fmt.Errorf("%w: could not read the WITH list", ErrStatement)
	j := 1
	if j < len(toks) && toks[j].Is("RECURSIVE") {
		j++
	}
	if j >= len(toks) {
		return 0, bad
	}
	head := s.sql[toks[0].Pos:toks[j].Pos]
	first := j
	for {
		if j >= len(toks) || (toks[j].Kind != sqlscan.Word && toks[j].Kind != sqlscan.QuotedIdent) {
			return 0, bad
		}
		name := j
		j++
		if j < len(toks) && toks[j].punct("(") { // column list
			if j = closing(toks, j); j < 0 {
				return 0, bad
			}
			j++
		}
		if j >= len(toks) || !toks[j].Is("AS") {
			return 0, bad
		}
		j++
		for j < len(toks) && (toks[j].Is("NOT") || toks[j].Is("MATERIALIZED")) {
			j++
		}
		if j >= len(toks) || !toks[j].punct("(") {
			return 0, bad
		}
		cl := closing(toks, j)
		if cl < 0 {
			return 0, bad
		}
		defs := head + s.text(first, cl+1) + " "
		s.out = append(s.out, Part{
			Clause: ClauseCTE, Label: trim(s.text(name, cl+1), 120),
			Start: toks[name].Pos, End: toks[cl].end(),
			SQL:     defs + "SELECT count(*) FROM " + toks[name].Text,
			countAt: len(defs) + len("SELECT "),
		})
		j = cl + 1
		if j < len(toks) && toks[j].punct(",") {
			j++
			continue
		}
		s.with = head + s.text(first, cl+1) + " "
		return j, nil
	}
}

// setOps splits toks[from:end] at top-level UNION, INTERSECT, and EXCEPT
// into [start, end) pairs. A trailing ORDER BY or LIMIT belongs to the
// whole query, so it is left out of the last branch.
func setOps(toks []tok, from, end int) [][2]int {
	var out [][2]int
	start := from
	for k := from; k < end; k++ {
		t := toks[k]
		if t.depth != 0 || !t.Is("UNION") && !t.Is("INTERSECT") && !t.Is("EXCEPT") {
			continue
		}
		out = append(out, [2]int{start, k})
		start = k + 1
		if start < end && (toks[start].Is("ALL") || toks[start].Is("DISTINCT")) {
			start++
		}
	}
	if len(out) == 0 {
		return nil
	}
	last := end
	for k := start; k < end; k++ {
		if toks[k].depth == 0 && (toks[k].Is("ORDER") || toks[k].Is("LIMIT") || toks[k].Is("OFFSET") || toks[k].Is("FETCH")) {
			last = k
			break
		}
	}
	if start >= last {
		return nil
	}
	return append(out, [2]int{start, last})
}

// selectParts adds the FROM, JOIN, WHERE, GROUP BY, and HAVING parts of
// the SELECT in toks[sel:end].
func (s *splitter) selectParts(sel, end int) error {
	toks := s.toks
	at := map[string]int{}
	var order []int // clause starts, in order
	for k := sel + 1; k < end; k++ {
		t := toks[k]
		if t.depth != 0 || t.Kind != sqlscan.Word {
			continue
		}
		kw := t.Upper()
		switch kw {
		case "FROM":
			// a IS [NOT] DISTINCT FROM b
			if k >= 2 && toks[k-1].Is("DISTINCT") && (toks[k-2].Is("IS") || toks[k-2].Is("NOT")) {
				continue
			}
		case "GROUP":
			if toks[k-1].Is("WITHIN") { // WITHIN GROUP (ORDER BY ...)
				continue
			}
		case "INTO":
			return fmt.Errorf("%w: SELECT ... INTO stores the rows, so it is not split", ErrStatement)
		case "OFFSET":
			// Not reserved in MySQL, where it only follows LIMIT.
			if _, limit := at["LIMIT"]; s.dialect == driver.MySQL && !limit {
				continue
			}
		case "WHERE", "HAVING", "WINDOW", "ORDER", "LIMIT", "FETCH":
		default:
			continue
		}
		if _, seen := at[kw]; !seen {
			at[kw] = k
			order = append(order, k)
		}
	}
	// next is where the clause starting at k ends.
	next := func(k int) int {
		for _, o := range order {
			if o > k {
				return o
			}
		}
		return end
	}
	from, ok := at["FROM"]
	if !ok {
		return nil
	}
	fromEnd := next(from)
	if fromEnd <= from+1 {
		return nil
	}
	for _, it := range joinItems(toks, from+1, fromEnd) {
		c := ClauseJoin
		if it[0] == from+1 {
			c = ClauseFrom
		}
		s.add(c, it[0], it[1], s.text(it[0], it[1]), "FROM "+s.text(from+1, it[1]))
	}
	if w, ok := at["WHERE"]; ok && w > from {
		e := next(w)
		s.add(ClauseWhere, w, e, s.text(w, e), s.text(from, e))
	}
	// Groups count over the query's own select list: GROUP BY and HAVING
	// may name its columns by position or alias.
	// The select list may name a window from the WINDOW clause.
	window := ""
	if w, ok := at["WINDOW"]; ok {
		window = " " + s.text(w, next(w))
	}
	if g, ok := at["GROUP"]; ok && g > from {
		e := next(g)
		s.addWrapped(ClauseGroup, g, e, s.text(g, e), s.text(sel, e)+window)
	}
	if h, ok := at["HAVING"]; ok && h > from {
		e := next(h)
		s.addWrapped(ClauseHaving, h, e, s.text(h, e), s.text(sel, e)+window)
	}
	return nil
}

// joinWords start a join.
var joinWords = set("JOIN", "INNER", "LEFT", "RIGHT", "FULL", "CROSS", "NATURAL", "STRAIGHT_JOIN")

// joinItems splits a FROM list toks[from:end] into its tables, as
// [start, end) pairs: one per comma-separated item and per JOIN.
func joinItems(toks []tok, from, end int) [][2]int {
	var starts []int
	starts = append(starts, from)
	for k := from + 1; k < end; k++ {
		t := toks[k]
		if t.depth != toks[from].depth {
			continue
		}
		switch {
		case t.punct(","):
			if k+1 < end {
				starts = append(starts, k+1)
			}
		case t.Kind == sqlscan.Word && joinWords[t.Upper()]:
			prev := toks[k-1]
			if prev.Kind == sqlscan.Word && (joinWords[prev.Upper()] || prev.Is("OUTER")) && !prev.Is("JOIN") {
				continue // LEFT [OUTER] JOIN: the join starts at LEFT
			}
			if k+1 < end && toks[k+1].punct("(") && !t.Is("JOIN") {
				continue // LEFT(...) is a function
			}
			starts = append(starts, k)
		}
	}
	out := make([][2]int, len(starts))
	for i, st := range starts {
		e := end
		if i+1 < len(starts) {
			e = starts[i+1]
			if toks[e-1].punct(",") {
				e--
			}
		}
		out[i] = [2]int{st, e}
	}
	return out
}

// rollbackTimeout bounds a ROLLBACK after the work's own time is up.
const rollbackTimeout = 10 * time.Second
