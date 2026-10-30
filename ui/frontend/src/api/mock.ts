// In-memory backend for `npm run dev` in a plain browser. It is loaded only
// in development builds (see index.ts) and never ships in dist/.
import type { AuditEntry, Backend, Connection, HistoryEntry, Kind, Page, Row, Status, Summary } from "./types";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const now = () => new Date().toISOString();

const classify = (sql: string): Kind => {
  const body = sql.replace(/^(\s*--[^\n]*\n|\s*\/\*[\s\S]*?\*\/)*/, "");
  const word = /^\s*(\w+)/.exec(body)?.[1]?.toLowerCase() ?? "";
  if (["select", "with", "show", "explain", "values"].includes(word)) return "read";
  if (["create", "alter", "drop", "grant", "revoke", "truncate"].includes(word)) return "schema";
  if (["begin", "commit", "rollback", "set"].includes(word)) return "session";
  return "write";
};

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
    // Like query.Runner: writes record rows changed in rowCount.
    const rowCount = sum.kind !== "read" && sum.rowsAffected > 0 ? sum.rowsAffected : sum.rowCount;
    history.unshift({ id: history.length + 1, connId, startedAt: now(), durationMs: sum.durationMs, kind: sum.kind, status: sum.status, rowCount, sql, error: sum.error });
    if (sum.kind === "write" || sum.kind === "schema") {
      const c = conns.find((x) => x.id === connId);
      audit.push({ seq: audit.length + 1, at: now(), connId, connName: c?.name ?? "", dialect: c?.dialect ?? "", env: c?.env ?? "", kind: sum.kind, status: sum.status, rowsAffected: sum.rowsAffected, durationMs: sum.durationMs, sql, error: sum.error, hash: Math.random().toString(16).slice(2).padEnd(64, "0") });
    }
  };

  return {
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

    auditList: async ({ connId, afterSeq = 0, limit = 200 }) =>
      audit.filter((e) => e.seq > afterSeq && (!connId || e.connId === connId)).slice(0, limit),
    auditVerify: async () => ({ entries: audit.length, ok: true }),
    async auditExport() {
      await sleep(200);
      return true;
    },
  };
}
