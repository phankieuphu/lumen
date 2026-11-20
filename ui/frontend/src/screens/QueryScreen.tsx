import { type RefObject, useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";

import { type Connection, errorMessage, type SavedFile, type Transaction, useBackend } from "../api";
import { Elapsed } from "../components/Elapsed";
import { Icon } from "../components/icons";
import { DebugView } from "../components/DebugView";
import { type ExplainState, ExplainView } from "../components/ExplainView";
import { LocksView } from "../components/LocksView";
import { ResultDetails } from "../components/ResultDetails";
import { ResultGrid } from "../components/ResultGrid";
import { type EditorHandle, type EditorIssue, type RunTarget, SqlEditor } from "../components/SqlEditor";
import { Banner, Confirm, EnvBadge, EnvDot, KindBadge, StatusBadge, TxTag } from "../components/ui";
import { useDebug } from "../hooks/useDebug";
import { useQueryRunner } from "../hooks/useQueryRunner";
import { useSchemaCompletion } from "../hooks/useSchemaCompletion";
import { useTxLocks } from "../hooks/useTxLocks";
import { type TxNotice, useTransaction } from "../hooks/useTransaction";
import { connTarget, dialectName, formatCount, formatDuration } from "../lib/format";
import { statementAt } from "../lib/statements";

const MAC = /Mac|iPhone|iPad/.test(navigator.userAgent);
const RUN_KEYS = MAC ? "⌘ Enter" : "Ctrl+Enter";
const SAVE_KEYS = MAC ? "⌘S" : "Ctrl+S";
const SAVE_AS_KEYS = MAC ? "⇧⌘S" : "Ctrl+Shift+S";
const OPEN_KEYS = MAC ? "⌘O" : "Ctrl+O";
const EXPLAIN_KEYS = MAC ? "⌘E" : "Ctrl+E";

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
  // The selection, or the statement at the cursor when the editor holds
  // several, which flashes. If splitting fails, the whole text goes and the
  // backend says why.
  const pick = async (t: RunTarget): Promise<string> => {
    if (t.selection !== undefined) return t.selection;
    const ranges = await backend.statements(conn.dialect, t.text).catch(() => []);
    const at = ranges.length > 1 ? statementAt(ranges, t.cursor) : undefined;
    if (!at) return t.text;
    editorRef.current?.flash(at.from, at.to);
    return t.text.slice(at.from, at.to);
  };
  const [tab, setTab] = useState<"results" | "explain" | "locks" | "debug">("results");
  const runSql = (t: RunTarget) => {
    txn.dismiss();
    setTab("results");
    void pick(t).then(run);
  };
  const [explained, setExplained] = useState<ExplainState>({ phase: "idle" });
  // Only the latest Explain may show its plan.
  const explainSeq = useRef(0);
  const explainSql = (t: RunTarget) => {
    const seq = ++explainSeq.current;
    setTab("explain");
    setExplained({ phase: "running" });
    void pick(t)
      .then((sql) => backend.explain(conn.id, sql))
      .then(
        (plan) => seq === explainSeq.current && setExplained({ phase: "done", plan }),
        (err: unknown) => seq === explainSeq.current && setExplained({ phase: "error", error: errorMessage(err) }),
      );
  };
  const debug = useDebug({
    conn,
    editorRef,
    txId: tx?.id,
    onRan: () => {
      onRan();
      void txn.refresh();
    },
  });
  // Each debug action takes the editor's selection, or its statement or text.
  const onEditor = (fn: (t: RunTarget) => void) => () => editorRef.current && fn(editorRef.current.target());
  const file = useSqlFile(editorRef);
  const completion = useSchemaCompletion(conn.id);
  // Syntax errors, then lint findings: a statement with a syntax error gets no
  // lint findings, since it does not parse.
  const checkSql = useCallback(
    async (sql: string): Promise<EditorIssue[]> => {
      const [syntax, rules] = await Promise.all([
        backend.checkSyntax(conn.dialect, sql),
        backend.lint(conn.id, sql).catch(() => []),
      ]);
      return [
        ...syntax.map((i) => ({ ...i, severity: "error" as const })),
        ...rules.map((r) => ({ from: r.from, to: r.to, severity: r.severity, message: `${r.rule}: ${r.message}` })),
      ];
    },
    [backend, conn.dialect, conn.id],
  );
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
  const txLocks = useTxLocks(tx?.id, tab === "locks", running, tx?.statements ?? 0);
  const { summary } = state;
  // Rows can still be arriving after the summary; keep showing progress until
  // the store holds as many as the summary reports.
  const loaded = useSyncExternalStore(store.subscribe, () => store.rowCount);
  const hasColumns = useSyncExternalStore(store.subscribe, () => store.columns.length > 0);
  const loading = running || (state.phase === "done" && summary?.kind === "read" && loaded < summary.rowCount);
  // A statement that returned no columns gets a details panel instead of an empty grid.
  const details = !loading && !hasColumns && (state.phase === "done" || state.phase === "error") && state.sql !== undefined;

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
            onClick={() => editorRef.current && runSql(editorRef.current.target())}
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
        <button
          type="button"
          onClick={() => editorRef.current && explainSql(editorRef.current.target())}
          disabled={explained.phase === "running"}
          title={`Explain how the database runs the statement (${EXPLAIN_KEYS})`}
        >
          <Icon name="plan" size={14} /> Explain
        </button>
        <button type="button" onClick={file.open} title={`Open a SQL file (${OPEN_KEYS})`}>
          <Icon name="open" size={14} /> Open
        </button>
        <SaveControls file={file.current} dirty={file.dirty} onSave={(as) => void file.save(as)} />
        <span className="spacer" />
        <span className="conn-pill" title={`${dialectName(conn.dialect)} · ${connTarget(conn)}`}>
          <EnvDot env={conn.env} />
          <span>{conn.name}</span>
          <span className="muted">{dialectName(conn.dialect)}</span>
        </span>
      </div>
      <SqlEditor
        ref={editorRef}
        dialect={conn.dialect}
        onRun={runSql}
        check={checkSql}
        onSave={(as) => void file.save(as)}
        onOpen={file.open}
        onExplain={explainSql}
        onChange={file.markDirty}
        completion={completion}
      />
      {file.error && <Banner tone="danger">{file.error}</Banner>}
      {txn.error && <Banner tone="danger">{txn.error}</Banner>}
      {txn.notice && <TxEndBanner notice={txn.notice} />}
      {summary?.error && hasColumns && <Banner tone="danger">{summary.error}</Banner>}
      {summary?.truncated && <Banner tone="warn">Stopped at the row limit. Refine the query to see the rest.</Banner>}
      <div className="result-head" aria-live="polite">
        <div className="result-tabs" role="tablist" aria-label="Result">
          <button type="button" role="tab" aria-selected={tab === "results"} className="result-tab" onClick={() => setTab("results")}>
            Results
          </button>
          <button type="button" role="tab" aria-selected={tab === "explain"} className="result-tab" onClick={() => setTab("explain")}>
            Explain
            {explained.plan && explained.plan.findings.length > 0 && (
              <span className="tab-count" title="Findings">
                {explained.plan.findings.length}
              </span>
            )}
          </button>
          <button type="button" role="tab" aria-selected={tab === "debug"} className="result-tab" onClick={() => setTab("debug")}>
            Debug
            {(debug.steps.phase === "ready" || debug.steps.phase === "running") && (
              <span className="tab-count" title="Stepping through a script">
                {debug.steps.results.filter(Boolean).length}/{debug.steps.steps.filter((s) => !s.control).length}
              </span>
            )}
          </button>
          <button type="button" role="tab" aria-selected={tab === "locks"} className="result-tab" onClick={() => setTab("locks")}>
            Locks
            {tx && txLocks.state.report?.waiting && (
              <span className="tab-count waiting" title="Waiting for a lock">
                !
              </span>
            )}
          </button>
        </div>
        {tab === "results" && summary && (
          <>
            <StatusBadge status={summary.status} />
            <KindBadge kind={summary.kind} />
            {summary.txId && <TxTag id={summary.txId} label={summary.txLabel} />}
          </>
        )}
        <span className="spacer" />
        {tab === "results" && loading && (
          <span className="result-loading-note muted" role="status">
            <span className="spinner" aria-hidden="true" />
            {running ? "Running…" : "Loading rows…"}
            {loaded > 0 && ` ${formatCount(loaded)} rows`}
          </span>
        )}
        {tab !== "results" ? null : copied ? (
          <span className={copied.failed ? "copy-note danger-text" : "copy-note ok-text"} role="status">
            <Icon name={copied.failed ? "alert" : "check"} size={13} strokeWidth={2} /> {copied.text}
          </span>
        ) : (
          summary?.kind === "read" &&
          summary.rowCount > 0 && <span className="muted small-text">Click a header to sort, a cell to copy it, or # for the whole row</span>
        )}
        {tab === "results" && summary && !loading && (
          <span className="mono muted">
            {summary.kind === "read"
              ? `${formatCount(summary.rowCount)} rows`
              : summary.rowsAffected >= 0 && `${formatCount(summary.rowsAffected)} rows affected`}
            {" · "}
            {formatDuration(summary.durationMs)}
          </span>
        )}
      </div>
      <div className={loading ? "result loading" : "result"} aria-busy={loading} hidden={tab !== "results"}>
        {loading && <div className="result-progress" aria-hidden="true" />}
        {loading && !hasColumns ? (
          <div className="result-wait">
            <span className="spinner large" aria-hidden="true" />
            <span>{running ? "Running query…" : "Loading rows…"}</span>
          </div>
        ) : details ? (
          <ResultDetails
            conn={conn}
            summary={summary}
            error={state.error}
            sql={state.sql ?? ""}
            finishedAt={state.finishedAt}
            onCopy={copy}
          />
        ) : (
          <ResultGrid store={store} onCopy={copy} />
        )}
      </div>
      {tab === "explain" && (
        <div className="result">
          <ExplainView state={explained} keys={EXPLAIN_KEYS} />
        </div>
      )}
      {tab === "debug" && (
        <div className="result">
          <DebugView
            conn={conn}
            mode={debug.mode}
            onMode={debug.setMode}
            parts={debug.parts}
            dry={debug.dry}
            steps={debug.steps}
            limitMs={debug.limitMs}
            onLimit={debug.setLimitMs}
            onSplit={onEditor(debug.splitParts)}
            onDryRun={onEditor(debug.dryRun)}
            onCancelDry={debug.cancelDry}
            onStartSteps={onEditor(debug.startSteps)}
            onStep={debug.runStep}
            onStepAll={debug.runAllSteps}
            onShowRows={(sql) => {
              setTab("results");
              run(sql);
            }}
            onFlash={debug.flash}
          />
        </div>
      )}
      {tab === "locks" && (
        <div className="result">
          <LocksView tx={tx} state={txLocks.state} onRefresh={txLocks.refresh} />
        </div>
      )}
      <footer className="statusbar">
        <span className="statusbar-item">
          <EnvDot env={conn.env} />
          {conn.name} · {connTarget(conn)}
        </span>
        <EnvBadge env={conn.env} />
        <span className="statusbar-item">{conn.readOnly ? "Read-only" : "Read and write"}</span>
        {tx && <span className="statusbar-item tx-status">In a transaction: nothing is kept until you commit</span>}
        <span className="spacer" />
        <span className="statusbar-item">
          {RUN_KEYS} runs the selection, or the statement at the cursor · {EXPLAIN_KEYS} explains it
        </span>
      </footer>
      {file.confirmingOpen && (
        <Confirm title="Discard unsaved changes?" confirmLabel="Discard and open" danger onConfirm={file.openAnyway} onCancel={file.cancelOpen}>
          <p>
            {file.current ? (
              <>
                Your changes to <strong>{file.current.name}</strong> are not saved.
              </>
            ) : (
              "The SQL in the editor is not saved to a file."
            )}{" "}
            Opening another file replaces it.
          </p>
        </Confirm>
      )}
      {debug.confirming && (
        <Confirm
          title={debug.confirming.what === "dry" ? "Dry run on production?" : "Write to production?"}
          confirmLabel={debug.confirming.what === "dry" ? "Dry run on production" : "Run on production"}
          danger
          onConfirm={debug.confirm}
          onCancel={debug.dismiss}
        >
          <p>
            <strong>{conn.name}</strong> is a production connection.{" "}
            {debug.confirming.what === "dry"
              ? "The writes really run and are then rolled back: triggers fire and rows stay locked until the end. Each one is recorded in the audit log."
              : "This statement changes data or schema in the stepping's transaction, and is only kept if you commit. It is recorded in the audit log."}
          </p>
          <pre className="sql-preview">{debug.confirming.sql}</pre>
        </Confirm>
      )}
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

/**
 * useSqlFile opens and saves the editor text as a file: the first save, and
 * Save As, ask where with the native dialog; later saves go to the same file.
 * dirty is set by edits since the last open or save; opening over unsaved
 * edits asks first. Unsaved changes to a file are reported to the app, which
 * asks before the window closes.
 */
function useSqlFile(editorRef: RefObject<EditorHandle | null>) {
  const backend = useBackend();
  const [current, setCurrent] = useState<SavedFile>();
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState<string>();
  const [confirmingOpen, setConfirmingOpen] = useState(false);
  const busy = useRef(false);

  const unsaved = !!current && dirty;
  useEffect(() => {
    backend.setUnsaved(unsaved).catch(() => {});
  }, [backend, unsaved]);

  const save = useCallback(
    async (as: boolean) => {
      const editor = editorRef.current;
      if (!editor || busy.current) return;
      busy.current = true;
      const text = editor.target().text;
      try {
        const saved =
          current && !as ? await backend.saveSql(current.path, text) : await backend.saveSqlAs(current?.name ?? "query.sql", text);
        if (!saved.path) return; // the dialog was cancelled
        setCurrent(saved);
        setError(undefined);
        // Edits made while saving stay unsaved.
        if (editor.target().text === text) setDirty(false);
      } catch (err) {
        setError(`Not saved: ${errorMessage(err)}`);
      } finally {
        busy.current = false;
      }
    },
    [backend, current, editorRef],
  );

  const openAnyway = useCallback(async () => {
    setConfirmingOpen(false);
    const editor = editorRef.current;
    if (!editor || busy.current) return;
    busy.current = true;
    try {
      const opened = await backend.openSql();
      if (!opened.path) return; // the dialog was cancelled
      editor.setText(opened.sql);
      setCurrent({ path: opened.path, name: opened.name });
      setDirty(false); // after setText, whose change marked it dirty
      setError(undefined);
      requestAnimationFrame(() => editor.focus());
    } catch (err) {
      setError(`Not opened: ${errorMessage(err)}`);
    } finally {
      busy.current = false;
    }
  }, [backend, editorRef]);

  const hasText = () => (editorRef.current?.target().text.trim() ?? "") !== "";
  const open = () => {
    if (dirty && hasText()) setConfirmingOpen(true);
    else void openAnyway();
  };
  const cancelOpen = useCallback(() => setConfirmingOpen(false), []);
  const markDirty = useCallback(() => setDirty(true), []);
  return { current, dirty, error, save, open, openAnyway, cancelOpen, confirmingOpen, markDirty };
}

interface SaveControlsProps {
  file: SavedFile | undefined;
  dirty: boolean;
  onSave: (as: boolean) => void;
}

/** SaveControls shows the file the SQL is saved to, with Save and Save As. */
function SaveControls({ file, dirty, onSave }: SaveControlsProps) {
  if (!file) {
    return (
      <button type="button" onClick={() => onSave(false)} title={`Save the SQL to a file (${SAVE_KEYS})`}>
        <Icon name="save" size={14} /> Save
      </button>
    );
  }
  return (
    <div className="save-bar" role="group" aria-label="SQL file">
      <button
        type="button"
        onClick={() => onSave(false)}
        title={`${dirty ? "Save changes to" : "Saved to"} ${file.path} (${SAVE_KEYS})`}
      >
        <Icon name="save" size={14} />
        <span className="file-name">{file.name}</span>
        {dirty && <span className="dirty-dot" role="status" aria-label="Unsaved changes" />}
      </button>
      <button type="button" onClick={() => onSave(true)} title={`Save to another file (${SAVE_AS_KEYS})`}>
        Save as…
      </button>
    </div>
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
