import type { DebugPart, DebugStep, DryRunResult } from "../api";
import { formatCount } from "./format";

/** The clauses that build on the part before them, in order. */
const CHAIN = new Set<DebugPart["clause"]>(["from", "join", "where", "group", "having", "result"]);

export interface PartFlow {
  part: DebugPart;
  /** Rows compared with the part this one builds on; undefined if none. */
  change?: "more" | "fewer" | "same";
  /** The change as a factor (2 = twice as many) or a share kept (0.25). */
  ratio?: number;
  /** Set on the first part where rows run out: everything after is empty. */
  stops: boolean;
}

/**
 * partFlow compares each part with the one it builds on, so the tab can say
 * where a join multiplies rows or a filter throws them all away. WITH
 * queries and UNION sides stand alone.
 */
export function partFlow(parts: DebugPart[]): PartFlow[] {
  const branches = parts.some((p) => p.clause === "branch");
  let prev: DebugPart | undefined;
  let stopped = false;
  return parts.map((part) => {
    const flow: PartFlow = { part, stops: false };
    const chained = CHAIN.has(part.clause) && !(branches && part.clause === "result");
    if (!chained || !part.ran) {
      if (chained) prev = undefined;
      return flow;
    }
    if (prev?.ran) {
      const a = prev.rows;
      const b = part.rows;
      flow.change = b > a ? "more" : b < a ? "fewer" : "same";
      if (a > 0 && b !== a) flow.ratio = b / a;
      if (!stopped && a > 0 && b === 0) flow.stops = stopped = true;
    }
    prev = part;
    return flow;
  });
}

/** changeText says how a part changed the rows, e.g. "×2.4" or "−75%". */
export function changeText(f: PartFlow): string {
  if (!f.change || f.change === "same" || f.ratio === undefined) return f.change === "same" ? "no change" : "";
  if (f.change === "more") return `×${f.ratio >= 10 ? Math.round(f.ratio) : f.ratio.toFixed(1)}`;
  const pct = Math.round((1 - f.ratio) * 100);
  return `−${pct === 100 && f.part.rows > 0 ? 99 : pct}%`;
}

/** clauseName is a part's clause as the tab shows it. */
export function clauseName(c: DebugPart["clause"]): string {
  switch (c) {
    case "cte":
      return "WITH";
    case "from":
      return "FROM";
    case "join":
      return "JOIN";
    case "where":
      return "WHERE";
    case "group":
      return "GROUP BY";
    case "having":
      return "HAVING";
    case "branch":
      return "UNION side";
    case "result":
      return "Result";
  }
}

/** nextStep is the index of the next step to run at or after from, skipping transaction control; -1 at the end. */
export function nextStep(steps: DebugStep[], from: number): number {
  for (let i = from; i < steps.length; i++) if (!steps[i]?.control) return i;
  return -1;
}

/** dryRunTotals sums what a dry run did, e.g. "3 of 3 ran · 12 rows changed". */
export function dryRunTotals(res: DryRunResult): string {
  const ran = res.statements.filter((s) => s.ran).length;
  const changed = res.statements.reduce((n, s) => n + (s.ran && s.kind !== "read" && s.rowsAffected > 0 ? s.rowsAffected : 0), 0);
  const parts = [`${ran} of ${res.statements.length} ran`];
  if (changed > 0) parts.push(`${formatCount(changed)} ${changed === 1 ? "row" : "rows"} changed`);
  return parts.join(" · ");
}
