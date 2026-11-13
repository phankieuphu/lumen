package advisor

import (
	"context"
	"fmt"

	"github.com/phankieuphu/lumen/internal/driver"
)

const myDigestTable = "performance_schema.events_statements_summary_by_digest"

// myCheck verifies performance_schema is on and readable. information_schema
// lists only tables the user holds a privilege on, so a missing row means
// missing access; that needs no engine error codes.
func myCheck(ctx context.Context, s driver.Session) error {
	vals, err := one(ctx, s, `SELECT @@performance_schema`)
	if err != nil {
		return fmt.Errorf("advisor: check performance_schema: %w", err)
	}
	if len(vals) == 0 || num(vals[0]) == 0 {
		return fmt.Errorf("%w: start the server with performance_schema=ON", ErrNotEnabled)
	}
	vals, err = one(ctx, s, `SELECT COUNT(*) FROM information_schema.TABLES
WHERE TABLE_SCHEMA = 'performance_schema' AND TABLE_NAME = 'events_statements_summary_by_digest'`)
	if err != nil {
		return fmt.Errorf("advisor: check performance_schema: %w", err)
	}
	if len(vals) == 0 || num(vals[0]) == 0 {
		return fmt.Errorf("%w: ask an administrator to run GRANT SELECT ON %s TO your user", ErrNoAccess, myDigestTable)
	}
	return nil
}

// myOrder maps a sort to a fixed ORDER BY; the text never comes from input.
var myOrder = map[Sort]string{
	SortShare: "d.total_ps DESC, d.DIGEST",
	SortMean:  "d.total_ps / d.calls DESC, d.DIGEST",
	SortCalls: "d.calls DESC, d.DIGEST",
}

// myScope keeps statements of the connection's database, or of every
// database when the profile names none.
const myScope = `DIGEST IS NOT NULL AND SCHEMA_NAME <=> COALESCE(DATABASE(), SCHEMA_NAME)`

// myList reads one row per statement digest. Timers are picoseconds; the
// division by the double 1e9 gives milliseconds as a double. The advisor's
// own reads are left out.
func myList(f Filter, id string) (string, []any) {
	sql := `SELECT d.DIGEST, d.DIGEST_TEXT, CAST(d.calls AS SIGNED), d.total_ps / d.calls / 1e9,
       COALESCE(100 * d.total_ps / NULLIF(t.total_ps, 0), 0) + 0e0
FROM (SELECT DIGEST, MAX(DIGEST_TEXT) AS DIGEST_TEXT, SUM(COUNT_STAR) AS calls,
             SUM(SUM_TIMER_WAIT) AS total_ps, MAX(LAST_SEEN) AS last_seen
      FROM ` + myDigestTable + `
      WHERE ` + myScope + `
      GROUP BY DIGEST) d
CROSS JOIN (SELECT SUM(SUM_TIMER_WAIT) AS total_ps
            FROM ` + myDigestTable + `
            WHERE ` + myScope + `) t
WHERE d.calls > 0
  AND d.DIGEST_TEXT IS NOT NULL
  AND d.DIGEST_TEXT NOT LIKE '%performance\_schema%'
  AND (? = '' OR d.DIGEST = ?)
  AND (? <> '' OR (
        d.total_ps / d.calls / 1e9 >= ?
    AND d.calls >= ?
    AND (? = '' OR LOWER(d.DIGEST_TEXT) LIKE ? ESCAPE '\\')
    AND (? = '' OR LOWER(LTRIM(d.DIGEST_TEXT)) LIKE CONCAT(?, '%'))
    AND (? = 0 OR d.last_seen >= NOW() - INTERVAL ? HOUR)))
ORDER BY ` + myOrder[f.Sort] + `
LIMIT ?`
	pat, kind := likePattern(f.Search), string(f.Kind)
	return sql, []any{id, id, id, f.MinMeanMs, f.MinCalls, pat, pat, kind, kind, f.WindowHours, f.WindowHours, f.Limit}
}

// myPlan gives no plan: the digest text has "?" in place of every literal,
// and MySQL cannot plan that. A plan needs a sample statement run through
// the Explain feature of phase 2.
func myPlan(context.Context, driver.Session, string) (string, []PlanNode) {
	return "No plan yet: MySQL keeps statements without their values, so a plan needs the Explain feature (phase 2)", nil
}
