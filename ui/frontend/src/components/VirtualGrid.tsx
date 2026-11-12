import { memo, type ReactNode, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";

import { visibleRange } from "../lib/grid";

export interface GridColumn {
  key: string;
  title: string;
  width: number;
  /** Shown after the title, e.g. the column type. */
  hint?: string;
  align?: "start" | "end";
  /** Whether clicking the header sorts by this column; needs onSort. */
  sortable?: boolean;
}

export interface GridSort {
  /** Index into columns. */
  col: number;
  dir: "asc" | "desc";
}

interface Props {
  columns: readonly GridColumn[];
  rowCount: number;
  renderCell: (row: number, col: number) => ReactNode;
  /** Change it to re-render visible rows when the data behind them changes. */
  version?: number;
  rowHeight?: number;
  selected?: number;
  onSelect?: (row: number) => void;
  /** Called when a cell is clicked; the grid then shows a copy cursor. */
  onCellClick?: (row: number, col: number) => void;
  /** The cell to highlight, e.g. the one just copied. */
  activeCell?: { row: number; col: number } | undefined;
  /** Called when the last rows come into view, to load the next page. */
  onEndReached?: () => void;
  empty?: ReactNode;
  /** The column the rows are sorted by, shown with an arrow in its header. */
  sort?: GridSort | undefined;
  /** Called when a sortable column's header is clicked. */
  onSort?: (col: number) => void;
}

const OVERSCAN = 8;

/**
 * VirtualGrid renders only the rows in view, so its cost depends on the
 * window height, not on the row count. Rows have a fixed height; the header
 * is sticky inside the same scroller, so horizontal scrolling stays in sync.
 */
export function VirtualGrid({
  columns,
  rowCount,
  renderCell,
  version = 0,
  rowHeight = 32,
  selected,
  onSelect,
  onCellClick,
  activeCell,
  onEndReached,
  empty,
  sort,
  onSort,
}: Props) {
  const scroller = useRef<HTMLDivElement>(null);
  const [viewport, setViewport] = useState(0);
  const [first, setFirst] = useState(0);

  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setViewport(e?.contentRect.height ?? 0));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // Re-render on scroll only when the first visible row changes.
  const onScroll = useCallback(() => {
    const el = scroller.current;
    if (el) setFirst(Math.floor(el.scrollTop / rowHeight));
  }, [rowHeight]);

  const { start, end } = visibleRange(first * rowHeight, viewport, rowHeight, rowCount, OVERSCAN);

  useEffect(() => {
    if (onEndReached && rowCount > 0 && end >= rowCount) onEndReached();
  }, [onEndReached, end, rowCount]);

  const width = columns.reduce((w, c) => w + c.width, 0);
  const template = columns.map((c) => `${c.width}px`).join(" ");

  const rows: ReactNode[] = [];
  for (let i = start; i < end; i++) {
    rows.push(
      <GridRow
        key={i}
        index={i}
        top={i * rowHeight}
        height={rowHeight}
        template={template}
        columns={columns}
        renderCell={renderCell}
        version={version}
        selected={i === selected}
        onSelect={onSelect}
        onCellClick={onCellClick}
        activeCol={activeCell?.row === i ? activeCell.col : undefined}
      />,
    );
  }

  return (
    <div className={onCellClick ? "grid copyable" : "grid"} ref={scroller} onScroll={onScroll} role="grid" aria-rowcount={rowCount}>
      <div className="grid-head" style={{ gridTemplateColumns: template, width }} role="row">
        {columns.map((c, col) => {
          const label = c.hint ? `${c.title} (${c.hint})` : c.title;
          const dir = sort?.col === col ? sort.dir : undefined;
          const ariaSort = dir === "asc" ? "ascending" : dir === "desc" ? "descending" : undefined;
          if (!c.sortable || !onSort) {
            return (
              <div key={c.key} className="grid-cell grid-th" role="columnheader" title={label}>
                <span>{c.title}</span>
                {c.hint && <small>{c.hint}</small>}
              </div>
            );
          }
          const next = dir === "asc" ? "descending" : dir === "desc" ? "original order" : "ascending";
          return (
            <div key={c.key} className="grid-cell grid-th" role="columnheader" aria-sort={ariaSort}>
              <button type="button" className="grid-sort" onClick={() => onSort(col)} title={`${label} — click to sort ${next}`}>
                <span className="grid-th-title">{c.title}</span>
                {c.hint && <small>{c.hint}</small>}
                <span className={dir ? "grid-sort-icon on" : "grid-sort-icon"} aria-hidden="true">
                  {dir === "desc" ? "▼" : dir === "asc" ? "▲" : "⇅"}
                </span>
              </button>
            </div>
          );
        })}
      </div>
      {rowCount === 0 ? (
        empty && <div className="grid-empty">{empty}</div>
      ) : (
        <div className="grid-body" style={{ height: rowCount * rowHeight, width }}>
          {rows}
        </div>
      )}
    </div>
  );
}

interface RowProps {
  index: number;
  top: number;
  height: number;
  template: string;
  columns: readonly GridColumn[];
  renderCell: (row: number, col: number) => ReactNode;
  version: number;
  selected: boolean;
  onSelect: ((row: number) => void) | undefined;
  onCellClick: ((row: number, col: number) => void) | undefined;
  /** The highlighted cell's column in this row, if any. */
  activeCol: number | undefined;
}

// Memoized so scrolling by one row renders one new row, not the whole window.
const GridRow = memo(function GridRow({ index, top, height, template, columns, renderCell, selected, onSelect, onCellClick, activeCol }: RowProps) {
  return (
    <div
      className={selected ? "grid-row selected" : "grid-row"}
      style={{ transform: `translateY(${top}px)`, height, gridTemplateColumns: template }}
      role="row"
      aria-rowindex={index + 1}
      aria-selected={selected}
      onClick={onSelect && (() => onSelect(index))}
    >
      {columns.map((c, col) => (
        <div
          key={c.key}
          className={`grid-cell${c.align === "end" ? " end" : ""}${col === activeCol ? " active" : ""}`}
          role="gridcell"
          onClick={onCellClick && (() => onCellClick(index, col))}
        >
          {renderCell(index, col)}
        </div>
      ))}
    </div>
  );
});
