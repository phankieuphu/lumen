import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { errorMessage, needsConfirmation, type Summary, useBackend } from "../api";
import { ResultStore } from "../lib/resultStore";

export type RunPhase = "idle" | "running" | "done" | "error" | "confirm";

export interface RunState {
  phase: RunPhase;
  summary?: Summary;
  error?: string;
  /** SQL waiting for the user to confirm a production write. */
  pending?: string;
  /** The SQL of the last request that finished, and when it finished. */
  sql?: string;
  finishedAt?: Date;
}

/** Rows per page requested from the backend. */
const PAGE_SIZE = 500;

/**
 * useQueryRunner runs one statement at a time on connId and streams its rows
 * into a ResultStore. Only the current request may touch the store or the
 * state; a request that was cancelled or superseded is ignored. onFinish is
 * called after every request, whatever its outcome, with the summary when
 * there is one. txId is asked for the open transaction as each statement
 * starts, so the statement runs inside it; if it rejects, nothing runs.
 *
 * Pages arrive as events, apart from the call's result, so the last ones can
 * land after runQuery resolves. The store therefore keeps taking pages for
 * the request it shows until the next run replaces it.
 */
export function useQueryRunner(connId: string, onFinish?: (summary?: Summary) => void, txId?: () => Promise<string | undefined>) {
  const backend = useBackend();
  const store = useMemo(() => new ResultStore(), []);
  const [state, setState] = useState<RunState>({ phase: "idle" });
  // The running request; null once it finished.
  const active = useRef<string | null>(null);
  // The request whose rows the store holds, finished or not.
  const shown = useRef<string | null>(null);
  const finished = useRef(onFinish);
  finished.current = onFinish;

  useEffect(
    () =>
      backend.onPage((page) => {
        if (page.requestId === shown.current) store.append(page);
      }),
    [backend, store],
  );

  const run = useCallback(
    async (sql: string, confirmWrite = false) => {
      if (active.current || !sql.trim()) return;
      const requestId = crypto.randomUUID();
      const current = () => active.current === requestId;
      active.current = requestId;
      shown.current = requestId;
      store.clear();
      setState({ phase: "running" });
      let summary: Summary | undefined;
      try {
        const tx = await txId?.();
        summary = await backend.runQuery({ requestId, connId, sql, pageSize: PAGE_SIZE, confirmWrite, txId: tx });
        if (current()) setState({ phase: "done", summary, sql, finishedAt: new Date() });
      } catch (err) {
        if (current()) {
          setState(
            needsConfirmation(err)
              ? { phase: "confirm", pending: sql }
              : { phase: "error", error: errorMessage(err), sql, finishedAt: new Date() },
          );
        }
      } finally {
        if (current()) active.current = null;
        finished.current?.(summary);
      }
    },
    [backend, connId, store, txId],
  );

  /** stop asks the backend to cancel the running request. */
  const stop = useCallback(() => {
    const id = active.current;
    if (!id) return;
    // Rejects when the request already finished: nothing left to cancel.
    backend.cancelQuery(id).catch(() => {});
  }, [backend]);

  // A different connection starts from a clean slate; its predecessor's
  // request is cancelled rather than left streaming into this screen.
  useEffect(() => {
    store.clear();
    setState({ phase: "idle" });
    return () => {
      stop();
      active.current = null;
      shown.current = null;
    };
  }, [connId, stop, store]);

  const confirm = useCallback(() => {
    if (state.pending) void run(state.pending, true);
  }, [run, state.pending]);

  const dismiss = useCallback(() => setState({ phase: "idle" }), []);

  return { store, state, run, cancel: stop, confirm, dismiss };
}
