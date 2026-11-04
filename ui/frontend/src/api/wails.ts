import { Call, Clipboard, Dialogs, Events } from "@wailsio/runtime";

import type { AuditFilter, Backend, ExportFormat, Page } from "./types";

// Bound methods of *app.API, as the shell registers them.
const APP = "github.com/phankieuphu/lumen/internal/app.API.";
// app.EventQueryPage.
const EVENT_QUERY_PAGE = "query:page";

const call = <T>(method: string, ...args: unknown[]): Promise<T> => Call.ByName(APP + method, ...args) as Promise<T>;

const exportFilters: Record<ExportFormat, { DisplayName: string; Pattern: string }> = {
  csv: { DisplayName: "CSV", Pattern: "*.csv" },
  json: { DisplayName: "JSON", Pattern: "*.json" },
};

/**
 * notYet rejects calls for features whose Go side is not built. The screens
 * show the message instead of data, so nothing pretends to work.
 */
const notYet = (feature: string, phase: number) => () =>
  Promise.reject(new Error(`${feature} is not available yet: it arrives in phase ${phase} of docs/plan.md.`));

const advisor = notYet("The slow-query advisor", 4);
const monitor = notYet("The monitor", 5);
const ai = notYet("AI", 3);

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
  schema: (connId) => call("Schema", connId),

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

  // The native clipboard: the web view may refuse navigator.clipboard.
  copyText: (text) => Clipboard.SetText(text),

  advisorList: advisor,
  advisorDetail: advisor,

  monitorSnapshot: monitor,
  monitorEndSession: monitor,
  monitorSetAlert: monitor,

  aiProviders: ai,
  aiSettings: ai,
  saveAISettings: ai,
  aiPreview: ai,
};
