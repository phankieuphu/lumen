// Package driver defines the engine-neutral interfaces every database engine
// implements, and a registry engines add themselves to.
//
// Core packages depend only on these interfaces; engine packages live in
// internal/driver/<engine> and register from init.
package driver

import (
	"context"
	"errors"
	"time"
)

// Dialect names a database engine.
type Dialect string

// Supported dialects.
const (
	Postgres Dialect = "postgres"
	MySQL    Dialect = "mysql"
	SQLite   Dialect = "sqlite"
)

// Sentinel errors shared by all engines.
var (
	ErrUnknownDialect = errors.New("driver: unknown dialect")
	ErrClosed         = errors.New("driver: connection closed")
	// ErrMultipleStatements is returned when one call carries more than one
	// statement. Engines reject these so a single request cannot smuggle a
	// second statement past the classifier.
	ErrMultipleStatements = errors.New("driver: only one statement per request is allowed")
)

// Driver opens connections for one engine.
type Driver interface {
	Dialect() Dialect
	// Open creates a pooled connection and verifies it with a ping.
	Open(ctx context.Context, cfg Config) (Conn, error)
}

// Conn is a pooled handle to one database. It is safe for concurrent use.
type Conn interface {
	Dialect() Dialect
	// Session pins one physical connection from the pool. The caller must
	// Close it to return the connection.
	Session(ctx context.Context) (Session, error)
	Ping(ctx context.Context) error
	Stats() PoolStats
	Close() error
}

// Session is one physical connection. It is not safe for concurrent use,
// except Cancel, which may be called from another goroutine while a
// statement runs.
type Session interface {
	// Query runs a statement that returns rows. The SQL is executed exactly
	// as given; engines must not allow more than one statement.
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	// Exec runs a statement that does not return rows.
	Exec(ctx context.Context, sql string, args ...any) (Result, error)
	// Cancel asks the server to stop the statement currently running on this
	// session. It is best effort and returns nil when nothing is running.
	Cancel(ctx context.Context) error
	// Close returns the connection to the pool.
	Close() error
}

// Rows is a forward-only cursor over a result set.
type Rows interface {
	Columns() []Column
	Next() bool
	// Values decodes the current row into dst, which has len(Columns()).
	// Values are normalized with Normalize so they are safe to serialize.
	// Reusing dst across rows avoids per-row allocation of the slice.
	Values(dst []any) error
	Err() error
	Close() error
	// RowsAffected is valid after Close for statements that report it
	// (e.g. INSERT ... RETURNING); otherwise it returns -1.
	RowsAffected() int64
}

// Result describes a statement that returned no rows.
type Result struct {
	RowsAffected int64
}

// Column describes one result column.
type Column struct {
	Name string `json:"name"`
	// Type is the engine's type name, e.g. "int4", "VARCHAR", "TEXT".
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

// PoolStats is a snapshot of pool usage.
type PoolStats struct {
	Open  int `json:"open"`
	InUse int `json:"inUse"`
	Idle  int `json:"idle"`
	Max   int `json:"max"`
}

// TLSMode controls transport encryption.
type TLSMode string

// TLS modes, ordered from weakest to strongest.
const (
	TLSDisable    TLSMode = "disable"
	TLSPrefer     TLSMode = "prefer"
	TLSRequire    TLSMode = "require"
	TLSVerifyFull TLSMode = "verify-full"
)

// Config holds everything needed to open a connection. Zero values select
// safe defaults; see each field.
type Config struct {
	Host string
	// Port defaults to the engine's standard port.
	Port     int
	User     string
	Password string //nolint:gosec // never logged; see Redacted
	// Database is the database name, or the file path for SQLite.
	Database string
	// TLS defaults to TLSPrefer for network engines.
	TLS TLSMode
	// ReadOnly makes the engine reject writes at the session level, in
	// addition to the classifier check in package query.
	ReadOnly bool
	// MaxConns caps the pool. Defaults to 4.
	MaxConns int
	// ConnectTimeout defaults to 10s.
	ConnectTimeout time.Duration
	// AppName is reported to the server where supported. Defaults to "lumen".
	AppName string
}

// Defaults for Config.
const (
	DefaultMaxConns       = 4
	DefaultConnectTimeout = 10 * time.Second
	DefaultAppName        = "lumen"
)

// WithDefaults returns a copy of c with zero fields set to their defaults.
func (c Config) WithDefaults() Config {
	if c.MaxConns <= 0 {
		c.MaxConns = DefaultMaxConns
	}
	if c.ConnectTimeout <= 0 {
		c.ConnectTimeout = DefaultConnectTimeout
	}
	if c.AppName == "" {
		c.AppName = DefaultAppName
	}
	if c.TLS == "" {
		c.TLS = TLSPrefer
	}
	return c
}

// Redacted returns a copy safe to log: the password is masked.
func (c Config) Redacted() Config {
	if c.Password != "" {
		c.Password = "***"
	}
	return c
}
