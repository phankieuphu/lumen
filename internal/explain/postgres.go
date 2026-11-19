package explain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/phankieuphu/lumen/internal/driver"
)

type postgres struct{}

func (postgres) analyzes() bool { return true }

func (postgres) plan(ctx context.Context, s driver.Session, sql string, analyze bool, timeout time.Duration) (string, Plan, error) {
	opts := "FORMAT JSON"
	if analyze {
		opts = "ANALYZE, BUFFERS, FORMAT JSON"
	}
	var raw string
	// EXPLAIN without ANALYZE plans a write without running it, even in a
	// read-only transaction; with ANALYZE, a write fails there.
	err := inReadOnlyTx(ctx, s, "BEGIN READ ONLY", func() error {
		if _, err := s.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", timeout.Milliseconds())); err != nil {
			return fmt.Errorf("explain: set a time limit: %w", err)
		}
		// The user's statement, verbatim: the driver allows one statement
		// per call, so the text cannot add a second.
		vals, err := query(ctx, s, "EXPLAIN ("+opts+") "+sql)
		if err != nil {
			return fmt.Errorf("explain: %w", err)
		}
		if len(vals) == 0 {
			return fmt.Errorf("explain: the server returned no plan")
		}
		raw = text(vals[0])
		return nil
	})
	if err != nil {
		return "", Plan{}, err
	}
	p, err := parsePostgres([]byte(raw))
	return raw, p, err
}

type pgNode struct {
	NodeType     string   `json:"Node Type"`
	RelationName string   `json:"Relation Name"`
	Alias        string   `json:"Alias"`
	IndexName    string   `json:"Index Name"`
	JoinType     string   `json:"Join Type"`
	Strategy     string   `json:"Strategy"`
	TotalCost    float64  `json:"Total Cost"`
	PlanRows     float64  `json:"Plan Rows"`
	ActualRows   float64  `json:"Actual Rows"`
	ActualLoops  float64  `json:"Actual Loops"`
	ActualTime   float64  `json:"Actual Total Time"`
	RemovedF     float64  `json:"Rows Removed by Filter"`
	RemovedJ     float64  `json:"Rows Removed by Join Filter"`
	RemovedR     float64  `json:"Rows Removed by Index Recheck"`
	Filter       string   `json:"Filter"`
	IndexCond    string   `json:"Index Cond"`
	RecheckCond  string   `json:"Recheck Cond"`
	HashCond     string   `json:"Hash Cond"`
	MergeCond    string   `json:"Merge Cond"`
	JoinFilter   string   `json:"Join Filter"`
	SortKey      []string `json:"Sort Key"`
	GroupKey     []string `json:"Group Key"`
	SortSpace    string   `json:"Sort Space Type"`
	HashBatches  float64  `json:"Hash Batches"`
	Plans        []pgNode `json:"Plans"`
}

// parsePostgres reads EXPLAIN (FORMAT JSON) output.
func parsePostgres(raw []byte) (Plan, error) {
	var doc []struct {
		Plan        *pgNode `json:"Plan"`
		PlanningMs  float64 `json:"Planning Time"`
		ExecutionMs float64 `json:"Execution Time"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Plan{}, fmt.Errorf("explain: read the plan: %w", err)
	}
	if len(doc) == 0 || doc[0].Plan == nil {
		return Plan{}, fmt.Errorf("explain: read the plan: no plan node")
	}
	return Plan{
		Estimated:   true,
		Root:        pgTree(doc[0].Plan, 0),
		PlanningMs:  doc[0].PlanningMs,
		ExecutionMs: doc[0].ExecutionMs,
	}, nil
}

func pgTree(n *pgNode, depth int) *Node {
	out := &Node{
		Op:          pgOp(n),
		Kind:        pgKind(n.NodeType),
		Relation:    n.RelationName,
		Index:       n.IndexName,
		Detail:      pgDetail(n),
		EstRows:     n.PlanRows,
		EstCost:     n.TotalCost,
		Rows:        n.ActualRows,
		Loops:       n.ActualLoops,
		TimeMs:      n.ActualTime,
		RowsRemoved: n.RemovedF + n.RemovedJ + n.RemovedR,
		Spilled:     n.SortSpace == "Disk" || n.HashBatches > 1,
	}
	if depth < MaxDepth {
		for i := range n.Plans {
			out.Children = append(out.Children, pgTree(&n.Plans[i], depth+1))
		}
	}
	return out
}

func pgOp(n *pgNode) string {
	op := n.NodeType
	if n.JoinType != "" && n.JoinType != "Inner" {
		op = n.JoinType + " " + op
	}
	if n.Strategy != "" && n.NodeType == "Aggregate" {
		op = n.Strategy + " " + op
	}
	return op
}

func pgKind(t string) Kind {
	switch t {
	case "Seq Scan":
		return KindScan
	case "Index Scan", "Index Only Scan", "Bitmap Heap Scan", "Bitmap Index Scan":
		return KindIndexScan
	case "Nested Loop", "Hash Join", "Merge Join":
		return KindJoin
	case "Sort", "Incremental Sort":
		return KindSort
	case "Aggregate", "GroupAggregate", "HashAggregate", "Group", "WindowAgg":
		return KindAggregate
	case "Limit":
		return KindLimit
	}
	return KindOther
}

func pgDetail(n *pgNode) string {
	for _, s := range []string{n.IndexCond, n.HashCond, n.MergeCond, n.JoinFilter, n.Filter, n.RecheckCond} {
		if s != "" {
			return s
		}
	}
	if len(n.SortKey) > 0 {
		return strings.Join(n.SortKey, ", ")
	}
	return strings.Join(n.GroupKey, ", ")
}
