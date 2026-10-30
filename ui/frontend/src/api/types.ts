// JSON shapes from internal/app/dto.go. Keep in sync with that file:
// durations are milliseconds, times are RFC 3339 strings.

export type Dialect = "postgres" | "mysql" | "sqlite";
export type Env = "dev" | "staging" | "prod";
export type TLSMode = "disable" | "prefer" | "require" | "verify-full";
export type Kind = "read" | "session" | "write" | "schema";
export type Status = "ok" | "error" | "blocked" | "cancelled";

export interface ConnectionInput {
  name: string;
  dialect: Dialect;
  host: string;
  port: number;
  user: string;
  database: string;
  tls: TLSMode;
  env: Env;
  readOnly: boolean;
  maxConns: number;
  /** Omitted keeps the stored password on update; "" removes it. */
  password?: string;
}

export interface Connection extends Omit<ConnectionInput, "password"> {
  id: string;
  createdAt: string;
  updatedAt: string;
}

export interface Column {
  name: string;
  type: string;
  nullable: boolean;
}

export type Cell = unknown;
export type Row = Cell[];

export interface RunInput {
  requestId: string;
  connId: string;
  sql: string;
  pageSize?: number;
  maxRows?: number;
  confirmWrite?: boolean;
  timeoutMs?: number;
}

export interface Page {
  requestId: string;
  seq: number;
  columns?: Column[];
  rows: Row[];
}

export interface Summary {
  requestId: string;
  kind: Kind;
  status: Status;
  columns: Column[] | null;
  rowCount: number;
  rowsAffected: number;
  truncated: boolean;
  durationMs: number;
  error?: string;
}

export interface HistoryEntry {
  id: number;
  connId: string;
  startedAt: string;
  durationMs: number;
  kind: Kind;
  status: Status;
  rowCount: number;
  sql: string;
  error?: string;
}

export interface AuditFilter {
  connId?: string;
  since?: string;
  until?: string;
  afterSeq?: number;
  limit?: number;
}

export interface AuditEntry {
  seq: number;
  at: string;
  connId: string;
  connName: string;
  dialect: string;
  env: string;
  kind: string;
  status: Status;
  rowsAffected: number;
  durationMs: number;
  sql: string;
  error?: string;
  hash: string;
}

export interface VerifyResult {
  entries: number;
  ok: boolean;
  brokenAt?: number;
  reason?: string;
}

export type ExportFormat = "csv" | "json";

/** Backend is the app façade (internal/app.App) as the UI sees it. */
export interface Backend {
  listConnections(): Promise<Connection[]>;
  createConnection(input: ConnectionInput): Promise<Connection>;
  updateConnection(id: string, input: ConnectionInput): Promise<Connection>;
  deleteConnection(id: string): Promise<void>;
  testConnection(input: ConnectionInput): Promise<void>;

  /** Resolves with the summary after the last page was delivered. */
  runQuery(input: RunInput): Promise<Summary>;
  cancelQuery(requestId: string): Promise<void>;
  /** Subscribes to every query page; returns an unsubscribe function. */
  onPage(fn: (page: Page) => void): () => void;
  queryHistory(connId: string, beforeId: number, limit: number): Promise<HistoryEntry[]>;

  auditList(filter: AuditFilter): Promise<AuditEntry[]>;
  auditVerify(): Promise<VerifyResult>;
  /** Asks the user for a file and exports to it; resolves false if they cancelled. */
  auditExport(format: ExportFormat, filter: AuditFilter): Promise<boolean>;
}
