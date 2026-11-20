import { type RefObject, useCallback, useEffect, useRef, useState } from "react";

import {
  type Connection,
  type DebugStep,
  type DryRunResult,
  errorMessage,
  needsConfirmation,
  type PartsReport,
  type Summary,
  useBackend,
} from "../api";
import type { EditorHandle, RunTarget } from "../components/SqlEditor";
import { statementAt } from "../lib/statements";
import { nextStep } from "../lib/debug";

export type DebugMode = "parts" | "steps" | "dry";

/** Where the SQL a debug action took sits in the editor. */
interface Taken {
  sql: string;
  /** The offset of sql in the editor text. */
  base: number;
}

export interface PartsState extends Partial<Taken> {
  phase: "idle" | "running" | "done" | "error";
  report?: PartsReport;
  error?: string;
}

export interface DryState extends Partial<Taken> {
  phase: "idle" | "running" | "done" | "error";
  result?: DryRunResult;
  error?: string;
}

/** StepResult is what running one step gave. */
export interface StepResult {
  summary?: Summary;
  error?: string;
}

export interface StepsState extends Partial<Taken> {
  phase: "idle" | "starting" | "ready" | "running" | "ended" | "error";
  steps: DebugStep[];
  results: (StepResult | undefined)[];
  /** The next step to run, or -1 when all have run. */
  at: number;
  txId?: string;
  error?: string;
}

/** A production write waiting for the user to confirm. */
export interface DebugConfirm {
  what: "dry" | "step";
  sql: string;
}

export const DRY_RUN_LIMITS = [
  { ms: 10_000, label: "10 s" },
  { ms: 30_000, label: "30 s" },
  { ms: 120_000, label: "2 min" },
  { ms: 600_000, label: "10 min" },
];

// Steps report row counts, not rows: the Results tab shows rows.
const STEP_PAGE = 100;
const STEP_MAX_ROWS = 10_000;
const PREVIEW_ROWS = 20;
const IDLE_STEPS: StepsState = { phase: "idle", steps: [], results: [], at: -1 };

interface Options {
  conn: Connection;
  editorRef: RefObject<EditorHandle | null>;
  /** The transaction open on the connection, if any. */
  txId?: string;
  /** Called after a step ran or a transaction began, to refresh counters and history. */
  onRan: () => void;
}

/**
 * useDebug drives the Debug tab: splitting a query into counted parts,
 * dry-running a script in a transaction that is rolled back, and stepping
 * through a script one statement at a time inside a real transaction that
 * the user commits or rolls back.
 */
export function useDebug({ conn, editorRef, txId, onRan }: Options) {
  const backend = useBackend();
  const [mode, setMode] = useState<DebugMode>("parts");
  const [parts, setParts] = useState<PartsState>({ phase: "idle" });
  const [dry, setDry] = useState<DryState>({ phase: "idle" });
  const [steps, setStepsState] = useState<StepsState>(IDLE_STEPS);
  const [confirming, setConfirming] = useState<DebugConfirm>();
  const [limitMs, setLimitMs] = useState(30_000);
  const partsSeq = useRef(0);
  const stepsSeq = useRef(0);
  const dryId = useRef<string | null>(null);
  // The stepping state, current at once: a run-all loop reads it between
  // steps, before React renders.
  const stepsRef = useRef(steps);
  const setSteps = useCallback((next: StepsState | ((s: StepsState) => StepsState)) => {
    stepsRef.current = typeof next === "function" ? next(stepsRef.current) : next;
    setStepsState(stepsRef.current);
  }, []);
  // Set while "Run the rest" runs, so a production confirmation resumes it.
  const runningAll = useRef(false);
  // The pending confirmation, current at once for the run-all loop.
  const confirmingRef = useRef<DebugConfirm | undefined>(undefined);

  // A new connection starts over; a dry run still running is stopped, as it
  // is when the screen goes away.
  useEffect(() => {
    const stop = () => {
      if (dryId.current) void backend.cancelDebug(dryId.current).catch(() => undefined);
      dryId.current = null;
    };
    partsSeq.current++;
    stepsSeq.current++;
    runningAll.current = false;
    stop();
    setParts({ phase: "idle" });
    setDry({ phase: "idle" });
    setSteps(IDLE_STEPS);
    confirmingRef.current = undefined;
    setConfirming(undefined);
    return stop;
  }, [backend, conn.id, setSteps]);

  // Stepping lives in one transaction: once it ends, so does the stepping.
  useEffect(() => {
    setSteps((s) => (s.txId && s.txId !== txId && (s.phase === "ready" || s.phase === "running") ? { ...s, phase: "ended" } : s));
  }, [txId, setSteps]);

  const flash = useCallback(
    (base: number | undefined, from: number, to: number) => {
      const ed = editorRef.current;
      if (!ed || base === undefined) return;
      const len = ed.target().text.length;
      ed.flash(Math.min(base + from, len), Math.min(base + to, len));
    },
    [editorRef],
  );

  /** script is the selection, or the whole editor text. */
  const script = (t: RunTarget): Taken =>
    t.selection !== undefined ? { sql: t.selection, base: t.selectionFrom ?? 0 } : { sql: t.text, base: 0 };

  /** statement is the selection, or the statement at the cursor. */
  const statement = async (t: RunTarget): Promise<Taken> => {
    if (t.selection !== undefined) return script(t);
    const ranges = await backend.statements(conn.dialect, t.text).catch(() => []);
    const at = ranges.length > 1 ? statementAt(ranges, t.cursor) : undefined;
    return at ? { sql: t.text.slice(at.from, at.to), base: at.from } : { sql: t.text, base: 0 };
  };

  const splitParts = async (t: RunTarget) => {
    const seq = ++partsSeq.current;
    setMode("parts");
    setParts({ phase: "running" });
    const taken = await statement(t);
    if (seq !== partsSeq.current) return;
    flash(taken.base, 0, taken.sql.length);
    setParts({ phase: "running", ...taken });
    try {
      const report = await backend.debugParts(conn.id, taken.sql);
      if (seq === partsSeq.current) setParts({ phase: "done", report, ...taken });
    } catch (err) {
      if (seq === partsSeq.current) setParts({ phase: "error", error: errorMessage(err), ...taken });
    }
  };

  const runDry = async (taken: Taken, confirmWrite: boolean) => {
    const requestId = crypto.randomUUID();
    dryId.current = requestId;
    setDry({ phase: "running", ...taken });
    try {
      const result = await backend.dryRun({
        requestId, connId: conn.id, sql: taken.sql, confirmWrite, timeoutMs: limitMs, previewRows: PREVIEW_ROWS,
      });
      if (dryId.current === requestId) setDry({ phase: "done", result, ...taken });
    } catch (err) {
      if (dryId.current !== requestId) return;
      if (needsConfirmation(err)) {
        setDry({ phase: "idle", ...taken });
        setConfirming({ what: "dry", sql: taken.sql });
      } else {
        setDry({ phase: "error", error: errorMessage(err), ...taken });
      }
    } finally {
      if (dryId.current === requestId) dryId.current = null;
    }
  };

  const dryRun = (t: RunTarget) => {
    setMode("dry");
    void runDry(script(t), false);
  };

  const cancelDry = () => {
    if (dryId.current) void backend.cancelDebug(dryId.current).catch(() => undefined);
  };

  const startSteps = async (t: RunTarget) => {
    setMode("steps");
    if (stepsRef.current.phase === "starting") return;
    const seq = ++stepsSeq.current;
    const taken = script(t);
    setSteps({ ...IDLE_STEPS, phase: "starting" });
    let opened: string | undefined;
    try {
      const list = await backend.debugSteps(conn.dialect, taken.sql);
      if (list.length === 0) throw new Error("No statement to step through.");
      // Use the open transaction, or open one for the stepping.
      let id = txId;
      if (!id) {
        id = opened = (await backend.beginTx(conn.id, "Step through")).id;
        onRan();
      }
      if (seq !== stepsSeq.current) {
        // The screen moved on: do not leave behind a transaction nobody sees.
        if (opened) await backend.rollbackTx(opened).finally(onRan);
        return;
      }
      const at = nextStep(list, 0);
      setSteps({ phase: "ready", steps: list, results: list.map(() => undefined), at, txId: id, ...taken });
      const first = list[at];
      if (first) flash(taken.base, first.start, first.end);
    } catch (err) {
      if (seq === stepsSeq.current) setSteps({ ...IDLE_STEPS, phase: "error", error: errorMessage(err), ...taken });
    }
  };

  /** runStep runs the next step; it resolves true if the run may go on. */
  const runStep = async (confirmWrite = false): Promise<boolean> => {
    const s = stepsRef.current;
    const step = s.steps[s.at];
    if (!step || !s.txId || s.phase !== "ready") return false;
    const index = s.at;
    flash(s.base, step.start, step.end);
    setSteps((cur) => ({ ...cur, phase: "running", error: undefined }));
    let result: StepResult;
    try {
      const summary = await backend.runQuery({
        requestId: crypto.randomUUID(), connId: conn.id, sql: step.sql,
        pageSize: STEP_PAGE, maxRows: STEP_MAX_ROWS, confirmWrite, txId: s.txId,
      });
      result = { summary, error: summary.error };
    } catch (err) {
      if (needsConfirmation(err)) {
        setSteps((cur) => ({ ...cur, phase: cur.phase === "running" ? "ready" : cur.phase }));
        confirmingRef.current = { what: "step", sql: step.sql };
        setConfirming(confirmingRef.current);
        return false;
      }
      result = { error: errorMessage(err) };
    }
    onRan();
    const ok = !result.error;
    setSteps((cur) => {
      if (cur.txId !== s.txId) return cur;
      const results = cur.results.slice();
      results[index] = result;
      const at = nextStep(cur.steps, index + 1);
      // The transaction may have ended while the step ran, or with it.
      const phase = cur.phase === "ended" || result.summary?.txEnd ? "ended" : "ready";
      return { ...cur, results, at, phase };
    });
    const next = s.steps[nextStep(s.steps, index + 1)];
    if (next) flash(s.base, next.start, next.end);
    return ok && stepsRef.current.phase === "ready" && stepsRef.current.at >= 0;
  };

  /** runRest runs steps until one fails, asks for confirmation, or none are left. */
  const runRest = async (confirmFirst = false) => {
    runningAll.current = true;
    try {
      let go = await runStep(confirmFirst);
      while (go && runningAll.current) go = await runStep();
    } finally {
      // A confirmation prompt keeps the run going once answered.
      if (!confirmingRef.current) runningAll.current = false;
    }
  };

  const confirm = () => {
    const c = confirming;
    setConfirming(undefined);
    confirmingRef.current = undefined;
    if (!c) return;
    if (c.what === "dry" && dry.sql !== undefined) void runDry({ sql: dry.sql, base: dry.base ?? 0 }, true);
    if (c.what !== "step") return;
    if (stepsRef.current.phase !== "ready") {
      runningAll.current = false;
      setSteps((cur) => ({ ...cur, error: "The step did not run: the transaction ended." }));
      return;
    }
    if (runningAll.current) void runRest(true);
    else void runStep(true);
  };

  const dismiss = () => {
    runningAll.current = false;
    confirmingRef.current = undefined;
    setConfirming(undefined);
  };

  return {
    mode, setMode, parts, dry, steps, confirming, limitMs, setLimitMs,
    splitParts: (t: RunTarget) => void splitParts(t),
    dryRun, cancelDry,
    startSteps: (t: RunTarget) => void startSteps(t),
    runStep: () => void runStep(),
    runAllSteps: () => void runRest(),
    confirm, dismiss,
    flash,
  };
}
