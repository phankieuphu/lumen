import { useCallback } from "react";

import { type Connection, type HistoryEntry, useBackend } from "../api";
import { type ItemColumn, ItemGrid } from "../components/ItemGrid";
import { ErrorText, KindBadge, StatusBadge } from "../components/ui";
import { usePaged } from "../hooks/usePaged";
import { formatCount, formatDuration, formatTime, oneLine } from "../lib/format";

const PAGE = 100;

const columns: ItemColumn<HistoryEntry>[] = [
  { key: "at", title: "Started", width: 196, render: (h) => <span className="mono muted">{formatTime(h.startedAt)}</span> },
  { key: "status", title: "Status", width: 96, render: (h) => <StatusBadge status={h.status} /> },
  { key: "kind", title: "Kind", width: 86, render: (h) => <KindBadge kind={h.kind} /> },
  { key: "ms", title: "Time", width: 96, align: "end", render: (h) => <span className="mono">{formatDuration(h.durationMs)}</span> },
  { key: "rows", title: "Rows", width: 96, align: "end", render: (h) => <span className="mono">{formatCount(h.rowCount)}</span> },
  { key: "sql", title: "SQL", width: 900, render: (h) => <span className="mono" title={h.error}>{oneLine(h.sql)}</span> },
];

interface Props {
  conn: Connection;
  /** Bump to reload, e.g. after a query ran. */
  refresh: number;
  onOpen: (sql: string) => void;
}

/** HistoryScreen lists past requests for one connection, newest first. */
export function HistoryScreen({ conn, refresh, onOpen }: Props) {
  const backend = useBackend();
  const fetchPage = useCallback(
    (last: HistoryEntry | undefined, limit: number) => backend.queryHistory(conn.id, last?.id ?? 0, limit),
    [backend, conn.id],
  );
  const { items, error, loading, loadMore } = usePaged(fetchPage, PAGE, [conn.id, refresh]);

  return (
    <section className="page list-page" aria-label="History">
      <header className="page-head">
        <div>
          <h1 className="title">History</h1>
          <p className="muted">
            Queries run on <strong className="fg">{conn.name}</strong>, newest first. Click one to open it in the editor.
          </p>
        </div>
      </header>
      <ErrorText error={error} />
      <div className="card grid-card">
        <ItemGrid
          columns={columns}
          items={items}
          onSelect={(i) => onOpen(items[i]!.sql)}
          onEndReached={loadMore}
          empty={loading ? "Loading…" : "No queries yet."}
        />
      </div>
    </section>
  );
}
