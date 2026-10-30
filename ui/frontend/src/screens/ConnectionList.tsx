import { useState } from "react";

import { type Connection, errorMessage, useBackend } from "../api";
import { Confirm, EnvBadge, ErrorText } from "../components/ui";

interface Props {
  connections: readonly Connection[];
  selectedId: string | undefined;
  onSelect: (id: string) => void;
  onNew: () => void;
  onEdit: (c: Connection) => void;
  onDeleted: () => void;
}

export function ConnectionList({ connections, selectedId, onSelect, onNew, onEdit, onDeleted }: Props) {
  const backend = useBackend();
  const [deleting, setDeleting] = useState<Connection>();
  const [error, setError] = useState<string>();

  const remove = async (c: Connection) => {
    setDeleting(undefined);
    try {
      await backend.deleteConnection(c.id);
      setError(undefined);
      onDeleted();
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  return (
    <nav className="sidebar" aria-label="Connections">
      <div className="sidebar-head">
        <h2>Connections</h2>
        <button type="button" onClick={onNew} title="New connection">
          + New
        </button>
      </div>
      <ErrorText error={error} />
      {connections.length === 0 && <p className="muted pad">No connections yet.</p>}
      <ul className="conn-list">
        {connections.map((c) => (
          <li key={c.id} className={c.id === selectedId ? "selected" : undefined}>
            <button type="button" className="conn-main" onClick={() => onSelect(c.id)}>
              <span className="conn-name">{c.name}</span>
              <span className="conn-meta">
                {c.dialect} · {c.dialect === "sqlite" ? c.database : `${c.host}${c.database ? `/${c.database}` : ""}`}
              </span>
            </button>
            <span className="conn-tags">
              <EnvBadge env={c.env} />
              {c.readOnly && <span className="badge">ro</span>}
            </span>
            <span className="conn-actions">
              <button type="button" className="link" onClick={() => onEdit(c)}>
                Edit
              </button>
              <button type="button" className="link danger" onClick={() => setDeleting(c)}>
                Delete
              </button>
            </span>
          </li>
        ))}
      </ul>
      {deleting && (
        <Confirm
          title="Delete connection"
          confirmLabel="Delete"
          danger
          onConfirm={() => void remove(deleting)}
          onCancel={() => setDeleting(undefined)}
        >
          Delete <strong>{deleting.name}</strong> and its stored password? Its audit entries are kept.
        </Confirm>
      )}
    </nav>
  );
}
