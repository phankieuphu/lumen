//go:build !cgo || windows

package sqlparse

// checkPostgres checks nothing: libpg_query needs CGO, which Windows builds
// do without (docs/plan.md: a pure Go parser if Windows needs one).
func checkPostgres(string) *failure { return nil }
