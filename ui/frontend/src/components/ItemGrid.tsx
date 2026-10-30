import { type ReactNode, useCallback } from "react";

import { type GridColumn, VirtualGrid } from "./VirtualGrid";

export interface ItemColumn<T> extends Omit<GridColumn, "hint"> {
  render: (item: T) => ReactNode;
}

interface Props<T> {
  columns: readonly ItemColumn<T>[];
  items: readonly T[];
  selected?: number;
  onSelect?: (index: number) => void;
  onEndReached?: () => void;
  empty?: ReactNode;
}

/** ItemGrid is a VirtualGrid over an array of records, one column per field. */
export function ItemGrid<T>({ columns, items, ...rest }: Props<T>) {
  const renderCell = useCallback((row: number, col: number) => columns[col]!.render(items[row]!), [columns, items]);
  return <VirtualGrid columns={columns} rowCount={items.length} renderCell={renderCell} {...rest} />;
}
