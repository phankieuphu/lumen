package explain

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/phankieuphu/lumen/internal/driver"
)

type mysql struct{}

func (mysql) analyzes() bool { return true }

// plan reads the tree format: EXPLAIN ANALYZE (MySQL 8.0.18+) or EXPLAIN
// FORMAT=TREE. The time limit is the request context: the driver cancels
// the query when it ends.
func (mysql) plan(ctx context.Context, s driver.Session, sql string, analyze bool, _ time.Duration) (string, Plan, error) {
	stmt := "EXPLAIN FORMAT=TREE " + sql
	if analyze {
		stmt = "EXPLAIN ANALYZE " + sql
	}
	var raw string
	read := func() error {
		vals, err := query(ctx, s, stmt)
		if err != nil {
			return fmt.Errorf("explain: %w", err)
		}
		if len(vals) == 0 {
			return fmt.Errorf("explain: the server returned no plan")
		}
		raw = text(vals[0])
		return nil
	}
	// ANALYZE runs the statement, so it runs where writes fail. MySQL
	// refuses even to plan a write in a read-only transaction, and a plan
	// without ANALYZE never runs anything, so that one runs outside.
	var err error
	if analyze {
		err = inReadOnlyTx(ctx, s, "START TRANSACTION READ ONLY", read)
	} else {
		err = read()
	}
	if err != nil {
		return "", Plan{}, err
	}
	p, err := parseMySQL(raw)
	return raw, p, err
}

// Pieces of a tree line, matched from its end:
//
//	-> Table scan on o  (cost=5074 rows=50337) (actual time=0.116..3.7 rows=50000 loops=1)
var (
	myActual = regexp.MustCompile(`\s*\(actual time=([0-9.e+-]+?)\.\.([0-9.e+-]+) rows=([0-9.e+-]+) loops=([0-9.e+-]+)\)$`)
	myNever  = regexp.MustCompile(`\s*\(never executed\)$`)
	myCost   = regexp.MustCompile(`\s*\(cost=(?:[0-9.e+-]+?\.\.)?([0-9.e+-]+) rows=([0-9.e+-]+)\)$`)
	myOn     = regexp.MustCompile(` on (\S+)`)
	myUsing  = regexp.MustCompile(` using (\S+)`)
)

// parseMySQL reads the tree format: one "-> " line per node, four spaces of
// indent per level.
func parseMySQL(raw string) (Plan, error) {
	p := Plan{}
	var stack []*Node // stack[d] is the last node seen at depth d
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if !strings.HasPrefix(trimmed, "-> ") {
			continue
		}
		depth := (len(line) - len(trimmed)) / 4
		n := myNode(strings.TrimPrefix(trimmed, "-> "), &p)
		if depth == 0 || len(stack) == 0 {
			if p.Root != nil {
				return Plan{}, fmt.Errorf("explain: read the plan: more than one root")
			}
			p.Root, stack = n, []*Node{n}
			continue
		}
		depth = min(depth, len(stack))
		parent := stack[depth-1]
		parent.Children = append(parent.Children, n)
		stack = append(stack[:depth], n)
	}
	if p.Root == nil {
		return Plan{}, fmt.Errorf("explain: read the plan: no plan node")
	}
	p.Root = foldFilters(p.Root)
	if p.Analyzed = p.Root.Loops > 0; p.Analyzed {
		p.ExecutionMs = p.Root.TimeMs * p.Root.Loops
	}
	return p, nil
}

func myNode(s string, p *Plan) *Node {
	n := &Node{}
	if m := myActual.FindStringSubmatch(s); m != nil {
		n.TimeMs, n.Rows, n.Loops = num(m[2]), num(m[3]), num(m[4])
		s = s[:len(s)-len(m[0])]
	} else if m := myNever.FindString(s); m != "" {
		n.NeverRan = true
		s = s[:len(s)-len(m)]
	}
	if m := myCost.FindStringSubmatch(s); m != nil {
		n.EstCost, n.EstRows = num(m[1]), num(m[2])
		p.Estimated = true
		s = s[:len(s)-len(m[0])]
	}
	n.Op = strings.TrimSpace(s)
	if m := myOn.FindStringSubmatch(n.Op); m != nil {
		n.Relation = strings.Trim(m[1], "`")
	}
	if m := myUsing.FindStringSubmatch(n.Op); m != nil {
		n.Index = strings.Trim(m[1], "`")
	}
	lower := strings.ToLower(n.Op)
	head, rest, _ := strings.Cut(n.Op, ": ")
	switch {
	case strings.HasPrefix(lower, "table scan on "):
		n.Kind = KindScan
	case strings.Contains(lower, "index ") && n.Relation != "":
		n.Kind = KindIndexScan
		if i := strings.Index(n.Op, " ("); i >= 0 {
			n.Detail = n.Op[i+1:]
		}
	case strings.Contains(lower, " join") || strings.HasSuffix(lower, "join"):
		n.Kind = KindJoin
		if i := strings.Index(n.Op, " ("); i >= 0 {
			n.Detail, n.Op = n.Op[i+1:], n.Op[:i]
		}
	case head == "Sort" || strings.HasPrefix(lower, "sort"):
		n.Kind, n.Detail, n.Op = KindSort, rest, head
	case head == "Filter":
		n.Kind, n.Detail, n.Op = KindFilter, rest, head
	case strings.Contains(lower, "aggregate"):
		n.Kind = KindAggregate
		if rest != "" {
			n.Detail, n.Op = rest, head
		}
	case head == "Limit":
		n.Kind, n.Op = KindLimit, head
	default:
		n.Kind = KindOther
	}
	return n
}

// foldFilters merges a Filter into the scan right under it, as PostgreSQL
// reports them: the scan keeps the rows that pass and counts the rest as
// removed.
func foldFilters(n *Node) *Node {
	for i, c := range n.Children {
		n.Children[i] = foldFilters(c)
	}
	if n.Kind != KindFilter || len(n.Children) != 1 {
		return n
	}
	c := n.Children[0]
	if c.Kind != KindScan && c.Kind != KindIndexScan {
		return n
	}
	c.RowsRemoved = max(c.Rows-n.Rows, 0)
	c.Rows, c.EstRows, c.TimeMs = n.Rows, n.EstRows, n.TimeMs
	if c.Detail == "" {
		c.Detail = n.Detail
	} else {
		c.Detail += " and " + n.Detail
	}
	return c
}

func num(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}
