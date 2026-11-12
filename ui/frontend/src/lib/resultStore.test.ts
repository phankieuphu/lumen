import { describe, expect, it } from "vitest";

import type { Page } from "../api/types";
import { compareCells, ResultStore } from "./resultStore";

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

  describe("sort", () => {
    const rowsPage = (rows: unknown[][]): Page => ({ requestId: "r", seq: 0, rows });
    const col0 = (s: ResultStore) => Array.from({ length: s.rowCount }, (_, i) => s.row(i)?.[0]);

    it("orders by a column, nulls last both ways, and restores the original order", () => {
      const s = new ResultStore(manual().schedule);
      s.append(rowsPage([[3], [null], [1], [2]]));
      s.setSort({ column: 0, dir: "asc" });
      expect(col0(s)).toEqual([1, 2, 3, null]);
      s.setSort({ column: 0, dir: "desc" });
      expect(col0(s)).toEqual([3, 2, 1, null]);
      s.setSort(undefined);
      expect(col0(s)).toEqual([3, null, 1, 2]);
    });

    it("sorts rows that arrive later and is dropped by clear", () => {
      const s = new ResultStore(manual().schedule);
      s.append(rowsPage([["b"], ["d"]]));
      s.setSort({ column: 0, dir: "asc" });
      expect(col0(s)).toEqual(["b", "d"]);
      s.append(rowsPage([["a"], ["c"]]));
      expect(col0(s)).toEqual(["a", "b", "c", "d"]);
      s.clear();
      expect(s.sort).toBeUndefined();
    });

    it("keeps ties in result order", () => {
      const s = new ResultStore(manual().schedule);
      s.append(rowsPage([[1, "x"], [0, "y"], [1, "z"]]));
      s.setSort({ column: 0, dir: "desc" });
      expect(Array.from({ length: 3 }, (_, i) => s.row(i)?.[1])).toEqual(["x", "z", "y"]);
    });
  });
});

describe("compareCells", () => {
  it.each<{ name: string; a: unknown; b: unknown; want: number }>([
    { name: "numbers numerically", a: 9, b: 10, want: -1 },
    { name: "bigint with number", a: 10n, b: 9, want: 1 },
    { name: "booleans false first", a: false, b: true, want: -1 },
    { name: "text in natural order", a: "item2", b: "item10", want: -1 },
    { name: "text ignoring case", a: "abc", b: "ABC", want: 0 },
    { name: "equal numbers", a: 5, b: 5, want: 0 },
  ])("$name", ({ a, b, want }) => {
    expect(Math.sign(compareCells(a, b))).toBe(want);
  });
});
