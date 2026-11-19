import type { ExplainPlan, ExplainNode } from "../api";

/** PlanRow is one line of the plan table. */
export interface PlanRow {
  node: ExplainNode;
  depth: number;
  /** Rows over every loop, or the estimate when the plan did not run. */
  rows: number;
  /** Time spent in this step alone, without its children, over every loop. */
  selfMs: number;
  /** This step's own share of the whole plan, 0–100: time when it ran, else cost. */
  sharePct: number;
}

const total = (n: ExplainNode) => n.timeMs * Math.max(n.loops, 1);

/** planRows flattens the plan tree in preorder, the order the table shows. */
export function planRows(plan: ExplainPlan): PlanRow[] {
  const root = plan.root;
  if (!root) return [];
  const whole = plan.analyzed ? total(root) : root.estCost;
  const out: PlanRow[] = [];
  const visit = (n: ExplainNode, depth: number) => {
    const kids = n.children ?? [];
    const selfMs = plan.analyzed ? Math.max(total(n) - kids.reduce((s, c) => s + total(c), 0), 0) : 0;
    const selfCost = Math.max(n.estCost - kids.reduce((s, c) => s + c.estCost, 0), 0);
    const self = plan.analyzed ? selfMs : selfCost;
    out.push({
      node: n,
      depth,
      rows: plan.analyzed ? n.rows * Math.max(n.loops, 1) : n.estRows,
      selfMs,
      sharePct: whole > 0 ? Math.min(100, Math.round((100 * self) / whole)) : 0,
    });
    for (const c of kids) visit(c, depth + 1);
  };
  visit(root, 0);
  return out;
}

/** stepText drops the "3. " that numbers a step; the list numbers them. */
export const stepText = (s: string) => s.replace(/^\d+\.\s+/, "");
