// Pure layout math for VirtualGrid, kept apart from React so it is testable.

export interface Range {
  start: number;
  end: number; // exclusive
}

/** visibleRange returns the rows to render for a scroll position. */
export function visibleRange(scrollTop: number, viewport: number, rowHeight: number, count: number, overscan: number): Range {
  if (count <= 0 || rowHeight <= 0) return { start: 0, end: 0 };
  const first = Math.floor(Math.max(0, scrollTop) / rowHeight);
  const start = Math.max(0, Math.min(first, count - 1) - overscan);
  const end = Math.min(count, first + Math.ceil(Math.max(0, viewport) / rowHeight) + overscan + 1);
  return { start, end };
}

const CHAR_PX = 7.5;
const PAD_PX = 20;
export const MIN_COL_PX = 64;
export const MAX_COL_PX = 360;

/**
 * columnWidths estimates a width per column from its header and a sample of
 * rows. It reads at most `sample` rows so the cost does not grow with the
 * result size.
 */
export function columnWidths(headers: readonly string[], sampleRows: readonly (readonly string[])[]): number[] {
  return headers.map((h, c) => {
    let chars = h.length;
    for (const row of sampleRows) chars = Math.max(chars, row[c]?.length ?? 0);
    return Math.round(Math.min(MAX_COL_PX, Math.max(MIN_COL_PX, chars * CHAR_PX + PAD_PX)));
  });
}
