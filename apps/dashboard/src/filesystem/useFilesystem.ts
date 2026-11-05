import { useCallback, useMemo, useRef, useState } from 'react';
import type { ConnectionStatus } from '../shared/api/connection';
import { desktopDatabase } from '../shared/api/desktop';
import { filesystemApi } from './api';
import type { FilesystemData } from './context';
import type { Entry } from './types';

export function useFilesystem(
  onConnectionChange: (status: ConnectionStatus) => void,
  initialEntries: Entry[] = [],
) {
  const [entries, setEntries] = useState<Entry[]>(initialEntries);
  const [selectionWindowId, setSelectionWindowId] = useState<string | null>(
    null,
  );
  const [metadataListeners] = useState(
    () => new Set<(entry: Entry, revision: number) => void>(),
  );
  const [selections, setSelections] = useState<Record<string, string[]>>({});
  const [clipboard, setClipboard] = useState<FilesystemData['clipboard']>(null);
  const generation = useRef(0);
  const refresh = useCallback(async () => {
    const current = ++generation.current;
    try {
      const result = await filesystemApi.entries();
      desktopDatabase.assertActive();
      if (current === generation.current) setEntries(result.entries);
      onConnectionChange('connected');
    } catch (error) {
      onConnectionChange('offline');
      throw error;
    }
  }, [onConnectionChange]);
  const data = useMemo<FilesystemData>(
    () => ({
      entries,
      refresh,
      selections,
      selectionWindowId,
      metadataChanged: (entry, revision) => {
        desktopDatabase.assertActive();
        for (const listener of metadataListeners) listener(entry, revision);
      },
      onMetadataChanged: (listener) => {
        metadataListeners.add(listener);
        return () => {
          metadataListeners.delete(listener);
        };
      },
      select: (id, ids) => {
        setSelectionWindowId(id);
        setSelections((previous) => ({ ...previous, [id]: ids }));
      },
      clipboard,
      setClipboard,
    }),
    [
      entries,
      refresh,
      selections,
      clipboard,
      selectionWindowId,
      metadataListeners,
    ],
  );
  return { data, setEntries };
}
