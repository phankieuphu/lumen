// Display helpers shared by the result grid, history, and audit screens.

/** Longest text a grid cell renders; the detail view shows the full value. */
export const MAX_CELL_CHARS = 300;

/** cellText renders a result value for display. null is handled by the caller. */
export function cellText(value: unknown): string {
  let s: string;
  switch (typeof value) {
    case "string":
      s = value;
      break;
    case "number":
    case "bigint":
    case "boolean":
      return String(value);
    default:
      s = JSON.stringify(value) ?? "";
  }
  return s.length > MAX_CELL_CHARS ? `${s.slice(0, MAX_CELL_CHARS)}…` : s;
}

export function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(2)} s`;
  const m = Math.floor(ms / 60_000);
  return `${m} m ${Math.round((ms % 60_000) / 1000)} s`;
}

const timeFmt = new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "medium" });

export function formatTime(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : timeFmt.format(d);
}

const countFmt = new Intl.NumberFormat();

export const formatCount = (n: number): string => countFmt.format(n);

/** oneLine collapses whitespace so SQL fits a single grid row. */
export function oneLine(sql: string, max = MAX_CELL_CHARS): string {
  const s = sql.replace(/\s+/g, " ").trim();
  return s.length > max ? `${s.slice(0, max)}…` : s;
}

/** connTarget is where a connection points: host/database, or the SQLite file. */
export function connTarget(c: { dialect: string; host: string; database: string }): string {
  if (c.dialect === "sqlite") return c.database;
  return c.database ? `${c.host}/${c.database}` : c.host;
}

const dialectNames: Record<string, string> = { postgres: "PostgreSQL", mysql: "MySQL", sqlite: "SQLite" };

/** dialectName is the product name shown for a dialect id. */
export const dialectName = (d: string): string => dialectNames[d] ?? d;

/** copyValue is a result value as copied text: full length, NULL for null. */
export function copyValue(value: unknown): string {
  if (value === null || value === undefined) return "NULL";
  switch (typeof value) {
    case "string":
      return value;
    case "number":
    case "bigint":
    case "boolean":
      return String(value);
    default:
      return JSON.stringify(value) ?? "";
  }
}

/**
 * rowToTSV is a result row as one tab-separated line that pastes into a
 * spreadsheet: null is an empty field, and a field holding a tab or line
 * break, or starting with a quote, is quoted with inner quotes doubled.
 * Spreadsheets treat a quote as special only at the start of a field, so
 * JSON and other quoted text elsewhere pass through unchanged.
 */
export function rowToTSV(values: readonly unknown[]): string {
  return values
    .map((v) => {
      if (v === null || v === undefined) return "";
      const s = copyValue(v);
      return /[\t\n\r]/.test(s) || s.startsWith('"') ? `"${s.replace(/"/g, '""')}"` : s;
    })
    .join("\t");
}

/** txHue gives a transaction a stable colour, so its tagged rows match. */
export function txHue(txId: string): number {
  let h = 0;
  for (let i = 0; i < txId.length; i++) h = (h * 31 + txId.charCodeAt(i)) >>> 0;
  return h % 360;
}

/** txTagText is the label, or a short form of the id when there is none. */
export const txTagText = (txId: string, label?: string) => label || txId.replace(/^tx-/, "").slice(0, 6);
