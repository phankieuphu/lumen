// In-memory advisor, monitor, and AI settings for `npm run dev:mock`.
// Development builds only (see index.ts); the data is made up.
import type {
  AIPreview,
  AIProvider,
  AISettings,
  AlertRule,
  Backend,
  Connection,
  DbSession,
  PreviewLine,
  SlowQuery,
  SlowQueryDetail,
} from "./types";

type FeatureMethods = Pick<
  Backend,
  | "advisorList"
  | "advisorDetail"
  | "monitorSnapshot"
  | "monitorEndSession"
  | "monitorSetAlert"
  | "aiProviders"
  | "aiSettings"
  | "saveAISettings"
  | "aiPreview"
>;

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

const REVENUE_SQL = `SELECT c.id, c.name, SUM(o.total_amount) AS revenue
FROM orders o
JOIN customers c ON c.id = o.customer_id
WHERE o.created_at >= CURRENT_DATE - INTERVAL '30 days'
  AND o.status = 'paid'
GROUP BY c.id, c.name
ORDER BY revenue DESC LIMIT 20;`;

const slowQueries: SlowQueryDetail[] = [
  {
    id: "q1",
    title: "Revenue per customer, last 30 days",
    sql: REVENUE_SQL,
    tables: ["orders", "customers"],
    meanMs: 1840,
    calls: 312,
    sharePct: 38,
    state: "fix-verified",
    planSource: "EXPLAIN (FORMAT JSON)",
    plan: [
      { label: "Limit", depth: 0, cost: 9412.8, sharePct: 0 },
      { label: "Sort", depth: 1, cost: 9412.75, sharePct: 4 },
      { label: "HashAggregate", depth: 2, cost: 9060.1, sharePct: 9 },
      { label: "Hash Join", depth: 3, cost: 8214.4, sharePct: 14 },
      { label: "Seq Scan on orders", depth: 4, cost: 6870, sharePct: 71 },
      { label: "Seq Scan on customers", depth: 4, cost: 184, sharePct: 2 },
    ],
    advice: {
      kind: "index",
      title: "Index orders on status and created_at",
      explanation:
        "The plan reads every row of orders to find paid orders from the last 30 days. An index on (status, created_at) lets the database read only the rows it needs.",
      sql: "CREATE INDEX CONCURRENTLY orders_status_created_at_idx\n  ON orders (status, created_at);",
      verifiedBy: "Verified with a hypothetical index (HypoPG). No real index was created.",
      costBefore: 9412,
      costAfter: 1208,
    },
  },
  {
    id: "q2",
    title: "Products never ordered",
    sql: "SELECT p.id, p.name\nFROM products p\nWHERE p.id NOT IN (SELECT product_id FROM order_items);",
    tables: ["products", "order_items"],
    meanMs: 960,
    calls: 48,
    sharePct: 9,
    state: "rewrite-verified",
    planSource: "EXPLAIN (FORMAT JSON)",
    plan: [
      { label: "Seq Scan on products", depth: 0, cost: 4120.5, sharePct: 22 },
      { label: "SubPlan (hashed)", depth: 1, cost: 3204.1, sharePct: 78 },
      { label: "Seq Scan on order_items", depth: 2, cost: 3204.1, sharePct: 78 },
    ],
    advice: {
      kind: "rewrite",
      title: "Use NOT EXISTS instead of NOT IN",
      explanation:
        "NOT IN against a subquery cannot use an anti-join and returns no rows if product_id is ever NULL. NOT EXISTS gives the same answer and lets the planner use the order_items index.",
      sql: "SELECT p.id, p.name\nFROM products p\nWHERE NOT EXISTS (\n  SELECT 1 FROM order_items oi WHERE oi.product_id = p.id\n);",
      verifiedBy: "Verified with EXPLAIN: same columns, lower cost. The rewrite was not run.",
      costBefore: 4120,
      costAfter: 1388,
    },
  },
  {
    id: "q3",
    title: "Count all orders",
    sql: "SELECT count(*) FROM orders;",
    tables: ["orders"],
    meanMs: 410,
    calls: 1440,
    sharePct: 12,
    state: "no-fix",
    planSource: "EXPLAIN (FORMAT JSON)",
    plan: [
      { label: "Aggregate", depth: 0, cost: 5320.2, sharePct: 4 },
      { label: "Index Only Scan on orders_pkey", depth: 1, cost: 5108, sharePct: 96 },
    ],
    advice: null,
    noAdviceReason:
      "An exact count has to visit every row. It already uses the smallest index. If an estimate is enough, read reltuples from pg_class instead.",
  },
];

const summary = ({ plan: _p, advice: _a, noAdviceReason: _n, planSource: _s, ...q }: SlowQueryDetail): SlowQuery => q;

const providers: AIProvider[] = [
  {
    id: "anthropic",
    name: "Anthropic",
    local: false,
    note: "Claude models through the API",
    ready: true,
    keyNote: "API key saved in keychain",
    models: ["claude-haiku-5-5", "claude-sonnet-5-5", "claude-opus-5-5"],
  },
  {
    id: "openai",
    name: "OpenAI-compatible",
    local: false,
    note: "Any server with the same API shape",
    ready: false,
    keyNote: "No API key yet",
    models: ["[SMALL MODEL]", "[LARGE MODEL]"],
  },
  {
    id: "ollama",
    name: "Ollama",
    local: true,
    note: "Runs on this computer",
    ready: true,
    keyNote: "No key needed",
    models: ["[LOCAL MODEL]"],
  },
];

const destinations = {
  anthropic: "Sent to the Anthropic API",
  openai: "Sent to your OpenAI-compatible server",
  ollama: "Stays on this computer",
} as const;

function previewLines(task: "advise" | "inline", s: AISettings): PreviewLine[] {
  const L = (text: string, tone?: PreviewLine["tone"]): PreviewLine => (tone ? { text, tone } : { text });
  if (task === "inline" && !s.inlineCompletion) {
    return [L("Inline completion is off.", "muted"), L("No request is sent while you type.", "muted")];
  }
  const m = s.maskLiterals;
  const query = [
    L("SELECT c.id, c.name, SUM(o.total_amount) AS revenue"),
    L("FROM orders o JOIN customers c ON c.id = o.customer_id"),
    L(`WHERE o.created_at >= CURRENT_DATE - ${m ? "?" : "INTERVAL '30 days'"}`),
    L(`  AND o.status = ${m ? "?" : "'paid'"}`),
  ];
  const schema = [
    L("Schema (2 of 4 tables)", "head"),
    L("orders(id bigint PK, customer_id bigint FK, status text,"),
    L("       total_amount numeric, created_at timestamptz)"),
    L("  indexes: orders_pkey(id), orders_created_at_idx(created_at)", "muted"),
    L("customers(id bigint PK, name text, email text)"),
    L("  indexes: customers_pkey(id)", "muted"),
  ];
  const sample = s.sampleRows
    ? [
        L(""),
        L("Sample rows", "warn"),
        L("orders: (88213, 1042, 'paid', 129.90, '2026-10-02 09:14:55+00')", "warn"),
        L("customers: (1042, 'Blue Harbor Cafe', 'orders@blueharbor.example')", "warn"),
      ]
    : [];
  if (task === "inline") {
    return [
      L("Task: inline_complete", "head"),
      L("Dialect: postgres", "muted"),
      L(""),
      L("Text before the cursor", "head"),
      ...query,
      L("GROUP BY c.id, c.name"),
      L("ORDER BY "),
      L(""),
      ...schema,
      ...sample,
    ];
  }
  return [
    L("Task: advise_slow", "head"),
    L("Dialect: postgres", "muted"),
    L(""),
    L("Query", "head"),
    ...query,
    L(`GROUP BY c.id, c.name ORDER BY revenue DESC LIMIT ${m ? "?" : "20"}`),
    L(""),
    ...schema,
    L(""),
    L("Plan", "head"),
    L("Limit > Sort > HashAggregate > Hash Join"),
    L("  > Seq Scan on orders (71% of cost)"),
    L("  > Seq Scan on customers (2% of cost)"),
    ...sample,
  ];
}

/** A smooth, bounded random walk, so charts look like real samples. */
function walk(n: number, start: number, step: number, min: number, max: number): number[] {
  const out: number[] = [];
  let v = start;
  for (let i = 0; i < n; i++) {
    v = Math.min(max, Math.max(min, v + (Math.random() - 0.5) * step));
    out.push(Math.round(v));
  }
  return out;
}

export function createMockFeatures(conns: () => readonly Connection[]): FeatureMethods {
  const started = Date.now();
  const ended = new Set<number>();
  const alerts: AlertRule[] = [
    { id: "long-query", label: "A query runs longer than 30 seconds", enabled: true, firing: false },
    { id: "connections", label: "Connections are above 80% of the maximum", enabled: true, firing: false },
    { id: "lock-wait", label: "More than 5 sessions wait on a lock", enabled: true, firing: false },
    { id: "cache-hit", label: "Cache hit rate is below 95%", enabled: false, firing: false },
  ];
  let settings: AISettings = {
    provider: "anthropic",
    models: { inline: "claude-haiku-5-5", textToSql: "claude-haiku-5-5", fixError: "claude-sonnet-5-5", advise: "claude-sonnet-5-5" },
    sampleRows: false,
    maskLiterals: true,
    inlineCompletion: true,
  };

  const find = (connId: string) => {
    if (!conns().some((c) => c.id === connId)) throw new Error("conn: not found");
  };

  return {
    async advisorList(connId, f) {
      find(connId);
      await sleep(150);
      const needle = f.search.trim().toLowerCase();
      const hours = f.windowHours || 24;
      const order: Record<typeof f.sort, (q: SlowQuery) => number> = {
        share: (q) => q.sharePct,
        mean: (q) => q.meanMs,
        calls: (q) => q.calls,
      };
      const queries = slowQueries
        .map((q) => ({ ...summary(q), calls: Math.round((q.calls * hours) / 24) }))
        .filter((q) => q.meanMs >= f.minMeanMs && q.calls >= f.minCalls)
        .filter((q) => !needle || q.sql.toLowerCase().includes(needle))
        .filter((q) => !f.kind || q.sql.trimStart().toLowerCase().startsWith(f.kind))
        .sort((a, b) => order[f.sort](b) - order[f.sort](a));
      return { queries, statsSince: new Date(started - 36 * 3_600_000).toISOString(), windowed: f.windowHours > 0, truncated: false };
    },
    async advisorDetail(connId, id) {
      find(connId);
      await sleep(100);
      const q = slowQueries.find((x) => x.id === id);
      if (!q) throw new Error("advisor: query not found");
      return q;
    },

    async monitorSnapshot(connId, rangeMinutes) {
      find(connId);
      await sleep(80);
      // The long query started 20 s before the mock did, so the alert fires soon.
      const longSeconds = Math.round((Date.now() - started) / 1000) + 20;
      const sessions: DbSession[] = [
        { pid: 4412, user: "report_job", state: "active", seconds: longSeconds, query: "SELECT p.id, p.name FROM products p WHERE p.id NOT IN (SELECT product_id FROM order_items)" },
        { pid: 4398, user: "app", state: "active", seconds: 0.2, query: "SELECT c.id, c.name, SUM(o.total_amount) AS revenue FROM orders o JOIN customers c ON c.id = o.customer_id" },
        { pid: 4391, user: "app", state: "idle in transaction", seconds: 12, query: "UPDATE orders SET status = $1 WHERE id = $2" },
        { pid: 4380, user: "app", state: "waiting on lock", seconds: 4, query: "UPDATE orders SET status = $1 WHERE id = $2", blockedBy: 4391 },
        { pid: 4377, user: "admin", state: "idle", seconds: null, query: "" },
      ].filter((s) => !ended.has(s.pid)) as DbSession[];
      const blocked = sessions.filter((s) => s.blockedBy !== undefined && !ended.has(s.blockedBy));
      const longest = sessions.find((s) => s.pid === 4412);
      const points = Math.min(90, rangeMinutes * 2);
      const used = 14 - ended.size;
      const longRule = alerts.find((a) => a.id === "long-query")!;
      longRule.firing = longRule.enabled && !!longest && longSeconds > 30;
      return {
        at: new Date().toISOString(),
        activeSessions: sessions.filter((s) => s.state === "active").length,
        connectionsUsed: used,
        maxConnections: 100,
        waitingOnLocks: blocked.length,
        rootBlocker: blocked[0]?.blockedBy,
        cacheHitPct: 98.6,
        longest: longest && { pid: longest.pid, seconds: longSeconds, limitSeconds: 30 },
        series: {
          connections: [...walk(points - 1, 11, 2, 8, 18), used],
          tps: walk(points, 200, 40, 150, 260),
        },
        sessions,
        totalSessions: used,
        alerts: alerts.map((a) => ({ ...a })),
        firing: longRule.firing
          ? { message: `a query has been running for ${longSeconds} seconds (session 4412, limit 30 s).`, pid: 4412 }
          : undefined,
      };
    },
    async monitorEndSession(connId, pid) {
      find(connId);
      await sleep(150);
      ended.add(pid);
    },
    async monitorSetAlert(connId, ruleId, enabled) {
      find(connId);
      const rule = alerts.find((a) => a.id === ruleId);
      if (!rule) throw new Error("monitor: unknown alert rule");
      rule.enabled = enabled;
      if (!enabled) rule.firing = false;
    },

    aiProviders: async () => providers,
    aiSettings: async () => settings,
    async saveAISettings(next) {
      await sleep(100);
      settings = next;
      return settings;
    },
    async aiPreview(task, s): Promise<AIPreview> {
      const p = providers.find((x) => x.id === s.provider) ?? providers[0]!;
      return { destination: destinations[p.id], local: p.local, lines: previewLines(task, s) };
    },
  };
}
