package app

import (
	"context"

	"github.com/phankieuphu/lumen/internal/advisor"
	"github.com/phankieuphu/lumen/internal/audit"
	"github.com/phankieuphu/lumen/internal/schema"
)

// API is the set of App methods the UI may call. The shell binds API, not
// App, so lifecycle methods such as Close and SetEmitter are never reachable
// from the web view. Each method forwards to App unchanged.
type API struct{ a *App }

// NewAPI returns the UI-facing view of a.
func NewAPI(a *App) *API { return &API{a: a} }

// RunQuery forwards to App.RunQuery.
func (p *API) RunQuery(ctx context.Context, in RunInput) (SummaryDTO, error) {
	return p.a.RunQuery(ctx, in)
}

// CancelQuery forwards to App.CancelQuery.
func (p *API) CancelQuery(ctx context.Context, requestID string) error {
	return p.a.CancelQuery(ctx, requestID)
}

// BeginTx forwards to App.BeginTx.
func (p *API) BeginTx(ctx context.Context, connID, label string) (TxDTO, error) {
	return p.a.BeginTx(ctx, connID, label)
}

// CommitTx forwards to App.CommitTx.
func (p *API) CommitTx(ctx context.Context, txID string) (SummaryDTO, error) {
	return p.a.CommitTx(ctx, txID)
}

// RollbackTx forwards to App.RollbackTx.
func (p *API) RollbackTx(ctx context.Context, txID string) (SummaryDTO, error) {
	return p.a.RollbackTx(ctx, txID)
}

// Transactions forwards to App.Transactions.
func (p *API) Transactions(ctx context.Context) ([]TxDTO, error) {
	return p.a.Transactions(ctx)
}

// TransactionHistory forwards to App.TransactionHistory.
func (p *API) TransactionHistory(ctx context.Context, txID string) ([]HistoryDTO, error) {
	return p.a.TransactionHistory(ctx, txID)
}

// QueryHistory forwards to App.QueryHistory.
func (p *API) QueryHistory(ctx context.Context, connID string, beforeID int64, limit int) ([]HistoryDTO, error) {
	return p.a.QueryHistory(ctx, connID, beforeID, limit)
}

// Schema forwards to App.Schema.
func (p *API) Schema(ctx context.Context, connID string) (schema.Schema, error) {
	return p.a.Schema(ctx, connID)
}

// AdvisorList forwards to App.AdvisorList.
func (p *API) AdvisorList(ctx context.Context, connID string, f advisor.Filter) (advisor.List, error) {
	return p.a.AdvisorList(ctx, connID, f)
}

// AdvisorDetail forwards to App.AdvisorDetail.
func (p *API) AdvisorDetail(ctx context.Context, connID, id string) (advisor.Detail, error) {
	return p.a.AdvisorDetail(ctx, connID, id)
}

// ListConnections forwards to App.ListConnections.
func (p *API) ListConnections(ctx context.Context) ([]ConnectionDTO, error) {
	return p.a.ListConnections(ctx)
}

// CreateConnection forwards to App.CreateConnection.
func (p *API) CreateConnection(ctx context.Context, in ConnectionInput) (ConnectionDTO, error) {
	return p.a.CreateConnection(ctx, in)
}

// UpdateConnection forwards to App.UpdateConnection.
func (p *API) UpdateConnection(ctx context.Context, id string, in ConnectionInput) (ConnectionDTO, error) {
	return p.a.UpdateConnection(ctx, id, in)
}

// DeleteConnection forwards to App.DeleteConnection.
func (p *API) DeleteConnection(ctx context.Context, id string) error {
	return p.a.DeleteConnection(ctx, id)
}

// TestConnection forwards to App.TestConnection.
func (p *API) TestConnection(ctx context.Context, in ConnectionInput) error {
	return p.a.TestConnection(ctx, in)
}

// AuditList forwards to App.AuditList.
func (p *API) AuditList(ctx context.Context, f AuditFilter) ([]AuditDTO, error) {
	return p.a.AuditList(ctx, f)
}

// AuditVerify forwards to App.AuditVerify.
func (p *API) AuditVerify(ctx context.Context) (audit.VerifyResult, error) {
	return p.a.AuditVerify(ctx)
}

// AuditExport forwards to App.AuditExport.
func (p *API) AuditExport(ctx context.Context, path, format string, f AuditFilter) error {
	return p.a.AuditExport(ctx, path, format, f)
}
