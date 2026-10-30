import { type DependencyList, useCallback, useEffect, useState } from "react";

import { errorMessage } from "../api";

export interface AsyncState<T> {
  data: T | undefined;
  error: string | undefined;
  loading: boolean;
  reload: () => void;
}

/**
 * useAsync runs fn when deps change and drops results from superseded runs,
 * so a slow response never overwrites a newer one.
 */
export function useAsync<T>(fn: () => Promise<T>, deps: DependencyList): AsyncState<T> {
  const [state, setState] = useState<Omit<AsyncState<T>, "reload">>({ data: undefined, error: undefined, loading: true });
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let live = true;
    setState((s) => ({ ...s, loading: true }));
    fn().then(
      (data) => live && setState({ data, error: undefined, loading: false }),
      (err: unknown) => live && setState((s) => ({ ...s, error: errorMessage(err), loading: false })),
    );
    return () => {
      live = false;
    };
  }, [...deps, tick]);

  const reload = useCallback(() => setTick((t) => t + 1), []);
  return { ...state, reload };
}
