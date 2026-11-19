// Package explain reads how a database runs a statement: it takes the plan
// with EXPLAIN (with ANALYZE for reads), turns each engine's format into one
// plan tree, and finds what makes it slow, with a plain "how it ran" story.
//
// Safety: ANALYZE runs the statement, so the caller asks for it only for
// reads, and every plan is taken in a read-only transaction that is always
// rolled back. A write that reached ANALYZE by mistake fails instead of
// changing data.
package explain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// Errors returned by Run.
var (
	ErrUnsupported = errors.New("explain: this database has no plan reader")
	ErrStatement   = errors.New("explain: EXPLAIN does not cover this kind of statement")
)

// DefaultTimeout bounds a plan, ANALYZE included.
const DefaultTimeout = 60 * time.Second

// Bounds on a plan, so a huge one cannot flood the UI.
const (
	MaxNodes = 1000
	MaxDepth = 100
	maxRaw   = 256 << 10
)

// Options says how to take a plan.
type Options struct {
	// Analyze runs the statement for real row counts and times. Set it only
	// for reads: the caller classifies the statement.
	Analyze bool
	// Timeout bounds the plan; DefaultTimeout if zero.
	Timeout time.Duration
}

// Kind groups operations across engines.
type Kind string

// Node kinds.
const (
	KindScan      Kind = "scan"       // reads a whole table
	KindIndexScan Kind = "index-scan" // reads a table through an index
	KindJoin      Kind = "join"
	KindSort      Kind = "sort"
	KindAggregate Kind = "aggregate"
	KindLimit     Kind = "limit"
	KindFilter    Kind = "filter"
	KindOther     Kind = "other"
)

// Node is one operation of a plan. Row counts and times are per loop, as
// engines report them; multiply by Loops for the total.
type Node struct {
	ID       int    `json:"id"` // preorder position, for findings
	Op       string `json:"op"` // the engine's name, e.g. "Seq Scan"
	Kind     Kind   `json:"kind"`
	Relation string `json:"relation,omitempty"`
	Index    string `json:"index,omitempty"`
	// Detail is the node's condition or key: filter, join condition, sort
	// key, or index condition.
	Detail string `json:"detail,omitempty"`

	EstRows float64 `json:"estRows"`
	EstCost float64 `json:"estCost"`

	// Set when the plan was analyzed.
	Rows        float64 `json:"rows"`
	Loops       float64 `json:"loops"`
	TimeMs      float64 `json:"timeMs"` // inclusive time of one loop
	RowsRemoved float64 `json:"rowsRemoved"`
	NeverRan    bool    `json:"neverRan,omitempty"`
	// Spilled is set when a sort or hash ran out of memory and used disk.
	Spilled bool `json:"spilled,omitempty"`

	Children []*Node `json:"children,omitempty"`
}

// Severity is how much a finding matters.
type Severity string

// Severities.
const (
	Warning Severity = "warning"
	Info    Severity = "info"
)

// Finding is something about the plan worth fixing.
type Finding struct {
	Severity Severity `json:"severity"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	NodeID   int      `json:"nodeId"`
}

// Plan is a statement's plan with what was found in it.
type Plan struct {
	Dialect driver.Dialect `json:"dialect"`
	// Analyzed is set when the statement ran (and was rolled back).
	Analyzed bool `json:"analyzed"`
	// Estimated is set when the plan has row and cost estimates.
	Estimated bool `json:"estimated"`
	// Note says how the plan was taken, and why it was not analyzed.
	Note        string    `json:"note"`
	Root        *Node     `json:"root"`
	PlanningMs  float64   `json:"planningMs"`
	ExecutionMs float64   `json:"executionMs"`
	Findings    []Finding `json:"findings"`
	// Steps say how it ran, in the order the work happened.
	Steps []string `json:"steps"`
	// Raw is the engine's own output, as text.
	Raw       string `json:"raw"`
	Truncated bool   `json:"truncated"`
}

// engine takes a plan on one database.
type engine interface {
	// plan returns the raw output and the tree built from it.
	plan(ctx context.Context, s driver.Session, sql string, analyze bool, timeout time.Duration) (raw string, p Plan, err error)
	// analyzes reports whether the engine can run ANALYZE.
	analyzes() bool
}

var engines = map[driver.Dialect]engine{
	driver.Postgres: postgres{},
	driver.MySQL:    mysql{},
	driver.SQLite:   sqlite{},
}

// explainable are the statements EXPLAIN covers.
var explainable = map[string]bool{
	"SELECT": true, "WITH": true, "VALUES": true, "TABLE": true,
	"INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": true, "REPLACE": true,
}

// Run takes the plan of sql, a single statement, on c.
func Run(ctx context.Context, c driver.Conn, sql string, opt Options) (_ Plan, err error) {
	e, ok := engines[c.Dialect()]
	if !ok {
		return Plan{}, ErrUnsupported
	}
	if !explainable[leading(sql, sqlscan.Dialect(c.Dialect()))] {
		return Plan{}, ErrStatement
	}
	if opt.Timeout <= 0 {
		opt.Timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()

	s, err := c.Session(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("explain: %w", err)
	}
	defer func() { err = errors.Join(err, closeSession(s)) }()

	analyze := opt.Analyze && e.analyzes()
	raw, p, err := e.plan(ctx, s, sql, analyze, opt.Timeout)
	if err != nil {
		return Plan{}, err
	}
	p.Dialect = c.Dialect()
	p.Analyzed = analyze
	if len(raw) > maxRaw {
		raw = raw[:maxRaw]
		p.Truncated = true
	}
	p.Raw = raw
	switch {
	case analyze:
		p.Note = "Ran with EXPLAIN ANALYZE in a read-only transaction that was rolled back."
	case opt.Analyze:
		p.Note = "This database cannot analyze a plan, so it was planned but not run."
	default:
		p.Note = "Planned but not run: only reads are run with EXPLAIN ANALYZE, so the numbers are estimates."
	}
	if p.Root == nil {
		p.Note += " The database reported no plan steps for this statement."
	}
	complete(&p)
	return p, nil
}

// complete numbers the nodes and adds findings and steps.
func complete(p *Plan) {
	if p.Root != nil {
		ids := 0
		number(p.Root, &ids, 0, &p.Truncated)
		p.Findings = findings(*p)
		p.Steps = steps(*p)
	}
	if p.Findings == nil {
		p.Findings = []Finding{}
	}
	if p.Steps == nil {
		p.Steps = []string{}
	}
}

// number gives nodes their preorder IDs and cuts the tree at the bounds.
func number(n *Node, next *int, depth int, truncated *bool) {
	n.ID = *next
	*next++
	kept := n.Children[:0]
	for _, c := range n.Children {
		if *next >= MaxNodes || depth+1 > MaxDepth {
			*truncated = true
			break
		}
		number(c, next, depth+1, truncated)
		kept = append(kept, c)
	}
	n.Children = kept
}

// walk visits n and its children, children first when post is set.
func walk(n *Node, post bool, fn func(n, parent *Node)) {
	var visit func(n, parent *Node)
	visit = func(n, parent *Node) {
		if !post {
			fn(n, parent)
		}
		for _, c := range n.Children {
			visit(c, n)
		}
		if post {
			fn(n, parent)
		}
	}
	visit(n, nil)
}

// leading is the first keyword of sql, upper-cased.
func leading(sql string, d sqlscan.Dialect) string {
	sc := sqlscan.NewScanner(sql, d)
	for {
		t, ok := sc.Next()
		if !ok {
			return ""
		}
		if t.Kind == sqlscan.Word {
			return t.Upper()
		}
	}
}

// inReadOnlyTx runs fn in a read-only transaction that is always rolled
// back, even when ctx is done, so the session never goes back to the pool
// inside a transaction.
func inReadOnlyTx(ctx context.Context, s driver.Session, begin string, fn func() error) (err error) {
	if _, err := s.Exec(ctx, begin); err != nil {
		return fmt.Errorf("explain: start a read-only transaction: %w", err)
	}
	defer func() {
		if _, rerr := s.Exec(context.WithoutCancel(ctx), "ROLLBACK"); rerr != nil && err == nil {
			err = fmt.Errorf("explain: roll back: %w", rerr)
		}
	}()
	return fn()
}

// query returns the first column of every row.
func query(ctx context.Context, s driver.Session, sql string) (_ []any, err error) {
	rows, err := s.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var out []any
	vals := make([]any, len(rows.Columns()))
	for rows.Next() {
		if err := rows.Values(vals); err != nil {
			return nil, err
		}
		out = append(out, vals[0])
	}
	return out, rows.Err()
}

func closeSession(s driver.Session) error {
	if err := s.Close(); err != nil {
		return fmt.Errorf("explain: close session: %w", err)
	}
	return nil
}

// text is a plan column as text. A driver may hand back a JSON plan
// already decoded, so anything else is encoded back to JSON.
func text(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case json.RawMessage:
		return string(x)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// trim shortens a condition for one line of text.
func trim(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
