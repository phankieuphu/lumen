package app

import (
	"encoding/hex"
	"time"
	"unicode/utf16"

	"github.com/phankieuphu/lumen/internal/audit"
	"github.com/phankieuphu/lumen/internal/conn"
	"github.com/phankieuphu/lumen/internal/driver"
	"github.com/phankieuphu/lumen/internal/lint"
	"github.com/phankieuphu/lumen/internal/query"
	"github.com/phankieuphu/lumen/internal/sqlparse"
)

// DTOs are the JSON shapes the frontend sees. Durations are milliseconds and
// times are RFC 3339 strings, so TypeScript needs no special decoding.

// ConnectionInput creates or updates a profile.
type ConnectionInput struct {
	Name     string `json:"name"`
	Dialect  string `json:"dialect"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Database string `json:"database"`
	TLS      string `json:"tls"`
	Env      string `json:"env"`
	ReadOnly bool   `json:"readOnly"`
	MaxConns int    `json:"maxConns"`
	// Password: nil keeps the stored one on update; "" removes it.
	Password *string `json:"password,omitempty"`
}

func (in ConnectionInput) profile() conn.Profile {
	return conn.Profile{
		Name: in.Name, Dialect: driver.Dialect(in.Dialect), Host: in.Host, Port: in.Port, User: in.User,
		Database: in.Database, TLS: driver.TLSMode(in.TLS), Env: conn.Env(in.Env), ReadOnly: in.ReadOnly,
		MaxConns: in.MaxConns,
	}
}

func (in ConnectionInput) passwordOrEmpty() string {
	if in.Password == nil {
		return ""
	}
	return *in.Password
}

// ConnectionDTO is a saved profile. It never carries the password.
type ConnectionDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Dialect   string `json:"dialect"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	Database  string `json:"database"`
	TLS       string `json:"tls"`
	Env       string `json:"env"`
	ReadOnly  bool   `json:"readOnly"`
	MaxConns  int    `json:"maxConns"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func connectionDTO(p conn.Profile) ConnectionDTO {
	return ConnectionDTO{
		ID: p.ID, Name: p.Name, Dialect: string(p.Dialect), Host: p.Host, Port: p.Port, User: p.User,
		Database: p.Database, TLS: string(p.TLS), Env: string(p.Env), ReadOnly: p.ReadOnly, MaxConns: p.MaxConns,
		CreatedAt: timeString(p.CreatedAt), UpdatedAt: timeString(p.UpdatedAt),
	}
}

// RunInput is a query request from the UI.
type RunInput struct {
	RequestID    string `json:"requestId"`
	ConnID       string `json:"connId"`
	SQL          string `json:"sql"`
	PageSize     int    `json:"pageSize"`
	MaxRows      int    `json:"maxRows"`
	ConfirmWrite bool   `json:"confirmWrite"`
	TimeoutMs    int64  `json:"timeoutMs"`
	// TxID runs the statement inside this open transaction.
	TxID string `json:"txId,omitempty"`
}

func (in RunInput) request() query.Request {
	return query.Request{
		ID: in.RequestID, ConnID: in.ConnID, SQL: in.SQL, PageSize: in.PageSize, MaxRows: in.MaxRows,
		ConfirmWrite: in.ConfirmWrite, Timeout: time.Duration(in.TimeoutMs) * time.Millisecond, TxID: in.TxID,
	}
}

// TxDTO is an open transaction.
type TxDTO struct {
	ID         string `json:"id"`
	ConnID     string `json:"connId"`
	Label      string `json:"label,omitempty"`
	StartedAt  string `json:"startedAt"`
	Statements int64  `json:"statements"`
	Writes     int64  `json:"writes"`
}

func txDTO(t query.TxInfo) TxDTO {
	return TxDTO{
		ID: t.ID, ConnID: t.ConnID, Label: t.Label, StartedAt: timeString(t.StartedAt),
		Statements: t.Statements, Writes: t.Writes,
	}
}

// PageDTO is one page of rows.
type PageDTO struct {
	RequestID string          `json:"requestId"`
	Seq       int             `json:"seq"`
	Columns   []driver.Column `json:"columns,omitempty"`
	Rows      [][]any         `json:"rows"`
}

func pageDTO(p query.Page) PageDTO {
	return PageDTO{RequestID: p.RequestID, Seq: p.Seq, Columns: p.Columns, Rows: p.Rows}
}

// SummaryDTO describes a finished request.
type SummaryDTO struct {
	RequestID    string          `json:"requestId"`
	Kind         string          `json:"kind"`
	Status       string          `json:"status"`
	Columns      []driver.Column `json:"columns"`
	RowCount     int64           `json:"rowCount"`
	RowsAffected int64           `json:"rowsAffected"`
	Truncated    bool            `json:"truncated"`
	DurationMs   int64           `json:"durationMs"`
	Error        string          `json:"error,omitempty"`
	TxID         string          `json:"txId,omitempty"`
	TxLabel      string          `json:"txLabel,omitempty"`
	// TxEnd is "commit" or "rollback" when the request ended the transaction.
	TxEnd string `json:"txEnd,omitempty"`
}

func summaryDTO(s query.Summary) SummaryDTO {
	return SummaryDTO{
		RequestID: s.RequestID, Kind: string(s.Kind), Status: string(s.Status), Columns: s.Columns,
		RowCount: s.RowCount, RowsAffected: s.RowsAffected, Truncated: s.Truncated,
		DurationMs: s.Duration.Milliseconds(), Error: s.Error,
		TxID: s.TxID, TxLabel: s.TxLabel, TxEnd: string(s.TxEnd),
	}
}

// HistoryDTO is one past request.
type HistoryDTO struct {
	ID         int64  `json:"id"`
	ConnID     string `json:"connId"`
	StartedAt  string `json:"startedAt"`
	DurationMs int64  `json:"durationMs"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	RowCount   int64  `json:"rowCount"`
	SQL        string `json:"sql"`
	Error      string `json:"error,omitempty"`
	TxID       string `json:"txId,omitempty"`
	TxLabel    string `json:"txLabel,omitempty"`
}

func historyDTO(h query.HistoryEntry) HistoryDTO {
	return HistoryDTO{
		ID: h.ID, ConnID: h.ConnID, StartedAt: timeString(h.StartedAt), DurationMs: h.Duration.Milliseconds(),
		Kind: string(h.Kind), Status: string(h.Status), RowCount: h.RowCount, SQL: h.SQL, Error: h.Error,
		TxID: h.TxID, TxLabel: h.TxLabel,
	}
}

// AuditFilter selects audit entries.
type AuditFilter struct {
	ConnID   string `json:"connId"`
	Since    string `json:"since"` // RFC 3339; empty = no bound
	Until    string `json:"until"`
	AfterSeq int64  `json:"afterSeq"`
	Limit    int    `json:"limit"`
}

func (f AuditFilter) filter() audit.Filter {
	out := audit.Filter{ConnID: f.ConnID, AfterSeq: f.AfterSeq, Limit: f.Limit}
	if t, err := time.Parse(time.RFC3339, f.Since); err == nil {
		out.Since = t
	}
	if t, err := time.Parse(time.RFC3339, f.Until); err == nil {
		out.Until = t
	}
	return out
}

// AuditDTO is one audit entry.
type AuditDTO struct {
	Seq          int64  `json:"seq"`
	At           string `json:"at"`
	ConnID       string `json:"connId"`
	ConnName     string `json:"connName"`
	Dialect      string `json:"dialect"`
	Env          string `json:"env"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	RowsAffected int64  `json:"rowsAffected"`
	DurationMs   int64  `json:"durationMs"`
	SQL          string `json:"sql"`
	Error        string `json:"error,omitempty"`
	TxID         string `json:"txId,omitempty"`
	TxLabel      string `json:"txLabel,omitempty"`
	Hash         string `json:"hash"`
}

func auditDTO(e audit.Entry) AuditDTO {
	return AuditDTO{
		Seq: e.Seq, At: timeString(e.At), ConnID: e.ConnID, ConnName: e.ConnName, Dialect: e.Dialect, Env: e.Env,
		Kind: e.Kind, Status: string(e.Status), RowsAffected: e.RowsAffected, DurationMs: e.Duration.Milliseconds(),
		SQL: e.SQL, Error: e.Error, TxID: e.TxID, TxLabel: e.TxLabel, Hash: hex.EncodeToString(e.Hash),
	}
}

// StatementDTO is where one statement sits in the editor text, in UTF-16
// code units: [From, To).
type StatementDTO struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// statementDTOs converts byte spans of sql, in order, to UTF-16 offsets.
func statementDTOs(sql string, spans []query.Span) []StatementDTO {
	out := make([]StatementDTO, len(spans))
	at := utf16Offsets(sql)
	for i, sp := range spans {
		out[i] = StatementDTO{From: at(sp.Start), To: at(sp.End)}
	}
	return out
}

// SyntaxIssueDTO is a syntax error in the editor text, in UTF-16 code
// units: [From, To).
type SyntaxIssueDTO struct {
	From    int    `json:"from"`
	To      int    `json:"to"`
	Message string `json:"message"`
}

// syntaxIssueDTOs converts issues of sql, in order, to UTF-16 offsets.
func syntaxIssueDTOs(sql string, issues []sqlparse.Issue) []SyntaxIssueDTO {
	out := make([]SyntaxIssueDTO, len(issues))
	at := utf16Offsets(sql)
	for i, is := range issues {
		out[i] = SyntaxIssueDTO{From: at(is.Start), To: at(is.End), Message: is.Message}
	}
	return out
}

// utf16Offsets returns a converter from byte offsets of s to UTF-16 offsets.
// Offsets in non-decreasing order walk s once; one that goes back starts
// over.
func utf16Offsets(s string) func(b int) int {
	pos, units := 0, 0 // a byte offset in s and its UTF-16 offset
	return func(b int) int {
		if b < pos {
			pos, units = 0, 0
		}
		for _, r := range s[pos:b] {
			units += utf16.RuneLen(r)
		}
		pos = b
		return units
	}
}

// SchemaChangedDTO is the payload of EventSchemaChanged.
type SchemaChangedDTO struct {
	ConnID string `json:"connId"`
}

// LintIssueDTO is a lint finding in the editor text, in UTF-16 code units:
// [From, To).
type LintIssueDTO struct {
	From     int    `json:"from"`
	To       int    `json:"to"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// lintIssueDTOs converts findings of sql to UTF-16 offsets.
func lintIssueDTOs(sql string, fs []lint.Finding) []LintIssueDTO {
	out := make([]LintIssueDTO, len(fs))
	at := utf16Offsets(sql)
	for i, f := range fs {
		out[i] = LintIssueDTO{From: at(f.Start), To: at(f.End), Rule: f.Rule, Severity: string(f.Severity), Message: f.Message}
	}
	return out
}

// SavedFileDTO is a file the editor's SQL was saved to.
type SavedFileDTO struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// OpenedFileDTO is a SQL file opened into the editor.
type OpenedFileDTO struct {
	Path string `json:"path"`
	Name string `json:"name"`
	SQL  string `json:"sql"`
}

func timeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
