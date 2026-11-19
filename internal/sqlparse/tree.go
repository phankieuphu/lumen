package sqlparse

import (
	"errors"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// The shared syntax tree: what lint and debug need from a statement, the
// same for every dialect. Each dialect's adapter builds it from that
// dialect's own parser. Positions are byte offsets in the parsed SQL.

// StmtKind is what a statement does.
type StmtKind int

// Statement kinds.
const (
	StmtOther StmtKind = iota
	StmtSelect
	StmtInsert
	StmtUpdate
	StmtDelete
	StmtDDL
)

// Stmt is one parsed statement.
type Stmt struct {
	Kind StmtKind
	// Start and End are the statement's offsets, as sqlscan.Split gives them.
	Start, End int
	// Verb is the leading keyword, upper-cased: SELECT, UPDATE, DROP, ...
	Verb string
	// Select is the query of a SELECT, or of an INSERT ... SELECT.
	Select *Select
	// Where is the condition of an UPDATE or DELETE; nil when there is none.
	Where Expr
	// Columns reports whether an INSERT names its columns.
	Columns bool
	// Exprs holds other expressions of the statement (SET values, VALUES
	// rows), so walkers reach every subquery.
	Exprs []Expr
}

// Select is one SELECT block; set operations (UNION, ...) list the others in
// Set.
type Select struct {
	Pos   int
	Items []SelectItem
	// From has one entry per comma-separated FROM item: two or more mean a
	// comma join. Joins written with JOIN are one item.
	From  []FromItem
	Where Expr
	// OrderBy, Having, and GroupBy expressions.
	OrderBy []Expr
	Others  []Expr
	// Offset is the OFFSET value when it is a literal number.
	Offset *Literal
	Set    []*Select
}

// SelectItem is one entry of the select list.
type SelectItem struct {
	Pos int
	// Star is set for * and t.*.
	Star bool
	Expr Expr
}

// FromItem is one comma-separated FROM entry.
type FromItem struct {
	Pos int
	// Subqueries are derived tables and subqueries in join conditions.
	Subqueries []*Select
	Conds      []Expr
}

// Expr is an expression node.
type Expr interface{ Position() int }

// Column is a column reference.
type Column struct {
	Pos  int
	Name string
}

// LiteralKind is the kind of a constant.
type LiteralKind int

// Literal kinds.
const (
	LitOther LiteralKind = iota
	LitNull
	LitString
	LitNumber
)

// Literal is a constant.
type Literal struct {
	Pos   int
	Kind  LiteralKind
	Value string
}

// Func is a function call; Name is lower-case without a schema.
type Func struct {
	Pos  int
	Name string
	Args []Expr
}

// Binary is a binary operator: = <> != < > <= >= AND OR, or another.
type Binary struct {
	Pos  int
	Op   string
	L, R Expr
}

// Like is [NOT] LIKE or ILIKE.
type Like struct {
	Pos     int
	Not     bool
	Expr    Expr
	Pattern Expr
}

// In is [NOT] IN, with a list or a subquery.
type In struct {
	Pos      int
	Not      bool
	Expr     Expr
	List     []Expr
	Subquery *Select
}

// Subquery is a scalar subquery, or EXISTS (...).
type Subquery struct {
	Pos    int
	Exists bool
	Select *Select
}

// Other is any other expression, with its children so walkers go on.
type Other struct {
	Pos      int
	Children []Expr
}

// Position implements Expr.
func (e *Column) Position() int { return e.Pos }

// Position implements Expr.
func (e *Literal) Position() int { return e.Pos }

// Position implements Expr.
func (e *Func) Position() int { return e.Pos }

// Position implements Expr.
func (e *Binary) Position() int { return e.Pos }

// Position implements Expr.
func (e *Like) Position() int { return e.Pos }

// Position implements Expr.
func (e *In) Position() int { return e.Pos }

// Position implements Expr.
func (e *Subquery) Position() int { return e.Pos }

// Position implements Expr.
func (e *Other) Position() int { return e.Pos }

// WalkExpr calls fn for e and every expression under it, depth first. It
// does not enter subqueries: use Selects for those.
func WalkExpr(e Expr, fn func(Expr)) {
	if e == nil {
		return
	}
	fn(e)
	switch e := e.(type) {
	case *Func:
		for _, a := range e.Args {
			WalkExpr(a, fn)
		}
	case *Binary:
		WalkExpr(e.L, fn)
		WalkExpr(e.R, fn)
	case *Like:
		WalkExpr(e.Expr, fn)
		WalkExpr(e.Pattern, fn)
	case *In:
		WalkExpr(e.Expr, fn)
		for _, x := range e.List {
			WalkExpr(x, fn)
		}
	case *Other:
		for _, c := range e.Children {
			WalkExpr(c, fn)
		}
	}
}

// Selects calls fn for every SELECT block of st: its query, set operation
// branches, and subqueries anywhere, outermost first.
func (st *Stmt) Selects(fn func(*Select)) {
	var visitSel func(*Select)
	visitExpr := func(e Expr) {
		WalkExpr(e, func(x Expr) {
			switch x := x.(type) {
			case *In:
				if x.Subquery != nil {
					visitSel(x.Subquery)
				}
			case *Subquery:
				visitSel(x.Select)
			}
		})
	}
	visitSel = func(s *Select) {
		if s == nil {
			return
		}
		fn(s)
		for _, it := range s.Items {
			visitExpr(it.Expr)
		}
		for _, f := range s.From {
			for _, q := range f.Subqueries {
				visitSel(q)
			}
			for _, c := range f.Conds {
				visitExpr(c)
			}
		}
		visitExpr(s.Where)
		for _, e := range s.OrderBy {
			visitExpr(e)
		}
		for _, e := range s.Others {
			visitExpr(e)
		}
		for _, b := range s.Set {
			visitSel(b)
		}
	}
	visitSel(st.Select)
	visitExpr(st.Where)
	for _, e := range st.Exprs {
		visitExpr(e)
	}
}

// ddlVerbs are leading keywords of statements that change the schema.
var ddlVerbs = map[string]bool{
	"CREATE": true, "ALTER": true, "DROP": true, "TRUNCATE": true, "RENAME": true, "COMMENT": true,
}

// ErrNoAdapter is returned for a dialect without a syntax tree adapter.
var ErrNoAdapter = errors.New("sqlparse: no syntax tree for this dialect")

// Parse builds the shared syntax tree of each statement in src. Statements
// the dialect's parser rejects are left out: Check reports them. SQLite is
// read with the PostgreSQL parser, which accepts most of what SQLite users
// write; what it cannot read is left out too.
func Parse(src string, d driver.Dialect) ([]Stmt, error) {
	if len(src) > MaxBytes {
		return nil, nil
	}
	var parse func(stmt string, offset int) (*Stmt, bool)
	scan := sqlscan.Dialect(d)
	switch d {
	case driver.Postgres, driver.SQLite:
		if !postgresTrees {
			return nil, ErrNoAdapter
		}
		parse = parsePostgres
	case driver.MySQL:
		parse = parseMySQL
	default:
		return nil, ErrNoAdapter
	}
	stmts := sqlscan.Split(src, scan)
	out := make([]Stmt, 0, len(stmts))
	for _, st := range stmts {
		t, ok := parse(st.Text, st.Start)
		if !ok {
			continue
		}
		t.Start, t.End = st.Start, st.End
		t.Verb = st.Tokens[0].Upper()
		if t.Kind == StmtOther && ddlVerbs[t.Verb] {
			t.Kind = StmtDDL
		}
		out = append(out, *t)
	}
	return out, nil
}
