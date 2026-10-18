import { useCallback, useEffect, useRef, useState } from 'react';
import type { DesktopLayout } from '@cofy-x/axv-nextstep';
import type { ConnectionStatus } from '../shared/api/connection';
import { desktopApi } from './api';
import { createLayoutPersistence } from './layout-persistence';

export function useLayoutPersistence(
  onConnectionChange: (status: ConnectionStatus) => void,
) {
  const [persistenceError, setPersistenceError] = useState('');
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const persistence = useRef<ReturnType<typeof createLayoutPersistence> | null>(
    null,
  );
  if (!persistence.current) {
    persistence.current = createLayoutPersistence(
      desktopApi.layout,
      () => setPersistenceError(''),
      (error) => {
        setPersistenceError(
          `Desktop layout was not saved: ${error instanceof Error ? error.message : String(error)}`,
        );
        onConnectionChange('offline');
      },
    );
  }
  const { enqueue, flush: flushLayout } = persistence.current;
  const onLayoutChange = useCallback(
    (layout: DesktopLayout) => {
      enqueue(layout);
      clearTimeout(timer.current);
      timer.current = setTimeout(() => {
        void flushLayout();
      }, 350);
    },
    [enqueue, flushLayout],
  );
  useEffect(() => () => clearTimeout(timer.current), []);
  return { persistenceError, onLayoutChange, flushLayout };
}
