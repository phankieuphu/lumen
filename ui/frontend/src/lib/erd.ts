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
 * Members of a feature group are laid out as one dagre cluster.
 */
export function layout(
  g: Graph,
  sizes: Map<string, { width: number; height: number }>,
  dir: Direction,
  groups: Group[] = [],
): Placed[] {
  const grouped = new Map<string, string>();
  for (const grp of groups) for (const id of grp.members) if (g.tables.has(id)) grouped.set(id, grp.id);

  const dg = new dagre.graphlib.Graph({ multigraph: true, compound: grouped.size > 0 });
  // Groups draw a header and padding around their members; leave room.
  const sep = grouped.size ? GROUP_PAD * 2 + GROUP_HEAD : 0;
  dg.setGraph({ rankdir: dir, nodesep: 40 + sep, ranksep: 110 + sep, edgesep: 20, marginx: 20, marginy: 20 + sep });
  dg.setDefaultEdgeLabel(() => ({}));

  const loose: string[] = [];
  for (const id of g.tables.keys()) {
    // A table with no relation still joins its group's cluster.
    if (!g.adj.get(id)?.size && !grouped.has(id)) {
      loose.push(id);
      continue;
    }
    dg.setNode(id, { ...sizes.get(id)! });
    const parent = grouped.get(id);
    if (parent) {
      if (!dg.hasNode(parent)) dg.setNode(parent, {});
      dg.setParent(id, parent);
    }
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
      if (!g.tables.has(id)) continue; // a group cluster
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

// Feature groups: tables that belong to one area of the product (orders,
// catalog, auth, …), drawn inside one coloured box.

/** How tables are put into feature groups. */
export type GroupMode = "none" | "schema" | "prefix" | "related" | "custom";

/** Number of group colours in styles.css (.erd-g0 … .erd-g7). */
export const GROUP_COLORS = 8;
/** Space between a group's box and its members, and its header height. */
export const GROUP_PAD = 16;
export const GROUP_HEAD = 26;

export interface Group {
  /** React Flow node id; never a table id. */
  id: string;
  label: string;
  /** Table ids, sorted. */
  members: string[];
  /** Index into the group colours. */
  color: number;
}

/** CustomGroups maps a table id to the name of the feature it belongs to. */
export type CustomGroups = Record<string, string>;

export const groupId = (key: string) => `group:${key}`;

/**
 * featureGroups groups the graph's tables. Automatic modes drop groups of
 * one table, which say nothing; custom groups are kept as the user made
 * them. Groups come biggest first, so colours are stable for a schema.
 */
export function featureGroups(g: Graph, mode: GroupMode, custom: CustomGroups = {}): Group[] {
  if (mode === "none") return [];
  const by = new Map<string, string[]>();
  const put = (key: string, id: string) => {
    let list = by.get(key);
    if (!list) by.set(key, (list = []));
    list.push(id);
  };
  const labels = new Map<string, string>();
  let min = 2;

  switch (mode) {
    case "schema":
      min = 1;
      for (const [id, t] of g.tables) put(t.schema, id);
      break;
    case "prefix":
      for (const [id, t] of g.tables) put(nameStem(t.name), id);
      break;
    case "related": {
      for (const [id, c] of communities(g)) put(c, id);
      // Name a cluster after its most connected table.
      for (const [key, ids] of by) {
        const [hub] = [...ids].sort((a, b) => (g.adj.get(b)?.size ?? 0) - (g.adj.get(a)?.size ?? 0) || a.localeCompare(b));
        if (hub) labels.set(key, g.tables.get(hub)!.name);
      }
      break;
    }
    case "custom":
      min = 1;
      for (const id of g.tables.keys()) {
        const name = custom[id]?.trim();
        if (name) put(name, id);
      }
      break;
  }

  const out = [...by]
    .filter(([, ids]) => ids.length >= min)
    .map(([key, ids]) => ({ key, label: labels.get(key) ?? key, members: ids.sort() }))
    .sort((a, b) => b.members.length - a.members.length || a.label.localeCompare(b.label));
  return out.map((grp, i) => ({ id: groupId(grp.key), label: grp.label, members: grp.members, color: i % GROUP_COLORS }));
}

/**
 * nameStem is the feature a table name points at: its first word,
 * singular, so orders, order_lines, and OrderTotals all give "order".
 */
export function nameStem(name: string): string {
  const words = name
    .replace(/([a-z0-9])([A-Z])/g, "$1_$2")
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter(Boolean);
  return singular(words[0] ?? name.toLowerCase());
}

function singular(w: string): string {
  if (w.length > 4 && w.endsWith("ies")) return w.slice(0, -3) + "y";
  if (/(ss|us|is)$/.test(w)) return w;
  if (/(s|x|z|ch|sh)es$/.test(w)) return w.slice(0, -2);
  if (w.length > 1 && w.endsWith("s")) return w.slice(0, -1);
  return w;
}

/**
 * communities clusters tables by their foreign keys with the local-moving
 * phase of Louvain: each table joins the neighbouring cluster that raises
 * modularity most, until nothing moves. Tables are visited in id order so
 * the result is the same on every run. Returns table id → cluster key.
 */
export function communities(g: Graph): Map<string, string> {
  const ids = [...g.tables.keys()].sort();
  const w = new Map<string, Map<string, number>>();
  const inc = (a: string, b: string) => {
    let m = w.get(a);
    if (!m) w.set(a, (m = new Map()));
    m.set(b, (m.get(b) ?? 0) + 1);
  };
  let m2 = 0;
  for (const fk of g.edges) {
    const a = fkSource(fk);
    const b = fkTarget(fk);
    if (a === b) continue;
    inc(a, b);
    inc(b, a);
    m2 += 2;
  }
  const comm = new Map(ids.map((id) => [id, id]));
  if (m2 === 0) return comm;

  const deg = new Map(ids.map((id) => [id, [...(w.get(id)?.values() ?? [])].reduce((x, y) => x + y, 0)]));
  const tot = new Map(deg);
  for (let pass = 0, moved = true; moved && pass < 32; pass++) {
    moved = false;
    for (const id of ids) {
      const k = deg.get(id)!;
      if (k === 0) continue;
      const cur = comm.get(id)!;
      const links = new Map<string, number>();
      for (const [n, wt] of w.get(id)!) {
        const c = comm.get(n)!;
        links.set(c, (links.get(c) ?? 0) + wt);
      }
      tot.set(cur, tot.get(cur)! - k);
      const gain = (c: string) => (links.get(c) ?? 0) - (tot.get(c)! * k) / m2;
      let best = cur;
      let bestGain = gain(cur);
      for (const c of [...links.keys()].sort()) {
        const v = gain(c);
        if (v > bestGain + 1e-9) [best, bestGain] = [c, v];
      }
      tot.set(best, tot.get(best)! + k);
      if (best !== cur) {
        comm.set(id, best);
        moved = true;
      }
    }
  }
  return comm;
}

export interface GroupBox {
  group: Group;
  x: number;
  y: number;
  width: number;
  height: number;
}

/** groupBoxes frames each group around its members' current positions. */
export function groupBoxes(groups: Group[], boxes: Map<string, { x: number; y: number; width: number; height: number }>): GroupBox[] {
  const out: GroupBox[] = [];
  for (const group of groups) {
    let [x0, y0, x1, y1] = [Infinity, Infinity, -Infinity, -Infinity];
    for (const id of group.members) {
      const b = boxes.get(id);
      if (!b) continue;
      x0 = Math.min(x0, b.x);
      y0 = Math.min(y0, b.y);
      x1 = Math.max(x1, b.x + b.width);
      y1 = Math.max(y1, b.y + b.height);
    }
    if (x0 === Infinity) continue;
    out.push({
      group,
      x: x0 - GROUP_PAD,
      y: y0 - GROUP_PAD - GROUP_HEAD,
      width: x1 - x0 + GROUP_PAD * 2,
      height: y1 - y0 + GROUP_PAD * 2 + GROUP_HEAD,
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
