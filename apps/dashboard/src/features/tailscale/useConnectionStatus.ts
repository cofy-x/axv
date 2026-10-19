import { useCallback, useState, useSyncExternalStore } from 'react';
import { ConnectionStore } from './connection-store';

export function useConnectionStatus(interactive: boolean) {
  const [store] = useState(() => new ConnectionStore());
  const subscribe = useCallback(
    (listener: () => void) =>
      interactive ? store.subscribe(listener) : () => undefined,
    [store, interactive],
  );
  const snapshot = useSyncExternalStore(subscribe, store.getSnapshot);
  return { ...snapshot, refresh: store.refresh };
}
