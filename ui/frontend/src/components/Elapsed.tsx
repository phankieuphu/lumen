import { useEffect, useState } from "react";

import { formatDuration } from "../lib/format";

/** Elapsed shows time since mount; it re-renders only itself. */
export function Elapsed() {
  const [start] = useState(() => performance.now());
  const [ms, setMs] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setMs(Math.round(performance.now() - start)), 100);
    return () => clearInterval(id);
  }, [start]);
  return <span className="muted">{formatDuration(ms)}</span>;
}
