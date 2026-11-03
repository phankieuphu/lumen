import { useCallback, useRef, useState } from "react";

import { type Connection, useBackend } from "./api";
import { Icon, type IconName } from "./components/icons";
import type { EditorHandle } from "./components/SqlEditor";
import { ErrorText } from "./components/ui";
import { useAsync } from "./hooks/useAsync";
import { AuditScreen } from "./screens/AuditScreen";
import { ConnectionList } from "./screens/ConnectionList";
import { ConnectionsScreen } from "./screens/ConnectionsScreen";
import { HistoryScreen } from "./screens/HistoryScreen";
import { QueryScreen } from "./screens/QueryScreen";

type View = "query" | "history" | "connections" | "audit";

const views: { id: View; label: string; icon: IconName }[] = [
  { id: "query", label: "Query editor", icon: "code" },
  { id: "history", label: "History", icon: "history" },
  { id: "connections", label: "Connections", icon: "plug" },
  { id: "audit", label: "Audit log", icon: "audit" },
];

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

  const workspace = view === "query" || view === "history";
  return (
    <div className="app">
      <nav className="rail" aria-label="Main">
        <div className="rail-logo" title="lumen">
          <Icon name="database" size={18} strokeWidth={2} />
        </div>
        {views.map((v) => (
          <button
            key={v.id}
            type="button"
            className="rail-item"
            aria-label={v.label}
            title={v.label}
            aria-current={view === v.id ? "page" : undefined}
            onClick={() => setView(v.id)}
          >
            <Icon name={v.icon} size={20} />
          </button>
        ))}
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
      </div>
    </div>
  );
}
