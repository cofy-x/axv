import { useCallback, useEffect, useState } from 'react';
import { useFilesystem } from '../filesystem/useFilesystem';
import { usePreferences } from '../features/appearance/usePreferences';
import type { ConnectionStatus } from '../shared/api/connection';
import type { Bootstrap } from './api';
import { useBootstrap } from './useBootstrap';
import { useLayoutPersistence } from './useLayoutPersistence';

export function useDesktopSession() {
  const [connection, setConnection] = useState<ConnectionStatus>('connecting');
  const { data: filesystem, setEntries } = useFilesystem(setConnection);
  const { data: appearance, resetPreferences } = usePreferences(setConnection);
  const onLoaded = useCallback(
    (result: Bootstrap) => {
      setEntries(result.entries);
      resetPreferences(result.preferences);
    },
    [setEntries, resetPreferences],
  );
  const startup = useBootstrap(onLoaded, setConnection);
  const layout = useLayoutPersistence(setConnection);
  const { bootstrap } = startup;
  const { refresh } = filesystem;
  const { flushLayout } = layout;
  useEffect(() => {
    const visibility = () => {
      if (document.visibilityState === 'hidden') void flushLayout();
      else if (bootstrap) void refresh().catch(() => {});
    };
    const timer = setInterval(() => {
      void refresh()
        .then(() => {
          setConnection('connected');
          void flushLayout();
        })
        .catch(() => setConnection('offline'));
    }, 5000);
    const focus = () => {
      if (bootstrap) void refresh().catch(() => {});
    };
    globalThis.addEventListener('focus', focus);
    document.addEventListener('visibilitychange', visibility);
    return () => {
      clearInterval(timer);
      globalThis.removeEventListener('focus', focus);
      document.removeEventListener('visibilitychange', visibility);
    };
  }, [bootstrap, refresh, flushLayout]);
  return { ...startup, ...layout, connection, filesystem, appearance };
}
