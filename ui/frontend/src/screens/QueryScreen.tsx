import type { RefObject } from "react";

import type { Connection } from "../api";
import { Elapsed } from "../components/Elapsed";
import { Icon } from "../components/icons";
import { ResultGrid } from "../components/ResultGrid";
import { type EditorHandle, SqlEditor } from "../components/SqlEditor";
import { Banner, Confirm, EnvBadge, EnvDot, KindBadge, StatusBadge } from "../components/ui";
import { useQueryRunner } from "../hooks/useQueryRunner";
import { connTarget, dialectName, formatCount, formatDuration } from "../lib/format";

const RUN_KEYS = /Mac|iPhone|iPad/.test(navigator.userAgent) ? "⌘ Enter" : "Ctrl+Enter";

interface Props {
  conn: Connection;
  editorRef: RefObject<EditorHandle | null>;
  /** Called after each request, e.g. to refresh the history list. */
  onRan: () => void;
}

export function QueryScreen({ conn, editorRef, onRan }: Props) {
  const { store, state, run, cancel, confirm, dismiss } = useQueryRunner(conn.id, onRan);
  const running = state.phase === "running";
  const { summary } = state;

  return (
    <section className={conn.env === "prod" ? "query prod" : "query"} aria-label="Query editor">
      <div className="query-toolbar">
        {running ? (
          <button type="button" className="danger" onClick={cancel}>
            <Icon name="stop" size={12} strokeWidth={2} /> Cancel <Elapsed />
          </button>
        ) : (
          <button type="button" className="primary run" onClick={() => void run(editorRef.current?.sql() ?? "")} title={`Run (${RUN_KEYS})`}>
            <Icon name="play" size={12} strokeWidth={2} /> Run <kbd>{RUN_KEYS}</kbd>
          </button>
        )}
        <span className="spacer" />
        <span className="conn-pill" title={`${dialectName(conn.dialect)} · ${connTarget(conn)}`}>
          <EnvDot env={conn.env} />
          <span>{conn.name}</span>
          <span className="muted">{dialectName(conn.dialect)}</span>
        </span>
      </div>
      <SqlEditor ref={editorRef} dialect={conn.dialect} onRun={(sql) => void run(sql)} />
      {state.phase === "error" && <Banner tone="danger">{state.error}</Banner>}
      {summary?.error && <Banner tone="danger">{summary.error}</Banner>}
      {summary?.truncated && <Banner tone="warn">Stopped at the row limit. Refine the query to see the rest.</Banner>}
      <div className="result-head" aria-live="polite">
        <span className="result-tab">Results</span>
        {summary && (
          <>
            <StatusBadge status={summary.status} />
            <KindBadge kind={summary.kind} />
          </>
        )}
        <span className="spacer" />
        {summary && (
          <span className="mono muted">
            {summary.kind === "read"
              ? `${formatCount(summary.rowCount)} rows`
              : summary.rowsAffected >= 0 && `${formatCount(summary.rowsAffected)} rows affected`}
            {" · "}
            {formatDuration(summary.durationMs)}
          </span>
        )}
      </div>
      <div className="result">
        <ResultGrid store={store} />
      </div>
      <footer className="statusbar">
        <span className="statusbar-item">
          <EnvDot env={conn.env} />
          {conn.name} · {connTarget(conn)}
        </span>
        <EnvBadge env={conn.env} />
        <span className="statusbar-item">{conn.readOnly ? "Read-only" : "Read and write"}</span>
        <span className="spacer" />
        <span className="statusbar-item">{RUN_KEYS} runs the selection, or everything</span>
      </footer>
      {state.phase === "confirm" && (
        <Confirm title="Write to production?" confirmLabel="Run on production" danger onConfirm={confirm} onCancel={dismiss}>
          <p>
            <strong>{conn.name}</strong> is a production connection. This statement changes data or schema and will be
            recorded in the audit log.
          </p>
          <pre className="sql-preview">{state.pending}</pre>
        </Confirm>
      )}
    </section>
  );
}
