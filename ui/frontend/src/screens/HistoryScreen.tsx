import { useCallback, useEffect, useMemo, useState } from "react";

import { type Connection, errorMessage, type HistoryEntry, useBackend } from "../api";
import { type ItemColumn, ItemGrid } from "../components/ItemGrid";
import { ErrorText, KindBadge, StatusBadge, TxTag } from "../components/ui";
import { usePaged } from "../hooks/usePaged";
import { formatCount, formatDuration, formatTime, oneLine } from "../lib/format";

const PAGE = 100;

interface TxFilter {
  id: string;
  label?: string;
}

const historyColumns = (onTx: (f: TxFilter) => void): ItemColumn<HistoryEntry>[] => [
  { key: "at", title: "Started", width: 196, render: (h) => <span className="mono muted">{formatTime(h.startedAt)}</span> },
  { key: "status", title: "Status", width: 96, render: (h) => <StatusBadge status={h.status} /> },
  { key: "kind", title: "Kind", width: 86, render: (h) => <KindBadge kind={h.kind} /> },
  {
    key: "tx",
    title: "Transaction",
    width: 150,
    render: (h) => h.txId && <TxTag id={h.txId} label={h.txLabel} onClick={() => onTx({ id: h.txId!, label: h.txLabel })} />,
  },
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

/**
 * HistoryScreen lists past requests for one connection, newest first.
 * Clicking a transaction tag narrows the list to that transaction.
 */
export function HistoryScreen({ conn, refresh, onOpen }: Props) {
  const backend = useBackend();
  const [txFilter, setTxFilter] = useState<TxFilter>();
  useEffect(() => setTxFilter(undefined), [conn.id]);

  const fetchPage = useCallback(
    (last: HistoryEntry | undefined, limit: number) => backend.queryHistory(conn.id, last?.id ?? 0, limit),
    [backend, conn.id],
  );
  const paged = usePaged(fetchPage, PAGE, [conn.id, refresh]);
  const txList = useTxHistory(txFilter?.id, refresh);
  const columns = useMemo(() => historyColumns(setTxFilter), []);

  const items = txFilter ? txList.items : paged.items;
  const writes = txList.items.filter((h) => h.status === "ok" && (h.kind === "write" || h.kind === "schema")).length;

  return (
    <section className="page list-page" aria-label="History">
      <header className="page-head">
        <div>
          <h1 className="title">History</h1>
          <p className="muted">
            Queries run on <strong className="fg">{conn.name}</strong>, newest first. Click one to open it in the editor, or a
            transaction tag to see everything that ran in it.
          </p>
        </div>
      </header>
      {txFilter && (
        <div className="filters tx-filter" role="status">
          Transaction <TxTag id={txFilter.id} label={txFilter.label} />
          <span className="muted">
            {formatCount(txList.items.length)} {txList.items.length === 1 ? "statement" : "statements"} · {formatCount(writes)}{" "}
            {writes === 1 ? "write" : "writes"} · oldest first
          </span>
          <span className="spacer" />
          <button type="button" onClick={() => setTxFilter(undefined)}>
            Show all
          </button>
        </div>
      )}
      <ErrorText error={txFilter ? txList.error : paged.error} />
      <div className="card grid-card">
        <ItemGrid
          columns={columns}
          items={items}
          onSelect={(i) => onOpen(items[i]!.sql)}
          onEndReached={txFilter ? undefined : paged.loadMore}
          empty={(txFilter ? txList.loading : paged.loading) ? "Loading…" : "No queries yet."}
        />
      </div>
    </section>
  );
}

/** useTxHistory loads every statement of one transaction. */
function useTxHistory(txId: string | undefined, refresh: number) {
  const backend = useBackend();
  const [state, setState] = useState<{ items: HistoryEntry[]; error?: string; loading: boolean }>({ items: [], loading: false });
  useEffect(() => {
    if (!txId) return setState({ items: [], loading: false });
    let live = true;
    setState((s) => ({ ...s, loading: true }));
    backend.transactionHistory(txId).then(
      (items) => live && setState({ items, loading: false }),
      (err: unknown) => live && setState({ items: [], error: errorMessage(err), loading: false }),
    );
    return () => {
      live = false;
    };
  }, [backend, txId, refresh]);
  return state;
}
