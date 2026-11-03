// Pure model and layout for the relations (ER) diagram. No React here, so
// it is unit-tested in node and the screen stays a thin view.
import dagre from "@dagrejs/dagre";

import type { DbSchema, Dialect, ForeignKey, Table } from "../api";

export type Direction = "LR" | "TB";

/** Geometry shared with the CSS in styles.css (.erd-*). */
export const ROW_H = 22;
export const HEAD_H = 34;
const PAD_Y = 6;
const CHAR_W = 7.2;
const MIN_W = 180;
const MAX_W = 380;

export const tableId = (t: { schema: string; name: string }) => `${t.schema}.${t.name}`;
export const fkSource = (fk: ForeignKey) => `${fk.fromSchema}.${fk.fromTable}`;
export const fkTarget = (fk: ForeignKey) => `${fk.toSchema}.${fk.toTable}`;
export const fkId = (fk: ForeignKey) => `${fkSource(fk)}:${fk.name}:${fk.fromColumns.join(",")}`;

export interface Graph {
  tables: Map<string, Table>;
  /** Columns of each table that take part in a foreign key, either side. */
  linked: Map<string, Set<string>>;
  /** FK columns per table (the referencing side). */
  fkCols: Map<string, Set<string>>;
  /** Neighbours by table id, ignoring direction. */
  adj: Map<string, Set<string>>;
  edges: ForeignKey[];
}

export interface GraphFilter {
  showViews: boolean;
  /** Only tables in this schema; "" keeps all. */
  schema: string;
  /** When set, only this table and tables within `depth` hops. */
  focus?: string;
  depth?: number;
}

/** buildGraph indexes the schema after applying the filter. */
export function buildGraph(s: DbSchema, f: GraphFilter): Graph {
  const tables = new Map<string, Table>();
  for (const t of s.tables) {
    if (!f.showViews && t.kind === "view") continue;
    if (f.schema && t.schema !== f.schema) continue;
    tables.set(tableId(t), t);
  }
  let edges = s.foreignKeys.filter((fk) => tables.has(fkSource(fk)) && tables.has(fkTarget(fk)));

  if (f.focus && tables.has(f.focus)) {
    const keep = reach(adjacency(edges), f.focus, f.depth ?? 1);
    for (const id of [...tables.keys()]) if (!keep.has(id)) tables.delete(id);
    edges = edges.filter((fk) => keep.has(fkSource(fk)) && keep.has(fkTarget(fk)));
  }

  const linked = new Map<string, Set<string>>();
  const fkCols = new Map<string, Set<string>>();
  const add = (m: Map<string, Set<string>>, id: string, cols: string[]) => {
    let set = m.get(id);
    if (!set) m.set(id, (set = new Set()));
    for (const c of cols) set.add(c);
  };
  for (const fk of edges) {
    add(linked, fkSource(fk), fk.fromColumns);
    add(linked, fkTarget(fk), fk.toColumns);
    add(fkCols, fkSource(fk), fk.fromColumns);
  }
  return { tables, linked, fkCols, adj: adjacency(edges), edges };
}

function adjacency(edges: ForeignKey[]): Map<string, Set<string>> {
  const adj = new Map<string, Set<string>>();
  const link = (a: string, b: string) => {
    let set = adj.get(a);
    if (!set) adj.set(a, (set = new Set()));
    set.add(b);
  };
  for (const fk of edges) {
    link(fkSource(fk), fkTarget(fk));
    link(fkTarget(fk), fkSource(fk));
  }
  return adj;
}

/** reach returns ids within depth hops of start, including start. */
export function reach(adj: Map<string, Set<string>>, start: string, depth: number): Set<string> {
  const seen = new Set([start]);
  let frontier = [start];
  for (let d = 0; d < depth && frontier.length; d++) {
    const next: string[] = [];
    for (const id of frontier) {
      for (const n of adj.get(id) ?? []) {
        if (!seen.has(n)) {
          seen.add(n);
          next.push(n);
        }
      }
    }
    frontier = next;
  }
  return seen;
}

/** visibleColumns is what a node draws: all columns, or keys only. */
export function visibleColumns(t: Table, linked: Set<string> | undefined, keysOnly: boolean) {
  if (!keysOnly) return t.columns;
  return t.columns.filter((c) => c.primaryKey || linked?.has(c.name));
}

export function nodeSize(t: Table, rows: number): { width: number; height: number } {
  let chars = t.name.length + 4;
  for (const c of t.columns) chars = Math.max(chars, c.name.length + c.type.length + 6);
  return {
    width: Math.round(Math.min(MAX_W, Math.max(MIN_W, chars * CHAR_W + 24))),
    height: HEAD_H + Math.max(rows, 1) * ROW_H + PAD_Y * 2,
  };
}

export interface Placed {
  id: string;
  x: number;
  y: number;
  width: number;
  height: number;
}

/**
 * layout places connected tables with dagre (referenced tables first, so
 * parents sit left of / above children) and parks tables without any
 * relation in a grid after them, instead of one endless dagre rank.
 */
export function layout(g: Graph, sizes: Map<string, { width: number; height: number }>, dir: Direction): Placed[] {
  const dg = new dagre.graphlib.Graph({ multigraph: true });
  dg.setGraph({ rankdir: dir, nodesep: 40, ranksep: 110, edgesep: 20, marginx: 20, marginy: 20 });
  dg.setDefaultEdgeLabel(() => ({}));

  const loose: string[] = [];
  for (const id of g.tables.keys()) {
    if (!g.adj.get(id)?.size) {
      loose.push(id);
      continue;
    }
    dg.setNode(id, { ...sizes.get(id)! });
  }
  for (const fk of g.edges) {
    if (fkSource(fk) === fkTarget(fk)) continue; // self references don't rank
    dg.setEdge(fkTarget(fk), fkSource(fk), {}, fkId(fk));
  }

  const out: Placed[] = [];
  let maxX = 0;
  let maxY = 0;
  if (dg.nodeCount() > 0) {
    dagre.layout(dg);
    for (const id of dg.nodes()) {
      const n = dg.node(id);
      const { width, height } = sizes.get(id)!;
      // dagre gives centres; React Flow wants top-left corners.
      const p = { id, x: n.x - width / 2, y: n.y - height / 2, width, height };
      out.push(p);
      maxX = Math.max(maxX, p.x + width);
      maxY = Math.max(maxY, p.y + height);
    }
  }

  if (loose.length) {
    loose.sort();
    const gap = 40;
    const perRow = Math.max(1, Math.ceil(Math.sqrt(loose.length)));
    // Below the graph in LR (it is wide), to the right in TB (it is tall).
    const ox = out.length && dir === "TB" ? maxX + gap * 2 : 20;
    const oy = out.length && dir === "LR" ? maxY + gap * 2 : 20;
    let x = ox;
    let y = oy;
    let rowH = 0;
    loose.forEach((id, i) => {
      const { width, height } = sizes.get(id)!;
      if (i > 0 && i % perRow === 0) {
        x = ox;
        y += rowH + gap;
        rowH = 0;
      }
      out.push({ id, x, y, width, height });
      x += width + gap;
      rowH = Math.max(rowH, height);
    });
  }
  return out;
}

/** quoteIdent quotes one identifier for the dialect. */
export function quoteIdent(dialect: Dialect, name: string): string {
  if (dialect === "mysql") return "`" + name.replaceAll("`", "``") + "`";
  return '"' + name.replaceAll('"', '""') + '"';
}

/** qualifiedName is the table as it should appear in generated SQL. */
export function qualifiedName(dialect: Dialect, t: { schema: string; name: string }): string {
  const name = quoteIdent(dialect, t.name);
  if (dialect === "sqlite" || (dialect === "postgres" && t.schema === "public") || !t.schema) return name;
  return `${quoteIdent(dialect, t.schema)}.${name}`;
}

export const selectSQL = (dialect: Dialect, t: Table, limit = 100) =>
  `SELECT * FROM ${qualifiedName(dialect, t)} LIMIT ${limit};`;

const mermaidName = (id: string) => id.replace(/[^A-Za-z0-9_]/g, "_");
const mermaidType = (type: string) => type.replace(/[^A-Za-z0-9_]/g, "_") || "unknown";

/** toMermaid renders the graph as a Mermaid erDiagram, for docs and PRs. */
export function toMermaid(g: Graph): string {
  const lines = ["erDiagram"];
  for (const [id, t] of g.tables) {
    lines.push(`  ${mermaidName(id)} {`);
    const fks = g.fkCols.get(id);
    for (const c of t.columns) {
      const keys = [c.primaryKey && "PK", fks?.has(c.name) && "FK"].filter(Boolean).join(",");
      lines.push(`    ${mermaidType(c.type)} ${mermaidName(c.name)}${keys ? " " + keys : ""}`);
    }
    lines.push("  }");
  }
  for (const fk of g.edges) {
    const from = g.tables.get(fkSource(fk))!;
    // Optional on the parent side when every FK column is nullable.
    const optional = fk.fromColumns.every((n) => from.columns.find((c) => c.name === n)?.nullable ?? true);
    lines.push(
      `  ${mermaidName(fkTarget(fk))} ${optional ? "|o" : "||"}--o{ ${mermaidName(fkSource(fk))} : "${fk.fromColumns.join(", ")}"`,
    );
  }
  return lines.join("\n") + "\n";
}
