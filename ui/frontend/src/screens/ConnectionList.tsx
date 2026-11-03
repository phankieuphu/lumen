import type { Connection } from "../api";
import { Icon } from "../components/icons";
import { EnvBadge } from "../components/ui";
import { connTarget } from "../lib/format";

interface Props {
  connections: readonly Connection[];
  selectedId: string | undefined;
  onSelect: (id: string) => void;
  onNew: () => void;
}

/** ConnectionList is the query workspace's sidebar: pick the connection to query. */
export function ConnectionList({ connections, selectedId, onSelect, onNew }: Props) {
  return (
    <aside className="sidebar" aria-label="Connections">
      <div className="sidebar-head">
        <h2 className="eyebrow">Connections</h2>
        <button type="button" className="icon-button" onClick={onNew} aria-label="New connection" title="New connection">
          <Icon name="plus" strokeWidth={2} />
        </button>
      </div>
      {connections.length === 0 && <p className="muted pad">No connections yet.</p>}
      <ul className="conn-list">
        {connections.map((c) => (
          <li key={c.id}>
            <button
              type="button"
              className="conn-item"
              aria-current={c.id === selectedId ? "true" : undefined}
              onClick={() => onSelect(c.id)}
            >
              <span className="conn-name">{c.name}</span>
              {c.readOnly && (
                <span className="conn-lock" title="Read-only" aria-label="Read-only">
                  <Icon name="lock" size={13} />
                </span>
              )}
              <EnvBadge env={c.env} />
              <span className="conn-meta">
                {c.dialect} · {connTarget(c)}
              </span>
            </button>
          </li>
        ))}
      </ul>
    </aside>
  );
}
