import { useCallback, useRef, useState } from "react";

import { type Connection, useBackend } from "./api";
import type { EditorHandle } from "./components/SqlEditor";
import { ErrorText } from "./components/ui";
import { useAsync } from "./hooks/useAsync";
import { AuditScreen } from "./screens/AuditScreen";
import { ConnectionForm } from "./screens/ConnectionForm";
import { ConnectionList } from "./screens/ConnectionList";
import { HistoryScreen } from "./screens/HistoryScreen";
import { QueryScreen } from "./screens/QueryScreen";

type Tab = "query" | "history" | "audit";
type FormState = { conn?: Connection } | null;

const tabs: { id: Tab; label: string; needsConn: boolean }[] = [
  { id: "query", label: "Query", needsConn: true },
  { id: "history", label: "History", needsConn: true },
  { id: "audit", label: "Audit log", needsConn: false },
];

export function App() {
  const backend = useBackend();
  const conns = useAsync(() => backend.listConnections(), [backend]);
  const connections = conns.data ?? [];
  const [selectedId, setSelectedId] = useState<string>();
  const [tab, setTab] = useState<Tab>("query");
  const [form, setForm] = useState<FormState>(null);
  const [historyVersion, setHistoryVersion] = useState(0);
  const editor = useRef<EditorHandle>(null);

  const conn = connections.find((c) => c.id === selectedId) ?? connections[0];
  const bumpHistory = useCallback(() => setHistoryVersion((v) => v + 1), []);

  const openInEditor = useCallback((sql: string) => {
    setTab("query");
    editor.current?.setText(sql);
    editor.current?.focus();
  }, []);

  const saved = (c: Connection) => {
    setForm(null);
    setSelectedId(c.id);
    conns.reload();
  };

  return (
    <div className="app">
      <ConnectionList
        connections={connections}
        selectedId={conn?.id}
        onSelect={(id) => {
          setSelectedId(id);
          setForm(null);
        }}
        onNew={() => setForm({})}
        onEdit={(c) => setForm({ conn: c })}
        onDeleted={conns.reload}
      />
      <main className="main">
        <ErrorText error={conns.error} />
        {/* An overlay, so the query screen underneath keeps its state. */}
        {form && (
          <div className="overlay">
            <ConnectionForm key={form.conn?.id ?? "new"} conn={form.conn} onSaved={saved} onCancel={() => setForm(null)} />
          </div>
        )}
        <div className="tabs" role="tablist">
          {tabs.map((t) => (
            <button
              key={t.id}
              type="button"
              role="tab"
              aria-selected={tab === t.id}
              disabled={t.needsConn && !conn}
              onClick={() => setTab(t.id)}
            >
              {t.label}
            </button>
          ))}
        </div>
        {/* The query screen stays mounted so the editor text and the last result survive tab switches. */}
        {conn && (
          <div className="tab-panel" hidden={tab !== "query"}>
            <QueryScreen conn={conn} editorRef={editor} onRan={bumpHistory} />
          </div>
        )}
        {conn && tab === "history" && (
          <div className="tab-panel">
            <HistoryScreen connId={conn.id} refresh={historyVersion} onOpen={openInEditor} />
          </div>
        )}
        {tab === "audit" && (
          <div className="tab-panel">
            <AuditScreen connections={connections} />
          </div>
        )}
        {!conn && tab !== "audit" && !conns.loading && (
          <p className="muted pad">Add a connection to start querying.</p>
        )}
      </main>
    </div>
  );
}
