import type { Column, Page, Row } from "../api/types";

export type SortDir = "asc" | "desc";

/** Sort is a client-side ordering of the loaded rows by one column. */
export interface Sort {
  column: number;
  dir: SortDir;
}

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });

/**
 * compareCells orders two result values: numbers and bigints numerically,
 * booleans false first, everything else as text with natural number order.
 * null sorts last in either direction, so it is handled by the caller.
 */
export function compareCells(a: unknown, b: unknown): number {
  const numeric = (v: unknown) => typeof v === "number" || typeof v === "bigint";
  if (numeric(a) && numeric(b)) return a! < b! ? -1 : a! > b! ? 1 : 0;
  if (typeof a === "boolean" && typeof b === "boolean") return Number(a) - Number(b);
  const text = (v: unknown) => (typeof v === "string" ? v : (JSON.stringify(v) ?? ""));
  return collator.compare(text(a), text(b));
}

const isNull = (v: unknown) => v === null || v === undefined;

/**
 * ResultStore holds the rows of one query result outside React state, so a
 * page arriving does not copy the rows seen so far. Subscribers are told at
 * most once per animation frame, which keeps a fast stream of pages from
 * re-rendering the grid for each one.
 *
 * It implements the useSyncExternalStore contract: getVersion changes every
 * time the contents change.
 */
export class ResultStore {
  columns: Column[] = [];
  private rows: Row[] = [];
  private sortBy: Sort | undefined;
  /** Row indexes in display order while sorted; rebuilt lazily after a change. */
  private order: number[] | undefined;
  private version = 0;
  private listeners = new Set<() => void>();
  private scheduled = false;

  constructor(private readonly schedule: (fn: () => void) => void = defaultSchedule) {}

  get rowCount(): number {
    return this.rows.length;
  }

  /** row returns the i-th row in display order, which follows the sort. */
  row(i: number): Row | undefined {
    if (!this.sortBy) return this.rows[i];
    const idx = this.sorted()[i];
    return idx === undefined ? undefined : this.rows[idx];
  }

  get sort(): Sort | undefined {
    return this.sortBy;
  }

  /**
   * setSort orders the loaded rows by a column, or restores the original
   * order when sort is undefined. Rows that arrive later are sorted in too.
   */
  setSort(sort: Sort | undefined): void {
    this.sortBy = sort;
    this.order = undefined;
    this.changed();
  }

  private sorted(): number[] {
    if (this.order) return this.order;
    const { column, dir } = this.sortBy!;
    const sign = dir === "asc" ? 1 : -1;
    const rows = this.rows;
    const order = Array.from(rows.keys());
    // Array.prototype.sort is stable, so ties keep the result's own order.
    order.sort((x, y) => {
      const a = rows[x]![column];
      const b = rows[y]![column];
      if (isNull(a)) return isNull(b) ? 0 : 1;
      if (isNull(b)) return -1;
      return sign * compareCells(a, b);
    });
    this.order = order;
    return order;
  }

  /** sample returns up to n leading rows, for sizing columns. */
  sample(n: number): readonly Row[] {
    return this.rows.slice(0, n);
  }

  append(page: Page): void {
    if (page.columns) this.columns = page.columns;
    // push in a loop: spreading a large page can exceed the argument limit.
    for (const r of page.rows) this.rows.push(r);
    this.order = undefined;
    this.changed();
  }

  clear(): void {
    this.columns = [];
    this.rows = [];
    this.sortBy = undefined;
    this.order = undefined;
    this.changed();
  }

  subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };

  getVersion = (): number => this.version;

  private changed(): void {
    this.version++;
    if (this.scheduled) return;
    this.scheduled = true;
    this.schedule(() => {
      this.scheduled = false;
      this.listeners.forEach((fn) => fn());
    });
  }
}

const defaultSchedule = (fn: () => void) =>
  typeof requestAnimationFrame === "function" ? requestAnimationFrame(() => fn()) : setTimeout(fn, 16);
