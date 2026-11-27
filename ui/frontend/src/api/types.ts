// JSON shapes from internal/app/dto.go. Keep in sync with that file:
// durations are milliseconds, times are RFC 3339 strings.

export type Dialect = "postgres" | "mysql" | "sqlite";
export type Env = "dev" | "staging" | "prod";
export type TLSMode = "disable" | "prefer" | "require" | "verify-full";
export type Kind = "read" | "session" | "write" | "schema";
/** "rolledback" marks a dry run's statement in the audit log. */
export type Status = "ok" | "error" | "blocked" | "cancelled" | "rolledback";

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
  /** Runs the statement inside this open transaction. */
  txId?: string;
}

/** An open transaction (TxDTO). */
export interface Transaction {
  id: string;
  connId: string;
  label?: string;
  startedAt: string;
  statements: number;
  writes: number;
}

export type TxEnd = "commit" | "rollback";

/** Tags on anything that ran inside a transaction. */
export interface TxTagged {
  txId?: string;
  txLabel?: string;
}

export interface Page {
  requestId: string;
  seq: number;
  columns?: Column[];
  rows: Row[];
}

export interface Summary extends TxTagged {
  requestId: string;
  kind: Kind;
  status: Status;
  columns: Column[] | null;
  rowCount: number;
  rowsAffected: number;
  truncated: boolean;
  durationMs: number;
  error?: string;
  /** Set when this request ended the transaction (typed COMMIT/ROLLBACK). */
  txEnd?: TxEnd;
}

/** Where one statement sits in the editor text: [from, to), in string indexes. */
export interface StatementRange {
  from: number;
  to: number;
}

/** A syntax error in the editor text: [from, to), in string indexes. */
export interface SyntaxIssue {
  from: number;
  to: number;
  message: string;
}

/** A file the editor's SQL was saved to. */
export interface SavedFile {
  path: string;
  name: string;
}

/** A SQL file opened into the editor. */
export interface OpenedFile extends SavedFile {
  sql: string;
}

/** A lint finding in the editor text: [from, to), in string indexes. */
export interface LintIssue {
  from: number;
  to: number;
  rule: string;
  severity: "error" | "warning" | "info";
  message: string;
}

/** Mirrors internal/explain. Row counts and times are per loop. */
export interface ExplainNode {
  id: number;
  op: string;
  kind: "scan" | "index-scan" | "join" | "sort" | "aggregate" | "limit" | "filter" | "other";
  relation?: string;
  index?: string;
  detail?: string;
  estRows: number;
  estCost: number;
  rows: number;
  loops: number;
  timeMs: number;
  rowsRemoved: number;
  neverRan?: boolean;
  spilled?: boolean;
  children?: ExplainNode[];
}

export interface PlanFinding {
  severity: "warning" | "info";
  title: string;
  detail: string;
  nodeId: number;
}

export interface ExplainPlan {
  dialect: Dialect;
  /** The statement ran, in a read-only transaction that was rolled back. */
  analyzed: boolean;
  /** The plan has row and cost estimates. */
  estimated: boolean;
  note: string;
  root: ExplainNode | null;
  planningMs: number;
  executionMs: number;
  findings: PlanFinding[];
  /** How it ran, in the order the work happened. */
  steps: string[];
  raw: string;
  truncated: boolean;
}

/** Mirrors internal/debug.Step: one statement of a script, offsets in string indexes. */
export interface DebugStep {
  start: number;
  end: number;
  sql: string;
  kind: Kind;
  /** Starts or ends a transaction; stepping through skips it. */
  control: boolean;
  /** A dry run can take it; why says why not. */
  undoable: boolean;
  why?: string;
}

export type PartClause = "cte" | "from" | "join" | "where" | "group" | "having" | "branch" | "result";

/** Mirrors internal/debug.Part: one step of a query with the rows it gives. */
export interface DebugPart {
  clause: PartClause;
  label: string;
  /** Offsets of the part in the query that was split, in string indexes. */
  start: number;
  end: number;
  /** The count that ran. */
  sql: string;
  rows: number;
  ran: boolean;
  ms: number;
  error?: string;
}

export interface PartsReport {
  parts: DebugPart[];
  note: string;
  timedOut: boolean;
}

export interface DryRunInput {
  requestId: string;
  connId: string;
  sql: string;
  confirmWrite: boolean;
  timeoutMs: number;
  previewRows: number;
}

/** Mirrors internal/debug.Ran: what one statement of a dry run did. */
export interface DryRunStatement extends DebugStep {
  ran: boolean;
  /** Rows changed, or -1 if unknown. */
  rowsAffected: number;
  columns?: Column[];
  /** The first rows returned; rowCount counts them all. */
  rows?: Row[];
  rowCount: number;
  truncated: boolean;
  ms: number;
  error?: string;
}

export interface DryRunResult {
  statements: DryRunStatement[];
  /** Nothing the run did was kept. */
  rolledBack: boolean;
  timedOut: boolean;
  cancelled: boolean;
  note: string;
  ms: number;
}

/** Mirrors internal/locks. */
export interface Lock {
  type: "table" | "row" | "transaction" | "metadata" | "advisory" | "other";
  mode: string;
  /** False while the session waits for the lock. */
  granted: boolean;
  object: string;
  detail?: string;
  meaning: string;
}

export interface LockReport {
  locks: Lock[];
  waiting: boolean;
  note: string;
  truncated: boolean;
}

export interface HistoryEntry extends TxTagged {
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

export interface AuditEntry extends TxTagged {
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
  /** Grows with every load of the backend's schema cache. */
  version: number;
}

// Slow-query advisor (phase 4). Mirrors internal/advisor. The list and the
// plan are live; advice needs the AI provider of phase 3.

export type AdviceState = "fix-verified" | "rewrite-verified" | "no-fix" | "pending" | "not-analysed";

export type AdvisorSort = "share" | "mean" | "calls";
/** "" keeps every statement type. */
export type AdvisorKind = "" | "select" | "insert" | "update" | "delete";

export interface AdvisorFilter {
  /** Look-back window in hours; 0 for no bound. Only MySQL can apply it. */
  windowHours: number;
  /** Queries with a lower mean time are left out. */
  minMeanMs: number;
  /** Queries called fewer times are left out. */
  minCalls: number;
  /** Keeps queries whose text contains this, ignoring case. */
  search: string;
  kind: AdvisorKind;
  sort: AdvisorSort;
}

export interface AdvisorList {
  queries: SlowQuery[];
  /** When the server last reset its statistics (RFC 3339), or "". */
  statsSince: string;
  /** Whether windowHours was applied. */
  windowed: boolean;
  /** More queries matched than the list holds. */
  truncated: boolean;
}

export interface SlowQuery {
  id: string;
  /** A short name for the query, e.g. "SELECT on orders". */
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
  /** How the plan was read, or why there is none when plan is empty. */
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
export type AITask = "inline" | "textToSql" | "fixError";

export interface AISettings {
  provider: ProviderId;
  models: Partial<Record<AITask, string>>;
  baseUrl: string;
  maskLiterals: boolean;
  inlineCompletion: boolean;
}

export interface AIInput {
  connId: string;
  task: AITask;
  editor: string;
  cursor: number;
  request?: string;
  error?: string;
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
  /** Splits SQL into statements the way the backend counts them. */
  statements(dialect: Dialect, sql: string): Promise<StatementRange[]>;
  /** Syntax errors found offline by the dialect's parser. */
  checkSyntax(dialect: Dialect, sql: string): Promise<SyntaxIssue[]>;
  /** Lint findings (docs/lint-rules.md) for SQL on a connection; does not connect. */
  lint(connId: string, sql: string): Promise<LintIssue[]>;
  /** Subscribes to every query page; returns an unsubscribe function. */
  onPage(fn: (page: Page) => void): () => void;
  queryHistory(connId: string, beforeId: number, limit: number): Promise<HistoryEntry[]>;

  /** Opens a transaction; statements run with its id stay in it. */
  beginTx(connId: string, label: string): Promise<Transaction>;
  commitTx(txId: string): Promise<Summary>;
  rollbackTx(txId: string): Promise<Summary>;
  /** Open transactions on every connection. */
  transactions(): Promise<Transaction[]>;
  /** The locks an open transaction holds or waits for. */
  txLocks(txId: string): Promise<LockReport>;
  /** Every statement of one transaction, oldest first. */
  transactionHistory(txId: string): Promise<HistoryEntry[]>;
  /** Tables, columns, and foreign keys of the connection's database, cached until DDL. */
  schema(connId: string): Promise<DbSchema>;
  /** The plan of one statement: reads run with EXPLAIN ANALYZE (rolled back), others are planned only. */
  explain(connId: string, sql: string): Promise<ExplainPlan>;
  /** The statements of a script, for stepping through and dry runs. Does not connect. */
  debugSteps(dialect: Dialect, sql: string): Promise<DebugStep[]>;
  /** Splits one SELECT into its parts and counts each one's rows, read-only. */
  debugParts(connId: string, sql: string): Promise<PartsReport>;
  /** Runs a script in a transaction that is always rolled back. */
  dryRun(input: DryRunInput): Promise<DryRunResult>;
  /** Stops a running dry run. */
  cancelDebug(requestId: string): Promise<void>;
  /** Reads the schema again, e.g. after DDL run outside lumen. */
  refreshSchema(connId: string): Promise<DbSchema>;
  /** Subscribes to schema changes (DDL, end of a transaction); returns an unsubscribe function. */
  onSchemaChanged(fn: (connId: string) => void): () => void;

  auditList(filter: AuditFilter): Promise<AuditEntry[]>;
  auditVerify(): Promise<VerifyResult>;
  /** Asks the user for a file and exports to it; resolves false if they cancelled. */
  auditExport(format: ExportFormat, filter: AuditFilter): Promise<boolean>;

  /** Syntax colours the user changed: token key → #rrggbb. */
  editorColors(): Promise<Record<string, string>>;
  saveEditorColors(colors: Record<string, string>): Promise<Record<string, string>>;

  /**
   * Asks where to save with the native dialog and writes sql there; resolves
   * with an empty path when the user cancels.
   */
  saveSqlAs(filename: string, sql: string): Promise<SavedFile>;
  /** Saves again to a file chosen with saveSqlAs or openSql in this session. */
  saveSql(path: string, sql: string): Promise<SavedFile>;
  /** Asks for a SQL file with the native dialog; an empty path when cancelled. */
  openSql(): Promise<OpenedFile>;
  /** Tells the app this window's editor has a file with unsaved changes, to ask before closing or quitting. */
  setUnsaved(unsaved: boolean): Promise<void>;
  /** Opens another window with the full UI. */
  newWindow(): Promise<void>;

  /** Puts text on the system clipboard. */
  copyText(text: string): Promise<void>;

  advisorList(connId: string, filter: AdvisorFilter): Promise<AdvisorList>;
  advisorDetail(connId: string, id: string): Promise<SlowQueryDetail>;

  monitorSnapshot(connId: string, rangeMinutes: number): Promise<MonitorSnapshot>;
  /** Ends a database session (pg_terminate_backend / KILL); audited. */
  monitorEndSession(connId: string, pid: number): Promise<void>;
  monitorSetAlert(connId: string, ruleId: string, enabled: boolean): Promise<void>;

  aiSettings(): Promise<AISettings>;
  saveAISettings(settings: AISettings): Promise<AISettings>;
  setAIKey(provider: ProviderId, key: string): Promise<void>;
  aiPreview(input: AIInput, settings: AISettings): Promise<string>;
  aiSuggest(input: AIInput, signal: AbortSignal): Promise<string>;
}
