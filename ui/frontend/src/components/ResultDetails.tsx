import type { ReactNode } from "react";

import type { Connection, Summary } from "../api";
import { connTarget, dialectName, formatCount, formatDuration, outcomeTitle, statementVerb } from "../lib/format";
import { Icon } from "./icons";
import { EnvBadge, TxTag } from "./ui";

const kindText: Record<string, string> = {
  read: "Read",
  write: "Data change",
  schema: "Schema change",
  session: "Session",
};

interface Props {
  conn: Connection;
  /** The finished request's summary; absent when the call itself failed. */
  summary?: Summary;
  /** The error when the call failed without a summary. */
  error?: string;
  sql: string;
  finishedAt?: Date;
  onCopy: (text: string, what: string) => void;
}

/**
 * ResultDetails fills the result area for a statement that returned no
 * columns (a write, a schema change, a failure): what happened, the numbers,
 * where it ran, and the SQL itself.
 */
export function ResultDetails({ conn, summary, error, sql, finishedAt, onCopy }: Props) {
  const status = summary?.status ?? "error";
  const message = summary?.error || error;
  const failed = status !== "ok";
  const title = summary ? outcomeTitle(summary, sql) : "Statement failed";
  const verb = statementVerb(sql);

  return (
    <div className={`result-details${failed ? " failed" : ""}`}>
      <div className="result-details-head">
        <span className={`result-details-icon ${failed ? "danger-text" : "ok-text"}`} aria-hidden="true">
          <Icon name={failed ? "alert" : "check"} size={18} strokeWidth={2} />
        </span>
        <h2>{title}</h2>
      </div>
      {message && <pre className="result-details-error">{message}</pre>}
      <dl className="result-details-facts">
        {verb && (
          <Fact label="Statement">
            <span className="mono">{verb}</span>
            {summary && <span className="muted"> · {kindText[summary.kind] ?? summary.kind}</span>}
          </Fact>
        )}
        {summary && summary.kind !== "read" && (
          <Fact label="Rows affected">
            <span className="mono">{summary.rowsAffected >= 0 ? formatCount(summary.rowsAffected) : "Not reported by the database"}</span>
          </Fact>
        )}
        {summary?.kind === "read" && (
          <Fact label="Rows returned">
            <span className="mono">{formatCount(summary.rowCount)}</span>
          </Fact>
        )}
        {summary && (
          <Fact label="Duration">
            <span className="mono">{formatDuration(summary.durationMs)}</span>
          </Fact>
        )}
        {finishedAt && (
          <Fact label="Finished">
            <span className="mono">{finishedAt.toLocaleTimeString()}</span>
          </Fact>
        )}
        <Fact label="Connection">
          {conn.name} <span className="muted">· {dialectName(conn.dialect)} · {connTarget(conn)}</span> <EnvBadge env={conn.env} />
        </Fact>
        <Fact label="Transaction">
          {summary?.txId ? (
            <>
              <TxTag id={summary.txId} label={summary.txLabel} />
              <span className="muted"> {summary.txEnd ? "ended by this statement" : "kept only if you commit"}</span>
            </>
          ) : (
            <span className="muted">{noTxText(summary, failed)}</span>
          )}
        </Fact>
      </dl>
      <div className="result-details-sql">
        <div className="result-details-sql-head">
          <span className="eyebrow">SQL</span>
          <button type="button" className="small" onClick={() => onCopy(sql, "SQL")}>
            Copy
          </button>
        </div>
        <pre className="sql-preview">{sql.trim()}</pre>
      </div>
    </div>
  );
}

/** noTxText says what autocommit meant for a statement run outside a transaction. */
function noTxText(summary: Summary | undefined, failed: boolean): string {
  if (failed) return "None: nothing was changed";
  if (summary?.kind === "write" || summary?.kind === "schema") return "None: the change is already saved";
  return "None";
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </>
  );
}
