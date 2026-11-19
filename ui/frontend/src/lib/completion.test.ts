import { describe, expect, it } from "vitest";

import type { DbSchema, Table } from "../api";
import { schemaCompletion } from "./completion";

const table = (schema: string, name: string, kind: Table["kind"] = "table"): Table => ({
  schema,
  name,
  kind,
  columns: [
    { name: "id", type: "int8", nullable: false, primaryKey: true },
    { name: "email", type: "text", nullable: true, primaryKey: false },
  ],
});

const db = (tables: Table[]): DbSchema => ({ dialect: "postgres", tables, foreignKeys: [], truncated: false, version: 1 });

describe("schemaCompletion", () => {
  it("nests schema → table → column with types", () => {
    const { namespace } = schemaCompletion(db([table("public", "users"), table("public", "active_users", "view")]));
    const pub = (namespace as unknown as Record<string, { children: Record<string, { self: { type: string }; children: unknown[] }> }>).public!;
    expect(pub.children.users!.self.type).toBe("type");
    expect(pub.children.active_users!.self.type).toBe("class");
    expect(pub.children.users!.children).toEqual([
      { label: "id", type: "property", detail: "int8 · PK", boost: 1 },
      { label: "email", type: "property", detail: "text", boost: 0 },
    ]);
  });

  it.each([
    { name: "public wins", tables: [table("audit", "a"), table("audit", "b"), table("public", "c")], want: "public" },
    { name: "else the largest schema", tables: [table("shop", "a"), table("shop", "b"), table("logs", "c")], want: "shop" },
    { name: "none for an empty database", tables: [], want: undefined },
  ])("default schema: $name", ({ tables, want }) => {
    expect(schemaCompletion(db(tables)).defaultSchema).toBe(want);
  });
});

describe("completion speed", () => {
  // The backend's limits: 1,000 tables and 50,000 columns.
  const big: DbSchema = db(
    Array.from({ length: 1000 }, (_, i) => ({
      schema: "public",
      name: `table_${i}`,
      kind: "table" as const,
      columns: Array.from({ length: 50 }, (_, j) => ({ name: `col_${j}`, type: "text", nullable: true, primaryKey: j === 0 })),
    })),
  );

  it("completes a table and a column within 50 ms at the schema limits", async () => {
    const { schemaCompletionSource, PostgreSQL, sql } = await import("@codemirror/lang-sql");
    const { CompletionContext } = await import("@codemirror/autocomplete");
    const { EditorState } = await import("@codemirror/state");
    const { namespace, defaultSchema } = schemaCompletion(big);
    const config = { dialect: PostgreSQL, schema: namespace, defaultSchema };
    const source = schemaCompletionSource(config);
    const timeAt = (doc: string) => {
      const state = EditorState.create({ doc, extensions: [sql(config)] });
      const ctx = new CompletionContext(state, doc.length, true);
      source(ctx); // warm up
      const start = performance.now();
      const result = source(ctx);
      return { ms: performance.now() - start, result };
    };
    const tables = timeAt("SELECT * FROM table_9");
    const columns = timeAt("SELECT table_999.");
    expect(tables.result && "options" in tables.result && tables.result.options.length).toBeGreaterThan(0);
    expect(columns.result && "options" in columns.result && columns.result.options.length).toBe(50);
    expect(tables.ms).toBeLessThan(50);
    expect(columns.ms).toBeLessThan(50);
  });
});
