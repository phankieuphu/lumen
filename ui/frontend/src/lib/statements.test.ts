import { describe, expect, it } from "vitest";

import { splitStatements, statementAt } from "./statements";

describe("statementAt", () => {
  // "SELECT 1;\n\nUPDATE t SET a = 1;"
  const ranges = [
    { from: 0, to: 8 },
    { from: 11, to: 29 },
  ];
  it.each([
    { name: "inside the first", cursor: 3, want: 0 },
    { name: "at the start", cursor: 0, want: 0 },
    { name: "just after the semicolon", cursor: 9, want: 0 },
    { name: "on the blank line between", cursor: 10, want: 0 },
    { name: "inside the second", cursor: 15, want: 1 },
    { name: "at the very end", cursor: 30, want: 1 },
  ])("$name", ({ cursor, want }) => {
    expect(statementAt(ranges, cursor)).toBe(ranges[want]);
  });

  it("before every statement picks the first", () => {
    expect(statementAt([{ from: 5, to: 9 }], 1)).toEqual({ from: 5, to: 9 });
  });

  it("none", () => {
    expect(statementAt([], 0)).toBeUndefined();
  });
});

describe("splitStatements", () => {
  const texts = (sql: string) => splitStatements(sql).map((r) => sql.slice(r.from, r.to));
  it.each([
    { name: "two", sql: "SELECT 1; SELECT 2;", want: ["SELECT 1", "SELECT 2"] },
    { name: "semicolon in a string", sql: "SELECT ';'; SELECT 2", want: ["SELECT ';'", "SELECT 2"] },
    { name: "semicolon in a comment", sql: "SELECT 1 -- ;\n; SELECT 2", want: ["SELECT 1 -- ;", "SELECT 2"] },
    { name: "comment-only dropped", sql: "SELECT 1; -- done", want: ["SELECT 1"] },
  ])("$name", ({ sql, want }) => {
    expect(texts(sql)).toEqual(want);
  });
});
