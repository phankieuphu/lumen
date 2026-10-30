package query

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/patrick-rontech/kiemdb/internal/audit"
	"github.com/patrick-rontech/kiemdb/internal/driver"
	"github.com/patrick-rontech/kiemdb/internal/sqlscan"
	"github.com/patrick-rontech/kiemdb/internal/store"
)

// History limits.
const (
	maxHistorySQL   = 16 << 10
	maxHistoryError = 2 << 10
	MaxHistoryPage  = 500
)

// HistoryEntry is one past request.
type HistoryEntry struct {
	ID        int64
	ConnID    string
	Dialect   driver.Dialect // used for redaction; not stored
	StartedAt time.Time
	Duration  time.Duration
	Kind      Kind
	Status    audit.Status
	RowCount  int64
	SQL       string
	Error     string
}

// History stores the query history in the local store.
type History struct{ db *sql.DB }

// NewHistory returns a History over the store.
func NewHistory(s *store.Store) *History { return &History{db: s.DB()} }

// Add records e. SQL is redacted of password literals and capped.
func (h *History) Add(ctx context.Context, e HistoryEntry) error {
	q := clip(sqlscan.RedactSecrets(e.SQL, sqlscan.Dialect(e.Dialect)), maxHistorySQL)
	_, err := h.db.ExecContext(ctx, `INSERT INTO query_history
		(conn_id, started_at, duration_ms, kind, status, row_count, sql_text, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ConnID, store.FormatTime(e.StartedAt), e.Duration.Milliseconds(), string(e.Kind), string(e.Status),
		e.RowCount, q, clip(e.Error, maxHistoryError))
	if err != nil {
		return fmt.Errorf("query: history add: %w", err)
	}
	return nil
}

// List returns the newest entries for a connection (all connections when
// connID is empty), newest first. Pass the last ID as beforeID for the next
// page; 0 starts from the newest.
func (h *History) List(ctx context.Context, connID string, beforeID int64, limit int) ([]HistoryEntry, error) {
	if limit <= 0 || limit > MaxHistoryPage {
		limit = MaxHistoryPage
	}
	if beforeID <= 0 {
		beforeID = 1<<63 - 1
	}
	rows, err := h.db.QueryContext(ctx, `SELECT id, conn_id, started_at, duration_ms, kind, status, row_count,
		sql_text, error FROM query_history
		WHERE (? = '' OR conn_id = ?) AND id < ? ORDER BY id DESC LIMIT ?`,
		connID, connID, beforeID, limit)
	if err != nil {
		return nil, fmt.Errorf("query: history list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]HistoryEntry, 0, min(limit, 64))
	for rows.Next() {
		var (
			e                HistoryEntry
			at, kind, status string
			durMs            int64
		)
		if err := rows.Scan(&e.ID, &e.ConnID, &at, &durMs, &kind, &status, &e.RowCount, &e.SQL, &e.Error); err != nil {
			return nil, fmt.Errorf("query: history scan: %w", err)
		}
		if e.StartedAt, err = store.ParseTime(at); err != nil {
			return nil, err
		}
		e.Duration = time.Duration(durMs) * time.Millisecond
		e.Kind, e.Status = Kind(kind), audit.Status(status)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query: history list: %w", err)
	}
	return out, nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
