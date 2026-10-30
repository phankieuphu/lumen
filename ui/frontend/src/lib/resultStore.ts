import type { Column, Page, Row } from "../api/types";

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
  private version = 0;
  private listeners = new Set<() => void>();
  private scheduled = false;

  constructor(private readonly schedule: (fn: () => void) => void = defaultSchedule) {}

  get rowCount(): number {
    return this.rows.length;
  }

  row(i: number): Row | undefined {
    return this.rows[i];
  }

  /** sample returns up to n leading rows, for sizing columns. */
  sample(n: number): readonly Row[] {
    return this.rows.slice(0, n);
  }

  append(page: Page): void {
    if (page.columns) this.columns = page.columns;
    // push in a loop: spreading a large page can exceed the argument limit.
    for (const r of page.rows) this.rows.push(r);
    this.changed();
  }

  clear(): void {
    this.columns = [];
    this.rows = [];
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
