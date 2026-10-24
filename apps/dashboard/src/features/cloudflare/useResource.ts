import { useCallback, useEffect, useRef, useState } from 'react';

/** Each active view owns serial reads; mutations never use this hook. */
export function useResource<T>(
  read: (signal: AbortSignal) => Promise<T>,
  active: boolean,
  pollMs?: number,
  timeoutMs = 15000,
) {
  const [state, setState] = useState<{
    value?: T;
    loading: boolean;
    error?: string;
  }>({ loading: true });
  const [revision, setRevision] = useState(0);
  const generation = useRef(0);
  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  useEffect(() => {
    if (!active) return;
    const current = ++generation.current;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;

    async function load() {
      if (controller.signal.aborted) return;
      setState((previous) => ({ ...previous, loading: true }));
      const signal = AbortSignal.any([
        controller.signal,
        AbortSignal.timeout(timeoutMs),
      ]);
      try {
        const value = await read(signal);
        signal.throwIfAborted();
        if (generation.current === current) setState({ value, loading: false });
      } catch (error) {
        if (controller.signal.aborted || generation.current !== current) return;
        setState((previous) => ({
          ...previous,
          loading: false,
          error:
            error instanceof Error
              ? error.message
              : 'Could not load this view.',
        }));
      } finally {
        if (!controller.signal.aborted && pollMs)
          timer = setTimeout(() => void load(), pollMs);
      }
    }

    void load();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [active, pollMs, read, revision, timeoutMs]);
  return { ...state, refresh };
}
