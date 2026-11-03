import { describe, expect, it } from "vitest";

import type { DbSchema, Table } from "../api";
import { buildGraph, layout, nodeSize, qualifiedName, reach, selectSQL, toMermaid, visibleColumns } from "./erd";

const col = (name: string, type = "int", extra: { pk?: boolean; nullable?: boolean } = {}) => ({
  name,
  type,
  nullable: extra.nullable ?? false,
  primaryKey: extra.pk ?? false,
});

const shop: DbSchema = {
  dialect: "postgres",
  truncated: false,
  tables: [
    { schema: "public", name: "customers", kind: "table", columns: [col("id", "int", { pk: true }), col("email", "text")] },
    { schema: "public", name: "orders", kind: "table", columns: [col("id", "int", { pk: true }), col("customer_id")] },
    { schema: "public", name: "lines", kind: "table", columns: [col("order_id", "int", { pk: true }), col("note", "text", { nullable: true })] },
    { schema: "public", name: "settings", kind: "table", columns: [col("k", "text", { pk: true })] },
    { schema: "public", name: "v_orders", kind: "view", columns: [col("id")] },
    { schema: "audit", name: "events", kind: "table", columns: [col("id", "int", { pk: true }), col("customer_id", "int", { nullable: true })] },
  ],
  foreignKeys: [
    { name: "orders_customer", fromSchema: "public", fromTable: "orders", fromColumns: ["customer_id"], toSchema: "public", toTable: "customers", toColumns: ["id"] },
    { name: "lines_order", fromSchema: "public", fromTable: "lines", fromColumns: ["order_id"], toSchema: "public", toTable: "orders", toColumns: ["id"] },
    { name: "events_customer", fromSchema: "audit", fromTable: "events", fromColumns: ["customer_id"], toSchema: "public", toTable: "customers", toColumns: ["id"] },
  ],
};

const ids = (g: ReturnType<typeof buildGraph>) => [...g.tables.keys()].sort();

describe("buildGraph", () => {
  it("hides views and filters by schema, dropping edges that leave it", () => {
    const g = buildGraph(shop, { showViews: false, schema: "public" });
    expect(ids(g)).toEqual(["public.customers", "public.lines", "public.orders", "public.settings"]);
    expect(g.edges.map((e) => e.name)).toEqual(["orders_customer", "lines_order"]);
    expect([...g.fkCols.get("public.orders")!]).toEqual(["customer_id"]);
    expect([...g.linked.get("public.orders")!].sort()).toEqual(["customer_id", "id"]);
  });

  it("focus keeps the table and its neighbours within depth", () => {
    const one = buildGraph(shop, { showViews: true, schema: "", focus: "public.lines", depth: 1 });
    expect(ids(one)).toEqual(["public.lines", "public.orders"]);
    const two = buildGraph(shop, { showViews: true, schema: "", focus: "public.lines", depth: 2 });
    expect(ids(two)).toEqual(["public.customers", "public.lines", "public.orders"]);
  });

  it("ignores a focus that is filtered out", () => {
    const g = buildGraph(shop, { showViews: false, schema: "", focus: "public.v_orders" });
    expect(g.tables.size).toBe(5);
  });
});

describe("reach", () => {
  it("handles cycles", () => {
    const adj = new Map([
      ["a", new Set(["b"])],
      ["b", new Set(["a", "c"])],
      ["c", new Set(["b"])],
    ]);
    expect([...reach(adj, "a", 5)].sort()).toEqual(["a", "b", "c"]);
    expect([...reach(adj, "a", 0)]).toEqual(["a"]);
  });
});

describe("layout", () => {
  const g = buildGraph(shop, { showViews: true, schema: "" });
  const sizes = new Map([...g.tables].map(([id, t]) => [id, nodeSize(t, t.columns.length)]));

  it.each(["LR", "TB"] as const)("places every table without overlaps (%s)", (dir) => {
    const placed = layout(g, sizes, dir);
    expect(placed.map((p) => p.id).sort()).toEqual(ids(g));
    for (const a of placed) {
      for (const b of placed) {
        if (a === b) continue;
        const overlap = a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
        expect(overlap, `${a.id} overlaps ${b.id}`).toBe(false);
      }
    }
  });

  it("puts referenced tables before referencing ones in LR", () => {
    const at = new Map(layout(g, sizes, "LR").map((p) => [p.id, p]));
    expect(at.get("public.customers")!.x).toBeLessThan(at.get("public.orders")!.x);
    expect(at.get("public.orders")!.x).toBeLessThan(at.get("public.lines")!.x);
  });

  it("parks unrelated tables below the graph in LR", () => {
    const placed = layout(g, sizes, "LR");
    const at = new Map(placed.map((p) => [p.id, p]));
    const graphBottom = Math.max(...["public.customers", "public.orders", "public.lines", "audit.events"].map((id) => at.get(id)!.y + at.get(id)!.height));
    expect(at.get("public.settings")!.y).toBeGreaterThan(graphBottom);
  });
});

describe("visibleColumns", () => {
  const t: Table = shop.tables[2]!;
  it("keys only keeps PK and linked columns", () => {
    expect(visibleColumns(t, new Set(["order_id"]), true).map((c) => c.name)).toEqual(["order_id"]);
    expect(visibleColumns(t, undefined, false)).toHaveLength(2);
  });
});

describe("SQL helpers", () => {
  it("quotes and qualifies per dialect", () => {
    expect(qualifiedName("postgres", { schema: "public", name: "Order" })).toBe('"Order"');
    expect(qualifiedName("postgres", { schema: "audit", name: 'a"b' })).toBe('"audit"."a""b"');
    expect(qualifiedName("mysql", { schema: "shop", name: "or`der" })).toBe("`shop`.`or``der`");
    expect(qualifiedName("sqlite", { schema: "main", name: "t" })).toBe('"t"');
    expect(selectSQL("sqlite", shop.tables[0]!)).toBe('SELECT * FROM "customers" LIMIT 100;');
  });
});

describe("toMermaid", () => {
  it("renders entities, keys, and cardinality", () => {
    const out = toMermaid(buildGraph(shop, { showViews: false, schema: "" }));
    expect(out).toContain("public_orders {\n    int id PK\n    int customer_id FK\n  }");
    expect(out).toContain('public_customers ||--o{ public_orders : "customer_id"');
    expect(out).toContain('public_customers |o--o{ audit_events : "customer_id"');
  });
});
