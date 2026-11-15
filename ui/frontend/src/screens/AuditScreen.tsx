import { useCallback, useEffect, useMemo, useState } from "react";

import { type AuditEntry, type AuditFilter, type Connection, errorMessage, type ExportFormat, type VerifyResult, useBackend } from "../api";
import { Icon } from "../components/icons";
import { type ItemColumn, ItemGrid } from "../components/ItemGrid";
import { Banner, EnvBadge, EnvDot, ErrorText, Field, KindBadge, Segmented, StatusBadge, TxTag } from "../components/ui";
import { usePaged } from "../hooks/usePaged";
import { dialectName, formatCount, formatDuration, formatTime, oneLine } from "../lib/format";

const PAGE = 200;
/** Above this many connections the filter is a dropdown instead of buttons. */
const MAX_SEGMENTS = 5;

// Sequence number and rows affected are in the detail panel, so the list fits beside it.
const columns: ItemColumn<AuditEntry>[] = [
  { key: "at", title: "Time", width: 196, render: (e) => <span className="mono muted">{formatTime(e.at)}</span> },
  { key: "kind", title: "Kind", width: 80, render: (e) => <KindBadge kind={e.kind} /> },
  { key: "sql", title: "Statement", width: 320, render: (e) => <span className="mono">{oneLine(e.sql)}</span> },
  { key: "tx", title: "Transaction", width: 130, render: (e) => e.txId && <TxTag id={e.txId} label={e.txLabel} /> },
  {
    key: "conn",
    title: "Connection",
    width: 160,
    render: (e) => (
      <span className="with-icon">
        <EnvDot env={e.env} />
        {e.connName}
      </span>
    ),
  },
  { key: "status", title: "Status", width: 84, align: "end", render: (e) => <StatusBadge status={e.status} /> },
];

/** toRFC3339 converts a datetime-local value; empty means no bound. */
const toRFC3339 = (local: string) => (local ? new Date(local).toISOString() : "");

interface Props {
  connections: readonly Connection[];
  /** Opens a statement in the query editor on its connection. */
  onOpen: (sql: string, connId: string) => void;
}

/** AuditScreen browses, verifies, and exports the append-only audit log. */
export function AuditScreen({ connections, onOpen }: Props) {
  const backend = useBackend();
  const [connId, setConnId] = useState("");
  const [since, setSince] = useState("");
  const [until, setUntil] = useState("");
  const [selected, setSelected] = useState<number>();
  const [verify, setVerify] = useState<VerifyResult>();
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string>();
  const [notice, setNotice] = useState<string>();

  const filter = useMemo<AuditFilter>(() => ({ connId, since: toRFC3339(since), until: toRFC3339(until) }), [connId, since, until]);

  const fetchPage = useCallback(
    (last: AuditEntry | undefined, limit: number) => backend.auditList({ ...filter, afterSeq: last?.seq ?? 0, limit }),
    [backend, filter],
  );
  const { items, error, loading, loadMore, reload } = usePaged(fetchPage, PAGE, [filter]);
  // A new filter or refresh replaces the list, so the old index is meaningless.
  useEffect(() => setSelected(undefined), [fetchPage]);
  const entry = selected === undefined ? undefined : items[selected];

  const act = async (fn: () => Promise<void>) => {
    setBusy(true);
    setActionError(undefined);
    setNotice(undefined);
    try {
      await fn();
    } catch (err) {
      setActionError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const runVerify = () => act(async () => setVerify(await backend.auditVerify()));
  const runExport = (format: ExportFormat) =>
    act(async () => {
      if (await backend.auditExport(format, filter)) setNotice(`Exported as ${format.toUpperCase()}.`);
    });

  const connOptions = [{ value: "", label: "All connections" }, ...connections.map((c) => ({ value: c.id, label: c.name }))];

  return (
    <section className="page audit" aria-label="Audit log">
      <header className="page-head">
        <div>
          <h1 className="title">Audit log</h1>
          <p className="muted">Every change made through this app. Entries are hash-chained and cannot be edited or deleted.</p>
        </div>
        <span className="spacer" />
        <div className="page-actions">
          {verify?.ok && (
            <span className="chip chip-ok with-icon">
              <Icon name="check" size={14} strokeWidth={2} /> Hash chain verified · {formatCount(verify.entries)}
            </span>
          )}
          <button type="button" onClick={() => void runVerify()} disabled={busy}>
            Verify chain
          </button>
          <button type="button" onClick={() => void runExport("csv")} disabled={busy}>
            Export CSV
          </button>
          <button type="button" onClick={() => void runExport("json")} disabled={busy}>
            Export JSON
          </button>
        </div>
      </header>
      <div className="filters">
        {connections.length <= MAX_SEGMENTS ? (
          <Segmented label="Connection" value={connId} options={connOptions} onChange={setConnId} />
        ) : (
          <select aria-label="Connection" value={connId} onChange={(e) => setConnId(e.target.value)}>
            {connOptions.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        )}
        <Field label="Since">
          <input type="datetime-local" value={since} onChange={(e) => setSince(e.target.value)} />
        </Field>
        <Field label="Until">
          <input type="datetime-local" value={until} onChange={(e) => setUntil(e.target.value)} />
        </Field>
        <span className="spacer" />
        <span className="muted">{formatCount(items.length)} entries</span>
        <button type="button" onClick={reload}>
          Refresh
        </button>
      </div>
      {verify && !verify.ok && (
        <Banner tone="danger">
          Chain broken at #{verify.brokenAt}: {verify.reason}
        </Banner>
      )}
      {notice && <Banner tone="ok">{notice}</Banner>}
      <ErrorText error={error ?? actionError} />
      <div className="split">
        <div className="card grid-card">
          <ItemGrid columns={columns} items={items} selected={selected} onSelect={setSelected} onEndReached={loadMore} empty={loading ? "Loading…" : "No audit entries."} />
        </div>
        {entry && <AuditDetail entry={entry} onOpen={onOpen} />}
      </div>
    </section>
  );
}

function AuditDetail({ entry, onOpen }: { entry: AuditEntry; onOpen: Props["onOpen"] }) {
  const backend = useBackend();
  const [copied, setCopied] = useState(false);
  useEffect(() => setCopied(false), [entry]);

  const copy = () =>
    void backend.copyText(entry.sql).then(
      () => setCopied(true),
      () => setCopied(false),
    );

  return (
    <aside className="card detail" aria-label={`Audit entry ${entry.seq}`}>
      <div className="detail-head">
        <div className="with-icon">
          <KindBadge kind={entry.kind} />
          <StatusBadge status={entry.status} />
        </div>
        <h2>Entry #{entry.seq}</h2>
      </div>
      <div className="detail-body">
        <dl className="facts">
          <div>
            <dt>Time</dt>
            <dd>{formatTime(entry.at)}</dd>
          </div>
          <div>
            <dt>Connection</dt>
            <dd>{entry.connName}</dd>
          </div>
          <div>
            <dt>Environment</dt>
            <dd>
              <EnvBadge env={entry.env} />
            </dd>
          </div>
          <div>
            <dt>Engine</dt>
            <dd>{dialectName(entry.dialect)}</dd>
          </div>
          <div>
            <dt>Rows affected</dt>
            <dd>{entry.rowsAffected >= 0 ? formatCount(entry.rowsAffected) : "–"}</dd>
          </div>
          <div>
            <dt>Duration</dt>
            <dd>{formatDuration(entry.durationMs)}</dd>
          </div>
          {entry.txId && (
            <div>
              <dt>Transaction</dt>
              <dd>
                <TxTag id={entry.txId} label={entry.txLabel} /> <span className="mono muted small-text">{entry.txId}</span>
              </dd>
            </div>
          )}
        </dl>
        <div className="field-label">Statement</div>
        <pre className="sql-preview">{entry.sql}</pre>
        {entry.error && <Banner tone="danger">{entry.error}</Banner>}
      </div>
      <footer className="card-foot">
        <button type="button" onClick={copy}>
          {copied ? "Copied" : "Copy"}
        </button>
        <button type="button" onClick={() => onOpen(entry.sql, entry.connId)}>
          Open in editor
        </button>
        <span className="spacer" />
        <span className="mono muted small-text hash" title={entry.hash}>
          hash {entry.hash.slice(0, 12)}
        </span>
      </footer>
    </aside>
  );
}
