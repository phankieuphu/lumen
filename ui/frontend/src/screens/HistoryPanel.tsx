import { useCallback } from "react";

import { type HistoryEntry, useBackend } from "../api";
import { type ItemColumn, ItemGrid } from "../components/ItemGrid";
import { ErrorText, KindBadge, StatusBadge } from "../components/ui";
import { usePaged } from "../hooks/usePaged";
import { formatCount, formatDuration, formatTime, oneLine } from "../lib/format";

const PAGE = 100;

const columns: ItemColumn<HistoryEntry>[] = [
  { key: "at", title: "Started", width: 170, render: (h) => formatTime(h.startedAt) },
  { key: "status", title: "Status", width: 90, render: (h) => <StatusBadge status={h.status} /> },
  { key: "kind", title: "Kind", width: 80, render: (h) => <KindBadge kind={h.kind} /> },
  { key: "ms", title: "Time", width: 90, align: "end", render: (h) => formatDuration(h.durationMs) },
  { key: "rows", title: "Rows", width: 90, align: "end", render: (h) => formatCount(h.rowCount) },
  { key: "sql", title: "SQL", width: 900, render: (h) => <span title={h.error}>{oneLine(h.sql)}</span> },
];

interface Props {
  connId: string;
  /** Bump to reload, e.g. after a query ran. */
  refresh: number;
  onOpen: (sql: string) => void;
}

/** HistoryScreen lists past requests for one connection, newest first. */
export function HistoryScreen({ connId, refresh, onOpen }: Props) {
  const backend = useBackend();
  const fetchPage = useCallback(
    (last: HistoryEntry | undefined, limit: number) => backend.queryHistory(connId, last?.id ?? 0, limit),
    [backend, connId],
  );
  const { items, error, loading, loadMore } = usePaged(fetchPage, PAGE, [connId, refresh]);

  return (
    <section className="list-screen">
      <p className="muted pad">Click an entry to open it in the editor.</p>
      <ErrorText error={error} />
      <ItemGrid
        columns={columns}
        items={items}
        onSelect={(i) => onOpen(items[i]!.sql)}
        onEndReached={loadMore}
        empty={loading ? "Loading…" : "No queries yet."}
      />
    </section>
  );
}
