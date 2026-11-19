//go:build !cgo || windows

package sqlparse

// postgresChecked reports whether PostgreSQL syntax is checked in this build.
const postgresChecked = false

// checkPostgres checks nothing: libpg_query needs CGO, which Windows builds
// do without (docs/plan.md: a pure Go parser if Windows needs one).
func checkPostgres(string) *failure { return nil }
