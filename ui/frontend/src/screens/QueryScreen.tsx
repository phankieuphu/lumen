import { type RefObject, useCallback, useEffect, useState, useSyncExternalStore } from "react";

import { type Connection, errorMessage, type Transaction, useBackend } from "../api";
import { Elapsed } from "../components/Elapsed";
import { Icon } from "../components/icons";
import { ResultGrid } from "../components/ResultGrid";
import { type EditorHandle, SqlEditor } from "../components/SqlEditor";
import { Banner, Confirm, EnvBadge, EnvDot, KindBadge, StatusBadge, TxTag } from "../components/ui";
import { useQueryRunner } from "../hooks/useQueryRunner";
import { type TxNotice, useTransaction } from "../hooks/useTransaction";
import { connTarget, dialectName, formatCount, formatDuration } from "../lib/format";

const RUN_KEYS = /Mac|iPhone|iPad/.test(navigator.userAgent) ? "⌘ Enter" : "Ctrl+Enter";

interface Props {
  conn: Connection;
  editorRef: RefObject<EditorHandle | null>;
  /** Called after each request, e.g. to refresh the history list. */
  onRan: () => void;
}

export function QueryScreen({ conn, editorRef, onRan }: Props) {
  const backend = useBackend();
  const txn = useTransaction(conn.id);
  const { tx } = txn;
  const { store, state, run, cancel, confirm, dismiss } = useQueryRunner(
    conn.id,
    (summary) => {
      onRan();
      if (summary?.txEnd) txn.ended(summary);
      // Counters, or a transaction a failed statement ended.
      void txn.refresh();
    },
    tx?.id,
  );
  const runSql = (sql: string) => {
    txn.dismiss();
    void run(sql);
  };
  const txAction = (fn: () => Promise<void>) => () => void fn().finally(onRan);
  const [copied, setCopied] = useState<{ text: string; failed?: boolean }>();

  const copy = useCallback(
    (text: string, what: string) => {
      backend.copyText(text).then(
        () => setCopied({ text: `Copied ${what}` }),
        (err: unknown) => setCopied({ text: `Copy failed: ${errorMessage(err)}`, failed: true }),
      );
    },
    [backend],
  );
  // The notice hides itself; the timer is cleared if another copy replaces it.
  useEffect(() => {
    if (!copied) return;
    const id = setTimeout(() => setCopied(undefined), 1800);
    return () => clearTimeout(id);
  }, [copied]);
  const running = state.phase === "running";
  const { summary } = state;
  // Rows can still be arriving after the summary; keep showing progress until
  // the store holds as many as the summary reports.
  const loaded = useSyncExternalStore(store.subscribe, () => store.rowCount);
  const hasColumns = useSyncExternalStore(store.subscribe, () => store.columns.length > 0);
  const loading = running || (state.phase === "done" && summary?.kind === "read" && loaded < summary.rowCount);

  return (
    <section className={`query${conn.env === "prod" ? " prod" : ""}${tx ? " in-tx" : ""}`} aria-label="Query editor">
      <div className="query-toolbar">
        {running ? (
          <button type="button" className="danger" onClick={cancel}>
            <Icon name="stop" size={12} strokeWidth={2} /> Cancel <Elapsed />
          </button>
        ) : (
          <button
            type="button"
            className="primary run"
            onClick={() => runSql(editorRef.current?.sql() ?? "")}
            disabled={txn.busy}
            title={`Run (${RUN_KEYS})`}
          >
            <Icon name="play" size={12} strokeWidth={2} /> Run <kbd>{RUN_KEYS}</kbd>
          </button>
        )}
        <TxControls
          tx={tx}
          disabled={running || txn.busy}
          onBegin={(label) => txAction(() => txn.begin(label))()}
          onCommit={txAction(txn.commit)}
          onRollback={txAction(txn.rollback)}
        />
        <span className="spacer" />
        <span className="conn-pill" title={`${dialectName(conn.dialect)} · ${connTarget(conn)}`}>
          <EnvDot env={conn.env} />
          <span>{conn.name}</span>
          <span className="muted">{dialectName(conn.dialect)}</span>
        </span>
      </div>
      <SqlEditor ref={editorRef} dialect={conn.dialect} onRun={runSql} />
      {txn.error && <Banner tone="danger">{txn.error}</Banner>}
      {txn.notice && <TxEndBanner notice={txn.notice} />}
      {state.phase === "error" && <Banner tone="danger">{state.error}</Banner>}
      {summary?.error && <Banner tone="danger">{summary.error}</Banner>}
      {summary?.truncated && <Banner tone="warn">Stopped at the row limit. Refine the query to see the rest.</Banner>}
      <div className="result-head" aria-live="polite">
        <span className="result-tab">Results</span>
        {summary && (
          <>
            <StatusBadge status={summary.status} />
            <KindBadge kind={summary.kind} />
            {summary.txId && <TxTag id={summary.txId} label={summary.txLabel} />}
          </>
        )}
        <span className="spacer" />
        {loading && (
          <span className="result-loading-note muted" role="status">
            <span className="spinner" aria-hidden="true" />
            {running ? "Running…" : "Loading rows…"}
            {loaded > 0 && ` ${formatCount(loaded)} rows`}
          </span>
        )}
        {copied ? (
          <span className={copied.failed ? "copy-note danger-text" : "copy-note ok-text"} role="status">
            <Icon name={copied.failed ? "alert" : "check"} size={13} strokeWidth={2} /> {copied.text}
          </span>
        ) : (
          summary?.kind === "read" &&
          summary.rowCount > 0 && <span className="muted small-text">Click a header to sort, a cell to copy it, or # for the whole row</span>
        )}
        {summary && !loading && (
          <span className="mono muted">
            {summary.kind === "read"
              ? `${formatCount(summary.rowCount)} rows`
              : summary.rowsAffected >= 0 && `${formatCount(summary.rowsAffected)} rows affected`}
            {" · "}
            {formatDuration(summary.durationMs)}
          </span>
        )}
      </div>
      <div className={loading ? "result loading" : "result"} aria-busy={loading}>
        {loading && <div className="result-progress" aria-hidden="true" />}
        {loading && !hasColumns ? (
          <div className="result-wait">
            <span className="spinner large" aria-hidden="true" />
            <span>{running ? "Running query…" : "Loading rows…"}</span>
          </div>
        ) : (
          <ResultGrid store={store} onCopy={copy} />
        )}
      </div>
      <footer className="statusbar">
        <span className="statusbar-item">
          <EnvDot env={conn.env} />
          {conn.name} · {connTarget(conn)}
        </span>
        <EnvBadge env={conn.env} />
        <span className="statusbar-item">{conn.readOnly ? "Read-only" : "Read and write"}</span>
        {tx && <span className="statusbar-item tx-status">In a transaction: nothing is kept until you commit</span>}
        <span className="spacer" />
        <span className="statusbar-item">{RUN_KEYS} runs the selection, or everything</span>
      </footer>
      {state.phase === "confirm" && (
        <Confirm title="Write to production?" confirmLabel="Run on production" danger onConfirm={confirm} onCancel={dismiss}>
          <p>
            <strong>{conn.name}</strong> is a production connection. This statement changes data or schema and will be
            recorded in the audit log.
          </p>
          {tx && (
            <p>
              It runs inside transaction <TxTag id={tx.id} label={tx.label} />, so it is only kept if you commit.
            </p>
          )}
          <pre className="sql-preview">{state.pending}</pre>
        </Confirm>
      )}
    </section>
  );
}

interface TxControlsProps {
  tx: Transaction | undefined;
  disabled: boolean;
  onBegin: (label: string) => void;
  onCommit: () => void;
  onRollback: () => void;
}

/**
 * TxControls begins a transaction (with an optional label that tags its
 * statements in history and the audit log), or shows the open one with
 * Commit and Rollback.
 */
function TxControls({ tx, disabled, onBegin, onCommit, onRollback }: TxControlsProps) {
  const [asking, setAsking] = useState(false);
  const [label, setLabel] = useState("");
  useEffect(() => {
    if (tx) setAsking(false);
  }, [tx]);

  if (tx) {
    return (
      <div className="tx-bar" role="group" aria-label="Open transaction">
        <span className="tx-pill" role="status">
          <span className="tx-live" aria-hidden="true" />
          Transaction
          <TxTag id={tx.id} label={tx.label} />
          <span className="muted">
            {tx.statements} {tx.statements === 1 ? "statement" : "statements"} · {tx.writes} {tx.writes === 1 ? "write" : "writes"} ·{" "}
          </span>
          <Elapsed key={tx.id} since={tx.startedAt} />
        </span>
        <button type="button" className="primary" onClick={onCommit} disabled={disabled}>
          <Icon name="check" size={12} strokeWidth={2} /> Commit
        </button>
        <button type="button" className="danger" onClick={onRollback} disabled={disabled}>
          Rollback
        </button>
      </div>
    );
  }
  if (asking) {
    const start = () => {
      onBegin(label);
      setLabel("");
    };
    return (
      <div className="tx-bar" role="group" aria-label="Begin transaction">
        <input
          className="tx-label-input"
          autoFocus
          value={label}
          maxLength={80}
          placeholder="Label, e.g. fix order 42 (optional)"
          aria-label="Transaction label"
          onChange={(e) => setLabel(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") start();
            if (e.key === "Escape") setAsking(false);
          }}
        />
        <button type="button" className="primary" onClick={start} disabled={disabled}>
          Begin
        </button>
        <button type="button" onClick={() => setAsking(false)}>
          Cancel
        </button>
      </div>
    );
  }
  return (
    <button
      type="button"
      onClick={() => setAsking(true)}
      disabled={disabled}
      title="Run the next statements in one transaction; nothing is kept until you commit"
    >
      Begin transaction
    </button>
  );
}

function TxEndBanner({ notice }: { notice: TxNotice }) {
  const { summary, tx } = notice;
  const tag = <TxTag id={tx.id} label={tx.label} />;
  const writes = `${tx.writes} ${tx.writes === 1 ? "write" : "writes"}`;
  return summary.txEnd === "commit" ? (
    <Banner tone="ok">
      Committed {tag}: {writes} kept.
    </Banner>
  ) : (
    <Banner tone="warn">
      Rolled back {tag}: {writes} discarded.
    </Banner>
  );
}
