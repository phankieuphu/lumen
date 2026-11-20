import type { ReactNode } from "react";

import type { Cell, Connection, DebugPart, DryRunStatement } from "../api";
import { type DebugMode, DRY_RUN_LIMITS, type DryState, type PartsState, type StepsState } from "../hooks/useDebug";
import { changeText, clauseName, dryRunTotals, partFlow } from "../lib/debug";
import { formatCount } from "../lib/format";
import { Icon } from "./icons";
import { Banner, KindBadge, Segmented } from "./ui";

interface Props {
  conn: Connection;
  mode: DebugMode;
  onMode: (m: DebugMode) => void;
  parts: PartsState;
  dry: DryState;
  steps: StepsState;
  limitMs: number;
  onLimit: (ms: number) => void;
  /** Start an action on the editor's current selection or statement. */
  onSplit: () => void;
  onDryRun: () => void;
  onCancelDry: () => void;
  onStartSteps: () => void;
  onStep: () => void;
  onStepAll: () => void;
  /** Runs a read again in the Results tab, to see its rows. */
  onShowRows: (sql: string) => void;
  /** Highlights [from, to) of the SQL that was taken, offset by base. */
  onFlash: (base: number | undefined, from: number, to: number) => void;
}

const MODES = [
  { value: "parts", label: "Parts" },
  { value: "steps", label: "Step through" },
  { value: "dry", label: "Dry run" },
] as const;

/**
 * DebugView finds out why a query or script does what it does: the rows
 * each part of a query gives, a script run one statement at a time in a
 * transaction, or a dry run that is always rolled back.
 */
export function DebugView(p: Props) {
  return (
    <div className="debug">
      <div className="debug-head">
        <Segmented label="Debug" value={p.mode} options={MODES} onChange={p.onMode} />
        <span className="spacer" />
        {p.mode === "parts" && (
          <button type="button" className="primary" onClick={p.onSplit} disabled={p.parts.phase === "running"}>
            <Icon name="bug" size={14} /> Split into parts
          </button>
        )}
        {p.mode === "dry" && <DryControls {...p} />}
        {p.mode === "steps" && <StepControls {...p} />}
      </div>
      {p.mode === "parts" && <PartsPanel state={p.parts} onFlash={p.onFlash} />}
      {p.mode === "dry" && <DryPanel conn={p.conn} state={p.dry} onFlash={p.onFlash} />}
      {p.mode === "steps" && <StepsPanel state={p.steps} onShowRows={p.onShowRows} onFlash={p.onFlash} />}
    </div>
  );
}

function Empty({ icon, children }: { icon: "bug" | "step" | "play"; children: ReactNode }) {
  return (
    <div className="explain-empty">
      <Icon name={icon} size={22} />
      {children}
    </div>
  );
}

function Waiting({ text }: { text: string }) {
  return (
    <div className="result-wait">
      <span className="spinner large" aria-hidden="true" />
      <span>{text}</span>
    </div>
  );
}

function PartsPanel({ state, onFlash }: { state: PartsState; onFlash: Props["onFlash"] }) {
  if (state.phase === "idle") {
    return (
      <Empty icon="bug">
        <p>See where a query's rows come from and where they disappear.</p>
        <p className="muted">
          Put the cursor in a SELECT and press Split into parts. Each table, join, and filter is counted on its own, in a
          read-only transaction.
        </p>
      </Empty>
    );
  }
  if (state.phase === "running") return <Waiting text="Counting each part…" />;
  if (state.phase === "error" || !state.report) return <Banner tone="danger">{state.error ?? "No parts"}</Banner>;
  const flow = partFlow(state.report.parts);
  const stop = flow.find((f) => f.stops);
  return (
    <div className="debug-body">
      {stop && (
        <Banner tone="warn">
          Rows run out at <strong>{clauseName(stop.part.clause)}</strong>: everything after it is empty. Check its condition.
        </Banner>
      )}
      {state.report.timedOut && <Banner tone="warn">The time limit stopped the counts; parts not reached were not counted.</Banner>}
      <table className="debug-table">
        <thead>
          <tr>
            <th>Part</th>
            <th className="num">Rows</th>
            <th className="num">Change</th>
            <th className="num">Time</th>
          </tr>
        </thead>
        <tbody>
          {flow.map((f, i) => (
            <tr
              key={i}
              className={[f.stops ? "flag-warning" : "", f.part.clause === "result" ? "debug-result" : ""].join(" ")}
              onClick={() => f.part.clause !== "result" && onFlash(state.base, f.part.start, f.part.end)}
              title={f.part.clause === "result" ? f.part.sql : `Show in the editor\n\n${f.part.sql}`}
            >
              <td>
                <span className={`debug-clause clause-${f.part.clause}`}>{clauseName(f.part.clause)}</span>
                {f.part.clause !== "result" && <span className="mono debug-label">{partLabel(f.part)}</span>}
                {f.part.error && <span className="danger-text debug-error">{f.part.error}</span>}
              </td>
              <td className="num mono">{f.part.ran ? formatCount(f.part.rows) : "—"}</td>
              <td className={`num mono debug-change ${f.change ?? ""}`}>{changeText(f)}</td>
              <td className="num mono muted">{f.part.ran || f.part.error ? fmtMs(f.part.ms) : ""}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="muted small-text">{state.report.note}</p>
    </div>
  );
}

/** partLabel drops the clause keyword the badge already shows. */
function partLabel(p: DebugPart): string {
  const kw: Partial<Record<DebugPart["clause"], RegExp>> = {
    join: /^JOIN\s+/i,
    where: /^WHERE\s+/i,
    group: /^GROUP\s+BY\s+/i,
    having: /^HAVING\s+/i,
  };
  return p.label.replace(kw[p.clause] ?? /^$/, "");
}

function DryControls(p: Props) {
  const running = p.dry.phase === "running";
  return (
    <>
      <label className="debug-limit muted">
        Time limit
        <select value={p.limitMs} onChange={(e) => p.onLimit(Number(e.target.value))} disabled={running}>
          {DRY_RUN_LIMITS.map((l) => (
            <option key={l.ms} value={l.ms}>
              {l.label}
            </option>
          ))}
        </select>
      </label>
      {running ? (
        <button type="button" className="danger" onClick={p.onCancelDry}>
          <Icon name="stop" size={12} strokeWidth={2} /> Stop
        </button>
      ) : (
        <button type="button" className="primary" onClick={p.onDryRun} disabled={p.conn.readOnly}>
          <Icon name="play" size={12} strokeWidth={2} /> Dry run
        </button>
      )}
    </>
  );
}

function DryPanel({ conn, state, onFlash }: { conn: Connection; state: DryState; onFlash: Props["onFlash"] }) {
  if (state.phase === "idle") {
    return (
      <Empty icon="play">
        <p>Try a script and see what it would change, without keeping anything.</p>
        <p className="muted">
          The selection, or the whole editor, runs in one transaction that is always rolled back. Writes really run, so
          triggers fire and rows stay locked until the end{conn.env === "prod" ? "; on production you confirm first" : ""}.
          {conn.dialect === "mysql" && " MySQL commits schema changes at once, so they are refused."}
        </p>
        {conn.readOnly && <Banner tone="warn">This connection is read-only, so it cannot dry-run writes.</Banner>}
      </Empty>
    );
  }
  if (state.phase === "running") return <Waiting text="Dry run in progress…" />;
  if (state.phase === "error" || !state.result) return <Banner tone="danger">{state.error ?? "No result"}</Banner>;
  const res = state.result;
  return (
    <div className="debug-body">
      <div className="explain-note">
        <span className={`badge ${res.rolledBack ? "badge-ok" : "badge-error"}`}>{res.rolledBack ? "rolled back" : "check"}</span>
        <span className="muted">{res.note}</span>
        <span className="mono muted">
          {dryRunTotals(res)} · {fmtMs(res.ms)}
        </span>
      </div>
      <ol className="debug-steps">
        {res.statements.map((s, i) => (
          <DryStatement key={i} s={s} onFlash={() => onFlash(state.base, s.start, s.end)} />
        ))}
      </ol>
    </div>
  );
}

function DryStatement({ s, onFlash }: { s: DryRunStatement; onFlash: () => void }) {
  const status = s.error ? "error" : s.ran ? "ran" : "skipped";
  return (
    <li className={`debug-step ${status}`}>
      <StepHead sql={s.sql} kind={s.kind} status={status} onFlash={onFlash}>
        {s.ran && <span className="mono">{outcome(s.kind, s.rowsAffected, s.rowCount, s.truncated)}</span>}
        {(s.ran || s.error) && <span className="mono muted">{fmtMs(s.ms)}</span>}
      </StepHead>
      {s.error && <p className="danger-text debug-error">{s.error}</p>}
      {s.columns && s.columns.length > 0 && s.rows && s.rows.length > 0 && (
        <div className="debug-preview">
          <table>
            <thead>
              <tr>
                {s.columns.map((c, i) => (
                  <th key={i}>{c.name}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {s.rows.map((r, i) => (
                <tr key={i}>
                  {r.map((v, j) => (
                    <td key={j} className="mono">
                      {cell(v)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
          {s.truncated && <p className="muted small-text">First {s.rows.length} of {formatCount(s.rowCount)} rows</p>}
        </div>
      )}
    </li>
  );
}

function StepControls(p: Props) {
  const s = p.steps;
  const active = s.phase === "ready" || s.phase === "running";
  if (!active) {
    return (
      <button type="button" className="primary" onClick={p.onStartSteps} disabled={s.phase === "starting"}>
        <Icon name="step" size={14} /> Start
      </button>
    );
  }
  const busy = s.phase === "running";
  return (
    <>
      <button type="button" className="primary" onClick={p.onStep} disabled={busy || s.at < 0}>
        <Icon name="step" size={14} /> Next
      </button>
      <button type="button" onClick={p.onStepAll} disabled={busy || s.at < 0}>
        <Icon name="play" size={12} strokeWidth={2} /> Run the rest
      </button>
    </>
  );
}

function StepsPanel({ state, onShowRows, onFlash }: { state: StepsState; onShowRows: Props["onShowRows"]; onFlash: Props["onFlash"] }) {
  if (state.phase === "starting") return <Waiting text="Starting…" />;
  if (state.phase === "idle" || (state.phase === "error" && state.steps.length === 0)) {
    return (
      <Empty icon="step">
        <p>Run a script one statement at a time and look at the data between steps.</p>
        <p className="muted">
          Start opens a transaction (or uses the open one), then Next runs each statement in it. Run queries from the editor
          to look around; they see the changes so far. When you are done, Commit keeps everything and Rollback keeps nothing.
        </p>
        {state.error && <Banner tone="danger">{state.error}</Banner>}
      </Empty>
    );
  }
  const done = state.results.filter(Boolean).length;
  const failed = state.results.some((r) => r?.error);
  return (
    <div className="debug-body">
      {state.phase === "ended" ? (
        <Banner tone="ok">The transaction ended, so stepping stopped. Start again to step through once more.</Banner>
      ) : (
        <div className="explain-note">
          <span className="badge badge-info">in a transaction</span>
          <span className="muted">
            {done} of {state.steps.filter((s) => !s.control).length} run.{" "}
            {state.at < 0 ? "All statements ran. Commit above keeps them; Rollback keeps nothing." : "Nothing is kept until you commit."}
          </span>
        </div>
      )}
      {state.error && <Banner tone="danger">{state.error}</Banner>}
      {failed && state.phase !== "ended" && (
        <Banner tone="warn">A statement failed. On PostgreSQL the transaction cannot go on: roll back and start again.</Banner>
      )}
      <ol className="debug-steps">
        {state.steps.map((step, i) => {
          const r = state.results[i];
          const status = step.control ? "skipped" : r?.error ? "error" : r ? "ran" : i === state.at ? (state.phase === "running" ? "running" : "next") : "waiting";
          return (
            <li key={i} className={`debug-step ${status}`}>
              <StepHead sql={step.sql} kind={step.kind} status={status} onFlash={() => onFlash(state.base, step.start, step.end)}>
                {step.control && <span className="muted small-text">skipped: the stepping has its own transaction</span>}
                {r?.summary && !r.error && (
                  <span className="mono">{outcome(r.summary.kind, r.summary.rowsAffected, r.summary.rowCount, r.summary.truncated)}</span>
                )}
                {r?.summary && <span className="mono muted">{fmtMs(r.summary.durationMs)}</span>}
                {r?.summary && !r.error && r.summary.kind === "read" && r.summary.rowCount > 0 && (
                  <button type="button" className="link" onClick={() => onShowRows(step.sql)}>
                    Show rows
                  </button>
                )}
              </StepHead>
              {r?.error && <p className="danger-text debug-error">{r.error}</p>}
            </li>
          );
        })}
      </ol>
    </div>
  );
}

const STATUS_ICON = { ran: "check", error: "alert", running: "pulse", next: "step" } as const;

function StepHead(props: { sql: string; kind: string; status: string; onFlash: () => void; children?: ReactNode }) {
  const icon = STATUS_ICON[props.status as keyof typeof STATUS_ICON];
  return (
    <div className="debug-step-head">
      <span className="debug-step-mark" aria-label={props.status}>
        {icon ? <Icon name={icon} size={13} strokeWidth={2} /> : null}
      </span>
      <button type="button" className="debug-sql mono" onClick={props.onFlash} title="Show in the editor">
        {oneLine(props.sql)}
      </button>
      <KindBadge kind={props.kind} />
      {props.children}
    </div>
  );
}

function outcome(kind: string, affected: number, count: number, truncated: boolean): string {
  if (kind === "read") return `${formatCount(count)}${truncated ? "+" : ""} ${count === 1 ? "row" : "rows"}`;
  if (affected >= 0) return `${formatCount(affected)} ${affected === 1 ? "row" : "rows"} changed`;
  return count > 0 ? `${formatCount(count)} rows returned` : "done";
}

function oneLine(sql: string): string {
  const s = sql.replace(/\s+/g, " ").trim();
  return s.length > 160 ? `${s.slice(0, 160)}…` : s;
}

function cell(v: Cell): string {
  if (v === null) return "NULL";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

function fmtMs(ms: number): string {
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`;
  if (ms >= 100) return `${Math.round(ms)} ms`;
  return `${ms.toFixed(ms >= 10 ? 1 : 2)} ms`;
}
