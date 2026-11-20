// Package locks reads the locks one database session holds, or waits for,
// with what each means in plain words. It reads them from a separate
// session, so it never touches the transaction it reports on and still
// works while that transaction waits.
package locks

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/phankieuphu/lumen/internal/driver"
)

// Errors.
var (
	ErrUnsupported = errors.New("locks: this database has no lock list")
	ErrNoAccess    = errors.New("locks: no access to the lock tables")
	ErrNotEnabled  = errors.New("locks: lock tables are turned off")
)

// MaxLocks caps a report; a session can hold many row locks.
const MaxLocks = 500

// Type groups locks across engines.
type Type string

// Lock types.
const (
	TypeTable       Type = "table"
	TypeRow         Type = "row"
	TypeTransaction Type = "transaction"
	TypeMetadata    Type = "metadata"
	TypeAdvisory    Type = "advisory"
	TypeOther       Type = "other"
)

// Lock is one lock a session holds or waits for.
type Lock struct {
	Type Type `json:"type"`
	// Mode is the engine's name for it, e.g. RowExclusiveLock or X,REC_NOT_GAP.
	Mode string `json:"mode"`
	// Granted is false while the session waits for the lock.
	Granted bool `json:"granted"`
	// Object is what is locked: a table, or a transaction.
	Object string `json:"object"`
	// Detail narrows it down: the index and row key, or the page and tuple.
	Detail string `json:"detail,omitempty"`
	// Meaning says what the lock is for and what it blocks.
	Meaning string `json:"meaning"`
}

// Report is the locks of one session.
type Report struct {
	Locks []Lock `json:"locks"`
	// Waiting is set when the session waits for a lock.
	Waiting bool `json:"waiting"`
	// Note explains what the list can and cannot show on this database.
	Note      string `json:"note"`
	Truncated bool   `json:"truncated"`
}

// SessionID returns the database's ID for session s: the PostgreSQL
// backend pid or the MySQL connection id. Read it on the session to report
// on, then pass it to Held.
func SessionID(ctx context.Context, s driver.Session, d driver.Dialect) (int64, error) {
	var sql string
	switch d {
	case driver.Postgres:
		sql = `SELECT pg_backend_pid()`
	case driver.MySQL:
		sql = `SELECT CONNECTION_ID()`
	default:
		return 0, ErrUnsupported
	}
	rows, err := query(ctx, s, sql)
	if err != nil {
		return 0, fmt.Errorf("locks: read the session id: %w", err)
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return 0, errors.New("locks: read the session id: no row")
	}
	id, err := strconv.ParseInt(text(rows[0][0]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("locks: read the session id: %w", err)
	}
	return id, nil
}

// Held returns the locks session id holds or waits for, read from a new
// session of c.
func Held(ctx context.Context, c driver.Conn, id int64) (_ Report, err error) {
	var read func(context.Context, driver.Session, int64) (Report, error)
	switch c.Dialect() {
	case driver.Postgres:
		read = postgresLocks
	case driver.MySQL:
		read = mysqlLocks
	default:
		return Report{}, ErrUnsupported
	}
	s, err := c.Session(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("locks: %w", err)
	}
	defer func() {
		if cerr := s.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("locks: close session: %w", cerr)
		}
	}()
	r, err := read(ctx, s, id)
	if err != nil {
		return Report{}, err
	}
	if len(r.Locks) > MaxLocks {
		r.Locks, r.Truncated = r.Locks[:MaxLocks], true
	}
	for _, l := range r.Locks {
		if !l.Granted {
			r.Waiting = true
		}
	}
	if r.Locks == nil {
		r.Locks = []Lock{}
	}
	return r, nil
}

// query returns every row of sql.
func query(ctx context.Context, s driver.Session, sql string, args ...any) (_ [][]any, err error) {
	rows, err := s.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var out [][]any
	for rows.Next() {
		vals := make([]any, len(rows.Columns()))
		if err := rows.Values(vals); err != nil {
			return nil, err
		}
		out = append(out, vals)
		if len(out) > MaxLocks {
			break
		}
	}
	return out, rows.Err()
}

func text(v any) string {
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

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	}
	switch text(v) {
	case "1", "t", "true", "GRANTED":
		return true
	}
	return false
}
