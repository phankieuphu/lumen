import { memo, type ReactNode, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";

import { visibleRange } from "../lib/grid";

export interface GridColumn {
  key: string;
  title: string;
  width: number;
  /** Shown under the title, e.g. the column type. */
  hint?: string;
  align?: "start" | "end";
  /** Take up spare width; `width` is then the minimum. */
  grow?: boolean;
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
  /** Called when the last rows come into view, to load the next page. */
  onEndReached?: () => void;
  empty?: ReactNode;
}

const OVERSCAN = 8;

/**
 * VirtualGrid renders only the rows in view, so its cost depends on the
 * window height, not on the row count. Rows have a fixed height; the header
 * is sticky inside the same scroller, so horizontal scrolling stays in sync.
 */
export function VirtualGrid({ columns, rowCount, renderCell, version = 0, rowHeight = 26, selected, onSelect, onEndReached, empty }: Props) {
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

  // Fill the scroller when there is room; scroll sideways when there is not.
  const minWidth = columns.reduce((w, c) => w + c.width, 0);
  const template = columns.map((c) => (c.grow ? `minmax(${c.width}px, 1fr)` : `${c.width}px`)).join(" ");

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
      />,
    );
  }

  return (
    <div className="grid" ref={scroller} onScroll={onScroll} role="grid" aria-rowcount={rowCount}>
      <div className="grid-head" style={{ gridTemplateColumns: template, minWidth }} role="row">
        {columns.map((c) => (
          <div key={c.key} className="grid-cell grid-th" role="columnheader" title={c.hint ? `${c.title} (${c.hint})` : c.title}>
            <span>{c.title}</span>
            {c.hint && <small>{c.hint}</small>}
          </div>
        ))}
      </div>
      {rowCount === 0 ? (
        empty && <div className="grid-empty">{empty}</div>
      ) : (
        <div className="grid-body" style={{ height: rowCount * rowHeight, minWidth }}>
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
}

// Memoized so scrolling by one row renders one new row, not the whole window.
const GridRow = memo(function GridRow({ index, top, height, template, columns, renderCell, selected, onSelect }: RowProps) {
  return (
    <div
      className="grid-row"
      style={{ transform: `translateY(${top}px)`, height, gridTemplateColumns: template }}
      role="row"
      aria-rowindex={index + 1}
      aria-selected={selected}
      onClick={onSelect && (() => onSelect(index))}
    >
      {columns.map((c, col) => (
        <div key={c.key} className={c.align === "end" ? "grid-cell end" : "grid-cell"} role="gridcell">
          {renderCell(index, col)}
        </div>
      ))}
    </div>
  );
});
