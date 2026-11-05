import { useCallback, useEffect, useRef, useState } from 'react';
import { notificationError } from './api';

export function useNotificationResource<T>(
  read: (signal: AbortSignal) => Promise<T>,
  active: boolean,
  pollMs?: number,
) {
  const [state, setState] = useState<{
    value?: T;
    loading: boolean;
    error: string;
  }>({ loading: true, error: '' });
  const [revision, setRevision] = useState(0);
  const generation = useRef(0);
  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  const accept = useCallback((value: T) => {
    generation.current++;
    setState({ value, loading: false, error: '' });
    setRevision((current) => current + 1);
  }, []);
  useEffect(() => {
    if (!active) return;
    const current = ++generation.current;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;

    async function load() {
      setState((previous) => ({ ...previous, loading: true }));
      try {
        const signal = AbortSignal.any([
          controller.signal,
          AbortSignal.timeout(15000),
        ]);
        const value = await read(signal);
        signal.throwIfAborted();
        if (generation.current === current)
          setState({ value, loading: false, error: '' });
      } catch (error) {
        if (!controller.signal.aborted && generation.current === current)
          setState((previous) => ({
            ...previous,
            loading: false,
            error: notificationError(error),
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
  }, [active, read, pollMs, revision]);
  return { ...state, refresh, accept };
}
