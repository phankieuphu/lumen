import { useCallback, useMemo, useSyncExternalStore } from "react";

import type { Column } from "../api";
import { cellText, formatCount } from "../lib/format";
import { columnWidths } from "../lib/grid";
import type { ResultStore } from "../lib/resultStore";
import { type GridColumn, VirtualGrid } from "./VirtualGrid";

const NUMERIC = /int|serial|numeric|decimal|float|double|real|money/i;
const SAMPLE_ROWS = 50;

function gridColumns(columns: readonly Column[], sample: readonly unknown[][], rowCount: number): GridColumn[] {
  const widths = columnWidths(
    columns.map((c) => c.name),
    sample.map((row) => row.map((v) => (v === null ? "NULL" : cellText(v)))),
  );
  const rowNum: GridColumn = { key: "#", title: "#", width: Math.max(48, formatCount(rowCount).length * 8 + 20), align: "end" };
  return [
    rowNum,
    ...columns.map((c, i) => ({
      key: `${i}:${c.name}`,
      title: c.name,
      hint: c.type,
      width: widths[i]!,
      align: NUMERIC.test(c.type) ? ("end" as const) : ("start" as const),
    })),
  ];
}

/** ResultGrid shows a streaming query result from a ResultStore. */
export function ResultGrid({ store }: { store: ResultStore }) {
  const version = useSyncExternalStore(store.subscribe, store.getVersion);
  const { columns, rowCount } = store;

  // Sized once per result (when its columns arrive), not on every page, so
  // columns don't jump while rows stream in. The row-number column only
  // grows by order of magnitude.
  const magnitude = String(rowCount).length;
  const cols = useMemo(
    () => gridColumns(columns, store.sample(SAMPLE_ROWS) as unknown[][], 10 ** magnitude),
    [columns, store, magnitude],
  );

  const renderCell = useCallback(
    (row: number, col: number) => {
      if (col === 0) return row + 1;
      const v = store.row(row)?.[col - 1];
      return v === null || v === undefined ? <span className="null">NULL</span> : cellText(v);
    },
    [store],
  );

  if (columns.length === 0) return null;
  return <VirtualGrid columns={cols} rowCount={rowCount} renderCell={renderCell} version={version} />;
}
