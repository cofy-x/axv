import { useEffect, useState } from 'react';

interface ResourceState<T> {
  key: string | object | null;
  data?: T;
  error?: string;
  pending?: true;
}

export function useResource<T>(
  key: ResourceState<T>['key'],
  load: (signal: AbortSignal) => Promise<T>,
  assertCurrent?: () => void,
  { keepPreviousData = false }: { keepPreviousData?: boolean } = {},
): ResourceState<T> {
  const [state, setState] = useState<ResourceState<T>>({ key: null });
  useEffect(() => {
    if (key === null) return;
    const controller = new AbortController();
    const { signal } = controller;
    void load(signal)
      .then((data) => {
        if (signal.aborted) return;
        assertCurrent?.();
        setState({ key, data });
      })
      .catch((error: unknown) => {
        if (!signal.aborted)
          setState((previous) => ({
            key,
            data: keepPreviousData ? previous.data : undefined,
            error: error instanceof Error ? error.message : String(error),
          }));
      });
    return () => controller.abort();
  }, [key, load, assertCurrent, keepPreviousData]);
  return key === null || state.key === key
    ? state
    : { key, data: keepPreviousData ? state.data : undefined, pending: true };
}
