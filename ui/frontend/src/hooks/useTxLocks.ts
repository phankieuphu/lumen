import { useCallback, useEffect, useState } from "react";

import { errorMessage, type LockReport, useBackend } from "../api";

export interface LocksState {
  phase: "idle" | "loading" | "done" | "error";
  report?: LockReport;
  error?: string;
}

/** How often locks are read while a statement runs, to catch it waiting. */
const POLL_MS = 2000;

/**
 * useTxLocks reads the locks of the open transaction while the Locks tab is
 * shown: again after each statement (statements counts them), every
 * POLL_MS while one runs, and on refresh(). The last report stays shown
 * while a new one loads.
 */
export function useTxLocks(txId: string | undefined, shown: boolean, running: boolean, statements: number) {
  const backend = useBackend();
  const [state, setState] = useState<LocksState>({ phase: "idle" });
  const [tick, setTick] = useState(0);
  const refresh = useCallback(() => setTick((n) => n + 1), []);

  useEffect(() => {
    if (!txId) {
      setState({ phase: "idle" });
      return;
    }
    if (!shown) return;
    let live = true;
    setState((s) => ({ ...s, phase: s.report ? "done" : "loading", error: undefined }));
    backend.txLocks(txId).then(
      (report) => live && setState({ phase: "done", report }),
      (err: unknown) => live && setState({ phase: "error", error: errorMessage(err) }),
    );
    return () => {
      live = false;
    };
  }, [backend, txId, shown, statements, tick]);

  useEffect(() => {
    if (!txId || !shown || !running) return;
    const id = setInterval(refresh, POLL_MS);
    return () => clearInterval(id);
  }, [txId, shown, running, refresh]);

  return { state, refresh };
}
