import { describe, expect, it } from "vitest";

import type { Page } from "../api/types";
import { ResultStore } from "./resultStore";

const page = (seq: number, rows: number, withColumns = false): Page => ({
  requestId: "r",
  seq,
  rows: Array.from({ length: rows }, (_, i) => [seq * 1000 + i]),
  ...(withColumns ? { columns: [{ name: "id", type: "int8", nullable: false }] } : {}),
});

/** manual collects scheduled callbacks so tests decide when a frame runs. */
function manual() {
  const queue: (() => void)[] = [];
  return { schedule: (fn: () => void) => queue.push(fn), flush: () => queue.splice(0).forEach((fn) => fn()) };
}

describe("ResultStore", () => {
  it("appends pages in order and keeps first-page columns", () => {
    const s = new ResultStore(manual().schedule);
    s.append(page(0, 3, true));
    s.append(page(1, 2));
    expect(s.rowCount).toBe(5);
    expect(s.columns.map((c) => c.name)).toEqual(["id"]);
    expect(s.row(3)).toEqual([1000]);
    expect(s.row(99)).toBeUndefined();
  });

  it("notifies once per frame however many pages arrive", () => {
    const m = manual();
    const s = new ResultStore(m.schedule);
    let calls = 0;
    s.subscribe(() => calls++);
    const v0 = s.getVersion();
    for (let i = 0; i < 10; i++) s.append(page(i, 1));
    expect(calls).toBe(0);
    expect(s.getVersion()).toBeGreaterThan(v0);
    m.flush();
    expect(calls).toBe(1);
    s.append(page(10, 1));
    m.flush();
    expect(calls).toBe(2);
  });

  it("clear drops rows and columns and stops notifying unsubscribed listeners", () => {
    const m = manual();
    const s = new ResultStore(m.schedule);
    let calls = 0;
    const off = s.subscribe(() => calls++);
    s.append(page(0, 2, true));
    off();
    s.clear();
    m.flush();
    expect(s.rowCount).toBe(0);
    expect(s.columns).toEqual([]);
    expect(calls).toBe(0);
  });

  it("accepts pages larger than the spread-argument limit", () => {
    const s = new ResultStore(manual().schedule);
    s.append(page(0, 200_000));
    expect(s.rowCount).toBe(200_000);
  });
});
