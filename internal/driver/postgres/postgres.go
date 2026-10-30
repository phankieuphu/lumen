// Package postgres implements driver.Driver for PostgreSQL using pgx.
package postgres

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/patrick-rontech/kiemdb/internal/driver"
)

// DefaultPort is PostgreSQL's standard port.
const DefaultPort = 5432

// cancelDeadline is how long a cancelled statement may take to stop after the
// cancel request before the connection is torn down.
const cancelDeadline = 5 * time.Second

func init() { driver.Register(Driver{}) }

// Driver opens PostgreSQL connections.
type Driver struct{}

// Dialect implements driver.Driver.
func (Driver) Dialect() driver.Dialect { return driver.Postgres }

// Open implements driver.Driver.
func (Driver) Open(ctx context.Context, cfg driver.Config) (driver.Conn, error) {
	cfg = cfg.WithDefaults()
	pcfg, err := poolConfig(cfg)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: open: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return &conn{pool: pool}, nil
}

// poolConfig builds a pgx pool config from cfg. It goes through a URL so pgx
// applies its own sslmode handling, and so that no field can inject extra
// connection parameters.
func poolConfig(cfg driver.Config) (*pgxpool.Config, error) {
	port := cfg.Port
	if port == 0 {
		port = DefaultPort
	}
	u := url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(cfg.Host, strconv.Itoa(port)),
		Path:   "/" + cfg.Database,
	}
	if cfg.Password != "" {
		u.User = url.UserPassword(cfg.User, cfg.Password)
	} else if cfg.User != "" {
		u.User = url.User(cfg.User)
	}
	q := url.Values{}
	q.Set("sslmode", string(cfg.TLS))
	q.Set("application_name", cfg.AppName)
	u.RawQuery = q.Encode()

	pcfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		// pgx errors can echo the connection string; never return it.
		return nil, errors.New("postgres: invalid connection settings")
	}
	pcfg.MaxConns = int32(min(cfg.MaxConns, 1<<16)) //nolint:gosec // bounded above
	pcfg.MaxConnIdleTime = 5 * time.Minute
	pcfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	// Describe-only caching: no server-side prepared statements pile up from
	// ad-hoc user SQL, and the extended protocol still rejects multiple
	// statements in one call.
	pcfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe
	if cfg.ReadOnly {
		pcfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	}
	// On context cancel, ask the server to cancel first so the connection
	// survives; fall back to a socket deadline.
	pcfg.ConnConfig.BuildContextWatcherHandler = func(pc *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: pc, DeadlineDelay: cancelDeadline}
	}
	return pcfg, nil
}

type conn struct {
	pool *pgxpool.Pool
}

func (c *conn) Dialect() driver.Dialect { return driver.Postgres }

func (c *conn) Session(ctx context.Context) (driver.Session, error) {
	pc, err := c.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: acquire: %w", err)
	}
	return &session{pc: pc}, nil
}

func (c *conn) Ping(ctx context.Context) error {
	if err := c.pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres: ping: %w", err)
	}
	return nil
}

func (c *conn) Stats() driver.PoolStats {
	s := c.pool.Stat()
	return driver.PoolStats{
		Open:  int(s.TotalConns()),
		InUse: int(s.AcquiredConns()),
		Idle:  int(s.IdleConns()),
		Max:   int(s.MaxConns()),
	}
}

func (c *conn) Close() error {
	c.pool.Close()
	return nil
}

type session struct {
	pc      *pgxpool.Conn
	running atomic.Bool
}

func (s *session) Query(ctx context.Context, sql string, args ...any) (driver.Rows, error) {
	s.running.Store(true)
	r, err := s.pc.Query(ctx, sql, args...)
	if err != nil {
		s.running.Store(false)
		return nil, fmt.Errorf("postgres: query: %w", err)
	}
	return newRows(r, s.pc.Conn().TypeMap(), &s.running), nil
}

func (s *session) Exec(ctx context.Context, sql string, args ...any) (driver.Result, error) {
	s.running.Store(true)
	defer s.running.Store(false)
	var (
		tag pgconn.CommandTag
		err error
	)
	if len(args) == 0 {
		// pgx switches Exec to the simple protocol when there are no
		// arguments, and the simple protocol runs multiple statements.
		// ExecParams keeps the extended protocol: one statement only.
		tag, err = s.pc.Conn().PgConn().ExecParams(ctx, sql, nil, nil, nil, nil).Close()
	} else {
		tag, err = s.pc.Exec(ctx, sql, args...)
	}
	if err != nil {
		return driver.Result{}, fmt.Errorf("postgres: exec: %w", err)
	}
	return driver.Result{RowsAffected: tag.RowsAffected()}, nil
}

func (s *session) Cancel(ctx context.Context) error {
	if !s.running.Load() {
		return nil
	}
	if err := s.pc.Conn().PgConn().CancelRequest(ctx); err != nil {
		return fmt.Errorf("postgres: cancel: %w", err)
	}
	return nil
}

func (s *session) Close() error {
	s.pc.Release()
	return nil
}

type rows struct {
	r        pgx.Rows
	cols     []driver.Column
	oids     []uint32
	running  *atomic.Bool
	affected int64
	closed   bool
}

func newRows(r pgx.Rows, tm *pgtype.Map, running *atomic.Bool) *rows {
	fds := r.FieldDescriptions()
	cols := make([]driver.Column, len(fds))
	oids := make([]uint32, len(fds))
	for i, fd := range fds {
		name := strconv.FormatUint(uint64(fd.DataTypeOID), 10)
		if t, ok := tm.TypeForOID(fd.DataTypeOID); ok {
			name = t.Name
		}
		cols[i] = driver.Column{Name: fd.Name, Type: name, Nullable: true}
		oids[i] = fd.DataTypeOID
	}
	return &rows{r: r, cols: cols, oids: oids, running: running, affected: -1}
}

func (r *rows) Columns() []driver.Column { return r.cols }
func (r *rows) Next() bool               { return r.r.Next() }

func (r *rows) Values(dst []any) error {
	vals, err := r.r.Values()
	if err != nil {
		return fmt.Errorf("postgres: decode row: %w", err)
	}
	for i, v := range vals {
		dst[i] = normalize(r.oids[i], v)
	}
	return nil
}

func (r *rows) Err() error {
	if err := r.r.Err(); err != nil {
		return fmt.Errorf("postgres: rows: %w", err)
	}
	return nil
}

func (r *rows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	r.r.Close()
	r.running.Store(false)
	r.affected = r.r.CommandTag().RowsAffected()
	return r.Err()
}

func (r *rows) RowsAffected() int64 { return r.affected }

// normalize handles PostgreSQL types that driver.Normalize cannot render well.
func normalize(oid uint32, v any) any {
	switch x := v.(type) {
	case [16]byte:
		if oid == pgtype.UUIDOID {
			return formatUUID(x)
		}
	case pgtype.Numeric:
		// Keep full precision: numeric goes to the UI as a string.
		if !x.Valid {
			return nil
		}
		if x.NaN {
			return "NaN"
		}
		if x.InfinityModifier != pgtype.Finite {
			return x.InfinityModifier.String()
		}
		if s, err := x.Value(); err == nil {
			return s
		}
	}
	return driver.Normalize(v)
}

func formatUUID(b [16]byte) string {
	var buf [36]byte
	hex.Encode(buf[0:8], b[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], b[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], b[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], b[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:], b[10:16])
	return string(buf[:])
}
