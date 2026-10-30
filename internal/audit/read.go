package audit

import (
	"context"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/patrick-rontech/kiemdb/internal/store"
)

// Filter selects entries. Zero fields match everything.
type Filter struct {
	ConnID string
	Since  time.Time
	Until  time.Time
	// AfterSeq returns entries with seq > AfterSeq, for paging forward.
	AfterSeq int64
	// Limit caps the number of entries; 0 means no limit (export/verify).
	Limit int
}

// MaxPage is the largest Limit List accepts.
const MaxPage = 1000

// List returns entries matching f in sequence order. Limit is clamped to
// [1, MaxPage]; page by passing the last Seq as AfterSeq.
func (l *Log) List(ctx context.Context, f Filter) ([]Entry, error) {
	if f.Limit <= 0 || f.Limit > MaxPage {
		f.Limit = MaxPage
	}
	out := make([]Entry, 0, min(f.Limit, 64))
	err := l.scan(ctx, f, func(e Entry) error {
		out = append(out, e)
		return nil
	})
	return out, err
}

// scan streams matching entries to fn in sequence order without holding
// them all in memory.
func (l *Log) scan(ctx context.Context, f Filter, fn func(Entry) error) error {
	var (
		where []string
		args  []any
	)
	if f.ConnID != "" {
		where, args = append(where, "conn_id = ?"), append(args, f.ConnID)
	}
	if !f.Since.IsZero() {
		where, args = append(where, "at >= ?"), append(args, store.FormatTime(f.Since))
	}
	if !f.Until.IsZero() {
		where, args = append(where, "at < ?"), append(args, store.FormatTime(f.Until))
	}
	if f.AfterSeq > 0 {
		where, args = append(where, "seq > ?"), append(args, f.AfterSeq)
	}
	q := `SELECT seq, at, conn_id, conn_name, dialect, env, kind, status, rows_affected, duration_ms,
		sql_text, error, prev_hash, hash FROM audit_log`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ") //nolint:gosec // fixed fragments only; values are bound
	}
	q += " ORDER BY seq"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("audit: query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			e      Entry
			at     string
			status string
			durMs  int64
		)
		if err := rows.Scan(&e.Seq, &at, &e.ConnID, &e.ConnName, &e.Dialect, &e.Env, &e.Kind, &status,
			&e.RowsAffected, &durMs, &e.SQL, &e.Error, &e.PrevHash, &e.Hash); err != nil {
			return fmt.Errorf("audit: scan: %w", err)
		}
		if e.At, err = store.ParseTime(at); err != nil {
			return fmt.Errorf("audit: seq %d: %w", e.Seq, err)
		}
		e.Status = Status(status)
		e.Duration = time.Duration(durMs) * time.Millisecond
		if err := fn(e); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("audit: rows: %w", err)
	}
	return nil
}

// Format is an export format.
type Format string

// Export formats.
const (
	CSV  Format = "csv"
	JSON Format = "json"
)

var csvHeader = []string{
	"seq", "at", "conn_id", "conn_name", "dialect", "env", "kind", "status",
	"rows_affected", "duration_ms", "sql", "error", "prev_hash", "hash",
}

// Export streams matching entries to w. JSON is a single array; CSV has a
// header row. Hashes are hex-encoded.
func (l *Log) Export(ctx context.Context, w io.Writer, format Format, f Filter) error {
	switch format {
	case CSV:
		return l.exportCSV(ctx, w, f)
	case JSON:
		return l.exportJSON(ctx, w, f)
	default:
		return fmt.Errorf("audit: unknown export format %q", format)
	}
}

func (l *Log) exportCSV(ctx context.Context, w io.Writer, f Filter) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(csvHeader); err != nil {
		return fmt.Errorf("audit: export: %w", err)
	}
	rec := make([]string, len(csvHeader))
	err := l.scan(ctx, f, func(e Entry) error {
		rec[0] = strconv.FormatInt(e.Seq, 10)
		rec[1] = store.FormatTime(e.At)
		rec[2], rec[3], rec[4], rec[5], rec[6], rec[7] = e.ConnID, e.ConnName, e.Dialect, e.Env, e.Kind, string(e.Status)
		rec[8] = strconv.FormatInt(e.RowsAffected, 10)
		rec[9] = strconv.FormatInt(e.Duration.Milliseconds(), 10)
		rec[10], rec[11] = csvSafe(e.SQL), csvSafe(e.Error)
		rec[12], rec[13] = hex.EncodeToString(e.PrevHash), hex.EncodeToString(e.Hash)
		return cw.Write(rec)
	})
	if err != nil {
		return err
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("audit: export: %w", err)
	}
	return nil
}

// csvSafe defuses spreadsheet formula injection: a cell starting with
// = + - @ is prefixed with a quote so Excel shows it as text.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

type jsonEntry struct {
	Seq          int64  `json:"seq"`
	At           string `json:"at"`
	ConnID       string `json:"connId"`
	ConnName     string `json:"connName"`
	Dialect      string `json:"dialect"`
	Env          string `json:"env"`
	Kind         string `json:"kind"`
	Status       Status `json:"status"`
	RowsAffected int64  `json:"rowsAffected"`
	DurationMs   int64  `json:"durationMs"`
	SQL          string `json:"sql"`
	Error        string `json:"error,omitempty"`
	PrevHash     string `json:"prevHash"`
	Hash         string `json:"hash"`
}

func (l *Log) exportJSON(ctx context.Context, w io.Writer, f Filter) error {
	if _, err := io.WriteString(w, "["); err != nil {
		return fmt.Errorf("audit: export: %w", err)
	}
	enc := json.NewEncoder(w)
	first := true
	err := l.scan(ctx, f, func(e Entry) error {
		if !first {
			if _, err := io.WriteString(w, ","); err != nil {
				return fmt.Errorf("audit: export: %w", err)
			}
		}
		first = false
		return enc.Encode(jsonEntry{
			Seq: e.Seq, At: store.FormatTime(e.At), ConnID: e.ConnID, ConnName: e.ConnName,
			Dialect: e.Dialect, Env: e.Env, Kind: e.Kind, Status: e.Status, RowsAffected: e.RowsAffected,
			DurationMs: e.Duration.Milliseconds(), SQL: e.SQL, Error: e.Error,
			PrevHash: hex.EncodeToString(e.PrevHash), Hash: hex.EncodeToString(e.Hash),
		})
	})
	if err != nil {
		return fmt.Errorf("audit: export: %w", err)
	}
	if _, err := io.WriteString(w, "]\n"); err != nil {
		return fmt.Errorf("audit: export: %w", err)
	}
	return nil
}
