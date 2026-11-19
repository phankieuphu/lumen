import { useEffect, useRef, useState } from "react";

import type { ExplainNode, ExplainPlan, PlanFinding } from "../api";
import { formatCount } from "../lib/format";
import { planRows, stepText } from "../lib/plan";
import { Icon } from "./icons";
import { Banner } from "./ui";

export interface ExplainState {
  phase: "idle" | "running" | "done" | "error";
  plan?: ExplainPlan;
  error?: string;
}

interface Props {
  state: ExplainState;
  /** The keys that explain, shown in the empty state. */
  keys: string;
}

/**
 * ExplainView shows how the database runs a statement: what is worth fixing,
 * the plan as a table, how it ran step by step, and the raw output.
 */
export function ExplainView({ state, keys }: Props) {
  if (state.phase === "idle") {
    return (
      <div className="explain-empty">
        <Icon name="plan" size={22} />
        <p>Explain shows how the database runs a statement and what makes it slow.</p>
        <p className="muted">
          Put the cursor in a statement and press Explain (<kbd>{keys}</kbd>). Reads run for real numbers in a transaction that
          is rolled back; writes are planned, not run.
        </p>
      </div>
    );
  }
  if (state.phase === "running") {
    return (
      <div className="result-wait">
        <span className="spinner large" aria-hidden="true" />
        <span>Explaining…</span>
      </div>
    );
  }
  if (state.phase === "error" || !state.plan) {
    return (
      <div className="explain">
        <Banner tone="danger">{state.error ?? "No plan"}</Banner>
      </div>
    );
  }
  return <PlanView plan={state.plan} />;
}

function PlanView({ plan }: { plan: ExplainPlan }) {
  const [selected, setSelected] = useState<number>();
  useEffect(() => setSelected(undefined), [plan]);
  const rows = planRows(plan);
  const flagged = new Map<number, PlanFinding["severity"]>();
  for (const f of plan.findings) if (flagged.get(f.nodeId) !== "warning") flagged.set(f.nodeId, f.severity);
  const steps = plan.steps.filter((s) => /^\d+\./.test(s));
  const finished = plan.steps.find((s) => !/^\d+\./.test(s));

  return (
    <div className="explain">
      <div className="explain-note">
        <span className={`badge ${plan.analyzed ? "badge-ok" : "badge-info"}`}>{plan.analyzed ? "ran" : "estimated"}</span>
        <span className="muted">{plan.note}</span>
        {plan.analyzed && plan.executionMs > 0 && (
          <span className="mono muted">
            {fmtMs(plan.executionMs)} · planning {fmtMs(plan.planningMs)}
          </span>
        )}
      </div>
      {plan.truncated && <Banner tone="warn">The plan was too large and is cut short.</Banner>}
      <div className="explain-layout">
        <section className="card explain-plan" aria-label="Plan">
          {rows.length === 0 ? (
            <p className="muted pad">The database reported no plan steps for this statement.</p>
          ) : (
            <PlanTable rows={rows} analyzed={plan.analyzed} estimated={plan.estimated} flagged={flagged} selected={selected} />
          )}
        </section>
        <aside className="explain-side">
          {plan.findings.length > 0 && (
            <section className="card" aria-label="Findings">
              <header className="card-head small plain">
                <h2>Worth a look</h2>
              </header>
              <ul className="explain-findings">
                {plan.findings.map((f, i) => (
                  <li key={i}>
                    <button
                      type="button"
                      className={`explain-finding ${f.severity}${selected === f.nodeId ? " selected" : ""}`}
                      onClick={() => setSelected(f.nodeId)}
                      title="Show this step in the plan"
                    >
                      <Icon name={f.severity === "warning" ? "alert" : "bolt"} size={14} strokeWidth={2} />
                      <span>
                        <strong>{f.title}</strong>
                        <span>{f.detail}</span>
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          )}
          {steps.length > 0 && (
            <section className="card" aria-label="How it ran">
              <header className="card-head small plain">
                <h2>How it ran</h2>
              </header>
              <ol className="explain-steps">
                {steps.map((s, i) => (
                  <li key={i}>{stepText(s)}</li>
                ))}
              </ol>
              {finished && <p className="explain-finished muted">{finished}</p>}
            </section>
          )}
          <details className="card explain-raw">
            <summary>Raw output</summary>
            <pre>{plan.raw}</pre>
          </details>
        </aside>
      </div>
    </div>
  );
}

interface TableProps {
  rows: ReturnType<typeof planRows>;
  analyzed: boolean;
  estimated: boolean;
  flagged: Map<number, PlanFinding["severity"]>;
  selected?: number;
}

function PlanTable({ rows, analyzed, estimated, flagged, selected }: TableProps) {
  const selectedRow = useRef<HTMLTableRowElement>(null);
  // Braces: some browsers return a Promise from scrollIntoView, which an
  // effect must not return.
  useEffect(() => {
    selectedRow.current?.scrollIntoView({ block: "nearest" });
  }, [selected]);
  return (
    <table className="plan-table">
      <thead>
        <tr>
          <th>Step</th>
          {analyzed && <th className="num">Rows</th>}
          {estimated && <th className="num">{analyzed ? "Planned" : "Rows (est.)"}</th>}
          {analyzed && <th className="num">Loops</th>}
          {(analyzed || estimated) && <th>{analyzed ? "Own time" : "Own cost"}</th>}
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => {
          const n = r.node;
          const flag = flagged.get(n.id);
          return (
            <tr
              key={n.id}
              ref={selected === n.id ? selectedRow : undefined}
              className={[flag ? `flag-${flag}` : "", selected === n.id ? "selected" : "", n.neverRan ? "never" : ""].join(" ")}
            >
              <td>
                <div className="plan-step" style={{ paddingLeft: r.depth * 18 }}>
                  {r.depth > 0 && <span className="plan-branch" aria-hidden="true">└</span>}
                  <span className="plan-op">
                    <span className={`plan-kind kind-${n.kind}`}>{label(n)}</span>
                    {n.detail && <span className="plan-detail mono">{n.detail}</span>}
                    {n.spilled && <span className="badge badge-warn">disk</span>}
                    {n.neverRan && <span className="muted small-text">never needed</span>}
                  </span>
                </div>
              </td>
              {analyzed && (
                <td className="num mono">
                  {formatCount(Math.round(r.rows))}
                  {n.rowsRemoved > 0 && (
                    <span className="plan-removed" title="Thrown away by the filter">
                      −{formatCount(Math.round(n.rowsRemoved * Math.max(n.loops, 1)))}
                    </span>
                  )}
                </td>
              )}
              {estimated && <td className="num mono muted">{formatCount(Math.round(n.estRows))}</td>}
              {analyzed && <td className="num mono muted">{n.loops > 1 ? formatCount(n.loops) : ""}</td>}
              {(analyzed || estimated) && (
                <td>
                  <div className="plan-share" title={`${r.sharePct}% of the whole plan`}>
                    <span className="plan-bar" style={{ width: `${r.sharePct}%` }} />
                    <span className="mono">{analyzed ? fmtMs(r.selfMs) : `${r.sharePct}%`}</span>
                  </div>
                </td>
              )}
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function label(n: ExplainNode): string {
  let s = n.op;
  if (n.relation && !s.includes(` on ${n.relation}`)) s += ` on ${n.relation}`;
  if (n.index && !s.includes(n.index)) s += ` using ${n.index}`;
  return s;
}

function fmtMs(ms: number): string {
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`;
  if (ms >= 100) return `${Math.round(ms)} ms`;
  return `${ms.toFixed(ms >= 10 ? 1 : 2)} ms`;
}
