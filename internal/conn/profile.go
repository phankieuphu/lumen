// Package conn manages connection profiles and their pools. Profiles live in
// the local store; passwords live in the OS keychain; pools are opened on
// first use and cached until the profile changes or the app closes.
package conn

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/patrick-rontech/kiemdb/internal/driver"
	"github.com/patrick-rontech/kiemdb/internal/store"
)

// Env tags a connection by environment. Production gets extra confirmation
// before writes.
type Env string

// Environments.
const (
	EnvDev     Env = "dev"
	EnvStaging Env = "staging"
	EnvProd    Env = "prod"
)

// Errors.
var (
	ErrNotFound  = errors.New("conn: profile not found")
	ErrDuplicate = errors.New("conn: a profile with this name already exists")
	ErrInvalid   = errors.New("conn: invalid profile")
)

// MaxPoolSize is the largest pool a profile may request.
const MaxPoolSize = 64

// Profile is a saved connection. It never holds the password.
type Profile struct {
	ID       string
	Name     string
	Dialect  driver.Dialect
	Host     string
	Port     int
	User     string
	Database string // file path for SQLite
	TLS      driver.TLSMode
	Env      Env
	ReadOnly bool
	// MaxConns caps the pool; 0 uses driver.DefaultMaxConns.
	MaxConns  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate checks p and normalizes empty optional fields.
func (p *Profile) Validate() error {
	p.Name = strings.TrimSpace(p.Name)
	p.Host = strings.TrimSpace(p.Host)
	if p.Env == "" {
		p.Env = EnvDev
	}
	if p.TLS == "" {
		p.TLS = driver.TLSPrefer
	}
	var problems []string
	if p.Name == "" || utf8.RuneCountInString(p.Name) > 100 {
		problems = append(problems, "name must be 1-100 characters")
	}
	switch p.Dialect {
	case driver.Postgres, driver.MySQL:
		if p.Host == "" {
			problems = append(problems, "host is required")
		}
	case driver.SQLite:
		if p.Database == "" {
			problems = append(problems, "database file is required")
		}
	default:
		problems = append(problems, fmt.Sprintf("unknown dialect %q", p.Dialect))
	}
	if p.Port < 0 || p.Port > 65535 {
		problems = append(problems, "port must be 0-65535")
	}
	switch p.Env {
	case EnvDev, EnvStaging, EnvProd:
	default:
		problems = append(problems, fmt.Sprintf("unknown env %q", p.Env))
	}
	switch p.TLS {
	case driver.TLSDisable, driver.TLSPrefer, driver.TLSRequire, driver.TLSVerifyFull:
	default:
		problems = append(problems, fmt.Sprintf("unknown TLS mode %q", p.TLS))
	}
	if p.MaxConns < 0 || p.MaxConns > MaxPoolSize {
		problems = append(problems, fmt.Sprintf("max connections must be 0-%d", MaxPoolSize))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalid, strings.Join(problems, "; "))
	}
	return nil
}

// Config builds the driver config for p with the given password.
func (p Profile) Config(password string) driver.Config {
	return driver.Config{
		Host: p.Host, Port: p.Port, User: p.User, Password: password, Database: p.Database,
		TLS: p.TLS, ReadOnly: p.ReadOnly, MaxConns: p.MaxConns,
	}
}

// repo holds the SQL for the connections table.
type repo struct{ db *sql.DB }

const profileCols = `id, name, dialect, host, port, username, database, tls, env, read_only, max_conns, created_at, updated_at`

func (r repo) insert(ctx context.Context, p Profile) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO connections (`+profileCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, string(p.Dialect), p.Host, p.Port, p.User, p.Database, string(p.TLS), string(p.Env),
		p.ReadOnly, p.MaxConns, store.FormatTime(p.CreatedAt), store.FormatTime(p.UpdatedAt))
	return mapWriteErr(err)
}

func (r repo) update(ctx context.Context, p Profile) error {
	res, err := r.db.ExecContext(ctx, `UPDATE connections SET name = ?, dialect = ?, host = ?, port = ?,
		username = ?, database = ?, tls = ?, env = ?, read_only = ?, max_conns = ?, updated_at = ? WHERE id = ?`,
		p.Name, string(p.Dialect), p.Host, p.Port, p.User, p.Database, string(p.TLS), string(p.Env),
		p.ReadOnly, p.MaxConns, store.FormatTime(p.UpdatedAt), p.ID)
	if err != nil {
		return mapWriteErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r repo) delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM connections WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("conn: delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r repo) get(ctx context.Context, id string) (Profile, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+profileCols+` FROM connections WHERE id = ?`, id)
	p, err := scanProfile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	return p, err
}

func (r repo) list(ctx context.Context) ([]Profile, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+profileCols+` FROM connections ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("conn: list: %w", err)
	}
	defer rows.Close()
	var out []Profile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("conn: list: %w", err)
	}
	return out, nil
}

type scanner interface{ Scan(dest ...any) error }

func scanProfile(s scanner) (Profile, error) {
	var (
		p                    Profile
		dialect, tls, env    string
		createdAt, updatedAt string
	)
	err := s.Scan(&p.ID, &p.Name, &dialect, &p.Host, &p.Port, &p.User, &p.Database, &tls, &env,
		&p.ReadOnly, &p.MaxConns, &createdAt, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Profile{}, err
		}
		return Profile{}, fmt.Errorf("conn: scan: %w", err)
	}
	p.Dialect, p.TLS, p.Env = driver.Dialect(dialect), driver.TLSMode(tls), Env(env)
	if p.CreatedAt, err = store.ParseTime(createdAt); err != nil {
		return Profile{}, err
	}
	if p.UpdatedAt, err = store.ParseTime(updatedAt); err != nil {
		return Profile{}, err
	}
	return p, nil
}

func mapWriteErr(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "UNIQUE constraint failed: connections.name") {
		return ErrDuplicate
	}
	return fmt.Errorf("conn: save: %w", err)
}

func newID() string { return rand.Text() }
