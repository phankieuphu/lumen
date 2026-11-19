package explain

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/phankieuphu/lumen/internal/driver"
)

type sqlite struct{}

// analyzes is false: SQLite has no EXPLAIN ANALYZE, only the query plan.
func (sqlite) analyzes() bool { return false }

// plan reads EXPLAIN QUERY PLAN, which never runs the statement.
func (sqlite) plan(ctx context.Context, s driver.Session, sql string, _ bool, _ time.Duration) (_ string, _ Plan, err error) {
	rows, err := s.Query(ctx, "EXPLAIN QUERY PLAN "+sql)
	if err != nil {
		return "", Plan{}, fmt.Errorf("explain: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var lines []sqliteLine
	vals := make([]any, len(rows.Columns()))
	for rows.Next() {
		if err := rows.Values(vals); err != nil {
			return "", Plan{}, fmt.Errorf("explain: %w", err)
		}
		if len(vals) < 4 {
			return "", Plan{}, fmt.Errorf("explain: read the plan: %d columns", len(vals))
		}
		lines = append(lines, sqliteLine{id: num(text(vals[0])), parent: num(text(vals[1])), detail: text(vals[3])})
	}
	if err := rows.Err(); err != nil {
		return "", Plan{}, fmt.Errorf("explain: %w", err)
	}
	var raw strings.Builder
	for _, l := range lines {
		fmt.Fprintf(&raw, "%v|%v|%s\n", l.id, l.parent, l.detail)
	}
	return raw.String(), parseSQLite(lines), nil
}

type sqliteLine struct {
	id, parent float64
	detail     string
}

var (
	sqliteRel   = regexp.MustCompile(`^(?:SCAN|SEARCH) (\S+)`)
	sqliteIndex = regexp.MustCompile(`USING (?:COVERING )?INDEX (\S+)|USING (INTEGER PRIMARY KEY)`)
	sqliteCond  = regexp.MustCompile(`\((.*)\)$`)
)

// parseSQLite builds the tree from (id, parent) pairs. Several top-level
// lines hang under one "Query" node.
func parseSQLite(lines []sqliteLine) Plan {
	// SQLite may report no steps, e.g. for a DELETE without WHERE, which
	// empties the table directly.
	if len(lines) == 0 {
		return Plan{}
	}
	root := &Node{Op: "Query", Kind: KindOther}
	byID := map[float64]*Node{}
	for _, l := range lines {
		n := sqliteNode(l.detail)
		byID[l.id] = n
		if parent, ok := byID[l.parent]; ok {
			parent.Children = append(parent.Children, n)
		} else {
			root.Children = append(root.Children, n)
		}
	}
	if len(root.Children) == 1 {
		root = root.Children[0]
	}
	return Plan{Root: root}
}

func sqliteNode(d string) *Node {
	n := &Node{Op: d, Kind: KindOther}
	if m := sqliteRel.FindStringSubmatch(d); m != nil {
		n.Relation = m[1]
		n.Kind = KindScan
		if strings.HasPrefix(d, "SEARCH") || strings.Contains(d, " INDEX ") {
			n.Kind = KindIndexScan
		}
		n.Op = strings.Fields(d)[0]
	}
	if m := sqliteIndex.FindStringSubmatch(d); m != nil {
		n.Index = m[1] + m[2]
	}
	if m := sqliteCond.FindStringSubmatch(d); m != nil && n.Relation != "" {
		n.Detail = m[1]
	}
	switch {
	case strings.Contains(d, "TEMP B-TREE FOR ORDER BY"):
		n.Kind = KindSort
	case strings.Contains(d, "TEMP B-TREE FOR GROUP BY"), strings.Contains(d, "TEMP B-TREE FOR DISTINCT"):
		n.Kind = KindAggregate
	}
	return n
}
