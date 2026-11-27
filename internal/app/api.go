package app

import (
	"context"

	"github.com/phankieuphu/lumen/internal/advisor"
	"github.com/phankieuphu/lumen/internal/ai"
	"github.com/phankieuphu/lumen/internal/audit"
	"github.com/phankieuphu/lumen/internal/debug"
	"github.com/phankieuphu/lumen/internal/explain"
	"github.com/phankieuphu/lumen/internal/locks"
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

// Statements forwards to App.Statements.
func (p *API) Statements(dialect, sql string) []StatementDTO {
	return p.a.Statements(dialect, sql)
}

// Lint forwards to App.Lint.
func (p *API) Lint(ctx context.Context, connID, sql string) ([]LintIssueDTO, error) {
	return p.a.Lint(ctx, connID, sql)
}

// CheckSyntax forwards to App.CheckSyntax.
func (p *API) CheckSyntax(ctx context.Context, dialect, sql string) []SyntaxIssueDTO {
	return p.a.CheckSyntax(ctx, dialect, sql)
}

// EditorColors forwards to App.EditorColors.
func (p *API) EditorColors(ctx context.Context) (map[string]string, error) {
	return p.a.EditorColors(ctx)
}

// SaveEditorColors forwards to App.SaveEditorColors.
func (p *API) SaveEditorColors(ctx context.Context, colors map[string]string) (map[string]string, error) {
	return p.a.SaveEditorColors(ctx, colors)
}

// SaveSQLAs forwards to App.SaveSQLAs.
func (p *API) SaveSQLAs(ctx context.Context, filename, sql string) (SavedFileDTO, error) {
	return p.a.SaveSQLAs(ctx, filename, sql)
}

// SaveSQL forwards to App.SaveSQL.
func (p *API) SaveSQL(ctx context.Context, path, sql string) (SavedFileDTO, error) {
	return p.a.SaveSQL(ctx, path, sql)
}

// OpenSQL forwards to App.OpenSQL.
func (p *API) OpenSQL(ctx context.Context) (OpenedFileDTO, error) {
	return p.a.OpenSQL(ctx)
}

// SetUnsaved forwards to App.SetUnsaved.
func (p *API) SetUnsaved(ctx context.Context, unsaved bool) {
	p.a.SetUnsaved(ctx, unsaved)
}

// NewWindow forwards to App.NewWindow.
func (p *API) NewWindow(ctx context.Context) error {
	return p.a.NewWindow(ctx)
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

// TxLocks forwards to App.TxLocks.
func (p *API) TxLocks(ctx context.Context, txID string) (locks.Report, error) {
	return p.a.TxLocks(ctx, txID)
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

// RefreshSchema forwards to App.RefreshSchema.
func (p *API) RefreshSchema(ctx context.Context, connID string) (schema.Schema, error) {
	return p.a.RefreshSchema(ctx, connID)
}

// DebugSteps forwards to App.DebugSteps.
func (p *API) DebugSteps(dialect, sql string) []debug.Step {
	return p.a.DebugSteps(dialect, sql)
}

// DebugParts forwards to App.DebugParts.
func (p *API) DebugParts(ctx context.Context, connID, sql string) (debug.Report, error) {
	return p.a.DebugParts(ctx, connID, sql)
}

// DryRun forwards to App.DryRun.
func (p *API) DryRun(ctx context.Context, in DryRunInput) (debug.DryRunResult, error) {
	return p.a.DryRun(ctx, in)
}

// CancelDebug forwards to App.CancelDebug.
func (p *API) CancelDebug(ctx context.Context, requestID string) error {
	return p.a.CancelDebug(ctx, requestID)
}

// Explain forwards to App.Explain.
func (p *API) Explain(ctx context.Context, connID, sql string) (explain.Plan, error) {
	return p.a.Explain(ctx, connID, sql)
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

// AISettings forwards to App.AISettings.
func (p *API) AISettings(ctx context.Context) (AISettings, error) {
	return p.a.AISettings(ctx)
}

// SaveAISettings forwards to App.SaveAISettings.
func (p *API) SaveAISettings(ctx context.Context, settings AISettings) (AISettings, error) {
	return p.a.SaveAISettings(ctx, settings)
}

// SetAIKey forwards to App.SetAIKey.
func (p *API) SetAIKey(provider ai.ProviderID, key string) error {
	return p.a.SetAIKey(provider, key)
}

// AIPreview forwards to App.AIPreview.
func (p *API) AIPreview(ctx context.Context, input AIInput, settings AISettings) (string, error) {
	return p.a.AIPreview(ctx, input, settings)
}

// AISuggest forwards to App.AISuggest.
func (p *API) AISuggest(ctx context.Context, input AIInput) (string, error) {
	return p.a.AISuggest(ctx, input)
}
