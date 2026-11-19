// Package lint finds SQL that is likely wrong, dangerous, or slow, from the
// shared syntax tree of package sqlparse, so each rule is written once for
// every dialect. docs/lint-rules.md lists the rules.
package lint

import (
	"slices"
	"strconv"
	"strings"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/sqlparse"
	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// Severity is how much a finding matters.
type Severity string

// Severities.
const (
	Error   Severity = "error"   // likely a bug, or dangerous
	Warning Severity = "warning" // slow or fragile
	Info    Severity = "info"    // worth knowing
)

// Finding is one rule match at byte offsets [Start, End) of the SQL.
type Finding struct {
	Rule     string
	Severity Severity
	Message  string
	Start    int
	End      int
}

// Context is what rules know besides the SQL.
type Context struct {
	Dialect driver.Dialect
	// Prod marks a production connection.
	Prod bool
}

// MaxFindings caps the findings of one check.
const MaxFindings = 100

// DeepOffset is the OFFSET above which L105 warns.
const DeepOffset = 10_000

// Check runs every rule on src. Statements that do not parse are skipped;
// sqlparse.Check reports their syntax errors.
func Check(src string, ctx Context) []Finding {
	stmts, err := sqlparse.Parse(src, ctx.Dialect)
	if err != nil {
		return nil
	}
	l := &linter{src: src, dialect: sqlscan.Dialect(ctx.Dialect), ctx: ctx}
	for i := range stmts {
		l.stmt(&stmts[i])
	}
	slices.SortStableFunc(l.out, func(a, b Finding) int { return a.Start - b.Start })
	if len(l.out) > MaxFindings {
		l.out = l.out[:MaxFindings]
	}
	return l.out
}

type linter struct {
	src     string
	dialect sqlscan.Dialect
	ctx     Context
	out     []Finding
}

func (l *linter) add(rule string, sev Severity, start, end int, msg string) {
	l.out = append(l.out, Finding{Rule: rule, Severity: sev, Message: msg, Start: start, End: end})
}

// token is the span of the token at p.
func (l *linter) token(p int) (int, int) {
	sc := sqlscan.NewScanner(l.src[p:], l.dialect)
	if t, ok := sc.Next(); ok {
		return p + t.Pos, p + t.Pos + len(t.Text)
	}
	return p, p
}

// word finds the first token from p, before limit, with the given text.
func (l *linter) word(p, limit int, words ...string) (int, int, bool) {
	sc := sqlscan.NewScanner(l.src[p:limit], l.dialect)
	for {
		t, ok := sc.Next()
		if !ok {
			return 0, 0, false
		}
		if slices.ContainsFunc(words, t.Is) {
			return p + t.Pos, p + t.Pos + len(t.Text), true
		}
	}
}

func (l *linter) stmt(st *sqlparse.Stmt) {
	verbEnd := st.Start + len(st.Verb)
	switch st.Kind {
	case sqlparse.StmtUpdate, sqlparse.StmtDelete:
		if st.Where == nil {
			l.add("L001", Error, st.Start, verbEnd,
				st.Verb+" without WHERE changes every row in the table. Add a WHERE clause, or WHERE true if you mean it.")
		}
	case sqlparse.StmtInsert:
		if !st.Columns && (st.Select != nil || len(st.Exprs) > 0) {
			l.add("L106", Warning, st.Start, verbEnd,
				"INSERT without a column list breaks when columns are added or reordered. Name the columns.")
		}
	case sqlparse.StmtDDL:
		if l.ctx.Prod && (st.Verb == "DROP" || st.Verb == "ALTER" || st.Verb == "TRUNCATE") {
			l.add("L201", Info, st.Start, verbEnd,
				st.Verb+" on a production connection. It will ask for confirmation and be recorded in the audit log.")
		}
	}

	l.exprRules(st.Where)
	for _, e := range st.Exprs {
		l.exprRules(e)
	}
	exists := existsSelects(st)
	st.Selects(func(s *sqlparse.Select) {
		l.selectRules(st, s, exists[s])
	})
}

func (l *linter) selectRules(st *sqlparse.Stmt, s *sqlparse.Select, inExists bool) {
	// EXISTS (SELECT * ...) reads no columns.
	if !inExists {
		for _, it := range s.Items {
			if it.Star {
				start, end := l.token(it.Pos)
				l.add("L101", Warning, start, end,
					"SELECT * reads every column, and the result changes when columns are added. Name the columns you need.")
			}
		}
	}
	if len(s.From) > 1 && s.Where == nil {
		start, end, ok := l.word(s.Pos, st.End, "FROM")
		if !ok {
			start, end = l.token(s.Pos)
		}
		l.add("L004", Error, start, end,
			"Tables joined with a comma and no WHERE make a cross join: every row with every row. Add a join condition, or write CROSS JOIN if you mean it.")
	}
	for _, e := range s.OrderBy {
		if f, ok := e.(*sqlparse.Func); ok && (f.Name == "rand" || f.Name == "random") {
			start, end := l.token(f.Pos)
			l.add("L104", Warning, start, end,
				"ORDER BY "+strings.ToUpper(f.Name)+"() sorts the whole table to pick rows. On a large table, pick ids at random instead.")
		}
	}
	if o := s.Offset; o != nil {
		if n, err := strconv.Atoi(o.Value); err == nil && n > DeepOffset {
			start, end := l.token(o.Pos)
			l.add("L105", Warning, start, end,
				"OFFSET "+o.Value+" reads and throws away "+o.Value+" rows. Page by the last key seen instead (WHERE id > ...).")
		}
	}
	l.exprRules(s.Where)
	l.indexRules(s.Where)
	for _, f := range s.From {
		for _, c := range f.Conds {
			l.exprRules(c)
		}
	}
	for _, e := range s.OrderBy {
		l.exprRules(e)
	}
	for _, e := range s.Others {
		l.exprRules(e)
	}
	for _, it := range s.Items {
		l.exprRules(it.Expr)
	}
}

// exprRules checks rules that apply to any expression.
func (l *linter) exprRules(e sqlparse.Expr) {
	sqlparse.WalkExpr(e, func(x sqlparse.Expr) {
		switch x := x.(type) {
		case *sqlparse.Binary:
			if x.Op != "=" && x.Op != "<>" && x.Op != "!=" {
				return
			}
			for _, side := range []sqlparse.Expr{x.L, x.R} {
				if lit, ok := side.(*sqlparse.Literal); ok && lit.Kind == sqlparse.LitNull {
					start, end := l.token(lit.Pos)
					fix := "IS NULL"
					if x.Op != "=" {
						fix = "IS NOT NULL"
					}
					l.add("L002", Error, start, end,
						x.Op+" NULL is never true: comparing with NULL gives NULL. Use "+fix+".")
				}
			}
		case *sqlparse.In:
			if x.Not && x.Subquery != nil {
				start, end, ok := l.word(x.Pos, len(l.src), "IN")
				if notStart, _, okNot := l.word(x.Pos, start, "NOT"); ok && okNot {
					start = notStart
				}
				if !ok {
					start, end = l.token(x.Pos)
				}
				l.add("L003", Warning, start, end,
					"NOT IN (subquery) returns no rows at all if the subquery returns a NULL. Use NOT EXISTS, or make sure the column is NOT NULL.")
			}
		}
	})
}

// indexRules checks a WHERE condition for patterns that keep an index from
// being used.
func (l *linter) indexRules(where sqlparse.Expr) {
	sqlparse.WalkExpr(where, func(x sqlparse.Expr) {
		var operands []sqlparse.Expr
		switch x := x.(type) {
		case *sqlparse.Binary:
			switch x.Op {
			case "=", "<>", "!=", "<", ">", "<=", ">=":
				operands = []sqlparse.Expr{x.L, x.R}
			}
		case *sqlparse.Like:
			operands = []sqlparse.Expr{x.Expr}
			if lit, ok := x.Pattern.(*sqlparse.Literal); ok && lit.Kind == sqlparse.LitString && strings.HasPrefix(lit.Value, "%") {
				start, end := l.token(lit.Pos)
				l.add("L102", Warning, start, end,
					"A pattern that starts with % cannot use an index, so every row is read. Anchor the start, or use full-text search.")
			}
		case *sqlparse.In:
			operands = []sqlparse.Expr{x.Expr}
		}
		for _, op := range operands {
			if f, ok := op.(*sqlparse.Func); ok && hasColumnArg(f) {
				start, end := l.token(f.Pos)
				l.add("L103", Warning, start, end,
					"A function around a column in WHERE keeps an index on that column from being used. Compare the column itself, or index the expression.")
			}
		}
	})
}

func hasColumnArg(f *sqlparse.Func) bool {
	return slices.ContainsFunc(f.Args, func(a sqlparse.Expr) bool {
		_, ok := a.(*sqlparse.Column)
		return ok
	})
}

// existsSelects are the SELECT blocks used as EXISTS (...) subqueries.
func existsSelects(st *sqlparse.Stmt) map[*sqlparse.Select]bool {
	out := map[*sqlparse.Select]bool{}
	mark := func(e sqlparse.Expr) {
		sqlparse.WalkExpr(e, func(x sqlparse.Expr) {
			if q, ok := x.(*sqlparse.Subquery); ok && q.Exists && q.Select != nil {
				out[q.Select] = true
			}
		})
	}
	mark(st.Where)
	for _, e := range st.Exprs {
		mark(e)
	}
	st.Selects(func(s *sqlparse.Select) {
		mark(s.Where)
		for _, it := range s.Items {
			mark(it.Expr)
		}
		for _, e := range s.Others {
			mark(e)
		}
		for _, f := range s.From {
			for _, c := range f.Conds {
				mark(c)
			}
		}
	})
	return out
}
