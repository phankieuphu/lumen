import { describe, expect, it } from "vitest";

import { cellText, formatDuration, MAX_CELL_CHARS, oneLine } from "./format";

describe("cellText", () => {
  it.each([
    { name: "string as is", in: "abc", want: "abc" },
    { name: "number", in: 1.5, want: "1.5" },
    { name: "boolean", in: false, want: "false" },
    { name: "object as JSON", in: { a: [1, 2] }, want: '{"a":[1,2]}' },
  ])("$name", ({ in: v, want }) => {
    expect(cellText(v)).toBe(want);
  });

  it("truncates long values", () => {
    const out = cellText("x".repeat(MAX_CELL_CHARS * 3));
    expect(out).toHaveLength(MAX_CELL_CHARS + 1);
    expect(out.endsWith("…")).toBe(true);
  });
});

describe("formatDuration", () => {
  it.each([
    [0, "0 ms"],
    [999, "999 ms"],
    [1500, "1.50 s"],
    [125_000, "2 m 5 s"],
  ])("%d ms → %s", (ms, want) => {
    expect(formatDuration(ms)).toBe(want);
  });
});

describe("oneLine", () => {
  it("collapses whitespace and newlines", () => {
    expect(oneLine("SELECT *\n  FROM t\n\tWHERE x = 1")).toBe("SELECT * FROM t WHERE x = 1");
  });
});
