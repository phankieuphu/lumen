import { useEffect, useState } from "react";

import {
  type AdviceState,
  type AdvisorKind,
  type AdvisorSort,
  type Connection,
  type SlowQuery,
  type SlowQueryDetail,
  useBackend,
} from "../api";
import { Icon } from "../components/icons";
import { Badge, Banner, ConnSelect } from "../components/ui";
import { useAsync } from "../hooks/useAsync";
import { formatCount, formatDuration, formatTime, oneLine } from "../lib/format";

const windows = [
  { hours: 1, label: "Last hour" },
  { hours: 24, label: "Last 24 hours" },
  { hours: 168, label: "Last 7 days" },
];
const thresholds = [0, 10, 100, 200, 500, 1000];
const callCounts = [0, 10, 100, 1000, 10_000];
const kinds: { value: AdvisorKind; label: string }[] = [
  { value: "", label: "All statements" },
  { value: "select", label: "SELECT" },
  { value: "insert", label: "INSERT" },
  { value: "update", label: "UPDATE" },
  { value: "delete", label: "DELETE" },
];
const sorts: { value: AdvisorSort; label: string; note: string }[] = [
  { value: "share", label: "Sort: share of time", note: "share of database time" },
  { value: "mean", label: "Sort: mean time", note: "mean time" },
  { value: "calls", label: "Sort: calls", note: "number of calls" },
];

const defaults = { windowHours: 24, minMeanMs: 200, minCalls: 0, kind: "" as AdvisorKind, sort: "share" as AdvisorSort };
/** How long typing must pause before the search runs. */
const SEARCH_DELAY_MS = 300;

const stateLabel: Record<AdviceState, { tone: string; text: string }> = {
  "fix-verified": { tone: "ok", text: "Fix verified" },
  "rewrite-verified": { tone: "ok", text: "Rewrite verified" },
  "no-fix": { tone: "none", text: "No safe fix" },
  pending: { tone: "none", text: "Analysing" },
  "not-analysed": { tone: "none", text: "No advice yet" },
};

const statsSource: Record<string, string> = {
  postgres: "pg_stat_statements",
  mysql: "performance_schema",
};

interface Props {
  conn: Connection;
  connections: readonly Connection[];
  onSelectConn: (id: string) => void;
  /** Opens SQL in the query editor, where the normal run checks apply. */
  onOpen: (sql: string, connId: string) => void;
}

/**
 * AdvisorScreen lists the slowest queries with their plan and, once the
 * advice task exists, a verified fix for each. The backend applies every
 * filter, so the list is the true top of what matches.
 */
export function AdvisorScreen({ conn, connections, onSelectConn, onOpen }: Props) {
  const backend = useBackend();
  const [windowHours, setWindowHours] = useState(defaults.windowHours);
  const [minMeanMs, setMinMeanMs] = useState(defaults.minMeanMs);
  const [minCalls, setMinCalls] = useState(defaults.minCalls);
  const [kind, setKind] = useState(defaults.kind);
  const [sort, setSort] = useState(defaults.sort);
  const [searchText, setSearchText] = useState("");
  const [search, setSearch] = useState("");
  const [selectedId, setSelectedId] = useState<string>();
  const [skipped, setSkipped] = useState<ReadonlySet<string>>(new Set());

  // The search waits for a pause in typing; the timer is dropped on the
  // next key press and on unmount.
  useEffect(() => {
    const id = setTimeout(() => setSearch(searchText.trim()), SEARCH_DELAY_MS);
    return () => clearTimeout(id);
  }, [searchText]);

  // PostgreSQL totals run from the last statistics reset; only MySQL
  // records when a statement was last seen.
  const canWindow = conn.dialect !== "postgres";
  const list = useAsync(
    () => backend.advisorList(conn.id, { windowHours: canWindow ? windowHours : 0, minMeanMs, minCalls, search, kind, sort }),
    [backend, conn.id, canWindow, windowHours, minMeanMs, minCalls, search, kind, sort],
  );
  const queries = (list.data?.queries ?? []).filter((q) => !skipped.has(q.id));
  const selected = queries.find((q) => q.id === selectedId) ?? queries[0];

  const detail = useAsync(
    () => (selected ? backend.advisorDetail(conn.id, selected.id) : Promise.resolve(undefined)),
    [backend, conn.id, selected?.id],
  );
  // Another connection has other queries.
  useEffect(() => setSkipped(new Set()), [conn.id]);

  const skip = (id: string) => {
    setSkipped((s) => new Set(s).add(id));
    setSelectedId(undefined);
  };

  const filtered =
    searchText !== "" ||
    windowHours !== defaults.windowHours ||
    minMeanMs !== defaults.minMeanMs ||
    minCalls !== defaults.minCalls ||
    kind !== defaults.kind ||
    sort !== defaults.sort;
  const reset = () => {
    setSearchText("");
    setSearch("");
    setWindowHours(defaults.windowHours);
    setMinMeanMs(defaults.minMeanMs);
    setMinCalls(defaults.minCalls);
    setKind(defaults.kind);
    setSort(defaults.sort);
  };

  const source = statsSource[conn.dialect];
  const since = list.data?.statsSince;
  const sortNote = sorts.find((s) => s.value === sort)?.note;
  return (
    <div className="advisor">
      <aside className="sidebar advisor-list" aria-label="Slow queries">
        <div className="advisor-filters">
          <h1 className="title">Slow-query advisor</h1>
          <div className="chip-row">
            <ConnSelect connections={connections} value={conn.id} onChange={onSelectConn} />
            {canWindow && (
              <select className="pill-select" aria-label="Time window" value={windowHours} onChange={(e) => setWindowHours(Number(e.target.value))}>
                {windows.map((w) => (
                  <option key={w.hours} value={w.hours}>
                    {w.label}
                  </option>
                ))}
              </select>
            )}
          </div>
          <input
            type="search"
            className="advisor-search"
            aria-label="Search query text"
            placeholder="Search query text or table name"
            maxLength={200}
            value={searchText}
            onChange={(e) => setSearchText(e.target.value)}
          />
          <div className="chip-row">
            <select className="pill-select" aria-label="Statement type" value={kind} onChange={(e) => setKind(e.target.value as AdvisorKind)}>
              {kinds.map((k) => (
                <option key={k.value} value={k.value}>
                  {k.label}
                </option>
              ))}
            </select>
            <select className="pill-select" aria-label="Minimum mean time" value={minMeanMs} onChange={(e) => setMinMeanMs(Number(e.target.value))}>
              {thresholds.map((t) => (
                <option key={t} value={t}>
                  {t === 0 ? "Any mean time" : `Slower than ${formatDuration(t)}`}
                </option>
              ))}
            </select>
            <select className="pill-select" aria-label="Minimum calls" value={minCalls} onChange={(e) => setMinCalls(Number(e.target.value))}>
              {callCounts.map((n) => (
                <option key={n} value={n}>
                  {n === 0 ? "Any number of calls" : `At least ${formatCount(n)} calls`}
                </option>
              ))}
            </select>
            <select className="pill-select" aria-label="Sort order" value={sort} onChange={(e) => setSort(e.target.value as AdvisorSort)}>
              {sorts.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
            {filtered && (
              <button type="button" onClick={reset}>
                Clear filters
              </button>
            )}
          </div>
          <p className="muted small-text">
            {source
              ? `Source: ${source}, sorted by ${sortNote}${!canWindow && since ? `. Totals since ${formatTime(since)}` : ""}`
              : "Slow-query statistics are not available for SQLite."}
          </p>
          {list.data?.truncated && (
            <p className="muted small-text">Showing the top {formatCount(list.data.queries.length)}. Narrow the filters to see other queries.</p>
          )}
        </div>
        {list.error && <Banner tone="warn">{list.error}</Banner>}
        {!list.error && !list.loading && queries.length === 0 && (
          <p className="muted pad">{filtered ? "No queries match these filters." : `No queries slower than ${formatDuration(minMeanMs)}.`}</p>
        )}
        <ul>
          {queries.map((q) => (
            <li key={q.id}>
              <QueryCard q={q} current={q.id === selected?.id} onSelect={() => setSelectedId(q.id)} />
            </li>
          ))}
        </ul>
      </aside>
      <main className="page">
        {detail.error && <Banner tone="danger">{detail.error}</Banner>}
        {detail.data && (
          <Detail key={detail.data.id} q={detail.data} conn={conn} onOpen={(sql) => onOpen(sql, conn.id)} onSkip={() => skip(detail.data!.id)} />
        )}
      </main>
    </div>
  );
}

function QueryCard({ q, current, onSelect }: { q: SlowQuery; current: boolean; onSelect: () => void }) {
  const state = stateLabel[q.state];
  return (
    <button type="button" className="query-card" aria-current={current ? "true" : undefined} onClick={onSelect}>
      <span className="query-card-head">
        <span className="conn-name">{q.title}</span>
        <Badge tone={state.tone}>{state.text}</Badge>
      </span>
      <span className="mono muted query-card-sql">{oneLine(q.sql, 120)}</span>
      <span className="mono small-text query-card-stats">
        <span>
          <span className="muted">mean</span> {formatDuration(q.meanMs)}
        </span>
        <span>
          <span className="muted">calls</span> {formatCount(q.calls)}
        </span>
        <span>
          <span className="muted">share</span> {q.sharePct}%
        </span>
      </span>
    </button>
  );
}

const steps = ["Context collected", "Fix suggested", "Tested with EXPLAIN"];

function Detail({ q, conn, onOpen, onSkip }: { q: SlowQueryDetail; conn: Connection; onOpen: (sql: string) => void; onSkip: () => void }) {
  const backend = useBackend();
  const [copied, setCopied] = useState(false);
  const hottest = Math.max(...q.plan.map((n) => n.sharePct));
  const { advice } = q;
  const saved = advice ? Math.round((1 - advice.costAfter / advice.costBefore) * 100) : 0;

  const copy = (sql: string) =>
    void backend.copyText(sql).then(
      () => setCopied(true),
      () => setCopied(false),
    );

  return (
    <>
      <header className="page-head">
        <div>
          <h1 className="title">{q.title}</h1>
          <p className="muted">{q.tables.length > 0 ? `Tables used: ${q.tables.join(", ")}` : "No table names found in the statement"}</p>
        </div>
        <span className="spacer" />
        <dl className="stats">
          <div>
            <dt>Mean time</dt>
            <dd>{formatDuration(q.meanMs)}</dd>
          </div>
          <div>
            <dt>Calls</dt>
            <dd>{formatCount(q.calls)}</dd>
          </div>
          <div>
            <dt>Share of DB time</dt>
            <dd>{q.sharePct}%</dd>
          </div>
        </dl>
      </header>
      <div className="two-col">
        <section className="card" aria-label="Query">
          <header className="card-head small">
            <h2>Query</h2>
          </header>
          <pre className="code-block">{q.sql}</pre>
        </section>
        <section className="card" aria-label="Current plan">
          <header className="card-head small">
            <h2>Current plan</h2>
            <span className="spacer" />
            {q.plan.length > 0 && <span className="muted small-text">{q.planSource}</span>}
          </header>
          {q.plan.length === 0 && <p className="muted pad">{q.planSource}</p>}
          <table className="plan">
            <tbody>
              {q.plan.map((n, i) => (
                <tr key={i} className={n.sharePct === hottest && hottest >= 50 ? "hot" : undefined}>
                  <td style={{ paddingLeft: 8 + n.depth * 14 }}>{n.label}</td>
                  <td className="num">{n.cost.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}</td>
                  <td className="num">{n.sharePct}%</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      </div>
      <section className="card advice" aria-label="AI advice">
        <header className="card-head small">
          <h2 className="with-icon">
            <span className="ai-text">
              <Icon name="sparkle" size={15} strokeWidth={2} />
            </span>
            AI advice
          </h2>
          <span className="spacer" />
          {advice && (
            <ol className="steps">
              {steps.map((s) => (
                <li key={s} className="step done">
                  <Icon name="check" size={12} strokeWidth={2.5} /> {s}
                </li>
              ))}
              <li className="step current">
                <span className="mono">4</span> Your decision
              </li>
            </ol>
          )}
        </header>
        {advice ? (
          <>
            <div className="advice-body">
              <div className="advice-text">
                <div className="with-icon">
                  <span className="chip">{advice.kind === "index" ? "New index" : "Rewrite"}</span>
                  <strong>{advice.title}</strong>
                </div>
                <p>{advice.explanation}</p>
                <pre className="code-block boxed">{advice.sql}</pre>
              </div>
              <div className="advice-proof">
                <p className="ok-text with-icon top">
                  <Icon name="check" size={15} strokeWidth={2} />
                  <span>{advice.verifiedBy}</span>
                </p>
                <CostBar label="Before" value={advice.costBefore} max={advice.costBefore} tone="before" />
                <CostBar label="After" value={advice.costAfter} max={advice.costBefore} tone="after" />
                <p>
                  <span className="big accent-text">{saved}%</span> <span className="muted">lower plan cost</span>
                </p>
              </div>
            </div>
            <footer className="card-foot">
              <button type="button" className="primary" onClick={() => onOpen(advice.sql)}>
                Open fix in editor
              </button>
              <button type="button" onClick={() => copy(advice.sql)}>
                {copied ? "Copied" : "Copy SQL"}
              </button>
              <button type="button" onClick={onSkip}>
                Skip
              </button>
              <span className="spacer" />
              <span className="muted small-text">Nothing changes on {conn.name} until you run it.</span>
            </footer>
          </>
        ) : (
          <div className="advice-body">
            <p className="muted">{q.noAdviceReason ?? "No safe fix was found for this query."}</p>
          </div>
        )}
      </section>
    </>
  );
}

function CostBar({ label, value, max, tone }: { label: string; value: number; max: number; tone: "before" | "after" }) {
  return (
    <div className="cost-bar">
      <span className="muted">{label}</span>
      <span className="bar" role="img" aria-label={`${label}: plan cost ${formatCount(value)}`}>
        <span className={`bar-fill ${tone}`} style={{ width: `${(value / max) * 100}%` }} />
      </span>
      <span className="mono num">{formatCount(value)}</span>
    </div>
  );
}
