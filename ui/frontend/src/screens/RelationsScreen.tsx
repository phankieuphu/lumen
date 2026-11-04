// Relations: an interactive ER diagram of the connection's database, drawn
// with React Flow. Model and layout live in lib/erd.ts; this file is view.
import "@xyflow/react/dist/style.css";

import {
  Background,
  BackgroundVariant,
  type BuiltInEdge,
  Controls,
  Handle,
  MiniMap,
  type Node,
  type NodeProps,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useNodesState,
  useReactFlow,
} from "@xyflow/react";
import { type KeyboardEvent, memo, type ReactNode, useCallback, useEffect, useMemo, useState } from "react";

import { type Connection, type DbSchema, type ForeignKey, type Table, useBackend } from "../api";
import { ErrorText } from "../components/ui";
import { useAsync } from "../hooks/useAsync";
import {
  buildGraph,
  type Direction,
  fkId,
  fkSource,
  fkTarget,
  type Graph,
  layout,
  nodeSize,
  selectSQL,
  tableId,
  toMermaid,
  visibleColumns,
} from "../lib/erd";

/** Above this many tables only nodes in view are rendered. */
const VIRTUALIZE_AT = 150;

interface TableData extends Record<string, unknown> {
  table: Table;
  columns: Table["columns"];
  linked: Set<string> | undefined;
  fks: Set<string> | undefined;
  width: number;
  /** Columns joined to the active table, drawn highlighted. */
  hot?: Set<string>;
}
type TableNode = Node<TableData, "table">;

const sideHandles = (col: string) => (
  <>
    <Handle type="source" id={`s-l-${col}`} position={Position.Left} isConnectable={false} />
    <Handle type="source" id={`s-r-${col}`} position={Position.Right} isConnectable={false} />
    <Handle type="target" id={`t-l-${col}`} position={Position.Left} isConnectable={false} />
    <Handle type="target" id={`t-r-${col}`} position={Position.Right} isConnectable={false} />
  </>
);

const TableNodeView = memo(function TableNodeView({ data }: NodeProps<TableNode>) {
  const { table, columns, linked, fks, width, hot } = data;
  return (
    <div className={`erd-table erd-${table.kind}`} style={{ width }}>
      <div className="erd-head" title={`${table.schema}.${table.name}`}>
        <span className="erd-name">{table.name}</span>
        {table.kind === "view" && <span className="erd-tag">view</span>}
        <span className="erd-schema">{table.schema}</span>
      </div>
      {columns.length === 0 && <div className="erd-row muted">no key columns</div>}
      {columns.map((c) => (
        <div key={c.name} className={`erd-row${c.primaryKey ? " pk" : ""}${hot?.has(c.name) ? " hot" : ""}`}>
          <span className="erd-key" aria-label={c.primaryKey ? "primary key" : fks?.has(c.name) ? "foreign key" : undefined}>
            {c.primaryKey ? "PK" : fks?.has(c.name) ? "FK" : ""}
          </span>
          <span className="erd-col">{c.name}</span>
          <span className="erd-type">
            {c.type}
            {c.nullable ? "?" : ""}
          </span>
          {linked?.has(c.name) && sideHandles(c.name)}
        </div>
      ))}
    </div>
  );
});

const nodeTypes = { table: TableNodeView };

/** SVG markers for crow's-foot notation; edges reference them by id. */
function Markers() {
  const marker = (id: string, body: ReactNode) => (
    <marker id={id} viewBox="0 0 16 14" refX="16" refY="7" markerWidth="16" markerHeight="14" orient="auto-start-reverse" markerUnits="userSpaceOnUse">
      {body}
    </marker>
  );
  const sets = [
    ["", "erd-mk"],
    ["-hl", "erd-mk hl"],
  ] as const;
  return (
    <svg className="erd-markers" aria-hidden="true">
      <defs>
        {sets.map(([suffix, cls]) => (
          <g key={suffix} className={cls}>
            {/* many: crow's foot opening toward the table */}
            {marker(`erd-many${suffix}`, <path d="M4 7 L16 1 M4 7 L16 13 M4 7 L16 7 M2 1 L2 13" />)}
            {/* exactly one: two bars */}
            {marker(`erd-one${suffix}`, <path d="M0 7 L16 7 M8 1 L8 13 M12 1 L12 13" />)}
            {/* zero or one: ring and bar */}
            {marker(`erd-zero-one${suffix}`, <><path d="M0 7 L3 7 M12 1 L12 13 M12 7 L16 7" /><circle cx="7" cy="7" r="3.5" /></>)}
          </g>
        ))}
      </defs>
    </svg>
  );
}

interface Props {
  conn: Connection;
  onOpen: (sql: string) => void;
}

/** RelationsScreen loads the schema and shows the diagram. */
export function RelationsScreen({ conn, onOpen }: Props) {
  const backend = useBackend();
  const s = useAsync(() => backend.schema(conn.id), [backend, conn.id]);
  return (
    <section className="erd-screen">
      <ErrorText error={s.error} />
      {s.loading && !s.data && <p className="muted pad">Reading schema…</p>}
      {s.data && (
        <ReactFlowProvider>
          <Diagram schema={s.data} onOpen={onOpen} onRefresh={s.reload} loading={s.loading} />
        </ReactFlowProvider>
      )}
    </section>
  );
}

interface DiagramProps {
  schema: DbSchema;
  onOpen: (sql: string) => void;
  onRefresh: () => void;
  loading: boolean;
}

function Diagram({ schema, onOpen, onRefresh, loading }: DiagramProps) {
  const backend = useBackend();
  const flow = useReactFlow<TableNode, BuiltInEdge>();
  const schemas = useMemo(() => [...new Set(schema.tables.map((t) => t.schema))].sort(), [schema]);
  const [schemaFilter, setSchemaFilter] = useState("");
  const [showViews, setShowViews] = useState(false);
  const [keysOnly, setKeysOnly] = useState(false);
  const [dir, setDir] = useState<Direction>("LR");
  const [focus, setFocus] = useState<{ id: string; depth: number }>();
  const [selected, setSelected] = useState<string>();
  const [hovered, setHovered] = useState<string>();
  const [search, setSearch] = useState("");
  const [relayout, setRelayout] = useState(0);
  const [copied, setCopied] = useState(false);

  const graph = useMemo(
    () => buildGraph(schema, { showViews, schema: schemaFilter, focus: focus?.id, depth: focus?.depth }),
    [schema, showViews, schemaFilter, focus],
  );

  const [nodes, setNodes, onNodesChange] = useNodesState<TableNode>([]);

  // Lay out again only when what is drawn changes, not on selection.
  useEffect(() => {
    const sizes = new Map<string, { width: number; height: number }>();
    const cols = new Map<string, Table["columns"]>();
    for (const [id, t] of graph.tables) {
      const c = visibleColumns(t, graph.linked.get(id), keysOnly);
      cols.set(id, c);
      sizes.set(id, nodeSize(t, c.length));
    }
    const placed = layout(graph, sizes, dir);
    setNodes(
      placed.map((p) => ({
        id: p.id,
        type: "table",
        position: { x: p.x, y: p.y },
        data: {
          table: graph.tables.get(p.id)!,
          columns: cols.get(p.id)!,
          linked: graph.linked.get(p.id),
          fks: graph.fkCols.get(p.id),
          width: p.width,
        },
      })),
    );
    const t = requestAnimationFrame(() => void flow.fitView({ padding: 0.1, duration: 250 }));
    return () => cancelAnimationFrame(t);
  }, [graph, keysOnly, dir, relayout, setNodes, flow]);

  // Keep the selection valid when filters remove the table.
  useEffect(() => {
    if (selected && !graph.tables.has(selected)) setSelected(undefined);
  }, [graph, selected]);

  const active = selected ?? hovered;
  const related = useMemo(() => {
    if (!active) return undefined;
    return new Set([active, ...(graph.adj.get(active) ?? [])]);
  }, [active, graph]);

  const query = search.trim().toLowerCase();
  const matches = useMemo(
    () => (query ? new Set([...graph.tables.keys()].filter((id) => id.toLowerCase().includes(query))) : undefined),
    [graph, query],
  );

  // Columns on both ends of every relation of the active table.
  const hotCols = useMemo(() => {
    const m = new Map<string, Set<string>>();
    if (!active) return m;
    const add = (id: string, cols: string[]) => {
      let set = m.get(id);
      if (!set) m.set(id, (set = new Set()));
      for (const c of cols) set.add(c);
    };
    for (const fk of graph.edges) {
      if (fkSource(fk) !== active && fkTarget(fk) !== active) continue;
      add(fkSource(fk), fk.fromColumns);
      add(fkTarget(fk), fk.toColumns);
    }
    return m;
  }, [active, graph]);

  const shownNodes = useMemo(
    () =>
      nodes.map((n) => {
        const cls = [
          n.id === selected && "erd-selected",
          related && !related.has(n.id) && "erd-dim",
          matches?.has(n.id) && "erd-match",
          matches && !matches.has(n.id) && "erd-dim",
        ]
          .filter(Boolean)
          .join(" ");
        const hot = hotCols.get(n.id);
        if (cls === (n.className ?? "") && hot === n.data.hot) return n;
        return { ...n, className: cls, data: { ...n.data, hot } };
      }),
    [nodes, selected, related, matches, hotCols],
  );

  const edges = useMemo(() => buildEdges(graph, nodes, active), [graph, nodes, active]);

  const focusOn = useCallback(
    (id: string) => {
      setSelected(id);
      void flow.fitView({ nodes: [{ id }], padding: 0.6, duration: 300, maxZoom: 1.2 });
    },
    [flow],
  );

  const onSearchKey = (e: KeyboardEvent) => {
    if (e.key === "Enter" && matches?.size) {
      void flow.fitView({ nodes: [...matches].map((id) => ({ id })), padding: 0.3, duration: 300, maxZoom: 1.2 });
      if (matches.size === 1) setSelected([...matches][0]);
    }
    if (e.key === "Escape") setSearch("");
  };

  const copyMermaid = async () => {
    try {
      await backend.copyText(toMermaid(graph));
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  };

  const sel = selected ? graph.tables.get(selected) : undefined;

  return (
    <>
      <div className="toolbar erd-toolbar">
        <input
          type="search"
          placeholder="Find table…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          onKeyDown={onSearchKey}
          aria-label="Find table"
        />
        {schemas.length > 1 && (
          <select value={schemaFilter} onChange={(e) => setSchemaFilter(e.target.value)} aria-label="Schema">
            <option value="">All schemas</option>
            {schemas.map((s) => (
              <option key={s}>{s}</option>
            ))}
          </select>
        )}
        <label className="check">
          <input type="checkbox" checked={showViews} onChange={(e) => setShowViews(e.target.checked)} /> Views
        </label>
        <label className="check">
          <input type="checkbox" checked={keysOnly} onChange={(e) => setKeysOnly(e.target.checked)} /> Keys only
        </label>
        <select value={dir} onChange={(e) => setDir(e.target.value as Direction)} aria-label="Layout direction">
          <option value="LR">Left → right</option>
          <option value="TB">Top → bottom</option>
        </select>
        <button type="button" onClick={() => setRelayout((n) => n + 1)} title="Discard dragged positions and lay out again">
          Auto layout
        </button>
        <button type="button" onClick={copyMermaid} title="Copy the visible diagram as a Mermaid erDiagram">
          {copied ? "Copied" : "Copy Mermaid"}
        </button>
        <button type="button" onClick={onRefresh} disabled={loading}>
          {loading ? "Reading…" : "Refresh"}
        </button>
        <span className="spacer" />
        <span className="muted small">
          {graph.tables.size} tables · {graph.edges.length} relations
          {matches && ` · ${matches.size} found`}
        </span>
      </div>
      {focus && (
        <div className="erd-focus">
          Focused on <b>{graph.tables.get(focus.id)?.name ?? focus.id}</b>
          <label>
            depth{" "}
            <select value={focus.depth} onChange={(e) => setFocus({ ...focus, depth: Number(e.target.value) })}>
              {[1, 2, 3, 4].map((d) => (
                <option key={d}>{d}</option>
              ))}
            </select>
          </label>
          <button type="button" className="link" onClick={() => setFocus(undefined)}>
            Show all
          </button>
        </div>
      )}
      {schema.truncated && (
        <p className="warn pad small">The schema is large; only the first tables were read. Filter by schema to see the rest.</p>
      )}
      <div className="split">
        <div className="erd-canvas">
          <Markers />
          {graph.tables.size === 0 ? (
            <p className="muted pad">No tables{schemaFilter ? ` in ${schemaFilter}` : ""}.</p>
          ) : (
            <ReactFlow
              nodes={shownNodes}
              edges={edges}
              nodeTypes={nodeTypes}
              onNodesChange={onNodesChange}
              onNodeClick={(_, n) => setSelected(n.id)}
              onNodeDoubleClick={(_, n) => onOpen(selectSQL(schema.dialect, n.data.table))}
              onNodeMouseEnter={(_, n) => setHovered(n.id)}
              onNodeMouseLeave={() => setHovered(undefined)}
              onPaneClick={() => setSelected(undefined)}
              nodesConnectable={false}
              edgesFocusable={false}
              elementsSelectable
              onlyRenderVisibleElements={graph.tables.size > VIRTUALIZE_AT}
              minZoom={0.05}
              maxZoom={2}
              colorMode="system"
              proOptions={{ hideAttribution: true }}
              fitView
            >
              <Background variant={BackgroundVariant.Dots} gap={18} size={1} />
              <Controls showInteractive={false} />
              <MiniMap pannable zoomable nodeStrokeWidth={2} className="erd-minimap" />
            </ReactFlow>
          )}
        </div>
        {sel && (
          <TableDetail
            table={sel}
            graph={graph}
            onSelect={focusOn}
            onFocus={() => setFocus({ id: tableId(sel), depth: focus?.depth ?? 1 })}
            onQuery={() => onOpen(selectSQL(schema.dialect, sel))}
            onClose={() => setSelected(undefined)}
          />
        )}
      </div>
    </>
  );
}

/** buildEdges anchors each FK on its first column pair, on the facing sides. */
function buildEdges(graph: Graph, nodes: TableNode[], active: string | undefined): BuiltInEdge[] {
  const box = new Map(nodes.map((n) => [n.id, { x: n.position.x, w: n.data.width }]));
  const out: BuiltInEdge[] = [];
  for (const fk of graph.edges) {
    const src = fkSource(fk);
    const dst = fkTarget(fk);
    const a = box.get(src);
    const b = box.get(dst);
    if (!a || !b) continue;
    let s: "l" | "r" = "r";
    let t: "l" | "r" = "r";
    if (src !== dst) {
      if (a.x >= b.x + b.w) [s, t] = ["l", "r"];
      else if (b.x >= a.x + a.w) [s, t] = ["r", "l"];
      // Stacked boxes: run both ends out the same side.
    }
    const hl = active !== undefined && (src === active || dst === active);
    const sfx = hl ? "-hl" : "";
    const optional = isOptional(graph, fk);
    out.push({
      id: fkId(fk),
      source: src,
      target: dst,
      sourceHandle: `s-${s}-${fk.fromColumns[0]}`,
      targetHandle: `t-${t}-${fk.toColumns[0]}`,
      type: "smoothstep",
      pathOptions: { borderRadius: 10, offset: 18 },
      markerStart: `erd-many${sfx}`,
      markerEnd: `${optional ? "erd-zero-one" : "erd-one"}${sfx}`,
      className: hl ? "erd-edge hl" : active ? "erd-edge dim" : "erd-edge",
      animated: hl,
      selectable: false,
      focusable: false,
    });
  }
  return out;
}

function isOptional(graph: Graph, fk: ForeignKey): boolean {
  const t = graph.tables.get(fkSource(fk));
  return fk.fromColumns.every((n) => t?.columns.find((c) => c.name === n)?.nullable ?? true);
}

interface DetailProps {
  table: Table;
  graph: Graph;
  onSelect: (id: string) => void;
  onFocus: () => void;
  onQuery: () => void;
  onClose: () => void;
}

function TableDetail({ table, graph, onSelect, onFocus, onQuery, onClose }: DetailProps) {
  const id = tableId(table);
  const outgoing = graph.edges.filter((fk) => fkSource(fk) === id);
  const incoming = graph.edges.filter((fk) => fkTarget(fk) === id && fkSource(fk) !== id);
  const fks = graph.fkCols.get(id);
  const ref = (other: string, label: string) => (
    <button type="button" className="link" onClick={() => onSelect(other)}>
      {label}
    </button>
  );
  return (
    <aside className="detail erd-detail">
      <div className="erd-detail-head">
        <h2 title={id}>{table.name}</h2>
        <span className="muted small">{table.schema}</span>
        <span className="spacer" />
        <button type="button" className="link" onClick={onClose} aria-label="Close details">
          ✕
        </button>
      </div>
      <div className="erd-detail-actions">
        <button type="button" className="primary" onClick={onQuery}>
          Query
        </button>
        <button type="button" onClick={onFocus} title="Show only this table and its neighbours">
          Focus
        </button>
      </div>
      <h3>Columns ({table.columns.length})</h3>
      <table className="erd-cols">
        <tbody>
          {table.columns.map((c) => (
            <tr key={c.name}>
              <td className={c.primaryKey ? "erd-key pk" : "erd-key"}>{c.primaryKey ? "PK" : fks?.has(c.name) ? "FK" : ""}</td>
              <td className="mono">{c.name}</td>
              <td className="mono muted">
                {c.type}
                {c.nullable ? "" : " not null"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <h3>References ({outgoing.length})</h3>
      {outgoing.length === 0 && <p className="muted small">None.</p>}
      <ul className="erd-refs">
        {outgoing.map((fk) => (
          <li key={fkId(fk)}>
            <span className="mono">{fk.fromColumns.join(", ")}</span> → {ref(fkTarget(fk), fk.toTable)}
            <span className="mono muted">.{fk.toColumns.join(", ")}</span>
          </li>
        ))}
      </ul>
      <h3>Referenced by ({incoming.length})</h3>
      {incoming.length === 0 && <p className="muted small">None.</p>}
      <ul className="erd-refs">
        {incoming.map((fk) => (
          <li key={fkId(fk)}>
            {ref(fkSource(fk), fk.fromTable)}
            <span className="mono muted">.{fk.fromColumns.join(", ")}</span>
          </li>
        ))}
      </ul>
    </aside>
  );
}
