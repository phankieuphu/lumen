import { describe, expect, it } from "vitest";

import { cellText, connTarget, copyValue, dialectName, formatDuration, MAX_CELL_CHARS, oneLine, rowToTSV } from "./format";

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

describe("connTarget", () => {
  it.each([
    [{ dialect: "postgres", host: "db", database: "shop" }, "db/shop"],
    [{ dialect: "mysql", host: "db", database: "" }, "db"],
    [{ dialect: "sqlite", host: "", database: "/data/app.db" }, "/data/app.db"],
  ])("%o → %s", (c, want) => expect(connTarget(c)).toBe(want));
});

describe("dialectName", () => {
  it("names known dialects and passes others through", () => {
    expect(dialectName("postgres")).toBe("PostgreSQL");
    expect(dialectName("oracle")).toBe("oracle");
  });
});

describe("copyValue", () => {
  it.each<[unknown, string]>([
    [null, "NULL"],
    ["text", "text"],
    [42, "42"],
    [true, "true"],
    [{ tier: "gold" }, '{"tier":"gold"}'],
    ["x".repeat(500), "x".repeat(500)],
  ])("%o → %s", (v, want) => expect(copyValue(v)).toBe(want));
});

describe("rowToTSV", () => {
  it("joins with tabs, leaves null empty, and quotes awkward fields", () => {
    expect(rowToTSV([1, "a@b.com", null, { k: 1 }])).toBe('1\ta@b.com\t\t{"k":1}');
    expect(rowToTSV(["tab\there", 'say "hi"', "two\nlines", '"quoted"'])).toBe(
      '"tab\there"\tsay "hi"\t"two\nlines"\t"""quoted"""',
    );
  });
});
