import { useCallback, useEffect, useRef, useState } from 'react';
import type { ConnectionStatus } from '../shared/api/connection';
import { desktopApi } from './api';
import type { Bootstrap } from './api';

export function useBootstrap(
  onLoaded: (result: Bootstrap) => void,
  onConnectionChange: (status: ConnectionStatus) => void,
) {
  const [bootstrap, setBootstrap] = useState<Bootstrap | null>(null);
  const [bootError, setBootError] = useState('');
  const generation = useRef(0);
  const boot = useCallback(async () => {
    const current = ++generation.current;
    setBootError('');
    onConnectionChange('connecting');
    try {
      const result = await desktopApi.bootstrap();
      if (current !== generation.current) return;
      onLoaded(result);
      setBootstrap(result);
      onConnectionChange('connected');
    } catch (error) {
      if (current === generation.current) {
        setBootError(error instanceof Error ? error.message : String(error));
        onConnectionChange('offline');
      }
    }
  }, [onLoaded, onConnectionChange]);
  useEffect(() => {
    void boot();
  }, [boot]);
  return { bootstrap, bootError, boot };
}
