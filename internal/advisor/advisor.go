// Package advisor finds the statements that cost a database the most time
// and shows how the server plans to run them.
//
// It reads the engine's own statement statistics (pg_stat_statements on
// PostgreSQL, performance_schema on MySQL) with fixed, parameterized
// queries, so it is allowed on read-only and production connections. It
// depends only on the driver interfaces.
package advisor

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// Limits on one list read.
const (
	DefaultLimit = 50
	MaxLimit     = 200
	// MaxSearchBytes caps the search text sent to the server.
	MaxSearchBytes = 200
)

// Errors a caller can tell apart.
var (
	// ErrUnsupported is returned for a dialect without statement statistics.
	ErrUnsupported = errors.New("advisor: slow-query statistics are not available for this database type")
	// ErrNotEnabled is returned when the server does not collect statistics.
	// The wrapping message says how to turn them on.
	ErrNotEnabled = errors.New("advisor: statement statistics are not enabled")
	// ErrNoAccess is returned when the user may not read the statistics.
	// The wrapping message carries the GRANT to run.
	ErrNoAccess = errors.New("advisor: no access to statement statistics")
	// ErrNotFound is returned when a statement is no longer in the statistics.
	ErrNotFound = errors.New("advisor: query not found; the statistics may have been reset")
	// ErrBadFilter is returned for a filter value outside the allowed set.
	ErrBadFilter = errors.New("advisor: invalid filter")
)

// Sort orders the list.
type Sort string

// Sort orders. The zero value means SortShare.
const (
	SortShare Sort = "share" // total time, i.e. share of database time
	SortMean  Sort = "mean"  // mean time per call
	SortCalls Sort = "calls" // number of calls
)

// Kind narrows the list to one statement type.
type Kind string

// Statement kinds. The zero value means any.
const (
	KindAny    Kind = ""
	KindSelect Kind = "select"
	KindInsert Kind = "insert"
	KindUpdate Kind = "update"
	KindDelete Kind = "delete"
)

// Filter selects and orders slow queries. Zero values select defaults.
type Filter struct {
	// WindowHours keeps statements last seen within this many hours. Only
	// MySQL records when a statement was last seen; PostgreSQL totals
	// always run from the last statistics reset. 0 means no bound.
	WindowHours int `json:"windowHours"`
	// MinMeanMs leaves out statements with a lower mean time.
	MinMeanMs float64 `json:"minMeanMs"`
	// MinCalls leaves out statements called fewer times.
	MinCalls int64 `json:"minCalls"`
	// Search keeps statements whose text contains it, ignoring case.
	Search string `json:"search"`
	// Kind keeps one statement type.
	Kind Kind `json:"kind"`
	// Sort defaults to SortShare.
	Sort Sort `json:"sort"`
	// Limit defaults to DefaultLimit and is capped at MaxLimit.
	Limit int `json:"limit"`
}

// State says how far advice for a statement has come.
type State string

// StateNotAnalysed is the only state until the advice task exists.
const StateNotAnalysed State = "not-analysed"

// SlowQuery is one statement with its totals.
type SlowQuery struct {
	ID string `json:"id"`
	// Title is a short name built from the statement, e.g. "SELECT on orders".
	Title string `json:"title"`
	// SQL is the normalized text: the server has replaced literals with
	// placeholders, so it carries no row data.
	SQL    string   `json:"sql"`
	Tables []string `json:"tables"`
	MeanMs float64  `json:"meanMs"`
	Calls  int64    `json:"calls"`
	// SharePct is the share of total statement time in this database, 0–100.
	SharePct float64 `json:"sharePct"`
	State    State   `json:"state"`
}

// List is the answer to Top.
type List struct {
	Queries []SlowQuery `json:"queries"`
	// StatsSince is when the server last reset its statistics, RFC 3339, or
	// empty when it does not say.
	StatsSince string `json:"statsSince"`
	// Windowed reports whether Filter.WindowHours was applied.
	Windowed bool `json:"windowed"`
	// Truncated is set when more statements matched than Limit.
	Truncated bool `json:"truncated"`
}

// PlanNode is one line of a flattened plan tree.
type PlanNode struct {
	Label string  `json:"label"`
	Depth int     `json:"depth"`
	Cost  float64 `json:"cost"`
	// SharePct is the node's own share of the plan's cost, 0–100.
	SharePct float64 `json:"sharePct"`
}

// Advice is a suggested fix with its proof. Nothing produces one yet: the
// advice task needs the AI provider of phase 3.
type Advice struct {
	Kind        string  `json:"kind"` // "index" or "rewrite"
	Title       string  `json:"title"`
	Explanation string  `json:"explanation"`
	SQL         string  `json:"sql"`
	VerifiedBy  string  `json:"verifiedBy"`
	CostBefore  float64 `json:"costBefore"`
	CostAfter   float64 `json:"costAfter"`
}

// Detail is one statement with its current plan.
type Detail struct {
	SlowQuery
	// PlanSource names how the plan was read, or says why there is none.
	PlanSource string     `json:"planSource"`
	Plan       []PlanNode `json:"plan"`
	Advice     *Advice    `json:"advice"`
	// NoAdviceReason says why Advice is nil.
	NoAdviceReason string `json:"noAdviceReason,omitempty"`
}

const noAdviceYet = "Advice is not available yet: it needs the AI provider from phase 3 of docs/plan.md. " +
	"The statistics and the current plan above are live."

// engine holds what differs between dialects.
type engine struct {
	// check verifies the statistics exist and are readable.
	check func(ctx context.Context, s driver.Session) error
	// list builds the statement and arguments for a filter. id, when set,
	// selects one statement and ignores the thresholds.
	list func(f Filter, id string) (string, []any)
	// since returns the last statistics reset, or "".
	since func(ctx context.Context, s driver.Session) string
	// plan reads the current plan of a statement.
	plan func(ctx context.Context, s driver.Session, sql string) (source string, nodes []PlanNode)
	// windowed reports whether the engine can apply Filter.WindowHours.
	windowed bool
}

var engines = map[driver.Dialect]engine{
	driver.Postgres: {check: pgCheck, list: pgList, since: pgSince, plan: pgPlan},
	driver.MySQL:    {check: myCheck, list: myList, since: noSince, plan: myPlan, windowed: true},
}

func noSince(context.Context, driver.Session) string { return "" }

func (f Filter) normalized() (Filter, error) {
	switch f.Sort {
	case "":
		f.Sort = SortShare
	case SortShare, SortMean, SortCalls:
	default:
		return f, fmt.Errorf("%w: sort %q", ErrBadFilter, f.Sort)
	}
	switch f.Kind {
	case KindAny, KindSelect, KindInsert, KindUpdate, KindDelete:
	default:
		return f, fmt.Errorf("%w: kind %q", ErrBadFilter, f.Kind)
	}
	if f.Limit <= 0 {
		f.Limit = DefaultLimit
	}
	f.Limit = min(f.Limit, MaxLimit)
	f.WindowHours = max(f.WindowHours, 0)
	f.MinCalls = max(f.MinCalls, 0)
	if math.IsNaN(f.MinMeanMs) || f.MinMeanMs < 0 {
		f.MinMeanMs = 0
	}
	f.Search = strings.TrimSpace(f.Search)
	if len(f.Search) > MaxSearchBytes {
		return f, fmt.Errorf("%w: search text is longer than %d bytes", ErrBadFilter, MaxSearchBytes)
	}
	return f, nil
}

// Top returns the statements that match f, most expensive first.
func Top(ctx context.Context, c driver.Conn, f Filter) (_ List, err error) {
	e, ok := engines[c.Dialect()]
	if !ok {
		return List{}, ErrUnsupported
	}
	if f, err = f.normalized(); err != nil {
		return List{}, err
	}
	s, err := c.Session(ctx)
	if err != nil {
		return List{}, fmt.Errorf("advisor: %w", err)
	}
	defer func() { err = errors.Join(err, closeSession(s)) }()

	if err := e.check(ctx, s); err != nil {
		return List{}, err
	}
	// One extra row tells a full page from a cut-off one.
	limit := f.Limit
	f.Limit++
	sql, args := e.list(f, "")
	qs, err := readQueries(ctx, s, c.Dialect(), sql, args)
	if err != nil {
		return List{}, err
	}
	out := List{Queries: qs, Windowed: e.windowed && f.WindowHours > 0, StatsSince: e.since(ctx, s)}
	if len(qs) > limit {
		out.Queries, out.Truncated = qs[:limit], true
	}
	return out, nil
}

// Explain returns one statement with its current plan. The plan is read
// with a plain EXPLAIN, which never runs the statement.
func Explain(ctx context.Context, c driver.Conn, id string) (_ Detail, err error) {
	e, ok := engines[c.Dialect()]
	if !ok {
		return Detail{}, ErrUnsupported
	}
	if id == "" {
		return Detail{}, ErrNotFound
	}
	s, err := c.Session(ctx)
	if err != nil {
		return Detail{}, fmt.Errorf("advisor: %w", err)
	}
	defer func() { err = errors.Join(err, closeSession(s)) }()

	if err := e.check(ctx, s); err != nil {
		return Detail{}, err
	}
	sql, args := e.list(Filter{Sort: SortShare, Limit: 1}, id)
	qs, err := readQueries(ctx, s, c.Dialect(), sql, args)
	if err != nil {
		return Detail{}, err
	}
	if len(qs) == 0 {
		return Detail{}, ErrNotFound
	}
	out := Detail{SlowQuery: qs[0], Plan: []PlanNode{}, NoAdviceReason: noAdviceYet}
	source, nodes := e.plan(ctx, s, qs[0].SQL)
	out.PlanSource = source
	if nodes != nil {
		out.Plan = nodes
	}
	return out, nil
}

// readQueries expects (id, text, calls, mean_ms, share_pct) rows.
func readQueries(ctx context.Context, s driver.Session, d driver.Dialect, sql string, args []any) (_ []SlowQuery, err error) {
	rows, err := s.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("advisor: read statistics: %w", err)
	}
	defer func() { err = errors.Join(err, closeRows(rows)) }()

	vals := make([]any, len(rows.Columns()))
	if len(vals) != 5 {
		return nil, fmt.Errorf("advisor: read statistics: got %d fields, want 5", len(vals))
	}
	out := []SlowQuery{}
	for rows.Next() {
		if err := rows.Values(vals); err != nil {
			return nil, fmt.Errorf("advisor: read statistics: %w", err)
		}
		text := str(vals[1])
		verb, tables := Describe(text, sqlscan.Dialect(d))
		out = append(out, SlowQuery{
			ID: str(vals[0]), Title: title(verb, tables), SQL: text, Tables: tables,
			MeanMs: round(num(vals[3]), 3), Calls: int64(num(vals[2])), SharePct: round(num(vals[4]), 1),
			State: StateNotAnalysed,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("advisor: read statistics: %w", err)
	}
	return out, nil
}

// one runs a query that returns a single row and returns its values, or nil
// when there is no row.
func one(ctx context.Context, s driver.Session, sql string) (_ []any, err error) {
	rows, err := s.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if !rows.Next() {
		return nil, rows.Err()
	}
	vals := make([]any, len(rows.Columns()))
	if err := rows.Values(vals); err != nil {
		return nil, err
	}
	return vals, nil
}

func title(verb string, tables []string) string {
	if verb == "" {
		verb = "Statement"
	}
	switch n := len(tables); {
	case n == 0:
		return verb
	case n <= 3:
		return verb + " on " + strings.Join(tables, ", ")
	default:
		return fmt.Sprintf("%s on %s, +%d more", verb, strings.Join(tables[:3], ", "), n-3)
	}
}

// likePattern turns search text into a LIKE pattern that matches it
// anywhere, with the wildcard characters escaped by a backslash.
func likePattern(search string) string {
	if search == "" {
		return ""
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(strings.ToLower(search)) + "%"
}

func closeSession(s driver.Session) error {
	if err := s.Close(); err != nil {
		return fmt.Errorf("advisor: %w", err)
	}
	return nil
}

func closeRows(r driver.Rows) error {
	if err := r.Close(); err != nil {
		return fmt.Errorf("advisor: %w", err)
	}
	return nil
}

// str renders a normalized value as text.
func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return fmt.Sprint(x)
	}
}

// num reads numbers that engines return as integer, float, or text.
func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	case int:
		return float64(x)
	case uint64:
		return float64(x)
	case bool:
		if x {
			return 1
		}
		return 0
	case nil:
		return 0
	default:
		f, err := strconv.ParseFloat(str(x), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0
		}
		return f
	}
}

func round(f float64, places int) float64 {
	p := math.Pow10(places)
	return math.Round(f*p) / p
}

// rfc3339 re-renders a normalized timestamp, or returns "" when v is not one.
func rfc3339(v any) string {
	t, err := time.Parse(time.RFC3339Nano, str(v))
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
