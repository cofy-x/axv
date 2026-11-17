import { useCallback, useEffect, useRef, useState } from 'react';
import { notificationError } from './api';
import { SerialPoller } from '../../shared/api/serial-poller';

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
    const poller = new SerialPoller({
      timeoutMs: 15000,
      intervalMs: pollMs,
      onStart: () => setState((previous) => ({ ...previous, loading: true })),
      read: async ({ signal, isCurrent }) => {
        const value = await read(signal);
        if (!isCurrent()) return;
        signal.throwIfAborted();
        if (generation.current === current)
          setState({ value, loading: false, error: '' });
      },
      onError: (error) => {
        if (generation.current === current)
          setState((previous) => ({
            ...previous,
            loading: false,
            error: notificationError(error),
          }));
      },
    });
    poller.start();
    return () => poller.stop();
  }, [active, read, pollMs, revision]);
  return { ...state, refresh, accept };
}
