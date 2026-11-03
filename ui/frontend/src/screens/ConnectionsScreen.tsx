import type { Connection } from "../api";
import { Icon } from "../components/icons";
import { EnvBadge, EnvDot } from "../components/ui";
import { dialectName } from "../lib/format";
import { ConnectionForm } from "./ConnectionForm";

interface Props {
  connections: readonly Connection[];
  /** The connection in the form; "" for a new one. */
  editingId: string;
  onEdit: (id: string) => void;
  onNew: () => void;
  onSaved: (c: Connection) => void;
  onDeleted: () => void;
  /** Open the connection in the query editor. */
  onUse: (c: Connection) => void;
}

/** ConnectionsScreen lists saved connections and edits one at a time. */
export function ConnectionsScreen({ connections, editingId, onEdit, onNew, onSaved, onDeleted, onUse }: Props) {
  const conn = connections.find((c) => c.id === editingId);
  return (
    <div className="connections">
      <aside className="sidebar conn-cards" aria-label="Saved connections">
        <div className="sidebar-head">
          <h1 className="title">Connections</h1>
          <button type="button" onClick={onNew}>
            <Icon name="plus" size={14} strokeWidth={2} /> New
          </button>
        </div>
        {connections.length === 0 && <p className="muted pad">No connections yet.</p>}
        <ul>
          {connections.map((c) => (
            <li key={c.id}>
              <button type="button" className="conn-card" aria-current={c.id === conn?.id ? "true" : undefined} onClick={() => onEdit(c.id)}>
                <EnvDot env={c.env} />
                <span className="conn-card-text">
                  <span className="conn-name">{c.name}</span>
                  <span className="muted small-text">{dialectName(c.dialect)}</span>
                </span>
                <EnvBadge env={c.env} />
              </button>
            </li>
          ))}
        </ul>
      </aside>
      <main className="page">
        <ConnectionForm key={conn?.id ?? "new"} conn={conn} onSaved={onSaved} onDeleted={onDeleted} onUse={onUse} />
      </main>
    </div>
  );
}
