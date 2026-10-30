import { useCallback, useEffect, useMemo, useState } from "react";

import { type AuditEntry, type AuditFilter, type Connection, errorMessage, type ExportFormat, type VerifyResult, useBackend } from "../api";
import { type ItemColumn, ItemGrid } from "../components/ItemGrid";
import { EnvBadge, ErrorText, Field, KindBadge, StatusBadge } from "../components/ui";
import { usePaged } from "../hooks/usePaged";
import { formatCount, formatDuration, formatTime, oneLine } from "../lib/format";

const PAGE = 200;

const columns: ItemColumn<AuditEntry>[] = [
  { key: "seq", title: "#", width: 70, align: "end", render: (e) => e.seq },
  { key: "at", title: "Time", width: 170, render: (e) => formatTime(e.at) },
  { key: "conn", title: "Connection", width: 160, render: (e) => e.connName },
  { key: "env", title: "Env", width: 80, render: (e) => <EnvBadge env={e.env} /> },
  { key: "status", title: "Status", width: 90, render: (e) => <StatusBadge status={e.status} /> },
  { key: "kind", title: "Kind", width: 80, render: (e) => <KindBadge kind={e.kind} /> },
  { key: "rows", title: "Affected", width: 90, align: "end", render: (e) => (e.rowsAffected >= 0 ? formatCount(e.rowsAffected) : "") },
  { key: "sql", title: "SQL", width: 700, render: (e) => oneLine(e.sql) },
];

/** toRFC3339 converts a datetime-local value; empty means no bound. */
const toRFC3339 = (local: string) => (local ? new Date(local).toISOString() : "");

/** AuditScreen browses, verifies, and exports the append-only audit log. */
export function AuditScreen({ connections }: { connections: readonly Connection[] }) {
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

  return (
    <section className="list-screen audit">
      <div className="toolbar wrap">
        <Field label="Connection">
          <select value={connId} onChange={(e) => setConnId(e.target.value)}>
            <option value="">All</option>
            {connections.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Since">
          <input type="datetime-local" value={since} onChange={(e) => setSince(e.target.value)} />
        </Field>
        <Field label="Until">
          <input type="datetime-local" value={until} onChange={(e) => setUntil(e.target.value)} />
        </Field>
        <span className="spacer" />
        <button type="button" onClick={reload}>
          Refresh
        </button>
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
      {verify &&
        (verify.ok ? (
          <p className="ok pad">Chain intact: {formatCount(verify.entries)} entries verified.</p>
        ) : (
          <p className="error pad" role="alert">
            Chain broken at #{verify.brokenAt}: {verify.reason}
          </p>
        ))}
      {notice && <p className="ok pad">{notice}</p>}
      <ErrorText error={error ?? actionError} />
      <div className="split">
        <ItemGrid columns={columns} items={items} selected={selected} onSelect={setSelected} onEndReached={loadMore} empty={loading ? "Loading…" : "No audit entries."} />
        {entry && <AuditDetail entry={entry} />}
      </div>
    </section>
  );
}

function AuditDetail({ entry }: { entry: AuditEntry }) {
  return (
    <aside className="detail" aria-label={`Audit entry ${entry.seq}`}>
      <h3>Entry #{entry.seq}</h3>
      <dl>
        <dt>Time</dt>
        <dd>{formatTime(entry.at)}</dd>
        <dt>Connection</dt>
        <dd>
          {entry.connName} ({entry.dialect})
        </dd>
        <dt>Duration</dt>
        <dd>{formatDuration(entry.durationMs)}</dd>
        <dt>Hash</dt>
        <dd className="mono small">{entry.hash}</dd>
      </dl>
      {entry.error && <ErrorText error={entry.error} />}
      <pre className="sql-preview">{entry.sql}</pre>
    </aside>
  );
}
