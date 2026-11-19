import type { StatementRange } from "../api";

/**
 * statementAt picks the statement to run for a cursor offset: the one the
 * cursor is in (its trailing semicolon counts), else the nearest one before
 * the cursor, else the first.
 */
export function statementAt(ranges: readonly StatementRange[], cursor: number): StatementRange | undefined {
  let before: StatementRange | undefined;
  for (const r of ranges) {
    if (cursor >= r.from && cursor <= r.to) return r;
    if (r.to <= cursor) before = r;
  }
  return before ?? ranges[0];
}

/**
 * splitStatements is a rough splitter for the mock backend: top-level
 * semicolons, skipping quotes and comments. The real one is in Go.
 */
export function splitStatements(sql: string): StatementRange[] {
  const out: StatementRange[] = [];
  let start = 0;
  const flush = (end: number) => {
    const text = sql.slice(start, end);
    const lead = text.length - text.trimStart().length;
    const body = text.trim();
    if (body && !/^(--[^\n]*\s*|\/\*[\s\S]*?\*\/\s*)+$/.test(body)) {
      out.push({ from: start + lead, to: start + lead + body.length });
    }
  };
  for (let i = 0; i < sql.length; i++) {
    const c = sql[i];
    if (c === "'" || c === '"' || c === "`") {
      const close = sql.indexOf(c, i + 1);
      i = close < 0 ? sql.length : close;
    } else if (c === "-" && sql[i + 1] === "-") {
      const nl = sql.indexOf("\n", i);
      i = nl < 0 ? sql.length : nl;
    } else if (c === "/" && sql[i + 1] === "*") {
      const close = sql.indexOf("*/", i + 2);
      i = close < 0 ? sql.length : close + 1;
    } else if (c === ";") {
      flush(i);
      start = i + 1;
    }
  }
  flush(sql.length);
  return out;
}
