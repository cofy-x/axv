import { useCallback, useMemo, useState } from 'react';
import { requestDesktop, desktopDatabase } from '../../shared/api/desktop';
import type { ConnectionStatus } from '../../shared/api/connection';
import type { Preferences } from './preferences';
import { createPreferenceQueue, defaultPreferences } from './preferences';

export function usePreferences(
  onConnectionChange: (status: ConnectionStatus) => void,
  initialPreferences: Preferences = defaultPreferences,
) {
  const [preferences, setPreferences] = useState(initialPreferences);
  const [queue] = useState(() =>
    createPreferenceQueue(
      initialPreferences,
      (value) =>
        requestDesktop<Preferences>('/preferences', {
          method: 'PUT',
          body: JSON.stringify(value),
        }),
      desktopDatabase.assertActive,
    ),
  );
  const resetPreferences = useCallback(
    (value: Preferences) => {
      queue.reset(value);
      setPreferences(value);
    },
    [queue],
  );
  const savePreferences = useCallback(
    async (patch: Partial<Preferences>) => {
      try {
        const saved = await queue.save(patch);
        desktopDatabase.assertActive();
        setPreferences(saved);
        onConnectionChange('connected');
      } catch (error) {
        onConnectionChange('offline');
        throw error;
      }
    },
    [queue, onConnectionChange],
  );
  const data = useMemo(
    () => ({ preferences, savePreferences }),
    [preferences, savePreferences],
  );
  return { data, resetPreferences };
}
