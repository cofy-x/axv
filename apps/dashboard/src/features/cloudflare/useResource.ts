import { useCallback, useEffect, useState } from 'react';
import { SerialPoller } from '../../shared/api/serial-poller';

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
  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  useEffect(() => {
    if (!active) return;
    const poller = new SerialPoller({
      timeoutMs,
      intervalMs: pollMs,
      onStart: () => setState((previous) => ({ ...previous, loading: true })),
      read: async ({ signal, isCurrent }) => {
        const value = await read(signal);
        if (!isCurrent()) return;
        signal.throwIfAborted();
        setState({ value, loading: false });
      },
      onError: (error) => {
        setState((previous) => ({
          ...previous,
          loading: false,
          error:
            error instanceof Error
              ? error.message
              : 'Could not load this view.',
        }));
      },
    });
    poller.start();
    return () => poller.stop();
  }, [active, pollMs, read, revision, timeoutMs]);
  return { ...state, refresh };
}
