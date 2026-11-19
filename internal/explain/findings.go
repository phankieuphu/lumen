package explain

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Thresholds for findings.
const (
	// LargeRows is when a full scan or a filter is worth a finding.
	LargeRows = 10_000
	// Misestimate is how far off a row estimate must be (either way).
	Misestimate = 10
	// misestimateRows is the least rows a misestimate must involve.
	misestimateRows = 1000
	// ManyLoops is when a scan repeated by a join is worth a finding.
	ManyLoops = 1000
	maxSteps  = 60
)

func findings(p Plan) []Finding {
	var out []Finding
	add := func(sev Severity, n *Node, title, detail string) {
		out = append(out, Finding{Severity: sev, Title: title, Detail: detail, NodeID: n.ID})
	}
	walk(p.Root, false, func(n, parent *Node) {
		loops := max(n.Loops, 1)
		kept, removed := n.Rows*loops, n.RowsRemoved*loops
		switch {
		case n.Kind == KindScan && n.Relation != "" && !p.Analyzed && !p.Estimated:
			// SQLite: no numbers, so it can only say the table is read whole.
			add(Info, n, "Full scan of "+n.Relation,
				"Every row of "+n.Relation+" is read. That is fine for a small table; on a large one, an index on the columns in WHERE lets it read only the rows it needs.")
		case n.Kind == KindScan && n.Relation != "" && !temporary(n.Relation):
			read := kept + removed
			if !p.Analyzed {
				read = n.EstRows
			}
			if read < LargeRows {
				break
			}
			detail := fmt.Sprintf("%s reads every row of %s: %s rows", n.Op, n.Relation, count(read))
			if removed >= LargeRows && removed >= Misestimate*kept {
				detail += fmt.Sprintf(", and the filter throws away %s of them", count(removed))
			}
			if n.Detail != "" {
				detail += ". An index on the columns in " + trim(n.Detail, 120) + " would let it read only the rows it keeps."
			} else {
				detail += "."
			}
			add(Warning, n, "Full scan of "+n.Relation, detail)
		case removed >= LargeRows && removed >= Misestimate*kept:
			add(Warning, n, "The filter throws away most rows",
				fmt.Sprintf("%s reads %s rows and keeps %s. An index that covers %s would skip the rest.",
					n.Op, count(kept+removed), count(kept), trim(n.Detail, 120)))
		}

		if n.Spilled {
			what := "sort"
			if n.Kind != KindSort {
				what = "hash"
			}
			add(Warning, n, "The "+what+" spilled to disk",
				fmt.Sprintf("%s ran out of memory and used temporary files. Raise work_mem (PostgreSQL) or sort_buffer_size (MySQL) for this session, or sort or hash fewer rows.", n.Op))
		}

		if parent != nil && parent.Kind == KindJoin && n.Kind == KindScan && n.Loops >= ManyLoops {
			add(Warning, n, fmt.Sprintf("%s is read %s times", n.Relation, count(n.Loops)),
				fmt.Sprintf("The %s reads all of %s once for every row on its other side. An index on the join column would turn each read into a lookup.",
					strings.ToLower(parent.Op), n.Relation))
		}
	})
	if p.Analyzed && p.Estimated {
		out = append(out, misestimates(p.Root)...)
	}
	return out
}

// misestimates finds row estimates off by Misestimate or more. Each is
// reported where it starts: a node whose input was already misestimated
// inherits the error, so only the deepest node of a chain is reported.
// Nodes a LIMIT stops early return fewer rows than planned by design, so
// that is not counted against them.
func misestimates(root *Node) []Finding {
	limited := map[*Node]bool{}
	var mark func(n *Node, under bool)
	mark = func(n *Node, under bool) {
		limited[n] = under
		switch {
		case n.Kind == KindLimit:
			under = true
		// These read all of their input before returning a row.
		case n.Kind == KindSort, n.Kind == KindAggregate, strings.EqualFold(n.Op, "hash"):
			under = false
		}
		for _, c := range n.Children {
			mark(c, under)
		}
	}
	mark(root, false)

	var out []Finding
	var visit func(n *Node) bool // reports whether n or a descendant was flagged
	visit = func(n *Node) bool {
		below := false
		for _, c := range n.Children {
			below = visit(c) || below
		}
		if below || n.NeverRan || n.Loops == 0 {
			return below
		}
		if limited[n] && n.Rows < n.EstRows {
			return false
		}
		hi, lo := math.Max(n.Rows, n.EstRows), math.Max(math.Min(n.Rows, n.EstRows), 1)
		if hi < misestimateRows || hi/lo < Misestimate {
			return false
		}
		rel := relationUnder(n)
		fix := "Fresh statistics (ANALYZE) usually fix this."
		if rel != "" {
			fix = "Run ANALYZE " + rel + " so the planner has fresh statistics."
		}
		out = append(out, Finding{
			Severity: Info,
			Title:    fmt.Sprintf("Row estimate off by %s×", count(math.Round(hi/lo))),
			Detail: fmt.Sprintf("%s was planned for %s rows and got %s. A plan built on a wrong estimate can pick the wrong join or index. %s",
				n.Op, count(n.EstRows), count(n.Rows), fix),
			NodeID: n.ID,
		})
		return true
	}
	visit(root)
	return out
}

// relationUnder is the first table named at or under n.
func relationUnder(n *Node) string {
	rel := ""
	walk(n, false, func(x, _ *Node) {
		if rel == "" && x.Relation != "" && !temporary(x.Relation) {
			rel = x.Relation
		}
	})
	return rel
}

func temporary(rel string) bool { return strings.HasPrefix(rel, "<") }

// steps tells how the plan ran, children before their parents: the order
// the work happens in.
func steps(p Plan) []string {
	var out []string
	walk(p.Root, true, func(n, _ *Node) {
		if len(out) == maxSteps {
			return
		}
		s := describe(n)
		if s == "" {
			return
		}
		if p.Analyzed {
			s += numbers(n)
		} else if p.Estimated && n.EstRows > 0 {
			s += fmt.Sprintf(": about %s rows", count(n.EstRows))
		}
		out = append(out, fmt.Sprintf("%d. %s.", len(out)+1, s))
	})
	switch {
	case p.Analyzed && p.ExecutionMs > 0:
		out = append(out, fmt.Sprintf("Finished in %s ms, after %s ms of planning.", ms(p.ExecutionMs), ms(p.PlanningMs)))
	case p.Analyzed && p.Root != nil:
		out = append(out, fmt.Sprintf("Finished in %s ms.", ms(p.Root.TimeMs*max(p.Root.Loops, 1))))
	}
	return out
}

func describe(n *Node) string {
	cond := ""
	if n.Detail != "" {
		cond = " " + trim(n.Detail, 100)
	}
	switch n.Kind {
	case KindScan:
		if temporary(n.Relation) {
			return "Read back the rows stored in a temporary table"
		}
		s := "Read every row of " + n.Relation
		if n.Detail != "" {
			s += ", keeping those that match" + cond
		}
		return s
	case KindIndexScan:
		s := "Looked up " + n.Relation
		if n.Index != "" {
			s += " with index " + n.Index
		}
		return s + cond
	case KindJoin:
		return "Joined the rows above with a " + strings.ToLower(n.Op) + cond
	case KindSort:
		s := "Sorted the rows by" + cond
		if n.Detail == "" {
			s = "Sorted the rows"
		}
		if n.Spilled {
			s += ", on disk"
		}
		return s
	case KindAggregate:
		return "Grouped and aggregated the rows" + strings.TrimSuffix(" by"+cond, " by")
	case KindLimit:
		return "Kept only the first rows"
	case KindFilter:
		return "Kept the rows that match" + cond
	}
	switch strings.ToLower(n.Op) {
	case "hash":
		return "Built a hash table of those rows"
	case "", "result":
		return ""
	}
	return "Ran " + n.Op + cond
}

func numbers(n *Node) string {
	if n.NeverRan {
		return ": never needed"
	}
	loops := max(n.Loops, 1)
	s := fmt.Sprintf(": %s rows", count(n.Rows*loops))
	if n.RowsRemoved > 0 {
		s += fmt.Sprintf(", %s thrown away", count(n.RowsRemoved*loops))
	}
	if loops > 1 {
		return s + fmt.Sprintf(", %s times, %s ms in all", count(loops), ms(n.TimeMs*loops))
	}
	return s + fmt.Sprintf(", done %s ms in", ms(n.TimeMs))
}

// count formats a row count with thousands separators.
func count(f float64) string {
	n := strconv.FormatInt(int64(math.Round(f)), 10)
	if len(n) <= 3 {
		return n
	}
	var b strings.Builder
	for i, r := range n {
		if i > 0 && (len(n)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func ms(f float64) string {
	if f >= 100 {
		return strconv.FormatFloat(f, 'f', 0, 64)
	}
	return strconv.FormatFloat(f, 'f', 1, 64)
}
