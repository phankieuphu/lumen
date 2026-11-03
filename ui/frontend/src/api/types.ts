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

// internal/schema types, for the relations diagram.

export interface SchemaColumn {
  name: string;
  type: string;
  nullable: boolean;
  primaryKey: boolean;
}

export interface Table {
  schema: string;
  name: string;
  kind: "table" | "view";
  columns: SchemaColumn[];
}

export interface ForeignKey {
  name: string;
  fromSchema: string;
  fromTable: string;
  fromColumns: string[];
  toSchema: string;
  toTable: string;
  toColumns: string[];
}

export interface DbSchema {
  dialect: Dialect;
  tables: Table[];
  foreignKeys: ForeignKey[];
  /** A table or column limit stopped the read early. */
  truncated: boolean;
}

// Slow-query advisor (phase 4). No Go side yet; see wails.ts.

export type AdviceState = "fix-verified" | "rewrite-verified" | "no-fix" | "pending";

export interface AdvisorFilter {
  /** Look-back window in hours. */
  windowHours: number;
  /** Queries with a lower mean time are left out. */
  minMeanMs: number;
}

export interface SlowQuery {
  id: string;
  /** A short human name for the query, e.g. "Revenue per customer". */
  title: string;
  sql: string;
  tables: string[];
  meanMs: number;
  calls: number;
  /** Share of total database time, 0–100. */
  sharePct: number;
  state: AdviceState;
}

export interface PlanNode {
  label: string;
  depth: number;
  cost: number;
  /** Share of the plan's cost, 0–100. */
  sharePct: number;
}

export interface Advice {
  kind: "index" | "rewrite";
  title: string;
  explanation: string;
  sql: string;
  /** How the fix was checked, e.g. with a hypothetical index. */
  verifiedBy: string;
  costBefore: number;
  costAfter: number;
}

export interface SlowQueryDetail extends SlowQuery {
  planSource: string;
  plan: PlanNode[];
  /** null when no safe fix was found. */
  advice: Advice | null;
  /** Why there is no advice, when advice is null. */
  noAdviceReason?: string;
}

// Monitor (phase 5).

export type SessionState = "active" | "idle" | "idle in transaction" | "waiting on lock";

export interface DbSession {
  pid: number;
  user: string;
  state: SessionState;
  /** Seconds in the current state; null when idle. */
  seconds: number | null;
  query: string;
  blockedBy?: number;
}

export interface AlertRule {
  id: string;
  label: string;
  enabled: boolean;
  firing: boolean;
}

export interface MonitorSnapshot {
  at: string;
  activeSessions: number;
  connectionsUsed: number;
  maxConnections: number;
  waitingOnLocks: number;
  /** The session holding the lock others wait on. */
  rootBlocker?: number;
  cacheHitPct: number;
  longest?: { pid: number; seconds: number; limitSeconds: number };
  /** One sample per step, oldest first, covering the requested range. */
  series: { connections: number[]; tps: number[] };
  sessions: DbSession[];
  totalSessions: number;
  alerts: AlertRule[];
  /** The firing alert to show, if any. */
  firing?: { message: string; pid?: number };
}

// AI settings (phase 3).

export type ProviderId = "anthropic" | "openai" | "ollama";
export type AITask = "inline" | "textToSql" | "fixError" | "advise";

export interface AIProvider {
  id: ProviderId;
  name: string;
  /** Runs on this computer; nothing leaves it. */
  local: boolean;
  note: string;
  /** Whether an API key is stored, or none is needed. */
  ready: boolean;
  keyNote: string;
  models: string[];
}

export interface AISettings {
  provider: ProviderId;
  models: Record<AITask, string>;
  sampleRows: boolean;
  maskLiterals: boolean;
  inlineCompletion: boolean;
}

export interface PreviewLine {
  text: string;
  tone?: "head" | "muted" | "warn";
}

export interface AIPreview {
  destination: string;
  local: boolean;
  lines: PreviewLine[];
}

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
  /** Tables, columns, and foreign keys of the connection's database. */
  schema(connId: string): Promise<DbSchema>;

  auditList(filter: AuditFilter): Promise<AuditEntry[]>;
  auditVerify(): Promise<VerifyResult>;
  /** Asks the user for a file and exports to it; resolves false if they cancelled. */
  auditExport(format: ExportFormat, filter: AuditFilter): Promise<boolean>;

  /** Puts text on the system clipboard. */
  copyText(text: string): Promise<void>;

  advisorList(connId: string, filter: AdvisorFilter): Promise<SlowQuery[]>;
  advisorDetail(connId: string, id: string): Promise<SlowQueryDetail>;

  monitorSnapshot(connId: string, rangeMinutes: number): Promise<MonitorSnapshot>;
  /** Ends a database session (pg_terminate_backend / KILL); audited. */
  monitorEndSession(connId: string, pid: number): Promise<void>;
  monitorSetAlert(connId: string, ruleId: string, enabled: boolean): Promise<void>;

  aiProviders(): Promise<AIProvider[]>;
  aiSettings(): Promise<AISettings>;
  saveAISettings(settings: AISettings): Promise<AISettings>;
  /** The exact text a request for this task would send with these settings. */
  aiPreview(task: "advise" | "inline", settings: AISettings): Promise<AIPreview>;
}
