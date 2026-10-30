import { type DependencyList, useCallback, useEffect, useRef, useState } from "react";

import { errorMessage } from "../api";

/** Lists stop growing here; narrow the filter to see older entries. */
export const MAX_LIST_ITEMS = 10_000;

export interface Paged<T> {
  items: readonly T[];
  error: string | undefined;
  loading: boolean;
  /** No more pages, or MAX_LIST_ITEMS reached. */
  done: boolean;
  loadMore: () => void;
  reload: () => void;
}

/**
 * usePaged loads a keyset-paginated list one page at a time. fetchPage gets
 * the last item loaded so far (undefined for the first page) and returns at
 * most `limit` items; a short page means the end. Concurrent loadMore calls
 * are collapsed, and responses from before a reset are ignored.
 */
export function usePaged<T>(
  fetchPage: (last: T | undefined, limit: number) => Promise<T[]>,
  limit: number,
  deps: DependencyList,
): Paged<T> {
  const [items, setItems] = useState<readonly T[]>([]);
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(false);
  const [done, setDone] = useState(false);
  // Refs mirror state for the loader, which must not depend on renders.
  const ref = useRef({ items: [] as readonly T[], busy: false, done: false, gen: 0 });
  const fetchRef = useRef(fetchPage);
  fetchRef.current = fetchPage;

  const load = useCallback(async () => {
    const r = ref.current;
    if (r.busy || r.done) return;
    r.busy = true;
    const gen = r.gen;
    setLoading(true);
    try {
      const page = await fetchRef.current(r.items.at(-1), limit);
      if (gen !== r.gen) return;
      r.items = r.items.concat(page);
      r.done = page.length < limit || r.items.length >= MAX_LIST_ITEMS;
      setItems(r.items);
      setDone(r.done);
      setError(undefined);
    } catch (err) {
      if (gen === r.gen) setError(errorMessage(err));
    } finally {
      if (gen === r.gen) {
        r.busy = false;
        setLoading(false);
      }
    }
  }, [limit]);

  const reload = useCallback(() => {
    ref.current = { items: [], busy: false, done: false, gen: ref.current.gen + 1 };
    setItems([]);
    setDone(false);
    setError(undefined);
    void load();
  }, [load]);

  useEffect(reload, [reload, ...deps]);

  return { items, error, loading, done, loadMore: load, reload };
}
