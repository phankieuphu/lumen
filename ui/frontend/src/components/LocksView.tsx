import type { Lock, Transaction } from "../api";
import type { LocksState } from "../hooks/useTxLocks";
import { Icon } from "./icons";
import { Banner, TxTag } from "./ui";

interface Props {
  tx: Transaction | undefined;
  state: LocksState;
  onRefresh: () => void;
}

const typeLabel: Record<Lock["type"], string> = {
  table: "Table",
  row: "Row",
  transaction: "Transaction",
  metadata: "Metadata",
  advisory: "Advisory",
  other: "Other",
};

/**
 * LocksView lists what the open transaction locks, or waits for, with what
 * each lock means for everyone else.
 */
export function LocksView({ tx, state, onRefresh }: Props) {
  if (!tx) {
    return (
      <div className="explain-empty">
        <Icon name="lock" size={22} />
        <p>Locks last only as long as a transaction.</p>
        <p className="muted">
          Begin a transaction and run a statement to see what it locks, and what that holds up for everyone else until you commit or
          roll back.
        </p>
      </div>
    );
  }
  const report = state.report;
  return (
    <div className="explain locks">
      <div className="explain-note">
        <span className="muted">Locks held by</span>
        <TxTag id={tx.id} label={tx.label} />
        {report && (
          <span className="muted">
            {report.locks.length} {report.locks.length === 1 ? "lock" : "locks"}
          </span>
        )}
        <span className="spacer" />
        <button type="button" className="small" onClick={onRefresh} disabled={state.phase === "loading"}>
          Refresh
        </button>
      </div>
      {state.phase === "error" && <Banner tone="warn">{state.error}</Banner>}
      {report?.waiting && (
        <Banner tone="danger">
          Waiting for a lock another transaction holds. The statement goes on once that transaction commits or rolls back.
        </Banner>
      )}
      {report?.truncated && <Banner tone="warn">Only the first locks are shown.</Banner>}
      {state.phase === "loading" && !report && (
        <div className="result-wait">
          <span className="spinner large" aria-hidden="true" />
          <span>Reading locks…</span>
        </div>
      )}
      {report && report.locks.length === 0 && (
        <p className="muted">No locks yet: reads and writes in this transaction will take them.</p>
      )}
      {report && report.locks.length > 0 && (
        <section className="card explain-plan" aria-label="Locks">
          <table className="plan-table locks-table">
            <thead>
              <tr>
                <th>Status</th>
                <th>Lock</th>
                <th>On</th>
                <th>What it means</th>
              </tr>
            </thead>
            <tbody>
              {report.locks.map((l, i) => (
                <tr key={i} className={l.granted ? "" : "flag-warning waiting"}>
                  <td>
                    <span className={`badge ${l.granted ? "badge-ok" : "badge-error"}`}>{l.granted ? "held" : "waiting"}</span>
                  </td>
                  <td>
                    <span className="plan-kind">{typeLabel[l.type]}</span>
                    <span className="plan-detail mono"> {l.mode}</span>
                  </td>
                  <td>
                    <span className="mono">{l.object}</span>
                    {l.detail && <span className="plan-detail mono"> {l.detail}</span>}
                  </td>
                  <td className="locks-meaning">{l.meaning}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}
      {report?.note && <p className="muted small-text">{report.note}</p>}
    </div>
  );
}
