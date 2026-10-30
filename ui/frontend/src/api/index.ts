import { createContext, useContext } from "react";

import type { Backend } from "./types";
import { wailsBackend } from "./wails";

export type * from "./types";

/**
 * loadBackend picks the Wails bindings, or the in-memory mock for
 * `npm run dev:mock` in a plain browser. The mock branch is dead code in
 * production builds, so it is dropped from dist/.
 */
export async function loadBackend(): Promise<Backend> {
  if (import.meta.env.DEV && import.meta.env.VITE_BACKEND === "mock") {
    const { createMockBackend } = await import("./mock");
    return createMockBackend();
  }
  return wailsBackend;
}

export const BackendContext = createContext<Backend | null>(null);

export function useBackend(): Backend {
  const b = useContext(BackendContext);
  if (!b) throw new Error("useBackend: no BackendContext provider");
  return b;
}

/** errorMessage turns a rejected backend call into display text. */
export function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  return typeof err === "string" ? err : "unexpected error";
}

/**
 * needsConfirmation reports query.ErrConfirmRequired. Errors cross the
 * Wails bridge as text only, so this is the one place that matches on it.
 */
export function needsConfirmation(err: unknown): boolean {
  return errorMessage(err).includes("need confirmation");
}
