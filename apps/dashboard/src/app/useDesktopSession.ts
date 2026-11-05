import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';
import { useFilesystem } from '../filesystem/useFilesystem';
import { usePreferences } from '../features/appearance/usePreferences';
import type { ConnectionStatus } from '../shared/api/connection';
import { desktopDatabase } from '../shared/api/desktop';
import { APIError } from '../shared/api/http';
import type { ReadyDesktop } from './useBootstrap';
import { useLayoutPersistence } from './useLayoutPersistence';

export function useDesktopSession({ bootstrap, clientId }: ReadyDesktop) {
  const [connection, setConnection] = useState<ConnectionStatus>('connected');
  const { data: filesystem } = useFilesystem(setConnection, bootstrap.entries);
  const { data: appearance } = usePreferences(
    setConnection,
    bootstrap.preferences,
  );
  const databaseError = useSyncExternalStore(
    desktopDatabase.subscribe,
    desktopDatabase.getSnapshot,
  );
  const layout = useLayoutPersistence(clientId, setConnection);
  const [authenticationError, setAuthenticationError] = useState('');
  const authenticationGeneration = useRef(0);
  const { refresh: refreshFilesystem } = filesystem;
  const refresh = useCallback(async () => {
    const current = ++authenticationGeneration.current;
    try {
      await refreshFilesystem();
      if (current === authenticationGeneration.current)
        setAuthenticationError('');
    } catch (error) {
      if (
        current === authenticationGeneration.current &&
        error instanceof APIError &&
        error.code === 'authentication_required'
      )
        setAuthenticationError(error.message);
      throw error;
    }
  }, [refreshFilesystem]);
  const { flushLayout } = layout;
  useEffect(() => {
    if (databaseError) return;
    const visibility = () => {
      if (desktopDatabase.getSnapshot()) return;
      if (document.visibilityState === 'hidden') void flushLayout();
      else void refresh().catch(() => {});
    };
    const timer = setInterval(() => {
      if (desktopDatabase.getSnapshot()) return;
      void refresh()
        .then(() => {
          setConnection('connected');
          void flushLayout();
        })
        .catch(() => setConnection('offline'));
    }, 5000);
    const focus = () => {
      if (!desktopDatabase.getSnapshot()) void refresh().catch(() => {});
    };
    globalThis.addEventListener('focus', focus);
    document.addEventListener('visibilitychange', visibility);
    return () => {
      clearInterval(timer);
      globalThis.removeEventListener('focus', focus);
      document.removeEventListener('visibilitychange', visibility);
    };
  }, [databaseError, refresh, flushLayout]);
  return {
    ...layout,
    connection: databaseError ? ('offline' as const) : connection,
    filesystem,
    appearance,
    databaseError,
    authenticationError,
    retryAuthentication: refresh,
  };
}
