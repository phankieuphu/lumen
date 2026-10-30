import { useCallback, useRef, useState } from "react";

import { type Connection, useBackend } from "./api";
import { Icon, type IconName } from "./components/icons";
import type { EditorHandle } from "./components/SqlEditor";
import { ErrorText } from "./components/ui";
import { useAsync } from "./hooks/useAsync";
import { AuditScreen } from "./screens/AuditScreen";
import { ConnectionForm } from "./screens/ConnectionForm";
import { ConnectionList } from "./screens/ConnectionList";
import { QueryScreen } from "./screens/QueryScreen";

type View = "query" | "connections" | "audit";

const views: { id: View; label: string; icon: IconName }[] = [
  { id: "query", label: "Query editor", icon: "code" },
  { id: "connections", label: "Connections", icon: "plug" },
  { id: "audit", label: "Audit log", icon: "clipboard" },
];

export function App() {
  const backend = useBackend();
  const conns = useAsync(() => backend.listConnections(), [backend]);
  const connections = conns.data ?? [];
  const [selectedId, setSelectedId] = useState<string>();
  const [view, setView] = useState<View>("query");
  // The connections view edits the selected profile, or a new one.
  const [creating, setCreating] = useState(false);
  const editor = useRef<EditorHandle>(null);

  const conn = connections.find((c) => c.id === selectedId) ?? connections[0];

  const openSQL = useCallback((sql: string) => {
    setView("query");
    editor.current?.setText(sql);
    editor.current?.focus();
  }, []);

  const openInConn = useCallback(
    (connId: string, sql: string) => {
      setSelectedId(connId);
      openSQL(sql);
    },
    [openSQL],
  );

  const select = (id: string) => {
    setSelectedId(id);
    setCreating(false);
  };

  const startNew = () => {
    setCreating(true);
    setView("connections");
  };

  const saved = (c: Connection) => {
    setCreating(false);
    setSelectedId(c.id);
    conns.reload();
  };

  const editing = creating ? undefined : conn;
  return (
    <div className="app">
      <nav className="rail" aria-label="Main">
        <div className="logo" aria-hidden="true">
          <Icon name="database" size={18} />
        </div>
        {views.map((v) => (
          <button
            key={v.id}
            type="button"
            aria-label={v.label}
            title={v.label}
            aria-current={view === v.id ? "page" : undefined}
            onClick={() => setView(v.id)}
          >
            <Icon name={v.icon} size={20} />
          </button>
        ))}
      </nav>

      <ConnectionList connections={connections} selectedId={creating ? undefined : conn?.id} onSelect={select} onNew={startNew} />

      <main className="main">
        <ErrorText error={conns.error} />

        {/* Stays mounted so the editor text and the last result survive view switches. */}
        <div className="view" hidden={view !== "query"}>
          {conn ? (
            <QueryScreen conn={conn} editorRef={editor} onOpenSQL={openSQL} />
          ) : (
            !conns.loading && (
              <div className="page">
                <p className="muted">Add a connection to start querying.</p>
                <div>
                  <button type="button" className="primary" onClick={startNew}>
                    <Icon name="plus" size={14} /> Add connection
                  </button>
                </div>
              </div>
            )
          )}
        </div>

        {view === "connections" && (
          <div className="page">
            {editing || creating ? (
              <ConnectionForm
                key={editing?.id ?? "new"}
                conn={editing}
                onSaved={saved}
                onDeleted={() => {
                  setSelectedId(undefined);
                  conns.reload();
                }}
                onOpen={(c) => {
                  setSelectedId(c.id);
                  setView("query");
                }}
                onCancel={() => setCreating(false)}
              />
            ) : (
              <p className="muted">No connections yet. Use + to add one.</p>
            )}
          </div>
        )}

        {view === "audit" && <AuditScreen connections={connections} onOpen={openInConn} />}
      </main>
    </div>
  );
}
