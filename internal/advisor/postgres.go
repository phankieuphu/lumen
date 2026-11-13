package advisor

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/sqlscan"
)

const pgSetup = "run CREATE EXTENSION pg_stat_statements in this database, and make sure " +
	"shared_preload_libraries includes pg_stat_statements (needs a server restart)"

// pgCheck verifies the extension is installed in this database and loaded
// by the server; without the preload the view exists but every read fails.
func pgCheck(ctx context.Context, s driver.Session) error {
	vals, err := one(ctx, s, `SELECT to_regclass('pg_stat_statements') IS NOT NULL,
       current_setting('shared_preload_libraries') LIKE '%pg_stat_statements%'`)
	if err != nil {
		return fmt.Errorf("advisor: check pg_stat_statements: %w", err)
	}
	if len(vals) != 2 || num(vals[0]) == 0 || num(vals[1]) == 0 {
		return fmt.Errorf("%w: %s", ErrNotEnabled, pgSetup)
	}
	return nil
}

// pgOrder maps a sort to a fixed ORDER BY; the text never comes from input.
var pgOrder = map[Sort]string{
	SortShare: "total_ms DESC, queryid",
	SortMean:  "total_ms / calls DESC, queryid",
	SortCalls: "calls DESC, queryid",
}

// pgList reads one row per statement of the current database, summed over
// users. Statements of other users show as "<insufficient privilege>"
// without pg_read_all_stats; they count toward the total but are not listed.
// The advisor's own reads are left out.
func pgList(f Filter, id string) (string, []any) {
	sql := `WITH s AS (
  SELECT queryid, min(query) AS query, sum(calls)::bigint AS calls, sum(total_exec_time)::float8 AS total_ms
  FROM pg_stat_statements
  WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
    AND queryid IS NOT NULL
  GROUP BY queryid
)
SELECT queryid::text, query, calls, total_ms / calls,
       COALESCE(100 * total_ms / NULLIF((SELECT sum(total_ms) FROM s), 0), 0)::float8
FROM s
WHERE calls > 0
  AND query IS NOT NULL
  AND query <> '<insufficient privilege>'
  AND query NOT ILIKE '%pg\_stat\_statements%'
  AND ($1::text = '' OR queryid::text = $1::text)
  AND ($1::text <> '' OR (
        total_ms / calls >= $2::float8
    AND calls >= $3::bigint
    AND ($4::text = '' OR lower(query) LIKE $4::text ESCAPE '\')
    AND ($5::text = '' OR lower(ltrim(query)) LIKE $5::text || '%')))
ORDER BY ` + pgOrder[f.Sort] + `
LIMIT $6::int`
	return sql, []any{id, f.MinMeanMs, f.MinCalls, likePattern(f.Search), string(f.Kind), f.Limit}
}

// pgSince reads the last reset. The view exists from PostgreSQL 14; older
// servers, and any error, give "".
func pgSince(ctx context.Context, s driver.Session) string {
	vals, err := one(ctx, s, `SELECT to_regclass('pg_stat_statements_info') IS NOT NULL`)
	if err != nil || len(vals) == 0 || num(vals[0]) == 0 {
		return ""
	}
	vals, err = one(ctx, s, `SELECT stats_reset FROM pg_stat_statements_info`)
	if err != nil || len(vals) == 0 {
		return ""
	}
	return rfc3339(vals[0])
}

// pgExplainable lists the leading keywords EXPLAIN accepts.
var pgExplainable = map[string]bool{
	"SELECT": true, "WITH": true, "INSERT": true, "UPDATE": true, "DELETE": true,
	"MERGE": true, "VALUES": true, "TABLE": true,
}

// pgPlanName is the prepared statement pgPlan uses on its pinned session.
const pgPlanName = "lumen_advisor_plan"

// pgPlan reads the generic plan of a normalized statement. It never returns
// an error: a statement that cannot be planned gets a source line that says
// why, because the statistics are still worth showing.
//
// The statement text has $n where its values were, so it is prepared and
// its generic plan — the one that does not depend on the values — is read
// with EXPLAIN EXECUTE. There is no ANALYZE: the statement is planned and
// never run, inside a read-only transaction that is always rolled back.
func pgPlan(ctx context.Context, s driver.Session, sql string) (string, []PlanNode) {
	verb, params := leading(sql, sqlscan.Postgres)
	if !pgExplainable[verb] {
		return "No plan: EXPLAIN does not cover this kind of statement", nil
	}
	// Clean up even when the request context is done, so the session does
	// not return to the pool inside a transaction or holding the statement.
	// PREPARE is not undone by ROLLBACK.
	done := context.WithoutCancel(ctx)
	if _, err := s.Exec(ctx, `BEGIN READ ONLY`); err != nil {
		return "No plan: could not start a read-only transaction", nil
	}
	defer func() {
		_, _ = s.Exec(done, `ROLLBACK`)
		_, _ = s.Exec(done, `DEALLOCATE `+pgPlanName)
	}()
	if _, err := s.Exec(ctx, `SET LOCAL plan_cache_mode = force_generic_plan`); err != nil {
		return "No plan: the server has no generic plan mode (needs PostgreSQL 12 or later)", nil
	}
	// The user's own statement, passed through verbatim. The driver allows
	// one statement per call, so the text cannot add a second.
	if _, err := s.Exec(ctx, `PREPARE `+pgPlanName+` AS `+sql); err != nil {
		return "No plan: the server could not plan this statement", nil
	}
	// The generic plan ignores the values, so each placeholder gets NULL.
	execute := `EXPLAIN (FORMAT JSON) EXECUTE ` + pgPlanName
	if params > 0 {
		execute += `(NULL` + strings.Repeat(`, NULL`, params-1) + `)`
	}
	vals, err := one(ctx, s, execute)
	if err != nil || len(vals) == 0 {
		return "No plan: the server could not plan this statement", nil
	}
	nodes, err := flattenPgPlan(jsonBytes(vals[0]))
	if err != nil {
		return "No plan: the server's answer was not understood", nil
	}
	return "EXPLAIN (FORMAT JSON), generic plan", nodes
}

// maxPgParams is the most placeholders one PostgreSQL statement can carry.
const maxPgParams = 65535

// leading returns the first keyword of sql, upper-cased, and the highest
// $n placeholder number in it (0 when there is none).
func leading(sql string, d sqlscan.Dialect) (verb string, params int) {
	sc := sqlscan.NewScanner(sql, d)
	for {
		t, ok := sc.Next()
		if !ok {
			return verb, params
		}
		if verb == "" && t.Kind == sqlscan.Word {
			verb = t.Upper()
		}
		if t.Kind == sqlscan.Param {
			if n, err := strconv.Atoi(strings.TrimPrefix(t.Text, "$")); err == nil && n <= maxPgParams {
				params = max(params, n)
			}
		}
	}
}

func jsonBytes(v any) []byte {
	switch x := v.(type) {
	case json.RawMessage:
		return x
	case []byte:
		return x
	case string:
		return []byte(x)
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return nil
		}
		return b
	}
}

// Bounds on a flattened plan, so a huge plan cannot flood the UI.
const (
	maxPlanNodes = 500
	maxPlanDepth = 100
)

type pgNode struct {
	NodeType     string   `json:"Node Type"`
	RelationName string   `json:"Relation Name"`
	Alias        string   `json:"Alias"`
	IndexName    string   `json:"Index Name"`
	JoinType     string   `json:"Join Type"`
	TotalCost    float64  `json:"Total Cost"`
	Plans        []pgNode `json:"Plans"`
}

// flattenPgPlan turns EXPLAIN (FORMAT JSON) output into depth-first lines.
// A node's share is its own cost — its total minus its children's — over
// the root's total.
func flattenPgPlan(raw []byte) ([]PlanNode, error) {
	var doc []struct {
		Plan pgNode `json:"Plan"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("advisor: decode plan: %w", err)
	}
	if len(doc) == 0 || doc[0].Plan.NodeType == "" {
		return nil, fmt.Errorf("advisor: decode plan: no plan node")
	}
	root := doc[0].Plan
	out := make([]PlanNode, 0, 16)
	var walk func(n pgNode, depth int)
	walk = func(n pgNode, depth int) {
		if len(out) == maxPlanNodes || depth > maxPlanDepth {
			return
		}
		self := n.TotalCost
		for _, c := range n.Plans {
			self -= c.TotalCost
		}
		share := 0.0
		if root.TotalCost > 0 {
			share = round(100*max(self, 0)/root.TotalCost, 0)
		}
		out = append(out, PlanNode{Label: pgLabel(n), Depth: depth, Cost: n.TotalCost, SharePct: share})
		for _, c := range n.Plans {
			walk(c, depth+1)
		}
	}
	walk(root, 0)
	return out, nil
}

func pgLabel(n pgNode) string {
	var b strings.Builder
	if n.JoinType != "" && n.JoinType != "Inner" {
		b.WriteString(n.JoinType + " ")
	}
	b.WriteString(n.NodeType)
	if n.IndexName != "" {
		b.WriteString(" using " + n.IndexName)
	}
	if n.RelationName != "" {
		b.WriteString(" on " + n.RelationName)
		if n.Alias != "" && n.Alias != n.RelationName {
			b.WriteString(" " + n.Alias)
		}
	}
	return b.String()
}
