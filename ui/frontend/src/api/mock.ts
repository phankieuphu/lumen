// In-memory backend for `npm run dev` in a plain browser. It is loaded only
// in development builds (see index.ts) and never ships in dist/.
import { createMockFeatures } from "./mockFeatures";
import type { AuditEntry, Backend, Connection, DbSchema, ForeignKey, HistoryEntry, Kind, Page, Row, Status, Summary, Table } from "./types";

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
  return { dialect: conn?.dialect ?? "postgres", tables, foreignKeys, truncated: false };
}

export function createMockBackend(): Backend {
  let nextId = 1;
  const conns: Connection[] = [];
  const history: HistoryEntry[] = [];
  const audit: AuditEntry[] = [];
  const pageListeners = new Set<(p: Page) => void>();
  const cancelled = new Set<string>();

  const add = (c: Omit<Connection, "id" | "createdAt" | "updatedAt">) => {
    const conn = { ...c, id: `c${nextId++}`, createdAt: now(), updatedAt: now() };
    conns.push(conn);
    return conn;
  };
  add({ name: "local postgres", dialect: "postgres", host: "localhost", port: 5432, user: "lumen", database: "lumen", tls: "disable", env: "dev", readOnly: false, maxConns: 4 });
  add({ name: "orders prod", dialect: "mysql", host: "db.internal", port: 3306, user: "app", database: "orders", tls: "require", env: "prod", readOnly: false, maxConns: 4 });

  const record = (connId: string, sql: string, sum: Summary) => {
    history.unshift({ id: history.length + 1, connId, startedAt: now(), durationMs: sum.durationMs, kind: sum.kind, status: sum.status, rowCount: sum.rowCount, sql, error: sum.error });
    if (sum.kind === "write" || sum.kind === "schema") {
      const c = conns.find((x) => x.id === connId);
      audit.push({ seq: audit.length + 1, at: now(), connId, connName: c?.name ?? "", dialect: c?.dialect ?? "", env: c?.env ?? "", kind: sum.kind, status: sum.status, rowsAffected: sum.rowsAffected, durationMs: sum.durationMs, sql, error: sum.error, hash: Math.random().toString(16).slice(2).padEnd(64, "0") });
    }
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

    async runQuery({ requestId, connId, sql, pageSize = 500, maxRows = 100_000, confirmWrite }) {
      const conn = conns.find((c) => c.id === connId);
      const kind = classify(sql);
      const started = performance.now();
      const finish = (status: Status, extra: Partial<Summary> = {}) => {
        const sum: Summary = { requestId, kind, status, columns: null, rowCount: 0, rowsAffected: -1, truncated: false, durationMs: Math.round(performance.now() - started), ...extra };
        record(connId, sql, sum);
        return sum;
      };
      if (!sql.trim()) throw new Error("query: no statement to run");
      if (conn?.env === "prod" && kind !== "read" && kind !== "session" && !confirmWrite) {
        finish("blocked", { error: "query: writes on a production connection need confirmation" });
        throw new Error("query: writes on a production connection need confirmation");
      }
      if (kind !== "read") {
        await sleep(100);
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
      let sent = 0;
      for (let seq = 0; sent < Math.min(total, maxRows); seq++) {
        if (cancelled.delete(requestId)) return finish("cancelled", { columns, rowCount: sent, error: "cancelled" });
        const n = Math.min(pageSize, maxRows - sent, total - sent);
        const rows: Row[] = Array.from({ length: n }, (_, i) => {
          const id = sent + i + 1;
          return [id, `user${id}@example.com`, id % 7 === 0 ? null : (id * 13.37).toFixed(2), id % 5 === 0 ? { tier: "gold", tags: ["a", "b"] } : null, new Date(1_700_000_000_000 + id * 60_000).toISOString()];
        });
        const page: Page = { requestId, seq, rows, ...(seq === 0 ? { columns } : {}) };
        pageListeners.forEach((fn) => fn(page));
        sent += n;
        await sleep(20);
      }
      return finish("ok", { columns, rowCount: sent, truncated: total > maxRows });
    },
    async cancelQuery(requestId) {
      cancelled.add(requestId);
    },
    onPage(fn) {
      pageListeners.add(fn);
      return () => pageListeners.delete(fn);
    },
    queryHistory: async (connId, beforeId, limit) =>
      history.filter((h) => h.connId === connId && (beforeId <= 0 || h.id < beforeId)).slice(0, limit),

    async schema(connId) {
      await sleep(150);
      return mockSchema(conns.find((c) => c.id === connId));
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
