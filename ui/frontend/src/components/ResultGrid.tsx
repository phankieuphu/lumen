import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from "react";

import type { Column } from "../api";
import { cellText, copyValue, formatCount, rowToTSV } from "../lib/format";
import { columnWidths } from "../lib/grid";
import type { ResultStore } from "../lib/resultStore";
import { type GridColumn, type GridSort, VirtualGrid } from "./VirtualGrid";

const NUMERIC = /int|serial|numeric|decimal|float|double|real|money/i;
const SAMPLE_ROWS = 50;

function gridColumns(columns: readonly Column[], sample: readonly unknown[][], rowCount: number): GridColumn[] {
  const widths = columnWidths(
    // The header shows the type after the name and a sort arrow, so size for all three.
    columns.map((c) => `${c.name} ${c.type} ▲`),
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
      sortable: true,
    })),
  ];
}

interface Props {
  store: ResultStore;
  /**
   * Called with the text to copy when a cell is clicked: the cell's full
   * value, or the whole row as tab-separated text from the # column.
   */
  onCopy?: (text: string, what: string) => void;
}

/** ResultGrid shows a streaming query result from a ResultStore. */
export function ResultGrid({ store, onCopy }: Props) {
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

  // The last copied cell stays highlighted until the next result.
  const [active, setActive] = useState<{ row: number; col: number }>();
  useEffect(() => setActive(undefined), [columns]);

  const copyCell = useCallback(
    (row: number, col: number) => {
      const values = store.row(row);
      if (!values || !onCopy) return;
      setActive({ row, col });
      if (col === 0) onCopy(rowToTSV(values), `row ${formatCount(row + 1)}`);
      else onCopy(copyValue(values[col - 1]), `${columns[col - 1]?.name ?? "value"}, row ${formatCount(row + 1)}`);
    },
    [store, columns, onCopy],
  );

  // Header clicks cycle a column through ascending, descending, and the
  // result's own order. Grid column 0 is the row number, so data columns are
  // offset by one.
  const sort: GridSort | undefined = store.sort && { col: store.sort.column + 1, dir: store.sort.dir };
  const onSort = useCallback(
    (col: number) => {
      const column = col - 1;
      const cur = store.sort;
      if (cur?.column !== column) store.setSort({ column, dir: "asc" });
      else if (cur.dir === "asc") store.setSort({ column, dir: "desc" });
      else store.setSort(undefined);
      setActive(undefined);
    },
    [store],
  );

  if (columns.length === 0) return null;
  return (
    <VirtualGrid
      columns={cols}
      rowCount={rowCount}
      renderCell={renderCell}
      version={version}
      onCellClick={onCopy && copyCell}
      activeCell={active}
      sort={sort}
      onSort={onSort}
    />
  );
}
