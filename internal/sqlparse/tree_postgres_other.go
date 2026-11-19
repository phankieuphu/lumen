//go:build !cgo || windows

package sqlparse

// postgresTrees reports whether the PostgreSQL adapter is built in: it needs
// libpg_query, and so CGO.
const postgresTrees = false

func parsePostgres(string, int) (*Stmt, bool) { return nil, false }
