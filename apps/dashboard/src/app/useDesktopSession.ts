import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';
import { usePreferences } from '../features/appearance/usePreferences';
import type { ConnectionStatus } from '../shared/api/connection';
import { desktopDatabase } from '../shared/api/desktop';
import { APIError } from '../shared/api/http';
import { desktopApi } from './api';
import type { ReadyDesktop } from './useBootstrap';
import { useLayoutPersistence } from './useLayoutPersistence';

type DesktopAccess =
  | { phase: ConnectionStatus }
  | { phase: 'authorization-required'; message: string };

export function useDesktopSession({ bootstrap, clientId }: ReadyDesktop) {
  const [access, setAccess] = useState<DesktopAccess>({ phase: 'connected' });
  const reportConnection = useCallback((phase: ConnectionStatus) => {
    setAccess((previous) =>
      previous.phase === 'authorization-required' || previous.phase === phase
        ? previous
        : { phase },
    );
  }, []);
  const { data: appearance } = usePreferences(
    reportConnection,
    bootstrap.preferences,
  );
  const databaseError = useSyncExternalStore(
    desktopDatabase.subscribe,
    desktopDatabase.getSnapshot,
  );
  const layout = useLayoutPersistence(clientId, reportConnection);
  const { flushLayout } = layout;
  const latestRead = useRef<symbol | null>(null);
  const refresh = useCallback(
    async (retryLayout = false) => {
      const read = Symbol();
      latestRead.current = read;
      try {
        await desktopApi.health();
        desktopDatabase.assertActive();
      } catch (error) {
        if (latestRead.current !== read) return;
        if (
          error instanceof APIError &&
          error.code === 'authentication_required'
        ) {
          setAccess({
            phase: 'authorization-required',
            message: error.message,
          });
        } else reportConnection('offline');
        return;
      }
      if (latestRead.current !== read) return;
      setAccess((previous) =>
        previous.phase === 'connected' ? previous : { phase: 'connected' },
      );
      if (retryLayout) void flushLayout();
    },
    [flushLayout, reportConnection],
  );
  useEffect(() => {
    if (databaseError) return;
    const visibility = () => {
      if (desktopDatabase.getSnapshot()) return;
      if (document.visibilityState === 'hidden') void flushLayout();
      else void refresh();
    };
    const timer = setInterval(() => {
      if (desktopDatabase.getSnapshot()) return;
      void refresh(true);
    }, 5000);
    const focus = () => {
      if (!desktopDatabase.getSnapshot()) void refresh();
    };
    globalThis.addEventListener('focus', focus);
    document.addEventListener('visibilitychange', visibility);
    return () => {
      latestRead.current = null;
      clearInterval(timer);
      globalThis.removeEventListener('focus', focus);
      document.removeEventListener('visibilitychange', visibility);
    };
  }, [databaseError, refresh, flushLayout]);
  const authorizationRequired = access.phase === 'authorization-required';
  return {
    ...layout,
    connection:
      databaseError || authorizationRequired
        ? ('offline' as const)
        : access.phase,
    appearance,
    databaseError,
    authenticationError: authorizationRequired ? access.message : '',
    retryAuthentication: refresh,
  };
}
