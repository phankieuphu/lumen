import { useEffect, useState } from "react";

import { type AlertRule, type Connection, type DbSession, errorMessage, useBackend } from "../api";
import { Icon } from "../components/icons";
import { Banner, Confirm, ConnSelect, Segmented, SwitchRow } from "../components/ui";
import { useAsync } from "../hooks/useAsync";
import { formatCount, oneLine } from "../lib/format";

const POLL_MS = 5000;

const ranges = [
  { value: "15", label: "15 min" },
  { value: "60", label: "1 hour" },
  { value: "1440", label: "24 hours" },
] as const;
type Range = (typeof ranges)[number]["value"];

interface Props {
  conn: Connection;
  connections: readonly Connection[];
  onSelectConn: (id: string) => void;
  onAdvisor: () => void;
}

/** MonitorScreen polls live database stats, sessions, and alert rules. */
export function MonitorScreen({ conn, connections, onSelectConn, onAdvisor }: Props) {
  const backend = useBackend();
  const [range, setRange] = useState<Range>("15");
  const [paused, setPaused] = useState(false);
  const [ending, setEnding] = useState<DbSession>();
  const [actionError, setActionError] = useState<string>();

  const snap = useAsync(() => backend.monitorSnapshot(conn.id, Number(range)), [backend, conn.id, range]);
  const { reload } = snap;
  const failed = snap.error !== undefined && snap.data === undefined;

  // The interval belongs to this effect: it stops on pause, on a new
  // connection, and when the screen unmounts.
  useEffect(() => {
    if (paused || failed) return;
    const id = setInterval(reload, POLL_MS);
    return () => clearInterval(id);
  }, [paused, failed, reload, conn.id]);

  const act = async (fn: () => Promise<void>) => {
    setActionError(undefined);
    try {
      await fn();
      reload();
    } catch (err) {
      setActionError(errorMessage(err));
    }
  };

  const endSession = (s: DbSession) => {
    setEnding(undefined);
    void act(() => backend.monitorEndSession(conn.id, s.pid));
  };
  const setAlert = (rule: AlertRule, enabled: boolean) => void act(() => backend.monitorSetAlert(conn.id, rule.id, enabled));

  const d = snap.data;
  const rangeLabel = ranges.find((r) => r.value === range)!.label;
  return (
    <section className="page monitor" aria-label="Monitor">
      <header className="page-head center">
        <h1 className="title">Monitor</h1>
        <ConnSelect connections={connections} value={conn.id} onChange={onSelectConn} />
        {!failed && (
          <span className={paused ? "muted with-icon" : "ok-text with-icon"}>
            <span className={paused ? "dot" : "dot dot-dev"} aria-hidden="true" />
            {paused ? "Paused" : `Live, every ${POLL_MS / 1000} seconds`}
          </span>
        )}
        <span className="spacer" />
        <Segmented label="Time range" value={range} options={ranges} onChange={setRange} />
        <button type="button" onClick={() => setPaused((p) => !p)} disabled={failed}>
          {paused ? "Resume" : "Pause"}
        </button>
      </header>
      {failed && <Banner tone="warn">{snap.error}</Banner>}
      {actionError && <Banner tone="danger">{actionError}</Banner>}
      {d?.firing && (
        <div className="alert-banner" role="alert">
          <Icon name="alert" size={16} strokeWidth={2} />
          <span>
            <strong>Alert firing:</strong> {d.firing.message}
          </span>
          <span className="spacer" />
          <button type="button" className="ai-button" onClick={onAdvisor}>
            Open slow-query advisor
          </button>
        </div>
      )}
      {d && (
        <>
          <div className="stat-cards">
            <Stat label="Active sessions" value={formatCount(d.activeSessions)} note="running a query now" />
            <Stat
              label="Connections used"
              value={`${Math.round((d.connectionsUsed / d.maxConnections) * 100)}%`}
              note={`${d.connectionsUsed} of ${d.maxConnections} maximum`}
            />
            <Stat
              label="Waiting on locks"
              value={formatCount(d.waitingOnLocks)}
              note={d.rootBlocker ? `blocked by session ${d.rootBlocker}` : "no lock waits"}
            />
            <Stat label="Cache hit rate" value={`${d.cacheHitPct}%`} note="shared buffers" />
            <Stat
              label="Longest running query"
              value={d.longest ? `${d.longest.seconds} s` : "–"}
              note={d.longest ? `session ${d.longest.pid}, limit ${d.longest.limitSeconds} s` : "nothing running"}
              warn={!!d.longest && d.longest.seconds > d.longest.limitSeconds}
            />
          </div>
          <div className="two-col">
            <Chart title="Connections used" values={d.series.connections} max={Math.max(40, ...d.series.connections)} rangeLabel={rangeLabel} />
            <Chart title="Transactions per second" values={d.series.tps} max={Math.max(300, ...d.series.tps)} rangeLabel={rangeLabel} />
          </div>
          <div className="two-col wide-left">
            <section className="card" aria-label="Sessions">
              <header className="card-head small">
                <h2>Sessions</h2>
                <span className="spacer" />
                <span className="muted small-text">
                  {d.sessions.length} shown of {d.totalSessions}
                </span>
              </header>
              <div className="table-scroll">
                <table className="sessions">
                  <thead>
                    <tr>
                      <th scope="col">PID</th>
                      <th scope="col">User</th>
                      <th scope="col">State</th>
                      <th scope="col" className="num">
                        Time
                      </th>
                      <th scope="col">Query</th>
                      <th scope="col">
                        <span className="sr-only">Action</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {d.sessions.map((s) => {
                      const over = d.longest?.pid === s.pid && s.seconds !== null && s.seconds > d.longest.limitSeconds;
                      return (
                        <tr key={s.pid} className={over ? "hot" : undefined}>
                          <td className="mono">{s.pid}</td>
                          <td className="mono">{s.user}</td>
                          <td className={`state state-${s.state.replace(/ /g, "-")}`}>{s.state}</td>
                          <td className={over ? "num mono warn-text" : "num mono"}>{s.seconds === null ? "–" : `${s.seconds} s`}</td>
                          <td className="mono query-cell" title={s.query}>
                            {s.query ? oneLine(s.query, 80) : "–"}
                          </td>
                          <td className="num">
                            {s.blockedBy ? (
                              <span className="muted small-text">blocked by {s.blockedBy}</span>
                            ) : (
                              (over || s.pid === d.rootBlocker) && (
                                <button type="button" className="danger small" onClick={() => setEnding(s)}>
                                  End session
                                </button>
                              )
                            )}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </section>
            <section className="card" aria-label="Alert rules">
              <header className="card-head small">
                <h2>Alert rules</h2>
                <span className="spacer" />
                <span className="muted small-text">
                  {d.alerts.filter((a) => a.enabled).length} of {d.alerts.length} on
                </span>
              </header>
              <div className="switches flush">
                {d.alerts.map((a) => (
                  <SwitchRow
                    key={a.id}
                    label={a.label}
                    help={a.firing ? <span className="warn-text">Firing now</span> : a.enabled ? "On, not firing" : "Off"}
                    checked={a.enabled}
                    onChange={(v) => setAlert(a, v)}
                  />
                ))}
              </div>
              <p className="muted small-text card-note">Alerts show as desktop notifications. Samples are kept on this computer for 30 days.</p>
            </section>
          </div>
        </>
      )}
      {ending && (
        <Confirm title="End session?" confirmLabel={`End session ${ending.pid}`} danger onConfirm={() => endSession(ending)} onCancel={() => setEnding(undefined)}>
          <p>
            This stops session <strong>{ending.pid}</strong> ({ending.user}) on <strong>{conn.name}</strong> and rolls back its open
            transaction. The action is recorded in the audit log.
          </p>
          {ending.query && <pre className="sql-preview">{ending.query}</pre>}
        </Confirm>
      )}
    </section>
  );
}

function Stat({ label, value, note, warn }: { label: string; value: string; note: string; warn?: boolean }) {
  return (
    <div className="card stat">
      <div className="muted small-text">{label}</div>
      <div className={warn ? "stat-value warn-text" : "stat-value"}>{value}</div>
      <div className="muted small-text">{note}</div>
    </div>
  );
}

const CHART_W = 600;
const CHART_H = 130;

function Chart({ title, values, max, rangeLabel }: { title: string; values: readonly number[]; max: number; rangeLabel: string }) {
  const n = Math.max(values.length - 1, 1);
  const pts = values.map((v, i) => `${(i / n) * CHART_W},${CHART_H - (v / max) * CHART_H}`);
  const line = `M${pts.join(" L")}`;
  const area = `${line} L${CHART_W},${CHART_H} L0,${CHART_H} Z`;
  const now = values.at(-1) ?? 0;
  return (
    <section className="card chart" aria-label={title}>
      <header className="card-head small">
        <h2>{title}</h2>
        <span className="spacer" />
        <span className="mono muted small-text">
          {formatCount(now)} <span>now</span>
        </span>
      </header>
      <div className="chart-body">
        <div className="chart-y mono" aria-hidden="true">
          <span>{formatCount(max)}</span>
          <span>{formatCount(Math.round(max / 2))}</span>
          <span>0</span>
        </div>
        <svg viewBox={`0 0 ${CHART_W} ${CHART_H}`} preserveAspectRatio="none" role="img" aria-label={`${title} over the last ${rangeLabel}; now ${now}`}>
          <line x1="0" x2={CHART_W} y1={CHART_H / 2} y2={CHART_H / 2} className="gridline" />
          <path d={area} className="chart-area" />
          <path d={line} className="chart-line" />
        </svg>
        <div className="chart-x mono" aria-hidden="true">
          <span>−{rangeLabel}</span>
          <span>now</span>
        </div>
      </div>
    </section>
  );
}
