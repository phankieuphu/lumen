// In-memory backend for `npm run dev` in a plain browser. It is loaded only
// in development builds (see index.ts) and never ships in dist/.
import { splitStatements } from "../lib/statements";
import { createMockFeatures } from "./mockFeatures";
import type { AuditEntry, Backend, Connection, DbSchema, DebugPart, DebugStep, ExplainNode, ExplainPlan, ForeignKey, HistoryEntry, Kind, LintIssue, Lock, Page, Row, Status, Summary, Table, Transaction, TxEnd } from "./types";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const now = () => new Date().toISOString();

const classify = (sql: string): Kind => {
  // Skip leading comments, as the real classifier does.
  const body = sql.replace(/^(\s*(--[^\n]*(\n|$)|\/\*[\s\S]*?\*\/))*/, "");
  const word = /^\s*(\w+)/.exec(body)?.[1]?.toLowerCase() ?? "";
  if (["select", "with", "show", "explain", "values"].includes(word)) return "read";
  if (["create", "alter", "drop", "grant", "revoke", "truncate"].includes(word)) return "schema";
  if (["begin", "commit", "rollback", "set"].includes(word)) return "session";
  return "write";
};

// A small shop schema for the relations diagram: two schemas, a view, a
// self reference, a composite key, and tables with no relations.
function mockSteps(sql: string): DebugStep[] {
  return splitStatements(sql).map(({ from, to }) => {
    const text = sql.slice(from, to).trim();
    const start = from + sql.slice(from, to).indexOf(text);
    const kind = classify(text);
    const control = /^\s*(begin|commit|rollback|start\s+transaction)\b/i.test(text);
    const undoable = kind !== "session";
    return { start, end: start + text.length, sql: text, kind, control, undoable, why: undoable ? undefined : "it controls the transaction the dry run happens in" };
  });
}

/** mockParts finds FROM, each JOIN, and WHERE with a regular expression; the real splitter is in Go. */
function mockParts(sql: string): DebugPart[] {
  const parts: DebugPart[] = [];
  const add = (clause: DebugPart["clause"], start: number, end: number, rows: number) =>
    parts.push({ clause, label: sql.slice(start, end).replace(/\s+/g, " ").trim(), start, end, sql: "SELECT count(*) …", rows, ran: true, ms: 0.8 });
  const from = /\bfrom\s+/i.exec(sql);
  if (from) {
    const re = /\b((?:left|right|inner|full|cross)\s+(?:outer\s+)?)?join\b|\bwhere\b|\bgroup\b|\border\b|\blimit\b|$/gi;
    re.lastIndex = from.index + from[0].length;
    let start = from.index + from[0].length;
    let rows = 5000;
    let clause: DebugPart["clause"] = "from";
    for (let m = re.exec(sql); m; m = re.exec(sql)) {
      add(clause, start, m.index, rows);
      if (!/join/i.test(m[0])) {
        if (/where/i.test(m[0])) {
          const end = /\b(group|order|limit)\b|$/i.exec(sql.slice(m.index + 5));
          add("where", m.index, m.index + 5 + (end?.index ?? 0), 0);
        }
        break;
      }
      start = m.index;
      clause = "join";
      rows = Math.round(rows * 2.4);
      re.lastIndex = m.index + m[0].length;
    }
  }
  parts.push({ clause: "result", label: "Result", start: 0, end: sql.length, sql: "SELECT count(*) FROM (…)", rows: parts.at(-1)?.rows ?? 1, ran: true, ms: 1.1 });
  return parts;
}

/** mockPlan is modelled on a real PostgreSQL plan of a join over orders. */
function mockPlan(analyzed: boolean): ExplainPlan {
  const n = (id: number, op: string, kind: ExplainNode["kind"], x: Partial<ExplainNode>): ExplainNode => ({
    id, op, kind, estRows: 0, estCost: 0, rows: 0, loops: analyzed ? 1 : 0, timeMs: 0, rowsRemoved: 0, ...x,
  });
  const a = (v: number) => (analyzed ? v : 0);
  const root = n(0, "Sort", "sort", {
    detail: "o.total DESC", estRows: 138, estCost: 761, rows: a(500), timeMs: a(2.1),
    children: [
      n(1, "Hash Join", "join", {
        detail: "(o.customer_id = c.id)", estRows: 138, estCost: 756, rows: a(500), timeMs: a(2.0),
        children: [
          n(2, "Seq Scan", "scan", { relation: "orders", detail: "(status = 'open'::text)", estRows: 138, estCost: 713, rows: a(500), rowsRemoved: a(49500), timeMs: a(1.8) }),
          n(3, "Hash", "other", {
            estRows: 2000, estCost: 31, rows: a(2000), timeMs: a(0.2),
            children: [n(4, "Seq Scan", "scan", { relation: "customers", estRows: 2000, estCost: 31, rows: a(2000), timeMs: a(0.1) })],
          }),
        ],
      }),
    ],
  });
  return {
    dialect: "postgres", analyzed, estimated: true, root, planningMs: a(0.2), executionMs: a(2.1), truncated: false,
    note: analyzed
      ? "Ran with EXPLAIN ANALYZE in a read-only transaction that was rolled back."
      : "Planned but not run: only reads are run with EXPLAIN ANALYZE, so the numbers are estimates.",
    findings: [{
      severity: "warning", title: "Full scan of orders", nodeId: 2,
      detail: analyzed
        ? "Seq Scan reads every row of orders: 50,000 rows, and the filter throws away 49,500 of them. An index on the columns in (status = 'open'::text) would let it read only the rows it keeps."
        : "Seq Scan reads every row of orders. An index on the columns in (status = 'open'::text) would let it read only the rows it keeps.",
    }],
    steps: analyzed
      ? [
          "1. Read every row of orders, keeping those that match (status = 'open'::text): 500 rows, 49,500 thrown away, done 1.8 ms in.",
          "2. Read every row of customers: 2,000 rows, done 0.1 ms in.",
          "3. Built a hash table of those rows: 2,000 rows, done 0.2 ms in.",
          "4. Joined the rows above with a hash join (o.customer_id = c.id): 500 rows, done 2.0 ms in.",
          "5. Sorted the rows by o.total DESC: 500 rows, done 2.1 ms in.",
          "Finished in 2.1 ms, after 0.2 ms of planning.",
        ]
      : [
          "1. Read every row of orders, keeping those that match (status = 'open'::text): about 138 rows.",
          "2. Read every row of customers: about 2,000 rows.",
          "3. Built a hash table of those rows: about 2,000 rows.",
          "4. Joined the rows above with a hash join (o.customer_id = c.id): about 138 rows.",
          "5. Sorted the rows by o.total DESC: about 138 rows.",
        ],
    raw: JSON.stringify([{ Plan: { "Node Type": "Sort" } }], null, 2),
  };
}

function mockSchema(conn: Connection | undefined): DbSchema {
  const main = conn?.dialect === "postgres" ? "public" : conn?.dialect === "sqlite" ? "main" : (conn?.database ?? "app");
  const c = (name: string, type: string, flags = "") => ({ name, type, nullable: flags.includes("?"), primaryKey: flags.includes("pk") });
  const t = (name: string, columns: Table["columns"], schema = main, kind: Table["kind"] = "table"): Table => ({ schema, name, kind, columns });
  const tables: Table[] = [
    t("customers", [c("id", "bigint", "pk"), c("email", "text"), c("name", "text"), c("referred_by", "bigint", "?"), c("created_at", "timestamptz")]),
    t("addresses", [c("id", "bigint", "pk"), c("customer_id", "bigint"), c("line1", "text"), c("city", "text"), c("country", "char(2)")]),
    t("categories", [c("id", "int", "pk"), c("parent_id", "int", "?"), c("name", "text")]),
    t("products", [c("sku", "text", "pk"), c("category_id", "int", "?"), c("name", "text"), c("price", "numeric(12,2)")]),
    t("orders", [c("id", "bigint", "pk"), c("customer_id", "bigint"), c("shipping_address_id", "bigint", "?"), c("status", "text"), c("placed_at", "timestamptz")]),
    t("order_lines", [c("order_id", "bigint", "pk"), c("line_no", "int", "pk"), c("sku", "text"), c("qty", "int"), c("unit_price", "numeric(12,2)")]),
    t("payments", [c("id", "bigint", "pk"), c("order_id", "bigint"), c("amount", "numeric(12,2)"), c("provider", "text")]),
    t("shipments", [c("id", "bigint", "pk"), c("order_id", "bigint"), c("line_no", "int"), c("carrier", "text"), c("shipped_at", "timestamptz", "?")]),
    t("reviews", [c("id", "bigint", "pk"), c("sku", "text"), c("customer_id", "bigint"), c("rating", "smallint"), c("body", "text", "?")]),
    t("inventory", [c("sku", "text", "pk"), c("warehouse_id", "int", "pk"), c("on_hand", "int")]),
    t("warehouses", [c("id", "int", "pk"), c("name", "text"), c("country", "char(2)")]),
    t("settings", [c("key", "text", "pk"), c("value", "jsonb", "?")]),
    t("schema_migrations", [c("version", "bigint", "pk"), c("applied_at", "timestamptz")]),
    t("order_totals", [c("order_id", "bigint"), c("total", "numeric")], main, "view"),
  ];
  const fk = (name: string, from: string, fromColumns: string[], to: string, toColumns: string[], fromSchema = main, toSchema = main): ForeignKey => ({
    name, fromSchema, fromTable: from, fromColumns, toSchema, toTable: to, toColumns,
  });
  const foreignKeys = [
    fk("customers_referred_by_fkey", "customers", ["referred_by"], "customers", ["id"]),
    fk("addresses_customer_id_fkey", "addresses", ["customer_id"], "customers", ["id"]),
    fk("categories_parent_id_fkey", "categories", ["parent_id"], "categories", ["id"]),
    fk("products_category_id_fkey", "products", ["category_id"], "categories", ["id"]),
    fk("orders_customer_id_fkey", "orders", ["customer_id"], "customers", ["id"]),
    fk("orders_shipping_address_id_fkey", "orders", ["shipping_address_id"], "addresses", ["id"]),
    fk("order_lines_order_id_fkey", "order_lines", ["order_id"], "orders", ["id"]),
    fk("order_lines_sku_fkey", "order_lines", ["sku"], "products", ["sku"]),
    fk("payments_order_id_fkey", "payments", ["order_id"], "orders", ["id"]),
    fk("shipments_line_fkey", "shipments", ["order_id", "line_no"], "order_lines", ["order_id", "line_no"]),
    fk("reviews_sku_fkey", "reviews", ["sku"], "products", ["sku"]),
    fk("reviews_customer_id_fkey", "reviews", ["customer_id"], "customers", ["id"]),
    fk("inventory_sku_fkey", "inventory", ["sku"], "products", ["sku"]),
    fk("inventory_warehouse_id_fkey", "inventory", ["warehouse_id"], "warehouses", ["id"]),
  ];
  if (conn?.dialect === "postgres") {
    tables.push(t("events", [c("id", "bigint", "pk"), c("customer_id", "bigint", "?"), c("kind", "text"), c("at", "timestamptz")], "audit"));
    foreignKeys.push(fk("events_customer_id_fkey", "events", ["customer_id"], "customers", ["id"], "audit", main));
  }
  return { dialect: conn?.dialect ?? "postgres", tables, foreignKeys, truncated: false, version: 1 };
}

export function createMockBackend(): Backend {
  let nextId = 1;
  const conns: Connection[] = [];
  const history: HistoryEntry[] = [];
  const audit: AuditEntry[] = [];
  const pageListeners = new Set<(p: Page) => void>();
  const cancelled = new Set<string>();
  const dryRuns = new Set<string>();
  let editorColors: Record<string, string> = {};
  const files = new Map<string, string>();
  let schemaVersion = 1;
  const schemaListeners = new Set<(connId: string) => void>();
  const txs = new Map<string, Transaction>();

  const add = (c: Omit<Connection, "id" | "createdAt" | "updatedAt">) => {
    const conn = { ...c, id: `c${nextId++}`, createdAt: now(), updatedAt: now() };
    conns.push(conn);
    return conn;
  };
  add({ name: "local postgres", dialect: "postgres", host: "localhost", port: 5432, user: "lumen", database: "lumen", tls: "disable", env: "dev", readOnly: false, maxConns: 4 });
  add({ name: "orders prod", dialect: "mysql", host: "db.internal", port: 3306, user: "app", database: "orders", tls: "require", env: "prod", readOnly: false, maxConns: 4 });

  const record = (connId: string, sql: string, sum: Summary, audited = sum.kind === "write" || sum.kind === "schema") => {
    const tags = { txId: sum.txId, txLabel: sum.txLabel };
    history.unshift({ id: history.length + 1, connId, startedAt: now(), durationMs: sum.durationMs, kind: sum.kind, status: sum.status, rowCount: sum.rowCount, sql, error: sum.error, ...tags });
    if (audited) {
      const c = conns.find((x) => x.id === connId);
      audit.push({ seq: audit.length + 1, at: now(), connId, connName: c?.name ?? "", dialect: c?.dialect ?? "", env: c?.env ?? "", kind: sum.kind, status: sum.status, rowsAffected: sum.rowsAffected, durationMs: sum.durationMs, sql, error: sum.error, ...tags, hash: Math.random().toString(16).slice(2).padEnd(64, "0") });
    }
  };

  // Mirrors query.Runner's transactions: one per connection, tagged rows.
  const endTx = (tx: Transaction, how: TxEnd, requestId = tx.id): Summary => {
    txs.delete(tx.id);
    const sum: Summary = { requestId, kind: "session", status: "ok", columns: null, rowCount: 0, rowsAffected: -1, truncated: false, durationMs: 4, txId: tx.id, txLabel: tx.label, txEnd: how };
    record(tx.connId, how.toUpperCase(), sum, tx.writes > 0);
    return sum;
  };
  const txWord = (sql: string) => {
    const w = sql.trim().split(/\s+/).map((x) => x.replace(/;$/, "").toLowerCase());
    if (w[0] === "begin" || (w[0] === "start" && w[1] === "transaction")) return "begin";
    if ((w[0] === "commit" || w[0] === "end") && w[1] !== "prepared") return "commit";
    if ((w[0] === "rollback" || w[0] === "abort") && !w.includes("to")) return "rollback";
    return undefined;
  };

  return {
    ...createMockFeatures(() => conns),
    copyText: (text) => navigator.clipboard.writeText(text),
    listConnections: async () => [...conns],
    createConnection: async ({ password: _, ...input }) => add(input),
    async updateConnection(id, { password: _, ...input }) {
      const i = conns.findIndex((c) => c.id === id);
      if (i < 0) throw new Error("conn: not found");
      return (conns[i] = { ...conns[i]!, ...input, updatedAt: now() });
    },
    async deleteConnection(id) {
      const i = conns.findIndex((c) => c.id === id);
      if (i >= 0) conns.splice(i, 1);
    },
    async testConnection(input) {
      await sleep(300);
      if (input.dialect !== "sqlite" && !input.host) throw new Error("conn: host is required");
    },

    async runQuery({ requestId, connId, sql, pageSize = 500, maxRows = 100_000, confirmWrite, txId }) {
      const conn = conns.find((c) => c.id === connId);
      const kind = classify(sql);
      const started = performance.now();
      const tx = txId ? txs.get(txId) : undefined;
      if (txId && (!tx || tx.connId !== connId)) throw new Error("query: no open transaction with this ID");
      const finish = (status: Status, extra: Partial<Summary> = {}) => {
        const sum: Summary = { requestId, kind, status, columns: null, rowCount: 0, rowsAffected: -1, truncated: false, durationMs: Math.round(performance.now() - started), txId: tx?.id, txLabel: tx?.label, ...extra };
        record(connId, sql, sum);
        if (tx) {
          tx.statements++;
          if (status === "ok" && (kind === "write" || kind === "schema")) tx.writes++;
        }
        return sum;
      };
      if (!sql.trim()) throw new Error("query: no statement to run");
      const op = txWord(sql);
      if (op && !tx) {
        const msg = "query: start transactions with Begin; COMMIT and ROLLBACK run inside one";
        finish("blocked", { error: msg });
        throw new Error(msg);
      }
      if (op === "begin") {
        const msg = "query: a transaction is already open on this connection; commit or roll it back first";
        finish("blocked", { error: msg });
        throw new Error(msg);
      }
      if (tx && op) return endTx(tx, op, requestId);
      if (conn?.env === "prod" && kind !== "read" && kind !== "session" && !confirmWrite) {
        finish("blocked", { error: "query: writes on a production connection need confirmation" });
        throw new Error("query: writes on a production connection need confirmation");
      }
      if (kind !== "read") {
        await sleep(100);
        if (kind === "schema") {
          schemaVersion++;
          schemaListeners.forEach((fn) => fn(connId));
        }
        return finish("ok", { rowsAffected: 3 });
      }
      const total = Math.min(Number(/limit\s+(\d+)/i.exec(sql)?.[1] ?? 5000), maxRows + 1);
      const columns = [
        { name: "id", type: "int8", nullable: false },
        { name: "email", type: "text", nullable: false },
        { name: "amount", type: "numeric", nullable: true },
        { name: "meta", type: "jsonb", nullable: true },
        { name: "created_at", type: "timestamptz", nullable: false },
      ];
      // A real server takes a moment before the first row.
      await sleep(300);
      let sent = 0;
      for (let seq = 0; sent < Math.min(total, maxRows); seq++) {
        if (cancelled.delete(requestId)) return finish("cancelled", { columns, rowCount: sent, error: "cancelled" });
        const n = Math.min(pageSize, maxRows - sent, total - sent);
        const rows: Row[] = Array.from({ length: n }, (_, i) => {
          const id = sent + i + 1;
          return [id, `user${id}@example.com`, id % 7 === 0 ? null : (id * 13.37).toFixed(2), id % 5 === 0 ? { tier: "gold", tags: ["a", "b"] } : null, new Date(1_700_000_000_000 + id * 60_000).toISOString()];
        });
        const page: Page = { requestId, seq, rows, ...(seq === 0 ? { columns } : {}) };
        // Like Wails events, pages travel apart from the call's result and
        // can arrive after it resolves.
        setTimeout(() => pageListeners.forEach((fn) => fn(page)), 40);
        sent += n;
        await sleep(20);
      }
      return finish("ok", { columns, rowCount: sent, truncated: total > maxRows });
    },
    statements: async (_dialect, sql) => splitStatements(sql),
    // No file system in the browser: the dialog is a prompt, and files are kept in memory.
    async saveSqlAs(filename, sql) {
      const name = window.prompt("Save SQL as", filename);
      if (!name) return { path: "", name: "" };
      const path = `/mock/${name}`;
      files.set(path, sql);
      return { path, name };
    },
    async saveSql(path, sql) {
      if (!files.has(path)) throw new Error("app: save: choose the file with Save As first");
      files.set(path, sql);
      return { path, name: path.split("/").pop() ?? path };
    },
    // The real rules are in Go; the mock finds two of them roughly.
    async lint(_connId, sql) {
      const out: LintIssue[] = [];
      for (const r of splitStatements(sql)) {
        const text = sql.slice(r.from, r.to);
        const verb = /^(UPDATE|DELETE)\b/i.exec(text);
        if (verb && !/\bWHERE\b/i.test(text)) {
          out.push({ from: r.from, to: r.from + verb[0].length, rule: "L001", severity: "error", message: `${verb[0].toUpperCase()} without WHERE changes every row in the table.` });
        }
        const star = /^SELECT\s+(\*)/i.exec(text);
        if (star) {
          const at = r.from + star[0].length - 1;
          out.push({ from: at, to: at + 1, rule: "L101", severity: "warning", message: "SELECT * reads every column. Name the columns you need." });
        }
      }
      return out;
    },
    async openSql() {
      const names = [...files.keys()].map((p) => p.split("/").pop()).join(", ");
      const name = window.prompt(`Open SQL file${names ? ` (saved: ${names})` : ""}`);
      if (!name) return { path: "", name: "", sql: "" };
      const path = `/mock/${name}`;
      const sql = files.get(path);
      if (sql === undefined) throw new Error(`app: open: ${name}: no such file`);
      return { path, name, sql };
    },
    setUnsaved: async () => {},
    editorColors: async () => ({ ...editorColors }),
    async saveEditorColors(colors) {
      editorColors = { ...colors };
      return { ...editorColors };
    },
    // The real parsers are in Go; the mock flags a few common typos.
    checkSyntax: async (_dialect, sql) =>
      [...sql.matchAll(/\b(SELEC|FORM|WHER|UPDTE|DELTE|INSRT)\b/gi)].map((m) => ({
        from: m.index,
        to: m.index + m[0].length,
        message: `syntax error at or near "${m[0]}"`,
      })),
    async cancelQuery(requestId) {
      cancelled.add(requestId);
    },
    onPage(fn) {
      pageListeners.add(fn);
      return () => pageListeners.delete(fn);
    },
    queryHistory: async (connId, beforeId, limit) =>
      history.filter((h) => h.connId === connId && (beforeId <= 0 || h.id < beforeId)).slice(0, limit),

    async beginTx(connId, label) {
      if ([...txs.values()].some((t) => t.connId === connId)) {
        throw new Error("query: a transaction is already open on this connection; commit or roll it back first");
      }
      await sleep(60);
      const id = `tx-${Math.random().toString(16).slice(2, 14).padEnd(12, "0")}`;
      const tx: Transaction = { id, connId, label: label.trim().replace(/\s+/g, " ").slice(0, 80) || undefined, startedAt: now(), statements: 0, writes: 0 };
      txs.set(id, tx);
      record(connId, "BEGIN", { requestId: id, kind: "session", status: "ok", columns: null, rowCount: 0, rowsAffected: -1, truncated: false, durationMs: 2, txId: id, txLabel: tx.label }, false);
      return { ...tx };
    },
    async commitTx(txId) {
      const tx = txs.get(txId);
      if (!tx) throw new Error("query: no open transaction with this ID");
      await sleep(80);
      return endTx(tx, "commit");
    },
    async rollbackTx(txId) {
      const tx = txs.get(txId);
      if (!tx) throw new Error("query: no open transaction with this ID");
      await sleep(80);
      return endTx(tx, "rollback");
    },
    transactions: async () => [...txs.values()].map((t) => ({ ...t })),
    async txLocks(txId) {
      await sleep(120);
      const tx = txs.get(txId);
      if (!tx) throw new Error("query: no open transaction with this ID");
      const locks: Lock[] = [];
      if (tx.writes > 0) {
        locks.push(
          { type: "table", mode: "RowExclusiveLock", granted: true, object: "orders", meaning: "Taken by INSERT, UPDATE, and DELETE. Others can still read and write other rows; it blocks ALTER, DROP, TRUNCATE, and CREATE INDEX until you commit." },
          { type: "transaction", mode: "ExclusiveLock", granted: true, object: "transaction 7514", meaning: "Your transaction's own ID. Anyone who changes a row you changed waits on it until you commit or roll back." },
        );
      } else if (tx.statements > 0) {
        locks.push({ type: "table", mode: "AccessShareLock", granted: true, object: "orders", meaning: "Taken by reads. Blocks only ALTER TABLE, DROP, TRUNCATE, and VACUUM FULL." });
      }
      return {
        locks, waiting: false, truncated: false,
        note: "PostgreSQL keeps row locks on the rows themselves, not in this list: the table lock and your transaction ID stand for them.",
      };
    },
    transactionHistory: async (txId) => history.filter((h) => h.txId === txId).reverse(),

    async schema(connId) {
      await sleep(150);
      return { ...mockSchema(conns.find((c) => c.id === connId)), version: schemaVersion };
    },
    async explain(_connId, sql) {
      await sleep(250);
      if (!sql.trim()) throw new Error("query: no statement to run");
      return mockPlan(classify(sql) === "read");
    },
    debugSteps: async (_dialect, sql) => mockSteps(sql),
    async debugParts(_connId, sql) {
      await sleep(300);
      if (classify(sql) !== "read") throw new Error("debug: only a SELECT can be split into parts");
      return { parts: mockParts(sql), note: "Each part was counted in a read-only transaction that was rolled back (mock).", timedOut: false };
    },
    async dryRun({ requestId, connId, sql, confirmWrite }) {
      const steps = mockSteps(sql);
      if (steps.length === 0) throw new Error("debug: no statement to run");
      const conn = conns.find((c) => c.id === connId);
      if (conn?.env === "prod" && !confirmWrite && steps.some((s) => s.kind !== "read")) {
        throw new Error("query: writes on a production connection need confirmation");
      }
      const bad = steps.findIndex((s) => !s.undoable);
      if (bad >= 0) throw new Error(`debug: a dry run cannot undo this statement: statement ${bad + 1}: ${steps[bad]?.why ?? ""}`);
      dryRuns.add(requestId);
      await sleep(600);
      const cancelled = !dryRuns.delete(requestId);
      return {
        statements: steps.map((s, i) => {
          const read = s.kind === "read";
          return {
            ...s, ran: !cancelled, ms: 1.5 + i, truncated: false,
            rowsAffected: read ? -1 : 3, rowCount: read ? 2 : 0,
            columns: read ? [{ name: "id", type: "int4", nullable: false }, { name: "status", type: "text", nullable: true }] : undefined,
            rows: read ? [[1, "open"], [2, "paid"]] : undefined,
          };
        }),
        rolledBack: true, timedOut: false, cancelled, ms: 4.2,
        note: "Ran in a transaction that was rolled back: nothing it changed was kept (mock).",
      };
    },
    async cancelDebug(requestId) {
      if (!dryRuns.delete(requestId)) throw new Error("query: no running request with this ID");
    },
    async refreshSchema(connId) {
      schemaVersion++;
      return this.schema(connId);
    },
    onSchemaChanged(fn) {
      schemaListeners.add(fn);
      return () => schemaListeners.delete(fn);
    },

    auditList: async ({ connId, afterSeq = 0, limit = 200 }) =>
      audit.filter((e) => e.seq > afterSeq && (!connId || e.connId === connId)).slice(0, limit),
    auditVerify: async () => ({ entries: audit.length, ok: true }),
    async auditExport() {
      await sleep(200);
      return true;
    },
  };
}
