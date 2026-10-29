import { useCallback, useEffect, useRef, useState } from 'react';
import type { DesktopLayout } from '@cofy-x/axv-nextstep';
import type { ConnectionStatus } from '../shared/api/connection';
import { desktopDatabase } from '../shared/api/desktop';
import { desktopApi } from './api';
import { createLayoutPersistence } from './layout-persistence';

export function useLayoutPersistence(
  clientId: string,
  onConnectionChange: (status: ConnectionStatus) => void,
) {
  const [persistenceError, setPersistenceError] = useState('');
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const persistence = useRef<ReturnType<typeof createLayoutPersistence> | null>(
    null,
  );
  if (!persistence.current) {
    persistence.current = createLayoutPersistence(
      (layout) => desktopApi.layout(clientId, layout),
      () => setPersistenceError(''),
      (error) => {
        setPersistenceError(
          `Desktop layout was not saved: ${error instanceof Error ? error.message : String(error)}`,
        );
        onConnectionChange('offline');
      },
      desktopDatabase.assertActive,
    );
  }
  const { enqueue, flush } = persistence.current;
  const flushLayout = useCallback(async () => {
    if (!desktopDatabase.getSnapshot()) await flush();
  }, [flush]);
  const onLayoutChange = useCallback(
    (layout: DesktopLayout) => {
      enqueue(layout);
      clearTimeout(timer.current);
      if (desktopDatabase.getSnapshot()) return;
      timer.current = setTimeout(() => {
        void flushLayout();
      }, 350);
    },
    [enqueue, flushLayout],
  );
  useEffect(() => () => clearTimeout(timer.current), []);
  return { persistenceError, onLayoutChange, flushLayout };
}
