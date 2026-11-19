import { describe, expect, it } from "vitest";

import { cellText, connTarget, copyValue, dialectName, formatDuration, MAX_CELL_CHARS, oneLine, outcomeTitle, rowToTSV, statementVerb, txHue, txTagText } from "./format";

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

describe("transaction tags", () => {
  it("colour is stable and in range", () => {
    expect(txHue("tx-0a1b2c3d4e5f")).toBe(txHue("tx-0a1b2c3d4e5f"));
    for (const id of ["tx-1", "tx-ffffffffffff", ""]) {
      expect(txHue(id)).toBeGreaterThanOrEqual(0);
      expect(txHue(id)).toBeLessThan(360);
    }
  });
  it("text prefers the label", () => {
    expect(txTagText("tx-0a1b2c3d4e5f", "fix order 42")).toBe("fix order 42");
    expect(txTagText("tx-0a1b2c3d4e5f")).toBe("0a1b2c");
  });
});

describe("statementVerb", () => {
  it.each([
    { name: "plain", in: "update t set a = 1", want: "UPDATE" },
    { name: "object kind", in: "create table t (id int)", want: "CREATE TABLE" },
    { name: "skips comments", in: "-- fix\n/* why */ delete from t", want: "DELETE" },
    { name: "empty", in: "  ", want: "" },
    { name: "not a keyword", in: "(select 1)", want: "" },
  ])("$name", ({ in: sql, want }) => {
    expect(statementVerb(sql)).toBe(want);
  });
});

describe("outcomeTitle", () => {
  const ok = { status: "ok", kind: "write", rowCount: 0, rowsAffected: 3 };
  it.each([
    { name: "rows changed", s: ok, sql: "update t set a = 1", want: "Updated 3 rows" },
    { name: "one row", s: { ...ok, rowsAffected: 1 }, sql: "insert into t values (1)", want: "Inserted 1 row" },
    { name: "unknown count", s: { ...ok, rowsAffected: -1 }, sql: "delete from t", want: "DELETE completed" },
    { name: "schema", s: { ...ok, kind: "schema", rowsAffected: 0 }, sql: "create index i on t (a)", want: "CREATE INDEX completed" },
    { name: "read without rows", s: { ...ok, kind: "read" }, sql: "select 1 where false", want: "No rows returned" },
    { name: "commit", s: { ...ok, kind: "session", txEnd: "commit" }, sql: "commit", want: "Transaction committed" },
    { name: "error", s: { ...ok, status: "error" }, sql: "update t", want: "Statement failed" },
    { name: "blocked", s: { ...ok, status: "blocked" }, sql: "drop table t", want: "Statement blocked" },
  ])("$name", ({ s, sql, want }) => {
    expect(outcomeTitle(s, sql)).toBe(want);
  });
});
