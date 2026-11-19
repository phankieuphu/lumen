import { useEffect, useMemo, useState } from "react";

import { useBackend } from "../api";
import { type SchemaCompletion, schemaCompletion } from "../lib/completion";
import { useAsync } from "./useAsync";

/**
 * useSchemaCompletion loads connId's schema for autocomplete and loads it
 * again when the backend reports a change (DDL, the end of a transaction).
 * The backend caches the schema, so this is cheap. A connection that cannot
 * be read just completes keywords.
 */
export function useSchemaCompletion(connId: string): SchemaCompletion | undefined {
  const backend = useBackend();
  const [changes, setChanges] = useState(0);
  useEffect(
    () =>
      backend.onSchemaChanged((id) => {
        if (id === connId) setChanges((n) => n + 1);
      }),
    [backend, connId],
  );
  const loaded = useAsync(async () => ({ connId, schema: await backend.schema(connId) }), [backend, connId, changes]);
  // While another connection's schema is loading, keep none rather than its names.
  const data = loaded.data?.connId === connId ? loaded.data.schema : undefined;
  return useMemo(() => (data ? schemaCompletion(data) : undefined), [data]);
}
