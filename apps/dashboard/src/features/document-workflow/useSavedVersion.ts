import { desktopDatabase } from '../../shared/api/desktop';
import { useEffect, useRef, useState } from 'react';
import { documentsAPI } from '../../filesystem/documents';
import type { DocumentSnapshotInfo } from '../../filesystem/documents';
import { useDraftNamespace } from '../../shared/drafts/context';
import { loadSavedVersions } from './data';

export type SavedVersionChoice =
  | {
      kind: 'saved';
      entryId: string;
      revision: number;
      name: string;
      content: string;
    }
  | { kind: 'snapshot'; snapshot: DocumentSnapshotInfo };

export type SavedVersionData = Awaited<ReturnType<typeof loadSavedVersions>>;

export interface SavedVersionState {
  scope: string;
  data?: SavedVersionData;
  choice?: SavedVersionChoice;
  loading: boolean;
  error?: string;
}

export function currentSavedVersion(
  data: SavedVersionData,
): SavedVersionChoice | undefined {
  const entry = data.entry;
  return entry?.editable && !entry.trashedAt && typeof data.content === 'string'
    ? {
        kind: 'saved',
        entryId: entry.id,
        revision: entry.revision,
        name: entry.name,
        content: data.content,
      }
    : undefined;
}

export function versionInfo(choice: SavedVersionChoice) {
  return choice.kind === 'saved' ? choice : choice.snapshot;
}

export function scopedSavedVersion(
  scope: string,
  previous: SavedVersionState,
): SavedVersionState {
  return previous.scope === scope ? previous : { scope, loading: true };
}

export function loadedSavedVersion(
  scope: string,
  previous: SavedVersionState,
  data: SavedVersionData,
): SavedVersionState {
  return {
    scope,
    data,
    // Refreshing metadata must not replace a version already reviewed.
    choice:
      scopedSavedVersion(scope, previous).choice || currentSavedVersion(data),
    loading: false,
  };
}

export function selectedSavedVersion(
  state: SavedVersionState,
  value: string,
): SavedVersionChoice | undefined {
  if (value === 'reviewed') return state.choice;
  if (value === 'saved') return state.data && currentSavedVersion(state.data);
  if (state.choice?.kind === 'snapshot' && state.choice.snapshot.id === value)
    return state.choice;
  const snapshot = state.data?.snapshots.find((item) => item.id === value);
  return snapshot ? { kind: 'snapshot', snapshot } : undefined;
}

export async function resolveSavedVersion(
  databaseId: string,
  entryId: string,
  choice: SavedVersionChoice | undefined,
  captured: Map<string, DocumentSnapshotInfo>,
  capture = documentsAPI.snapshot,
) {
  if (!choice) throw new Error('Choose a saved version.');
  const info = versionInfo(choice);
  if (
    info.entryId !== entryId ||
    (choice.kind === 'snapshot' && choice.snapshot.databaseId !== databaseId)
  )
    throw new Error('Choose a saved version from this document.');
  if (choice.kind === 'snapshot') return choice.snapshot;
  const key = JSON.stringify([databaseId, choice.entryId, choice.revision]);
  const existing = captured.get(key);
  if (existing) return existing;
  const { snapshot } = await capture(choice.entryId, choice.revision);
  if (
    snapshot.databaseId !== databaseId ||
    snapshot.entryId !== choice.entryId ||
    snapshot.revision !== choice.revision
  )
    throw new Error(
      'The captured version does not match the reviewed document.',
    );
  desktopDatabase.assertUnchanged();
  captured.set(key, snapshot);
  return snapshot;
}

export function useSavedVersion(entryId: string, refreshKey = 0) {
  const { databaseId } = useDraftNamespace();
  const scope = JSON.stringify([databaseId, entryId]);
  const [refresh, setRefresh] = useState(0);
  const [state, setState] = useState<SavedVersionState>({
    scope,
    loading: true,
  });
  const visible = scopedSavedVersion(scope, state);
  const captured = useRef(new Map<string, DocumentSnapshotInfo>());

  useEffect(() => {
    let current = true;
    void loadSavedVersions(entryId)
      .then((data) => {
        desktopDatabase.assertUnchanged();
        if (current)
          setState((previous) => loadedSavedVersion(scope, previous, data));
      })
      .catch((error: unknown) => {
        if (current)
          setState((previous) => ({
            ...scopedSavedVersion(scope, previous),
            loading: false,
            error: error instanceof Error ? error.message : String(error),
          }));
      });
    return () => {
      current = false;
    };
  }, [entryId, scope, refresh, refreshKey]);

  function reload() {
    setState((previous) => ({
      ...scopedSavedVersion(scope, previous),
      loading: true,
      error: '',
    }));
    setRefresh((value) => value + 1);
  }

  function choose(value: string) {
    setState((previous) => {
      const current = scopedSavedVersion(scope, previous);
      return { ...current, choice: selectedSavedVersion(current, value) };
    });
  }

  function resolve() {
    return resolveSavedVersion(
      databaseId,
      entryId,
      visible.choice,
      captured.current,
    );
  }

  return {
    data: visible.data,
    choice: visible.choice,
    loading: visible.loading,
    error: visible.error,
    choose,
    reload,
    resolve,
  };
}

export type SavedVersionController = ReturnType<typeof useSavedVersion>;
