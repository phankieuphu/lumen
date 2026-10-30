import { Call, Dialogs, Events } from "@wailsio/runtime";

import type { AuditFilter, Backend, ExportFormat, Page } from "./types";

// Bound methods of *app.App, as the shell registers them.
const APP = "github.com/phankieuphu/lumen/internal/app.App.";
// app.EventQueryPage.
const EVENT_QUERY_PAGE = "query:page";

const call = <T>(method: string, ...args: unknown[]): Promise<T> => Call.ByName(APP + method, ...args) as Promise<T>;

const exportFilters: Record<ExportFormat, { DisplayName: string; Pattern: string }> = {
  csv: { DisplayName: "CSV", Pattern: "*.csv" },
  json: { DisplayName: "JSON", Pattern: "*.json" },
};

export const wailsBackend: Backend = {
  listConnections: () => call("ListConnections"),
  createConnection: (input) => call("CreateConnection", input),
  updateConnection: (id, input) => call("UpdateConnection", id, input),
  deleteConnection: (id) => call("DeleteConnection", id),
  testConnection: (input) => call("TestConnection", input),

  runQuery: (input) => call("RunQuery", input),
  cancelQuery: (requestId) => call("CancelQuery", requestId),
  onPage: (fn) => Events.On(EVENT_QUERY_PAGE, (ev) => fn(ev.data as Page)),
  queryHistory: (connId, beforeId, limit) => call("QueryHistory", connId, beforeId, limit),

  auditList: (filter) => call("AuditList", filter),
  auditVerify: () => call("AuditVerify"),
  async auditExport(format: ExportFormat, filter: AuditFilter) {
    const path = await Dialogs.SaveFile({
      Filename: `lumen-audit.${format}`,
      Filters: [exportFilters[format]],
    });
    if (!path) return false;
    await call("AuditExport", path, format, filter);
    return true;
  },
};
