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
