// Package sqlite implements driver.Driver for SQLite files using the pure-Go
// modernc.org/sqlite engine, so builds need no CGO.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite" // registers "sqlite"

	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/driver/sqladapter"
	"github.com/phankieuphu/lumen/internal/sqlscan"
)

// busyTimeout is how long a statement waits for a lock held by another
// connection or process.
const busyTimeout = 5 * time.Second

func init() { driver.Register(Driver{}) }

// Driver opens SQLite database files.
type Driver struct{}

// Dialect implements driver.Driver.
func (Driver) Dialect() driver.Dialect { return driver.SQLite }

// Open implements driver.Driver. cfg.Database is the file path, or
// ":memory:".
func (Driver) Open(ctx context.Context, cfg driver.Config) (driver.Conn, error) {
	cfg = cfg.WithDefaults()
	if cfg.Database == "" {
		return nil, errors.New("sqlite: database file path is required")
	}
	maxConns := cfg.MaxConns
	if cfg.Database == ":memory:" {
		maxConns = 1 // each connection would otherwise get its own database
	}
	db, err := sql.Open("sqlite", DSN(cfg.Database, cfg.ReadOnly))
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)

	pctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := db.PingContext(pctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: ping: %w", err)
	}
	return sqladapter.New(db, driver.SQLite, maxConns, sqladapter.Options{
		// SQLite runs every statement in a string; allow only one.
		Guard: func(q string) error {
			if sqlscan.Count(q, sqlscan.SQLite) > 1 {
				return driver.ErrMultipleStatements
			}
			return nil
		},
	}), nil
}

// DSN builds a modernc.org/sqlite DSN for path. Read-only opens the file with
// mode=ro and query_only, so writes fail inside the engine.
func DSN(path string, readOnly bool) string {
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()))
	if readOnly {
		q.Set("mode", "ro")
		q.Add("_pragma", "query_only(1)")
	}
	if path == ":memory:" {
		return "file::memory:?" + q.Encode()
	}
	return "file:" + (&url.URL{Path: path}).EscapedPath() + "?" + q.Encode()
}
