package explain

import (
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// analyzed parses a fixture taken with ANALYZE and completes it as Run does.
func analyzed(t *testing.T, p Plan, err error) Plan {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	p.Analyzed = true
	complete(&p)
	return p
}

// flat lists the tree in preorder as "depth:op".
func flat(n *Node) []string {
	var out []string
	var visit func(*Node, int)
	visit = func(n *Node, d int) {
		out = append(out, strings.Repeat(" ", d)+n.Op)
		for _, c := range n.Children {
			visit(c, d+1)
		}
	}
	visit(n, 0)
	return out
}

func titles(fs []Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Title)
	}
	return out
}

func TestPostgresJoin(t *testing.T) {
	p, err := parsePostgres([]byte(fixture(t, "postgres_join_analyze.json")))
	p = analyzed(t, p, err)
	if got := strings.Join(flat(p.Root), "|"); got != "Sort| Hash Join|  Seq Scan|  Hash|   Seq Scan" {
		t.Fatalf("tree = %s", got)
	}
	scan := p.Root.Children[0].Children[0]
	if scan.Kind != KindScan || scan.Relation != "orders" || scan.Rows != 500 || scan.RowsRemoved != 49500 || scan.Detail != "(status = 'open'::text)" {
		t.Fatalf("orders scan = %+v", scan)
	}
	if p.PlanningMs <= 0 || p.ExecutionMs <= 0 || !p.Estimated {
		t.Fatalf("plan times %v/%v estimated %v", p.PlanningMs, p.ExecutionMs, p.Estimated)
	}
	// 50,000 rows read to keep 500: a full scan worth an index.
	if got := titles(p.Findings); len(got) != 1 || got[0] != "Full scan of orders" {
		t.Fatalf("findings = %v", got)
	}
	if f := p.Findings[0]; f.NodeID != scan.ID || !strings.Contains(f.Detail, "49,500") {
		t.Fatalf("finding = %+v", f)
	}
	want := []string{
		"1. Read every row of orders, keeping those that match (status = 'open'::text): 500 rows, 49,500 thrown away",
		"2. Read every row of customers",
		"3. Built a hash table of those rows",
		"4. Joined the rows above with a hash join (o.customer_id = c.id)",
		"5. Sorted the rows by o.total DESC",
		"Finished in",
	}
	if len(p.Steps) != len(want) {
		t.Fatalf("steps:\n%s", strings.Join(p.Steps, "\n"))
	}
	for i, w := range want {
		if !strings.HasPrefix(p.Steps[i], w) {
			t.Errorf("step %d = %q, want prefix %q", i, p.Steps[i], w)
		}
	}
}

func TestPostgresDiskSort(t *testing.T) {
	p, err := parsePostgres([]byte(fixture(t, "postgres_disk_sort.json")))
	p = analyzed(t, p, err)
	if !p.Root.Spilled || p.Root.Kind != KindSort {
		t.Fatalf("root = %+v", p.Root)
	}
	got := titles(p.Findings)
	if !contains(got, "The sort spilled to disk") || !contains(got, "Full scan of orders") {
		t.Fatalf("findings = %v", got)
	}
	if !strings.Contains(strings.Join(p.Steps, "\n"), "on disk") {
		t.Fatalf("steps do not say the sort used disk:\n%s", strings.Join(p.Steps, "\n"))
	}
}

func TestMySQLJoin(t *testing.T) {
	p, err := parseMySQL(fixture(t, "mysql_join_analyze.txt"))
	p = analyzed(t, p, err)
	// The Filter folds into the scan under it.
	if got := strings.Join(flat(p.Root), "|"); got != "Limit| Nested loop inner join|  Sort|   Table scan on o|  Single-row index lookup on c using PRIMARY (id=o.customer_id)" {
		t.Fatalf("tree = %s", got)
	}
	scan := p.Root.Children[0].Children[0].Children[0]
	if scan.Kind != KindScan || scan.Relation != "o" || scan.Rows != 500 || scan.RowsRemoved != 49500 || !strings.Contains(scan.Detail, "'open'") {
		t.Fatalf("scan = %+v", scan)
	}
	lookup := p.Root.Children[0].Children[1]
	if lookup.Kind != KindIndexScan || lookup.Index != "PRIMARY" || lookup.Loops != 20 || lookup.TimeMs != 967e-6 {
		t.Fatalf("lookup = %+v", lookup)
	}
	// The scan's filter was planned for 50,337 rows and kept 500. The join
	// and sort above it return 20 of 50,337 because of LIMIT 20, which is
	// not a misestimate.
	if got := strings.Join(titles(p.Findings), "|"); got != "Full scan of o|Row estimate off by 101×" {
		t.Fatalf("findings = %v", got)
	}
	if p.ExecutionMs != 5.57 {
		t.Fatalf("execution = %v", p.ExecutionMs)
	}
}

func TestMySQLHashJoinAndEstimates(t *testing.T) {
	p, err := parseMySQL(fixture(t, "mysql_hash_join_analyze.txt"))
	p = analyzed(t, p, err)
	join := p.Root.Children[0].Children[0]
	if join.Kind != KindJoin || join.Op != "Inner hash join" || join.Detail != "(c.`name` = concat('c',o.customer_id))" || join.EstRows != 101e6 {
		t.Fatalf("join = %+v", join)
	}
	// Planned for 101 million rows, got 50,000.
	if got := titles(p.Findings); !contains(got, "Row estimate off by 2,020×") {
		t.Fatalf("findings = %v", got)
	}
	if p.Root.Relation != "<temporary>" || !strings.HasPrefix(p.Steps[len(p.Steps)-2], "6. Read back the rows stored in a temporary table") {
		t.Fatalf("steps:\n%s", strings.Join(p.Steps, "\n"))
	}

	est, err := parseMySQL(fixture(t, "mysql_join_estimate.txt"))
	if err != nil || est.Analyzed || !est.Estimated || est.Root.Op != "Limit" {
		t.Fatalf("estimate = %+v, %v", est, err)
	}
}

func TestMySQLNeverExecuted(t *testing.T) {
	raw := "-> Nested loop inner join  (cost=0.7 rows=1) (actual time=0.08..0.08 rows=0 loops=1)\n" +
		"    -> Table scan on o  (cost=0.35 rows=1) (actual time=0.003..0.003 rows=0 loops=1)\n" +
		"    -> Single-row index lookup on c using PRIMARY (id=o.customer_id)  (cost=0.35 rows=1) (never executed)\n"
	p, err := parseMySQL(raw)
	p = analyzed(t, p, err)
	if n := p.Root.Children[1]; !n.NeverRan || n.EstCost != 0.35 {
		t.Fatalf("lookup = %+v", n)
	}
	if !strings.Contains(p.Steps[1], "never needed") {
		t.Fatalf("steps:\n%s", strings.Join(p.Steps, "\n"))
	}
}

func TestSQLiteTree(t *testing.T) {
	p := parseSQLite([]sqliteLine{
		{id: 3, parent: 0, detail: "SCAN o"},
		{id: 7, parent: 0, detail: "SEARCH c USING INTEGER PRIMARY KEY (rowid=?)"},
		{id: 12, parent: 0, detail: "USE TEMP B-TREE FOR ORDER BY"},
	})
	complete(&p)
	if got := strings.Join(flat(p.Root), "|"); got != "Query| SCAN| SEARCH| USE TEMP B-TREE FOR ORDER BY" {
		t.Fatalf("tree = %s", got)
	}
	c := p.Root.Children[1]
	if c.Kind != KindIndexScan || c.Relation != "c" || c.Index != "INTEGER PRIMARY KEY" || c.Detail != "rowid=?" {
		t.Fatalf("search = %+v", c)
	}
	if got := titles(p.Findings); len(got) != 1 || got[0] != "Full scan of o" || p.Findings[0].Severity != Info {
		t.Fatalf("findings = %+v", p.Findings)
	}
}

func TestSQLiteNoSteps(t *testing.T) {
	p := parseSQLite(nil)
	if p.Root != nil {
		t.Fatalf("plan = %+v", p)
	}
	complete(&p)
	if p.Findings == nil || p.Steps == nil {
		t.Fatal("findings and steps must be empty lists, not null")
	}
}

func TestFindingThresholds(t *testing.T) {
	scan := func(rows, removed, loops float64) *Node {
		return &Node{Op: "Seq Scan", Kind: KindScan, Relation: "t", Rows: rows, RowsRemoved: removed, Loops: loops, EstRows: rows}
	}
	tests := []struct {
		name string
		root *Node
		want []string
	}{
		{"small table", scan(100, 50, 1), nil},
		{"large table", scan(20_000, 0, 1), []string{"Full scan of t"}},
		{"large over loops", scan(10, 0, 2_000), []string{"Full scan of t"}},
		{
			"repeated by a nested loop",
			&Node{Op: "Nested Loop", Kind: KindJoin, Children: []*Node{
				{Op: "Index Scan", Kind: KindIndexScan, Relation: "a", Rows: 1, Loops: 1, EstRows: 1},
				scan(2, 0, 5_000),
			}},
			[]string{"Full scan of t", "t is read 5,000 times"},
		},
		{
			"filter on an index scan",
			&Node{Op: "Index Scan", Kind: KindIndexScan, Relation: "t", Detail: "(a > 1)", Rows: 10, RowsRemoved: 50_000, Loops: 1, EstRows: 10},
			[]string{"The filter throws away most rows"},
		},
		{
			"estimate off",
			&Node{Op: "Hash Join", Kind: KindJoin, Rows: 50_000, EstRows: 100, Loops: 1, Children: []*Node{scan(5, 0, 1)}},
			[]string{"Row estimate off by 500×"},
		},
		{
			"estimate off once per chain",
			&Node{Op: "Sort", Kind: KindSort, Rows: 50_000, EstRows: 100, Loops: 1, Children: []*Node{
				{Op: "Hash Join", Kind: KindJoin, Rows: 50_000, EstRows: 100, Loops: 1},
			}},
			[]string{"Row estimate off by 500×"},
		},
		{
			"cut short by a limit",
			&Node{Op: "Limit", Kind: KindLimit, Rows: 10, EstRows: 10, Loops: 1, Children: []*Node{
				{Op: "Index Scan", Kind: KindIndexScan, Relation: "a", Rows: 10, EstRows: 90_000, Loops: 1},
			}},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Plan{Root: tt.root, Analyzed: true, Estimated: true}
			complete(&p)
			if got := titles(p.Findings); strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Fatalf("findings = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTreeBounds(t *testing.T) {
	root := &Node{Op: "Append"}
	for range MaxNodes + 50 {
		root.Children = append(root.Children, &Node{Op: "Result"})
	}
	p := Plan{Root: root}
	complete(&p)
	if len(root.Children) != MaxNodes-1 || !p.Truncated {
		t.Fatalf("kept %d children, truncated %v", len(root.Children), p.Truncated)
	}
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
