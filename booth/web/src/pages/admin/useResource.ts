import { useCallback, useEffect, useState } from "react";
import { api, ApiError } from "../../app/api";

// useResource is the shared load/refresh hook for every admin panel: it fetches
// a list endpoint, exposes {items, loading, error}, and a refresh() the panels
// call after a mutation. 403 is surfaced explicitly so a DevRel who reaches an
// admin panel sees the server's authoritative denial (§4), not an empty table.
export interface ResourceState<T> {
  items: T[];
  loading: boolean;
  /** "forbidden" when the server returned 403; a message otherwise; null when ok. */
  error: "forbidden" | string | null;
  refresh: () => void;
}

export function useResource<T>(path: string): ResourceState<T> {
  const [items, setItems] = useState<T[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<"forbidden" | string | null>(null);

  const refresh = useCallback(() => {
    setLoading(true);
    api
      .get<T[] | null>(path)
      .then((rows) => {
        setItems(rows ?? []);
        setError(null);
      })
      .catch((err) => {
        if (err instanceof ApiError && err.status === 403) setError("forbidden");
        else setError(err instanceof Error ? err.message : "failed to load");
      })
      .finally(() => setLoading(false));
  }, [path]);

  useEffect(refresh, [refresh]);

  return { items, loading, error, refresh };
}
