import { useCallback, useEffect, useRef, useState } from "react";

import { errorMessage, type Summary, type Transaction, useBackend } from "../api";

/** TxNotice reports how the last transaction ended. */
export interface TxNotice {
  summary: Summary;
  /** The transaction as it was just before it ended. */
  tx: Transaction;
}

/**
 * useTransaction tracks the open transaction on connId. The backend owns it
 * (it outlives this screen), so the hook asks for it on mount and after
 * every statement, which also keeps its counters current.
 */
export function useTransaction(connId: string) {
  const backend = useBackend();
  const [tx, setTx] = useState<Transaction>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [notice, setNotice] = useState<TxNotice>();
  // Answers for a connection the screen has left are dropped.
  const conn = useRef(connId);
  conn.current = connId;
  const last = useRef<Transaction | undefined>(undefined);
  last.current = tx;

  const refresh = useCallback(async () => {
    const asked = connId;
    try {
      const open = (await backend.transactions()).find((t) => t.connId === asked);
      // A typed COMMIT or ROLLBACK, or a deleted connection, may have ended it.
      if (conn.current === asked) setTx(open);
    } catch (err) {
      if (conn.current === asked) setError(errorMessage(err));
    }
  }, [backend, connId]);

  useEffect(() => {
    setTx(undefined);
    setError(undefined);
    setNotice(undefined);
    void refresh();
  }, [refresh]);

  const act = useCallback(
    async (fn: () => Promise<void>) => {
      setBusy(true);
      setError(undefined);
      try {
        await fn();
      } catch (err) {
        setError(errorMessage(err));
        // A failed COMMIT still ends the transaction; show what is left.
        await refresh();
      } finally {
        setBusy(false);
      }
    },
    [refresh],
  );

  const begin = useCallback(
    (label: string) =>
      act(async () => {
        setNotice(undefined);
        setTx(await backend.beginTx(connId, label));
      }),
    [act, backend, connId],
  );

  const end = useCallback(
    (how: "commit" | "rollback") =>
      act(async () => {
        const open = last.current;
        if (!open) return;
        const summary = await (how === "commit" ? backend.commitTx(open.id) : backend.rollbackTx(open.id));
        setTx(undefined);
        setNotice({ summary, tx: open });
      }),
    [act, backend],
  );

  /** ended records a typed COMMIT/ROLLBACK that the query runner saw. */
  const ended = useCallback((summary: Summary) => {
    const open = last.current;
    if (open && summary.txEnd) {
      setTx(undefined);
      setNotice({ summary, tx: { ...open, statements: open.statements + 1 } });
    }
  }, []);

  return {
    tx,
    busy,
    error,
    notice,
    begin,
    commit: useCallback(() => end("commit"), [end]),
    rollback: useCallback(() => end("rollback"), [end]),
    refresh,
    ended,
    dismiss: useCallback(() => {
      setNotice(undefined);
      setError(undefined);
    }, []),
  };
}
