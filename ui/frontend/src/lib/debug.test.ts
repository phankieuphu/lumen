import { describe, expect, it } from "vitest";

import type { DebugPart, DebugStep, DryRunResult } from "../api";
import { changeText, dryRunTotals, nextStep, partFlow } from "./debug";

const part = (clause: DebugPart["clause"], rows: number, ran = true): DebugPart => ({
  clause, label: clause, start: 0, end: 0, sql: "", rows, ran, ms: 1,
});

describe("partFlow", () => {
  it("compares each part with the one it builds on and marks where rows stop", () => {
    const flow = partFlow([part("cte", 7), part("from", 10), part("join", 30), part("where", 0), part("group", 0), part("result", 0)]);
    expect(flow.map((f) => f.change)).toEqual([undefined, undefined, "more", "fewer", "same", "same"]);
    expect(flow.map((f) => f.stops)).toEqual([false, false, false, true, false, false]);
    expect(changeText(flow[2]!)).toBe("×3.0");
    expect(changeText(flow[3]!)).toBe("−100%");
    expect(changeText(flow[4]!)).toBe("no change");
  });

  it("does not compare across a part that failed", () => {
    const flow = partFlow([part("from", 10), part("join", 0, false), part("where", 4)]);
    expect(flow.map((f) => f.change)).toEqual([undefined, undefined, undefined]);
  });

  it("leaves UNION sides and their result alone", () => {
    const flow = partFlow([part("branch", 5), part("branch", 10), part("result", 15)]);
    expect(flow.every((f) => f.change === undefined && !f.stops)).toBe(true);
  });

  it("keeps a small share from reading as all gone", () => {
    const [, f] = partFlow([part("from", 1000), part("where", 1)]);
    expect(changeText(f!)).toBe("−99%");
  });
});

describe("nextStep", () => {
  const step = (control: boolean): DebugStep => ({ start: 0, end: 0, sql: "", kind: "write", control, undoable: !control });
  it("skips transaction control", () => {
    const steps = [step(true), step(false), step(true)];
    expect(nextStep(steps, 0)).toBe(1);
    expect(nextStep(steps, 2)).toBe(-1);
  });
});

describe("dryRunTotals", () => {
  it("counts what ran and the rows writes changed", () => {
    const s = (kind: DebugStep["kind"], ran: boolean, rowsAffected: number) => ({
      start: 0, end: 0, sql: "", kind, control: false, undoable: true, ran, rowsAffected, rowCount: 0, truncated: false, ms: 0,
    });
    const res: DryRunResult = {
      statements: [s("write", true, 3), s("read", true, -1), s("write", true, 9), s("write", false, -1)],
      rolledBack: true, timedOut: false, cancelled: false, note: "", ms: 1,
    };
    expect(dryRunTotals(res)).toBe("3 of 4 ran · 12 rows changed");
  });
});
