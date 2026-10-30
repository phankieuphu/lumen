import { useCallback } from "react";

import { type HistoryEntry, useBackend } from "../api";
import { type ItemColumn, ItemGrid } from "../components/ItemGrid";
import { ErrorText, KindBadge, StatusText } from "../components/ui";
import { usePaged } from "../hooks/usePaged";
import { formatCount, formatDuration, formatTime, oneLine } from "../lib/format";

const PAGE = 100;

/** rowsText reads the history row count by kind: returned rows or rows changed. */
function rowsText(h: HistoryEntry): string {
  if (h.kind === "read") return formatCount(h.rowCount);
  return h.rowCount > 0 ? `${formatCount(h.rowCount)} changed` : "—";
}

const columns: ItemColumn<HistoryEntry>[] = [
  { key: "at", title: "Started", width: 170, render: (h) => formatTime(h.startedAt) },
  { key: "status", title: "Status", width: 90, render: (h) => <StatusText status={h.status} /> },
  { key: "kind", title: "Kind", width: 80, render: (h) => <KindBadge kind={h.kind} /> },
  { key: "ms", title: "Time", width: 90, align: "end", render: (h) => formatDuration(h.durationMs) },
  { key: "rows", title: "Rows", width: 110, align: "end", render: (h) => rowsText(h) },
  {
    key: "sql",
    title: "SQL",
    width: 320,
    grow: true,
    render: (h) => (
      <span className="sql-cell" title={h.error}>
        {oneLine(h.sql)}
      </span>
    ),
  },
];

interface Props {
  connId: string;
  /** Bump to reload, e.g. after a query ran. */
  refresh: number;
  onOpen: (sql: string) => void;
}

/** HistoryPanel lists past requests for one connection, newest first. */
export function HistoryPanel({ connId, refresh, onOpen }: Props) {
  const backend = useBackend();
  const fetchPage = useCallback(
    (last: HistoryEntry | undefined, limit: number) => backend.queryHistory(connId, last?.id ?? 0, limit),
    [backend, connId],
  );
  const { items, error, loading, loadMore } = usePaged(fetchPage, PAGE, [connId, refresh]);
  const open = useCallback((i: number) => onOpen(items[i]!.sql), [items, onOpen]);

  return (
    <div className="output-panel list-grid">
      <ErrorText error={error} />
      <ItemGrid
        columns={columns}
        items={items}
        rowHeight={34}
        onSelect={open}
        onEndReached={loadMore}
        empty={loading ? "Loading…" : "No queries on this connection yet."}
      />
    </div>
  );
}
