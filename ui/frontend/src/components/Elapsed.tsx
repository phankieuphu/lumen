import { useEffect, useState } from "react";

import { formatDuration } from "../lib/format";

/**
 * Elapsed shows time since mount, or since an RFC 3339 time; it re-renders
 * only itself.
 */
export function Elapsed({ since }: { since?: string }) {
  const [start] = useState(() => {
    const t = since ? Date.parse(since) : NaN;
    return Number.isNaN(t) ? Date.now() : t;
  });
  const [ms, setMs] = useState(() => Math.max(0, Date.now() - start));
  useEffect(() => {
    const id = setInterval(() => setMs(Math.max(0, Date.now() - start)), 100);
    return () => clearInterval(id);
  }, [start]);
  return <span className="muted">{formatDuration(ms)}</span>;
}
