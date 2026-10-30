import type { Connection } from "../api";
import { Icon } from "../components/icons";
import { EnvBadge } from "../components/ui";

interface Props {
  connections: readonly Connection[];
  selectedId: string | undefined;
  onSelect: (id: string) => void;
  onNew: () => void;
}

/** ConnectionList is the sidebar: pick the connection the views work on. */
export function ConnectionList({ connections, selectedId, onSelect, onNew }: Props) {
  return (
    <nav className="sidebar" aria-label="Connections">
      <div className="sidebar-head">
        <span className="eyebrow">Connections</span>
        <button type="button" className="icon ghost" onClick={onNew} aria-label="Add connection">
          <Icon name="plus" />
        </button>
      </div>
      {connections.length === 0 && <p className="muted pad">No connections yet.</p>}
      <ul className="conn-list">
        {connections.map((c) => (
          <li key={c.id}>
            <button
              type="button"
              className="conn-item"
              aria-current={c.id === selectedId}
              onClick={() => onSelect(c.id)}
              title={`${c.dialect} · ${c.dialect === "sqlite" ? c.database : `${c.host}/${c.database}`}`}
            >
              <span className="name">{c.name}</span>
              {c.readOnly && <Icon name="lock" size={13} label="Read-only" />}
              <EnvBadge env={c.env} />
            </button>
          </li>
        ))}
      </ul>
    </nav>
  );
}
