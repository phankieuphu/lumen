package advisor

import (
	"errors"
	"slices"
	"testing"

	"github.com/phankieuphu/lumen/internal/sqlscan"
)

func TestTitle(t *testing.T) {
	tests := []struct {
		name   string
		verb   string
		tables []string
		want   string
	}{
		{"verb only", "VACUUM", nil, "VACUUM"},
		{"no verb", "", nil, "Statement"},
		{"up to three tables", "SELECT", []string{"a", "b", "c"}, "SELECT on a, b, c"},
		{"more than three tables", "SELECT", []string{"a", "b", "c", "d", "e"}, "SELECT on a, b, c, +2 more"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := title(tt.verb, tt.tables); got != tt.want {
				t.Fatalf("title = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFilterNormalized(t *testing.T) {
	t.Run("zero value gets defaults", func(t *testing.T) {
		f, err := Filter{}.normalized()
		if err != nil || f.Sort != SortShare || f.Limit != DefaultLimit {
			t.Fatalf("got %+v, %v", f, err)
		}
	})
	t.Run("limit is capped and negatives are cleared", func(t *testing.T) {
		f, err := Filter{Limit: 1 << 20, MinCalls: -1, MinMeanMs: -5, WindowHours: -2, Search: "  x  "}.normalized()
		if err != nil || f.Limit != MaxLimit || f.MinCalls != 0 || f.MinMeanMs != 0 || f.WindowHours != 0 || f.Search != "x" {
			t.Fatalf("got %+v, %v", f, err)
		}
	})
	bad := map[string]Filter{
		"unknown sort is rejected":    {Sort: "total_ms; DROP TABLE x"},
		"unknown kind is rejected":    {Kind: "truncate"},
		"oversize search is rejected": {Search: string(make([]byte, MaxSearchBytes+1))},
	}
	for name, f := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := f.normalized(); !errors.Is(err, ErrBadFilter) {
				t.Fatalf("err = %v, want ErrBadFilter", err)
			}
		})
	}
}

func TestLikePattern(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"empty stays empty", "", ""},
		{"plain text is lowered and wrapped", "Orders", "%orders%"},
		{"wildcards are escaped", `50%_off\`, `%50\%\_off\\%`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := likePattern(tt.in); got != tt.want {
				t.Fatalf("likePattern = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOrderByCoversEverySort(t *testing.T) {
	for _, s := range []Sort{SortShare, SortMean, SortCalls} {
		if pgOrder[s] == "" || myOrder[s] == "" {
			t.Fatalf("sort %q has no ORDER BY", s)
		}
	}
}

func TestFlattenPgPlan(t *testing.T) {
	const doc = `[{"Plan": {"Node Type": "Limit", "Total Cost": 100.0, "Plans": [
		{"Node Type": "Hash Join", "Join Type": "Left", "Total Cost": 100.0, "Plans": [
			{"Node Type": "Seq Scan", "Relation Name": "orders", "Alias": "o", "Total Cost": 70.0},
			{"Node Type": "Index Scan", "Relation Name": "customers", "Alias": "customers", "Index Name": "customers_pkey", "Total Cost": 10.0}
		]}
	]}}]`
	got, err := flattenPgPlan([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := []PlanNode{
		{Label: "Limit", Depth: 0, Cost: 100, SharePct: 0},
		{Label: "Left Hash Join", Depth: 1, Cost: 100, SharePct: 20},
		{Label: "Seq Scan on orders o", Depth: 2, Cost: 70, SharePct: 70},
		{Label: "Index Scan using customers_pkey on customers", Depth: 2, Cost: 10, SharePct: 10},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("plan = %+v\nwant   %+v", got, want)
	}

	for name, raw := range map[string]string{"not json": `nope`, "empty list": `[]`, "no node": `[{}]`} {
		t.Run(name, func(t *testing.T) {
			if _, err := flattenPgPlan([]byte(raw)); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func TestLeading(t *testing.T) {
	tests := []struct {
		name, sql, verb string
		params          int
	}{
		{"highest placeholder wins", `select * from t where a = $2 and b = $11 and c = $2`, "SELECT", 11},
		{"no placeholders", `/* c */ WITH x AS (SELECT 1) SELECT * FROM x`, "WITH", 0},
		{"dollar in a string is not a placeholder", `SELECT '$1'`, "SELECT", 0},
		{"out of range number is ignored", `SELECT $99999999999999999999`, "SELECT", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verb, params := leading(tt.sql, sqlscan.Postgres)
			if verb != tt.verb || params != tt.params {
				t.Fatalf("leading = %q %d, want %q %d", verb, params, tt.verb, tt.params)
			}
		})
	}
}
