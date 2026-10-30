import { describe, expect, it } from "vitest";

import { columnWidths, MAX_COL_PX, MIN_COL_PX, visibleRange } from "./grid";

type Args = Parameters<typeof visibleRange>;

describe("visibleRange", () => {
  it.each<{ name: string; args: Args; want: { start: number; end: number } }>([
    { name: "empty result renders nothing", args: [0, 500, 25, 0, 5], want: { start: 0, end: 0 } },
    { name: "top of list adds overscan below only", args: [0, 100, 25, 1000, 2], want: { start: 0, end: 7 } },
    { name: "middle adds overscan on both sides", args: [2500, 100, 25, 1000, 2], want: { start: 98, end: 107 } },
    { name: "end is clamped to row count", args: [24_900, 100, 25, 1000, 2], want: { start: 994, end: 1000 } },
    { name: "scroll past the end keeps the last rows", args: [99_999, 100, 25, 10, 2], want: { start: 7, end: 10 } },
    { name: "negative scroll (overscroll bounce) is treated as top", args: [-40, 100, 25, 1000, 0], want: { start: 0, end: 5 } },
  ])("$name", ({ args, want }) => {
    expect(visibleRange(...args)).toEqual(want);
  });

  it("renders a window independent of row count", () => {
    const { start, end } = visibleRange(1_000_000, 800, 26, 100_000, 8);
    expect(end - start).toBeLessThanOrEqual(Math.ceil(800 / 26) + 2 * 8 + 1);
  });
});

describe("columnWidths", () => {
  it("fits the widest of header and sampled values within bounds", () => {
    const [id, name, blob] = columnWidths(["id", "name", "blob"], [["1", "alice", "x".repeat(500)]]);
    expect(id).toBe(MIN_COL_PX);
    expect(name).toBeGreaterThan(MIN_COL_PX - 1);
    expect(blob).toBe(MAX_COL_PX);
  });

  it("handles rows shorter than the header", () => {
    expect(columnWidths(["a", "b"], [["1"]])).toHaveLength(2);
  });
});
