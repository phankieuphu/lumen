package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Setting returns the value stored for key, or ErrNotFound.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: get setting: %w", err)
	}
	return v, nil
}

// SetSetting stores value for key, replacing any existing value.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, Now())
	if err != nil {
		return fmt.Errorf("store: set setting: %w", err)
	}
	return nil
}

// StartAppSession records an app launch and returns its ID.
func (s *Store) StartAppSession(ctx context.Context, appVersion string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO app_sessions (started_at, app_version) VALUES (?, ?)`,
		Now(), appVersion)
	if err != nil {
		return 0, fmt.Errorf("store: start session: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: start session: %w", err)
	}
	return id, nil
}

// EndAppSession records a clean shutdown.
func (s *Store) EndAppSession(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE app_sessions SET ended_at = ? WHERE id = ?`, Now(), id); err != nil {
		return fmt.Errorf("store: end session: %w", err)
	}
	return nil
}
