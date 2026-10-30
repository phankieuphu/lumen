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
}

/** Rows per page requested from the backend. */
const PAGE_SIZE = 500;

/**
 * useQueryRunner runs one statement at a time on connId and streams its rows
 * into a ResultStore. Only the current request may touch the store or the
 * state; a request that was cancelled or superseded is ignored. onFinish is
 * called after every request, whatever its outcome.
 */
export function useQueryRunner(connId: string, onFinish?: () => void) {
  const backend = useBackend();
  const store = useMemo(() => new ResultStore(), []);
  const [state, setState] = useState<RunState>({ phase: "idle" });
  const active = useRef<string | null>(null);
  const finished = useRef(onFinish);
  finished.current = onFinish;

  useEffect(
    () =>
      backend.onPage((page) => {
        if (page.requestId === active.current) store.append(page);
      }),
    [backend, store],
  );

  const run = useCallback(
    async (sql: string, confirmWrite = false) => {
      if (active.current || !sql.trim()) return;
      const requestId = crypto.randomUUID();
      const current = () => active.current === requestId;
      active.current = requestId;
      store.clear();
      setState({ phase: "running" });
      try {
        const summary = await backend.runQuery({ requestId, connId, sql, pageSize: PAGE_SIZE, confirmWrite });
        if (current()) setState({ phase: "done", summary });
      } catch (err) {
        if (current()) {
          setState(needsConfirmation(err) ? { phase: "confirm", pending: sql } : { phase: "error", error: errorMessage(err) });
        }
      } finally {
        if (current()) active.current = null;
        finished.current?.();
      }
    },
    [backend, connId, store],
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
    };
  }, [connId, stop, store]);

  const confirm = useCallback(() => {
    if (state.pending) void run(state.pending, true);
  }, [run, state.pending]);

  const dismiss = useCallback(() => setState({ phase: "idle" }), []);

  return { store, state, run, cancel: stop, confirm, dismiss };
}
