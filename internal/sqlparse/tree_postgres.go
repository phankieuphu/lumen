//go:build cgo && !windows

package sqlparse

import (
	"strconv"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// postgresTrees reports whether the PostgreSQL adapter is built in.
const postgresTrees = true

// parsePostgres builds the shared tree of one statement from libpg_query's
// tree. offset is where stmt starts in the whole SQL.
func parsePostgres(stmt string, offset int) (*Stmt, bool) {
	res, err := pg.Parse(stmt)
	if err != nil || len(res.GetStmts()) != 1 {
		return nil, false
	}
	c := pgConv{off: offset}
	return c.stmt(res.GetStmts()[0].GetStmt()), true
}

type pgConv struct{ off int }

// pos turns a libpg_query location (a byte offset, -1 when unknown) into an
// offset in the whole SQL.
func (c pgConv) pos(loc int32) int {
	if loc < 0 {
		return c.off
	}
	return c.off + int(loc)
}

func (c pgConv) stmt(n *pg.Node) *Stmt {
	switch x := n.GetNode().(type) {
	case *pg.Node_SelectStmt:
		return &Stmt{Kind: StmtSelect, Select: c.sel(x.SelectStmt)}
	case *pg.Node_InsertStmt:
		ins := x.InsertStmt
		st := &Stmt{Kind: StmtInsert, Columns: len(ins.GetCols()) > 0}
		if q := ins.GetSelectStmt().GetSelectStmt(); q != nil {
			if rows := q.GetValuesLists(); len(rows) > 0 {
				st.Exprs = c.exprs(rows)
			} else {
				st.Select = c.sel(q)
			}
		}
		return st
	case *pg.Node_UpdateStmt:
		up := x.UpdateStmt
		st := &Stmt{Kind: StmtUpdate, Where: c.expr(up.GetWhereClause())}
		for _, t := range up.GetTargetList() {
			st.Exprs = append(st.Exprs, c.expr(t.GetResTarget().GetVal()))
		}
		return st
	case *pg.Node_DeleteStmt:
		return &Stmt{Kind: StmtDelete, Where: c.expr(x.DeleteStmt.GetWhereClause())}
	case *pg.Node_ExplainStmt:
		return c.stmt(x.ExplainStmt.GetQuery())
	default:
		return &Stmt{Kind: StmtOther}
	}
}

func (c pgConv) sel(s *pg.SelectStmt) *Select {
	if s == nil {
		return nil
	}
	if s.GetOp() != pg.SetOperation_SETOP_NONE {
		out := c.sel(s.GetLarg())
		if out == nil {
			out = &Select{Pos: c.off}
		}
		if r := c.sel(s.GetRarg()); r != nil {
			out.Set = append(out.Set, r)
		}
		for _, sb := range s.GetSortClause() {
			out.OrderBy = append(out.OrderBy, c.expr(sb.GetSortBy().GetNode()))
		}
		return out
	}
	out := &Select{Pos: c.off}
	for i, t := range s.GetTargetList() {
		rt := t.GetResTarget()
		it := SelectItem{Pos: c.pos(rt.GetLocation())}
		if i == 0 {
			out.Pos = it.Pos
		}
		if isStar(rt.GetVal()) {
			it.Star = true
		} else {
			it.Expr = c.expr(rt.GetVal())
		}
		out.Items = append(out.Items, it)
	}
	for _, f := range s.GetFromClause() {
		item := FromItem{Pos: c.off}
		c.from(f, &item)
		out.From = append(out.From, item)
	}
	out.Where = c.expr(s.GetWhereClause())
	for _, sb := range s.GetSortClause() {
		out.OrderBy = append(out.OrderBy, c.expr(sb.GetSortBy().GetNode()))
	}
	out.Others = append(c.exprs(s.GetGroupClause()), c.expr(s.GetHavingClause()))
	if k := s.GetLimitOffset().GetAConst(); k != nil {
		if iv, ok := k.GetVal().(*pg.A_Const_Ival); ok {
			out.Offset = &Literal{Pos: c.pos(k.GetLocation()), Kind: LitNumber, Value: strconv.Itoa(int(iv.Ival.GetIval()))}
		}
	}
	return out
}

// from collects a FROM entry's position, derived tables, and join
// conditions.
func (c pgConv) from(n *pg.Node, item *FromItem) {
	switch x := n.GetNode().(type) {
	case *pg.Node_RangeVar:
		if item.Pos == c.off {
			item.Pos = c.pos(x.RangeVar.GetLocation())
		}
	case *pg.Node_RangeSubselect:
		if q := c.sel(x.RangeSubselect.GetSubquery().GetSelectStmt()); q != nil {
			item.Subqueries = append(item.Subqueries, q)
		}
	case *pg.Node_JoinExpr:
		c.from(x.JoinExpr.GetLarg(), item)
		c.from(x.JoinExpr.GetRarg(), item)
		if q := c.expr(x.JoinExpr.GetQuals()); q != nil {
			item.Conds = append(item.Conds, q)
		}
	}
}

func isStar(n *pg.Node) bool {
	fields := n.GetColumnRef().GetFields()
	return len(fields) > 0 && fields[len(fields)-1].GetAStar() != nil
}

func (c pgConv) exprs(ns []*pg.Node) []Expr {
	var out []Expr
	for _, n := range ns {
		if e := c.expr(n); e != nil {
			out = append(out, e)
		}
	}
	return out
}

func (c pgConv) expr(n *pg.Node) Expr {
	if n == nil || n.GetNode() == nil {
		return nil
	}
	switch x := n.GetNode().(type) {
	case *pg.Node_ColumnRef:
		var parts []string
		for _, f := range x.ColumnRef.GetFields() {
			if s := f.GetString_(); s != nil {
				parts = append(parts, s.GetSval())
			} else {
				parts = append(parts, "*")
			}
		}
		return &Column{Pos: c.pos(x.ColumnRef.GetLocation()), Name: strings.Join(parts, ".")}
	case *pg.Node_AConst:
		k := x.AConst
		lit := &Literal{Pos: c.pos(k.GetLocation())}
		switch v := k.GetVal().(type) {
		case nil:
			if k.GetIsnull() {
				lit.Kind = LitNull
			}
		case *pg.A_Const_Sval:
			lit.Kind, lit.Value = LitString, v.Sval.GetSval()
		case *pg.A_Const_Ival:
			lit.Kind, lit.Value = LitNumber, strconv.Itoa(int(v.Ival.GetIval()))
		case *pg.A_Const_Fval:
			lit.Kind, lit.Value = LitNumber, v.Fval.GetFval()
		}
		return lit
	case *pg.Node_AExpr:
		a := x.AExpr
		op := opName(a.GetName())
		p := c.pos(a.GetLocation())
		switch a.GetKind() {
		case pg.A_Expr_Kind_AEXPR_OP:
			return &Binary{Pos: p, Op: op, L: c.expr(a.GetLexpr()), R: c.expr(a.GetRexpr())}
		case pg.A_Expr_Kind_AEXPR_LIKE, pg.A_Expr_Kind_AEXPR_ILIKE:
			return &Like{Pos: p, Not: strings.HasPrefix(op, "!"), Expr: c.expr(a.GetLexpr()), Pattern: c.expr(a.GetRexpr())}
		case pg.A_Expr_Kind_AEXPR_IN:
			return &In{Pos: p, Not: op == "<>", Expr: c.expr(a.GetLexpr()), List: c.exprs(a.GetRexpr().GetList().GetItems())}
		}
	case *pg.Node_BoolExpr:
		b := x.BoolExpr
		// x NOT IN (SELECT ...) parses as NOT (x = ANY (SELECT ...)).
		if b.GetBoolop() == pg.BoolExprType_NOT_EXPR && len(b.GetArgs()) == 1 {
			if in, ok := c.expr(b.GetArgs()[0]).(*In); ok && in.Subquery != nil {
				in.Not = true
				return in
			}
		}
		return &Other{Pos: c.pos(b.GetLocation()), Children: c.exprs(b.GetArgs())}
	case *pg.Node_SubLink:
		s := x.SubLink
		q := c.sel(s.GetSubselect().GetSelectStmt())
		switch s.GetSubLinkType() {
		case pg.SubLinkType_EXISTS_SUBLINK:
			return &Subquery{Pos: c.pos(s.GetLocation()), Exists: true, Select: q}
		case pg.SubLinkType_ANY_SUBLINK:
			if len(s.GetOperName()) == 0 || opName(s.GetOperName()) == "=" {
				return &In{Pos: c.pos(s.GetLocation()), Expr: c.expr(s.GetTestexpr()), Subquery: q}
			}
		}
		return &Subquery{Pos: c.pos(s.GetLocation()), Select: q}
	case *pg.Node_FuncCall:
		f := x.FuncCall
		name := ""
		if parts := f.GetFuncname(); len(parts) > 0 {
			name = strings.ToLower(parts[len(parts)-1].GetString_().GetSval())
		}
		return &Func{Pos: c.pos(f.GetLocation()), Name: name, Args: c.exprs(f.GetArgs())}
	}
	return c.other(n)
}

// other keeps an expression the tree does not model, with its children.
func (c pgConv) other(n *pg.Node) Expr {
	m := n.ProtoReflect()
	return &Other{Pos: c.locate(m), Children: c.children(m)}
}

// children converts every Node under m's fields, looking through the
// messages that are not Nodes.
func (c pgConv) children(m protoreflect.Message) []Expr {
	var out []Expr
	add := func(sub protoreflect.Message) {
		if node, ok := sub.Interface().(*pg.Node); ok {
			if e := c.expr(node); e != nil {
				out = append(out, e)
			}
			return
		}
		out = append(out, c.children(sub)...)
	}
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.Kind() != protoreflect.MessageKind || fd.IsMap():
		case fd.IsList():
			l := v.List()
			for i := range l.Len() {
				add(l.Get(i).Message())
			}
		default:
			add(v.Message())
		}
		return true
	})
	return out
}

// locate finds the location field of the message a Node wraps.
func (c pgConv) locate(m protoreflect.Message) int {
	out := c.off
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Kind() == protoreflect.MessageKind && !fd.IsList() && !fd.IsMap() {
			if f := v.Message().Descriptor().Fields().ByName("location"); f != nil {
				if loc := v.Message().Get(f).Int(); loc >= 0 {
					out = c.off + int(loc)
				}
			}
		}
		return false
	})
	return out
}

func opName(name []*pg.Node) string {
	if len(name) == 0 {
		return ""
	}
	return name[len(name)-1].GetString_().GetSval()
}
