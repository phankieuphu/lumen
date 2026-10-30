import type { RefObject } from "react";

import type { Connection } from "../api";
import { Elapsed } from "../components/Elapsed";
import { ResultGrid } from "../components/ResultGrid";
import { type EditorHandle, SqlEditor } from "../components/SqlEditor";
import { Confirm, EnvBadge, ErrorText, KindBadge, StatusBadge } from "../components/ui";
import { useQueryRunner } from "../hooks/useQueryRunner";
import { formatCount, formatDuration } from "../lib/format";

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
    <section className="query">
      <div className="toolbar">
        <strong>{conn.name}</strong>
        <EnvBadge env={conn.env} />
        {conn.readOnly && <span className="badge">read-only</span>}
        <span className="spacer" />
        {running ? (
          <>
            <Elapsed />
            <button type="button" className="danger" onClick={cancel}>
              Cancel
            </button>
          </>
        ) : (
          <button type="button" className="primary" onClick={() => void run(editorRef.current?.sql() ?? "")} title="Run (Ctrl/⌘+Enter)">
            Run
          </button>
        )}
      </div>
      <SqlEditor ref={editorRef} dialect={conn.dialect} onRun={(sql) => void run(sql)} />
      <div className="result-bar" aria-live="polite">
        {state.phase === "error" && <ErrorText error={state.error} />}
        {summary && (
          <>
            <StatusBadge status={summary.status} />
            <KindBadge kind={summary.kind} />
            <span>
              {summary.kind === "read"
                ? `${formatCount(summary.rowCount)} rows`
                : summary.rowsAffected >= 0 && `${formatCount(summary.rowsAffected)} rows affected`}
            </span>
            <span className="muted">{formatDuration(summary.durationMs)}</span>
            {summary.truncated && <span className="warn">Stopped at the row limit; refine the query to see the rest.</span>}
            {summary.error && <ErrorText error={summary.error} />}
          </>
        )}
      </div>
      <div className="result">
        <ResultGrid store={store} />
      </div>
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
