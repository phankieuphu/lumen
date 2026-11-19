import { describe, expect, it } from "vitest";

import type { ExplainPlan, ExplainNode } from "../api";
import { planRows, stepText } from "./plan";

const node = (id: number, op: string, extra: Partial<ExplainNode> = {}): ExplainNode => ({
  id, op, kind: "other", estRows: 0, estCost: 0, rows: 0, loops: 1, timeMs: 0, rowsRemoved: 0, ...extra,
});

const plan = (root: ExplainNode, analyzed: boolean): ExplainPlan => ({
  dialect: "postgres", analyzed, estimated: true, note: "", root, planningMs: 0, executionMs: 0, findings: [], steps: [], raw: "", truncated: false,
});

describe("planRows", () => {
  // Sort (10 ms in all) over a scan that ran 4 loops of 2 ms each.
  const tree = node(0, "Sort", {
    timeMs: 10, estCost: 100, rows: 50, estRows: 40,
    children: [node(1, "Seq Scan", { timeMs: 2, loops: 4, estCost: 60, rows: 25, estRows: 30 })],
  });

  it("splits time between a step and its children when the plan ran", () => {
    const rows = planRows(plan(tree, true));
    expect(rows.map((r) => [r.node.op, r.depth, r.rows, r.selfMs, r.sharePct])).toEqual([
      ["Sort", 0, 50, 2, 20],
      ["Seq Scan", 1, 100, 8, 80],
    ]);
  });

  it("uses estimates and cost when the plan did not run", () => {
    const rows = planRows(plan(tree, false));
    expect(rows.map((r) => [r.rows, r.selfMs, r.sharePct])).toEqual([
      [40, 0, 40],
      [30, 0, 60],
    ]);
  });

  it("handles an empty plan", () => {
    expect(planRows({ ...plan(tree, true), root: null })).toEqual([]);
  });
});

describe("stepText", () => {
  it.each([
    ["1. Read every row of t.", "Read every row of t."],
    ["12. Sorted.", "Sorted."],
    ["Finished in 3 ms.", "Finished in 3 ms."],
  ])("%s", (input, want) => {
    expect(stepText(input)).toBe(want);
  });
});
