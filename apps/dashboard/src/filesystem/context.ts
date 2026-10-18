import { createContext, useContext } from 'react';
import type { Entry } from './types';

export interface FilesystemData {
  entries: Entry[];
  refresh: () => Promise<void>;
  selectionWindowId: string | null;
  metadataChanged: (entry: Entry, previousRevision: number) => void;
  onMetadataChanged: (
    listener: (entry: Entry, previousRevision: number) => void,
  ) => () => void;
  selections: Record<string, string[]>;
  select: (windowId: string, ids: string[]) => void;
  clipboard: { ids: string[]; mode: 'copy' | 'cut' } | null;
  setClipboard: (clipboard: FilesystemData['clipboard']) => void;
}

export const FilesystemContext = createContext<FilesystemData | null>(null);

export function useFilesystemData() {
  const value = useContext(FilesystemContext);
  if (!value) throw new Error('Desktop applications need FilesystemContext.');
  return value;
}
