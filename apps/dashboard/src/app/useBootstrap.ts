import { useCallback, useEffect, useRef, useState } from 'react';
import { desktopDatabase } from '../shared/api/desktop';
import { desktopApi } from './api';
import type { Bootstrap } from './api';
import { initializeClientId } from './client-identity';

export interface ReadyDesktop {
  bootstrap: Bootstrap;
  clientId: string;
}

export function useBootstrap() {
  const [ready, setReady] = useState<ReadyDesktop | null>(null);
  const [bootError, setBootError] = useState('');
  const generation = useRef(0);
  const loaded = useRef(false);
  const boot = useCallback(async () => {
    if (loaded.current) return;
    const current = ++generation.current;
    setBootError('');
    try {
      const clientId = await initializeClientId();
      if (current !== generation.current) return;
      const result = await desktopApi.bootstrap(clientId);
      if (current !== generation.current) return;
      desktopDatabase.bind(result.databaseId);
      loaded.current = true;
      setReady({ bootstrap: result, clientId });
    } catch (error) {
      if (current === generation.current) {
        setBootError(error instanceof Error ? error.message : String(error));
      }
    }
  }, []);
  const cancelBoot = useCallback(() => {
    generation.current++;
  }, []);
  useEffect(() => {
    void boot();
    return cancelBoot;
  }, [boot, cancelBoot]);
  return { ready, bootError, boot };
}
