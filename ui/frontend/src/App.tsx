import { lazy, Suspense, useCallback, useRef, useState } from "react";

import { type Connection, useBackend } from "./api";
import { Icon, type IconName } from "./components/icons";
import type { EditorHandle } from "./components/SqlEditor";
import { ErrorText } from "./components/ui";
import { useAsync } from "./hooks/useAsync";
import { AdvisorScreen } from "./screens/AdvisorScreen";
import { AuditScreen } from "./screens/AuditScreen";
import { ConnectionList } from "./screens/ConnectionList";
import { ConnectionsScreen } from "./screens/ConnectionsScreen";
import { HistoryScreen } from "./screens/HistoryScreen";
import { MonitorScreen } from "./screens/MonitorScreen";
import { QueryScreen } from "./screens/QueryScreen";
import { SettingsScreen } from "./screens/SettingsScreen";

// React Flow and dagre load only when the diagram is first opened.
const RelationsScreen = lazy(() => import("./screens/RelationsScreen").then((m) => ({ default: m.RelationsScreen })));

type View = "query" | "history" | "relations" | "advisor" | "monitor" | "connections" | "audit" | "settings";

const views: { id: View; label: string; icon: IconName }[] = [
  { id: "query", label: "Query editor", icon: "code" },
  { id: "history", label: "History", icon: "history" },
  { id: "relations", label: "Relations", icon: "relations" },
  { id: "advisor", label: "Slow-query advisor", icon: "bolt" },
  { id: "monitor", label: "Monitor", icon: "pulse" },
  { id: "connections", label: "Connections", icon: "plug" },
  { id: "audit", label: "Audit log", icon: "audit" },
];
// Pinned to the bottom of the rail.
const settingsView = { id: "settings", label: "AI settings", icon: "sliders" } as const;

/** NEW marks the connections screen's form as creating, not editing. */
const NEW = "";

export function App() {
  const backend = useBackend();
  const conns = useAsync(() => backend.listConnections(), [backend]);
  const connections = conns.data ?? [];
  const [selectedId, setSelectedId] = useState<string>();
  const [view, setView] = useState<View>("query");
  // The connection open in the connections screen; NEW for a new one.
  const [editingId, setEditingId] = useState<string>();
  const [historyVersion, setHistoryVersion] = useState(0);
  const editor = useRef<EditorHandle>(null);

  const conn = connections.find((c) => c.id === selectedId) ?? connections[0];
  const bumpHistory = useCallback(() => setHistoryVersion((v) => v + 1), []);

  const openInEditor = useCallback((sql: string, connId?: string) => {
    if (connId) setSelectedId(connId);
    setView("query");
    editor.current?.setText(sql);
    // The editor is hidden until this render commits.
    requestAnimationFrame(() => editor.current?.focus());
  }, []);

  const newConnection = () => {
    setEditingId(NEW);
    setView("connections");
  };

  const saved = (c: Connection) => {
    setEditingId(c.id);
    setSelectedId(c.id);
    conns.reload();
  };

  const deleted = () => {
    setEditingId(undefined);
    conns.reload();
  };

  const useInEditor = (c: Connection) => {
    setSelectedId(c.id);
    setView("query");
  };

  const workspace = view === "query" || view === "history" || view === "relations";
  return (
    <div className="app">
      <nav className="rail" aria-label="Main">
        <img className="rail-logo" src="/favicon.svg" alt="lumen" width={32} height={32} />
        {views.map((v) => (
          <RailItem key={v.id} {...v} current={view === v.id} onClick={() => setView(v.id)} />
        ))}
        <span className="spacer" />
        <RailItem {...settingsView} current={view === "settings"} onClick={() => setView("settings")} />
      </nav>
      <div className="view">
        <ErrorText error={conns.error} />
        {/* Hidden rather than unmounted, so the editor text and the last result survive view switches. */}
        <div className="workspace" hidden={!workspace}>
          <ConnectionList connections={connections} selectedId={conn?.id} onSelect={setSelectedId} onNew={newConnection} />
          <main className="workspace-main">
            {conn && (
              <div className="fill" hidden={view !== "query"}>
                <QueryScreen conn={conn} editorRef={editor} onRan={bumpHistory} />
              </div>
            )}
            {conn && view === "history" && (
              <HistoryScreen conn={conn} refresh={historyVersion} onOpen={(sql) => openInEditor(sql)} />
            )}
            {conn && view === "relations" && (
              <Suspense fallback={<p className="muted pad">Loading diagram…</p>}>
                <RelationsScreen key={conn.id} conn={conn} onOpen={(sql) => openInEditor(sql)} />
              </Suspense>
            )}
            {!conn && !conns.loading && (
              <div className="empty-state">
                <p>Add a connection to start querying.</p>
                <button type="button" className="primary" onClick={newConnection}>
                  <Icon name="plus" size={14} strokeWidth={2} /> New connection
                </button>
              </div>
            )}
          </main>
        </div>
        {view === "connections" && (
          <ConnectionsScreen
            connections={connections}
            editingId={editingId ?? conn?.id ?? NEW}
            onEdit={setEditingId}
            onNew={() => setEditingId(NEW)}
            onSaved={saved}
            onDeleted={deleted}
            onUse={useInEditor}
          />
        )}
        {view === "audit" && <AuditScreen connections={connections} onOpen={openInEditor} />}
        {(view === "advisor" || view === "monitor") && !conn && !conns.loading && (
          <div className="empty-state">
            <p>Add a connection first.</p>
            <button type="button" className="primary" onClick={newConnection}>
              <Icon name="plus" size={14} strokeWidth={2} /> New connection
            </button>
          </div>
        )}
        {view === "advisor" && conn && (
          <AdvisorScreen conn={conn} connections={connections} onSelectConn={setSelectedId} onOpen={openInEditor} />
        )}
        {view === "monitor" && conn && (
          <MonitorScreen conn={conn} connections={connections} onSelectConn={setSelectedId} onAdvisor={() => setView("advisor")} />
        )}
        {view === "settings" && <SettingsScreen />}
      </div>
    </div>
  );
}

interface RailItemProps {
  label: string;
  icon: IconName;
  current: boolean;
  onClick: () => void;
}

function RailItem({ label, icon, current, onClick }: RailItemProps) {
  return (
    <button type="button" className="rail-item" aria-label={label} title={label} aria-current={current ? "page" : undefined} onClick={onClick}>
      <Icon name={icon} size={20} />
    </button>
  );
}
