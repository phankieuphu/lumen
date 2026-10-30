import { type RefObject, useState } from "react";

import type { Connection, Summary } from "../api";
import { Elapsed } from "../components/Elapsed";
import { Icon } from "../components/icons";
import { ResultGrid } from "../components/ResultGrid";
import { type EditorHandle, SqlEditor } from "../components/SqlEditor";
import { Confirm, EnvDot, ErrorText, KindBadge, StatusText } from "../components/ui";
import { useQueryRunner } from "../hooks/useQueryRunner";
import { formatCount, formatDuration } from "../lib/format";
import { HistoryPanel } from "./HistoryPanel";

type OutputTab = "results" | "history";

const dialectLabel = { postgres: "PostgreSQL", mysql: "MySQL", sqlite: "SQLite" } as const;
const runKey = /Mac|iPhone|iPad/.test(navigator.userAgent) ? "⌘ Enter" : "Ctrl+Enter";

interface Props {
  conn: Connection;
  editorRef: RefObject<EditorHandle | null>;
  /** Puts SQL into the editor and focuses it. */
  onOpenSQL: (sql: string) => void;
}

export function QueryScreen({ conn, editorRef, onOpenSQL }: Props) {
  const [tab, setTab] = useState<OutputTab>("results");
  const [historyVersion, setHistoryVersion] = useState(0);
  const { store, state, run, cancel, confirm, dismiss } = useQueryRunner(conn.id, () => setHistoryVersion((v) => v + 1));
  const running = state.phase === "running";

  const runSQL = (sql: string) => {
    setTab("results");
    void run(sql);
  };

  return (
    <section className="query" aria-label="Query editor">
      <div className="toolbar">
        {running ? (
          <button type="button" className="danger-outline" onClick={cancel}>
            <Icon name="stop" size={14} /> Cancel <Elapsed />
          </button>
        ) : (
          <button type="button" className="primary" onClick={() => runSQL(editorRef.current?.sql() ?? "")}>
            <Icon name="play" size={14} /> Run <kbd>{runKey}</kbd>
          </button>
        )}
        <span className="spacer" />
        <span className="chip conn-chip">
          <EnvDot env={conn.env} />
          <span>{conn.name}</span>
          <span className="muted">{dialectLabel[conn.dialect]}</span>
          {conn.readOnly && <Icon name="lock" size={13} label="Read-only" />}
        </span>
      </div>

      <SqlEditor ref={editorRef} dialect={conn.dialect} onRun={runSQL} />

      <div className="output" aria-label="Query output">
        <div className="output-tabs" role="tablist">
          <button type="button" role="tab" aria-selected={tab === "results"} onClick={() => setTab("results")}>
            Results
          </button>
          <button type="button" role="tab" aria-selected={tab === "history"} onClick={() => setTab("history")}>
            History
          </button>
          <span className="spacer" />
          {tab === "results" && <RunInfo running={running} summary={state.summary} />}
        </div>
        {tab === "results" ? (
          <div className="output-panel" role="tabpanel">
            {state.phase === "error" && (
              <div className="output-message">
                <ErrorText error={state.error} />
              </div>
            )}
            {state.summary?.error && (
              <div className="output-message">
                <ErrorText error={state.summary.error} />
              </div>
            )}
            {state.summary?.truncated && (
              <p className="output-message warn">Stopped at the row limit. Narrow the query to see the rest.</p>
            )}
            {state.phase === "idle" && <p className="output-message muted">Run a statement to see its result here.</p>}
            {state.summary && state.summary.kind !== "read" && state.summary.status === "ok" && (
              <p className="output-message ok">Statement ran. {changedText(state.summary) || "No row count reported."}</p>
            )}
            <ResultGrid store={store} />
          </div>
        ) : (
          <HistoryPanel connId={conn.id} refresh={historyVersion} onOpen={onOpenSQL} />
        )}
      </div>

      {state.phase === "confirm" && (
        <Confirm title="Write to production?" confirmLabel="Run on production" danger onConfirm={confirm} onCancel={dismiss}>
          <p>
            <strong>{conn.name}</strong> is a production connection. This statement changes data or schema and will be
            recorded in the audit log.
          </p>
          <pre className="code-block">{state.pending}</pre>
        </Confirm>
      )}
    </section>
  );
}

const changedText = (s: Summary) => (s.rowsAffected >= 0 ? `${formatCount(s.rowsAffected)} rows changed` : "");

function RunInfo({ running, summary }: { running: boolean; summary: Summary | undefined }) {
  if (running) return <span className="output-info">running…</span>;
  if (!summary) return null;
  const rows = summary.kind === "read" ? `${formatCount(summary.rowCount)} rows` : changedText(summary);
  return (
    <span className="output-info" aria-live="polite">
      <StatusText status={summary.status} />
      <KindBadge kind={summary.kind} />
      <span>{[rows, formatDuration(summary.durationMs)].filter(Boolean).join(" · ")}</span>
    </span>
  );
}
