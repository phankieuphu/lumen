import { useCallback, useEffect, useMemo, useState } from "react";

import { type AuditEntry, type AuditFilter, type Connection, errorMessage, type ExportFormat, type VerifyResult, useBackend } from "../api";
import { Icon } from "../components/icons";
import { type ItemColumn, ItemGrid } from "../components/ItemGrid";
import { EnvDot, ErrorText, Field, KindBadge, Segmented, StatusText } from "../components/ui";
import { usePaged } from "../hooks/usePaged";
import { formatCount, formatDuration, formatTime, oneLine } from "../lib/format";

const PAGE = 200;

const columns: ItemColumn<AuditEntry>[] = [
  { key: "seq", title: "#", width: 56, align: "end", render: (e) => e.seq },
  { key: "at", title: "Time", width: 150, render: (e) => formatTime(e.at) },
  { key: "kind", title: "Kind", width: 76, render: (e) => <KindBadge kind={e.kind} /> },
  { key: "sql", title: "Statement", width: 160, grow: true, render: (e) => <span className="sql-cell">{oneLine(e.sql)}</span> },
  {
    key: "conn",
    title: "Connection",
    width: 140,
    render: (e) => (
      <>
        <EnvDot env={e.env} />
        <span>{e.connName}</span>
      </>
    ),
  },
  { key: "status", title: "Result", width: 80, align: "end", render: (e) => <StatusText status={e.status} /> },
];

/** toRFC3339 converts a datetime-local value; empty means no bound. */
const toRFC3339 = (local: string) => (local ? new Date(local).toISOString() : "");

interface Props {
  connections: readonly Connection[];
  onOpen: (connId: string, sql: string) => void;
}

/** AuditScreen browses, verifies, and exports the append-only audit log. */
export function AuditScreen({ connections, onOpen }: Props) {
  const backend = useBackend();
  const [connId, setConnId] = useState("");
  const [since, setSince] = useState("");
  const [until, setUntil] = useState("");
  const [selected, setSelected] = useState<number>();
  const [verify, setVerify] = useState<VerifyResult | "running">();
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string>();
  const [notice, setNotice] = useState<string>();

  const filter = useMemo<AuditFilter>(() => ({ connId, since: toRFC3339(since), until: toRFC3339(until) }), [connId, since, until]);
  const fetchPage = useCallback(
    (last: AuditEntry | undefined, limit: number) => backend.auditList({ ...filter, afterSeq: last?.seq ?? 0, limit }),
    [backend, filter],
  );
  const { items, error, loading, done, loadMore, reload } = usePaged(fetchPage, PAGE, [filter]);
  // A new filter replaces the list, so the old index is meaningless.
  useEffect(() => setSelected(undefined), [fetchPage]);
  const entry = selected === undefined ? undefined : items[selected];

  const runVerify = useCallback(async () => {
    setVerify("running");
    try {
      setVerify(await backend.auditVerify());
    } catch (err) {
      setVerify(undefined);
      setActionError(errorMessage(err));
    }
  }, [backend]);

  // Verifying reads the whole chain once; do it when the screen opens.
  useEffect(() => void runVerify(), [runVerify]);

  const runExport = async (format: ExportFormat) => {
    setBusy(true);
    setActionError(undefined);
    setNotice(undefined);
    try {
      if (await backend.auditExport(format, filter)) setNotice(`Exported as ${format.toUpperCase()}.`);
    } catch (err) {
      setActionError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const connOptions = useMemo(
    () => [{ value: "", label: "All" }, ...connections.map((c) => ({ value: c.id, label: c.name }))],
    [connections],
  );

  return (
    <div className="page">
      <div className="page-head">
        <div className="page-title">
          <h1>Audit log</h1>
          <span className="muted">Every change made through lumen. Entries cannot be edited or deleted.</span>
        </div>
        <div className="row">
          <VerifyPill result={verify} onVerify={() => void runVerify()} />
          <button type="button" onClick={() => void runExport("csv")} disabled={busy}>
            Export CSV
          </button>
          <button type="button" onClick={() => void runExport("json")} disabled={busy}>
            Export JSON
          </button>
        </div>
      </div>

      <div className="row">
        <Segmented label="Connection" options={connOptions} value={connId} onChange={setConnId} />
        <Field label="Since">
          <input type="datetime-local" value={since} onChange={(e) => setSince(e.target.value)} />
        </Field>
        <Field label="Until">
          <input type="datetime-local" value={until} onChange={(e) => setUntil(e.target.value)} />
        </Field>
        <span className="muted">
          {formatCount(items.length)}
          {done ? "" : "+"} entries
        </span>
        <span className="spacer" />
        <button type="button" className="ghost" onClick={reload}>
          Refresh
        </button>
      </div>

      {notice && <p className="ok">{notice}</p>}
      <ErrorText error={error ?? actionError} />

      <div className="audit-body">
        <section className="card list-grid" aria-label="Audit entries">
          <ItemGrid
            columns={columns}
            items={items}
            rowHeight={44}
            selected={selected}
            onSelect={setSelected}
            onEndReached={loadMore}
            empty={loading ? "Loading…" : "No entries match these filters."}
          />
        </section>
        <AuditDetail entry={entry} onOpen={onOpen} />
      </div>
    </div>
  );
}

function VerifyPill({ result, onVerify }: { result: VerifyResult | "running" | undefined; onVerify: () => void }) {
  if (result === "running") return <span className="pill">Verifying hash chain…</span>;
  if (!result)
    return (
      <button type="button" onClick={onVerify}>
        Verify chain
      </button>
    );
  return result.ok ? (
    <button type="button" className="pill tone-ok" onClick={onVerify} title="Verify again">
      <Icon name="shield" size={14} /> Hash chain verified · {formatCount(result.entries)}
    </button>
  ) : (
    <button type="button" className="pill tone-error" onClick={onVerify} title="Verify again">
      <Icon name="warning" size={14} /> Chain broken at #{result.brokenAt}: {result.reason}
    </button>
  );
}

function AuditDetail({ entry, onOpen }: { entry: AuditEntry | undefined; onOpen: Props["onOpen"] }) {
  if (!entry) {
    return (
      <section className="card detail" aria-label="Entry details">
        <p className="empty">Select an entry to see its details.</p>
      </section>
    );
  }
  const facts: [string, string][] = [
    ["Time", formatTime(entry.at)],
    ["Connection", `${entry.connName} · ${entry.env}`],
    ["Engine", entry.dialect],
    ["Duration", formatDuration(entry.durationMs)],
    ["Rows changed", entry.rowsAffected >= 0 ? formatCount(entry.rowsAffected) : "—"],
    ["Sequence", `#${entry.seq}`],
  ];
  return (
    <section className="card detail" aria-label="Entry details">
      <div className="card-head">
        <KindBadge kind={entry.kind} />
        <StatusText status={entry.status} />
      </div>
      <dl className="facts">
        {facts.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
      <div className="detail-sql">
        <span className="muted small">Statement</span>
        <pre className="code-block">{entry.sql}</pre>
      </div>
      <ErrorText error={entry.error} />
      <div className="detail-sql">
        <span className="muted small">Hash</span>
        <span className="mono small muted hash">
          {entry.hash}
        </span>
      </div>
      <div className="card-foot">
        <button type="button" onClick={() => void navigator.clipboard?.writeText(entry.sql)}>
          Copy SQL
        </button>
        <button type="button" onClick={() => onOpen(entry.connId, entry.sql)}>
          Open in editor
        </button>
      </div>
    </section>
  );
}
