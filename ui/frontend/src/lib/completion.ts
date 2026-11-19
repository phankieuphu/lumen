import type { Completion } from "@codemirror/autocomplete";
import type { SQLNamespace } from "@codemirror/lang-sql";

import type { DbSchema } from "../api";

/** SchemaCompletion is what the editor needs to complete names. */
export interface SchemaCompletion {
  namespace: SQLNamespace;
  /** Tables in this schema complete without the schema prefix. */
  defaultSchema?: string;
}

/**
 * schemaCompletion turns a database schema into the editor's completion
 * namespace: schema → table → column, with column types as details.
 */
export function schemaCompletion(s: DbSchema): SchemaCompletion {
  const schemas: Record<string, { self: Completion; children: Record<string, SQLNamespace> }> = {};
  const counts = new Map<string, number>();
  for (const t of s.tables) {
    const ns = (schemas[t.schema] ??= { self: { label: t.schema, type: "namespace" }, children: {} });
    counts.set(t.schema, (counts.get(t.schema) ?? 0) + 1);
    ns.children[t.name] = {
      self: { label: t.name, type: t.kind === "view" ? "class" : "type", detail: t.kind },
      children: t.columns.map((c) => ({
        label: c.name,
        type: "property",
        detail: c.primaryKey ? `${c.type} · PK` : c.type,
        boost: c.primaryKey ? 1 : 0,
      })),
    };
  }
  return { namespace: schemas, defaultSchema: defaultSchema(counts) };
}

// defaultSchema is PostgreSQL's public when present, else the schema with
// the most tables (MySQL's database, SQLite's main).
function defaultSchema(counts: Map<string, number>): string | undefined {
  if (counts.has("public")) return "public";
  let best: string | undefined;
  for (const [name, n] of counts) if (best === undefined || n > counts.get(best)!) best = name;
  return best;
}
