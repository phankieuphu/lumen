package sqlparse

import (
	"strconv"
	"strings"

	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/opcode"
	"github.com/pingcap/tidb/pkg/parser/test_driver"

	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// parseMySQL builds the shared tree of one statement from the TiDB parser's
// tree. offset is where stmt starts in the whole SQL.
//
// TiDB records where expressions and select fields start, but not where
// nested SELECTs, tables, or LIMIT values do; those are found by scanning
// the statement's tokens from the nearest known position.
func parseMySQL(stmt string, offset int) (*Stmt, bool) {
	if skipMySQL(stmt) {
		return nil, false
	}
	node, err := parser.New().ParseOneStmt(stmt, "", "")
	if err != nil {
		return nil, false
	}
	c := myConv{src: stmt, off: offset}
	return c.stmt(node), true
}

type myConv struct {
	src string // the statement
	off int    // where it starts in the whole SQL
}

func (c myConv) stmt(n ast.StmtNode) *Stmt {
	switch n := n.(type) {
	case *ast.SelectStmt, *ast.SetOprStmt:
		return &Stmt{Kind: StmtSelect, Select: c.result(n, 0)}
	case *ast.InsertStmt:
		st := &Stmt{Kind: StmtInsert, Columns: len(n.Columns) > 0 || n.Setlist}
		if n.Select != nil {
			st.Select = c.result(n.Select, 0)
		}
		for _, row := range n.Lists {
			for _, e := range row {
				st.Exprs = append(st.Exprs, c.expr(e))
			}
		}
		return st
	case *ast.UpdateStmt:
		st := &Stmt{Kind: StmtUpdate, Where: c.expr(n.Where)}
		for _, a := range n.List {
			st.Exprs = append(st.Exprs, c.expr(a.Expr))
		}
		return st
	case *ast.DeleteStmt:
		return &Stmt{Kind: StmtDelete, Where: c.expr(n.Where)}
	case *ast.ExplainStmt:
		return c.stmt(n.Stmt)
	default:
		return &Stmt{Kind: StmtOther}
	}
}

// result converts a query; at is where its text starts in the statement
// (its SELECT keyword, or the "(" before it).
func (c myConv) result(n ast.Node, at int) *Select {
	switch n := n.(type) {
	case *ast.SelectStmt:
		return c.sel(n, at)
	case *ast.SetOprStmt:
		if n.SelectList == nil || len(n.SelectList.Selects) == 0 {
			return nil
		}
		out := c.result(n.SelectList.Selects[0], at)
		if out == nil {
			return nil
		}
		for _, b := range n.SelectList.Selects[1:] {
			if s := c.result(b, at); s != nil {
				out.Set = append(out.Set, s)
			}
		}
		if n.OrderBy != nil {
			for _, it := range n.OrderBy.Items {
				out.OrderBy = append(out.OrderBy, c.expr(it.Expr))
			}
		}
		return out
	case *ast.SetOprSelectList:
		if len(n.Selects) == 0 {
			return nil
		}
		out := c.result(n.Selects[0], at)
		for _, b := range n.Selects[1:] {
			if s := c.result(b, at); s != nil && out != nil {
				out.Set = append(out.Set, s)
			}
		}
		return out
	}
	return nil
}

func (c myConv) sel(n *ast.SelectStmt, at int) *Select {
	out := &Select{Pos: c.off + at}
	if n.Fields != nil {
		for _, f := range n.Fields.Fields {
			it := SelectItem{Pos: c.off + f.Offset}
			if f.WildCard != nil {
				it.Star = true
			} else {
				it.Expr = c.expr(f.Expr)
			}
			out.Items = append(out.Items, it)
		}
		if len(out.Items) > 0 {
			out.Pos = out.Items[0].Pos
		}
	}
	if n.From != nil && n.From.TableRefs != nil {
		// A comma join and an explicit JOIN without ON look alike in TiDB's
		// tree; the text tells them apart.
		items := c.fromCommas(at) + 1
		first := FromItem{Pos: c.off + at}
		c.from(n.From.TableRefs, &first)
		out.From = append(out.From, first)
		for range items - 1 {
			out.From = append(out.From, FromItem{Pos: first.Pos})
		}
	}
	out.Where = c.expr(n.Where)
	if n.OrderBy != nil {
		for _, it := range n.OrderBy.Items {
			out.OrderBy = append(out.OrderBy, c.expr(it.Expr))
		}
	}
	if n.GroupBy != nil {
		for _, it := range n.GroupBy.Items {
			out.Others = append(out.Others, c.expr(it.Expr))
		}
	}
	if n.Having != nil {
		out.Others = append(out.Others, c.expr(n.Having.Expr))
	}
	if n.Limit != nil && n.Limit.Offset != nil {
		if v, ok := n.Limit.Offset.(*test_driver.ValueExpr); ok {
			out.Offset = &Literal{Pos: c.off + c.offsetAt(at), Kind: LitNumber, Value: datum(v)}
		}
	}
	return out
}

// from collects derived tables and join conditions.
func (c myConv) from(n ast.ResultSetNode, item *FromItem) {
	switch n := n.(type) {
	case *ast.Join:
		if n.Left != nil {
			c.from(n.Left, item)
		}
		if n.Right != nil {
			c.from(n.Right, item)
		}
		if n.On != nil {
			item.Conds = append(item.Conds, c.expr(n.On.Expr))
		}
	case *ast.TableSource:
		switch src := n.Source.(type) {
		case *ast.SelectStmt, *ast.SetOprStmt:
			if q := c.result(src, 0); q != nil {
				item.Subqueries = append(item.Subqueries, q)
			}
		default:
			c.from(src, item)
		}
	}
}

// clauseEnd are keywords that end a FROM clause.
var clauseEnd = map[string]bool{
	"WHERE": true, "GROUP": true, "HAVING": true, "ORDER": true, "LIMIT": true, "UNION": true,
	"EXCEPT": true, "INTERSECT": true, "FOR": true, "WINDOW": true, "LOCK": true, "INTO": true,
}

// fromCommas counts the top-level commas in the FROM clause of the query
// that starts at at.
func (c myConv) fromCommas(at int) int {
	toks := c.tokens(at)
	depth, inFrom, commas := 0, false, 0
	for _, t := range toks {
		switch {
		case t.Text == "(":
			depth++
		case t.Text == ")":
			depth--
			if depth < 0 {
				return commas
			}
		case depth != 0:
		case t.Is("FROM"):
			inFrom = true
		case inFrom && t.Text == ",":
			commas++
		case inFrom && clauseEnd[t.Upper()]:
			return commas
		}
	}
	return commas
}

// offsetAt is where the OFFSET value of the query at at starts: after
// OFFSET in LIMIT count OFFSET offset, or right after LIMIT in
// LIMIT offset, count.
func (c myConv) offsetAt(at int) int {
	toks := c.tokens(at)
	depth, limit := 0, -1
	for i, t := range toks {
		switch {
		case t.Text == "(":
			depth++
		case t.Text == ")":
			depth--
		case depth == 0 && t.Is("LIMIT"):
			limit = i
		case depth == 0 && limit >= 0 && t.Is("OFFSET") && i+1 < len(toks):
			return toks[i+1].Pos
		}
	}
	if limit >= 0 && limit+2 < len(toks) && toks[limit+2].Text == "," {
		return toks[limit+1].Pos
	}
	return at
}

// tokens are the statement's tokens from at, skipping a leading "(".
func (c myConv) tokens(at int) []sqlscan.Token {
	sc := sqlscan.NewScanner(c.src[at:], sqlscan.MySQL)
	var out []sqlscan.Token
	for {
		t, ok := sc.Next()
		if !ok {
			break
		}
		t.Pos += at
		out = append(out, t)
	}
	if len(out) > 0 && out[0].Text == "(" {
		out = out[1:]
	}
	return out
}

func (c myConv) expr(e ast.ExprNode) Expr {
	if e == nil {
		return nil
	}
	p := c.off + e.OriginTextPosition()
	switch e := e.(type) {
	case *ast.ColumnNameExpr:
		return &Column{Pos: p, Name: e.Name.String()}
	case *test_driver.ValueExpr:
		lit := &Literal{Pos: p}
		switch e.Kind() {
		case test_driver.KindNull:
			lit.Kind = LitNull
		case test_driver.KindString:
			lit.Kind, lit.Value = LitString, e.GetString()
		case test_driver.KindInt64, test_driver.KindUint64, test_driver.KindFloat64, test_driver.KindMysqlDecimal:
			lit.Kind, lit.Value = LitNumber, datum(e)
		}
		return lit
	case *ast.BinaryOperationExpr:
		return &Binary{Pos: p, Op: binaryOp(e.Op), L: c.expr(e.L), R: c.expr(e.R)}
	case *ast.FuncCallExpr:
		return &Func{Pos: p, Name: e.FnName.L, Args: c.exprList(e.Args)}
	case *ast.AggregateFuncExpr:
		return &Func{Pos: p, Name: strings.ToLower(e.F), Args: c.exprList(e.Args)}
	case *ast.PatternLikeOrIlikeExpr:
		return &Like{Pos: p, Not: e.Not, Expr: c.expr(e.Expr), Pattern: c.expr(e.Pattern)}
	case *ast.PatternInExpr:
		in := &In{Pos: p, Not: e.Not, Expr: c.expr(e.Expr), List: c.exprList(e.List)}
		if sq, ok := e.Sel.(*ast.SubqueryExpr); ok {
			in.Subquery = c.result(sq.Query, sq.OriginTextPosition())
		}
		return in
	case *ast.SubqueryExpr:
		return &Subquery{Pos: p, Exists: e.Exists, Select: c.result(e.Query, e.OriginTextPosition())}
	case *ast.ExistsSubqueryExpr:
		sq, _ := e.Sel.(*ast.SubqueryExpr)
		out := &Subquery{Pos: p, Exists: true}
		if sq != nil {
			out.Select = c.result(sq.Query, sq.OriginTextPosition())
		}
		return out
	case *ast.ParenthesesExpr:
		return c.expr(e.Expr)
	}
	return &Other{Pos: p, Children: c.exprList(children(e))}
}

func (c myConv) exprList(es []ast.ExprNode) []Expr {
	var out []Expr
	for _, e := range es {
		if x := c.expr(e); x != nil {
			out = append(out, x)
		}
	}
	return out
}

// children are the nearest expressions under n, looking through nodes that
// are not expressions (such as CASE's WHEN clauses).
func children(n ast.Node) []ast.ExprNode {
	v := &childVisitor{root: n}
	n.Accept(v)
	return v.kids
}

type childVisitor struct {
	root ast.Node
	kids []ast.ExprNode
}

func (v *childVisitor) Enter(n ast.Node) (ast.Node, bool) {
	if n == v.root {
		return n, false
	}
	if e, ok := n.(ast.ExprNode); ok {
		v.kids = append(v.kids, e)
		return n, true
	}
	return n, false
}

func (*childVisitor) Leave(n ast.Node) (ast.Node, bool) { return n, true }

func binaryOp(op opcode.Op) string {
	switch op {
	case opcode.EQ:
		return "="
	case opcode.NE:
		return "<>"
	case opcode.LT:
		return "<"
	case opcode.LE:
		return "<="
	case opcode.GT:
		return ">"
	case opcode.GE:
		return ">="
	case opcode.NullEQ:
		return "<=>"
	case opcode.LogicAnd:
		return "AND"
	case opcode.LogicOr:
		return "OR"
	}
	return strings.ToUpper(op.String())
}

func datum(v *test_driver.ValueExpr) string {
	switch v.Kind() {
	case test_driver.KindInt64:
		return strconv.FormatInt(v.GetInt64(), 10)
	case test_driver.KindUint64:
		return strconv.FormatUint(v.GetUint64(), 10)
	}
	return v.GetDatumString()
}
