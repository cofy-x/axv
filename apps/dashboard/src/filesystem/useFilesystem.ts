import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { desktopDatabase } from '../shared/api/desktop';
import { filesystemApi } from './api';
import type { FilesystemData } from './context';
import type { Entry } from './types';

type RefreshTrigger = 'requested' | 'periodic';

export type FilesystemObservation =
  | { phase: 'available'; trigger: RefreshTrigger }
  | { phase: 'unavailable'; error: unknown };

export function useFilesystem(
  onObservation: (result: FilesystemObservation) => void,
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
  const latestRead = useRef<symbol | null>(null);
  useEffect(
    () => () => {
      latestRead.current = null;
    },
    [],
  );
  const refresh = useCallback(
    async (trigger: RefreshTrigger = 'requested') => {
      const read = Symbol();
      latestRead.current = read;
      let entries: Entry[];
      try {
        ({ entries } = await filesystemApi.entries());
        desktopDatabase.assertActive();
      } catch (error) {
        if (latestRead.current === read)
          onObservation({ phase: 'unavailable', error });
        // A command still receives the outcome of the read it requested.
        throw error;
      }
      if (latestRead.current !== read) return;
      setEntries(entries);
      onObservation({ phase: 'available', trigger });
    },
    [onObservation],
  );
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
  return { data, refresh };
}
