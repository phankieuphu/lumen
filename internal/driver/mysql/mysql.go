// Package mysql implements driver.Driver for MySQL and MariaDB.
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/patrick-rontech/kiemdb/internal/driver"
	"github.com/patrick-rontech/kiemdb/internal/driver/sqladapter"
)

// DefaultPort is MySQL's standard port.
const DefaultPort = 3306

func init() { driver.Register(Driver{}) }

// Driver opens MySQL connections.
type Driver struct{}

// Dialect implements driver.Driver.
func (Driver) Dialect() driver.Dialect { return driver.MySQL }

// Open implements driver.Driver.
func (Driver) Open(ctx context.Context, cfg driver.Config) (driver.Conn, error) {
	cfg = cfg.WithDefaults()
	mc, err := mysqlConfig(cfg)
	if err != nil {
		return nil, err
	}
	connector, err := gomysql.NewConnector(mc)
	if err != nil {
		return nil, errors.New("mysql: invalid connection settings")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(cfg.MaxConns)
	db.SetMaxIdleConns(cfg.MaxConns)
	db.SetConnMaxIdleTime(5 * time.Minute)

	// KILL QUERY goes over its own connection so it never waits for a free
	// slot in a busy pool.
	control := sql.OpenDB(connector)
	control.SetMaxOpenConns(1)
	control.SetMaxIdleConns(0)

	pctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := db.PingContext(pctx); err != nil {
		_ = db.Close()
		_ = control.Close()
		return nil, fmt.Errorf("mysql: ping: %w", err)
	}
	return sqladapter.New(db, driver.MySQL, cfg.MaxConns, sqladapter.Options{
		NewCanceler: func(ctx context.Context, c *sql.Conn) (func(context.Context) error, error) {
			var id uint64
			if err := c.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&id); err != nil {
				return nil, fmt.Errorf("connection id: %w", err)
			}
			// id comes from the server as an integer; formatting it cannot inject SQL.
			kill := "KILL QUERY " + strconv.FormatUint(id, 10) //nolint:gosec // integer, not user input
			return func(ctx context.Context) error {
				if _, err := control.ExecContext(ctx, kill); err != nil {
					return fmt.Errorf("kill query: %w", err)
				}
				return nil
			}, nil
		},
		OnClose: control.Close,
	}), nil
}

func mysqlConfig(cfg driver.Config) (*gomysql.Config, error) {
	port := cfg.Port
	if port == 0 {
		port = DefaultPort
	}
	mc := gomysql.NewConfig()
	mc.User = cfg.User
	mc.Passwd = cfg.Password
	mc.Net = "tcp"
	mc.Addr = net.JoinHostPort(cfg.Host, strconv.Itoa(port))
	mc.DBName = cfg.Database
	mc.Timeout = cfg.ConnectTimeout
	// Security: one statement per call, no client-side interpolation, no
	// LOAD DATA LOCAL INFILE from arbitrary paths.
	mc.MultiStatements = false
	mc.InterpolateParams = false
	mc.AllowAllFiles = false
	// Show DATE/DATETIME exactly as the server stores them.
	mc.ParseTime = false
	mc.ConnectionAttributes = "program_name:" + cfg.AppName

	switch cfg.TLS {
	case driver.TLSDisable:
		mc.TLSConfig = "false"
	case driver.TLSPrefer:
		mc.TLSConfig = "preferred"
	case driver.TLSRequire:
		mc.TLSConfig = "skip-verify"
	case driver.TLSVerifyFull:
		mc.TLSConfig = "true"
	default:
		return nil, fmt.Errorf("mysql: unsupported TLS mode %q", cfg.TLS)
	}
	if cfg.ReadOnly {
		mc.Params = map[string]string{"transaction_read_only": "1"}
	}
	return mc, nil
}
