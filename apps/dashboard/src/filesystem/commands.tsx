import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { filesystemApi } from './api';
import type { Entry } from './types';
import type { FilesystemData } from './context';
import { affectedEntries, isInTrash, selectedRoots } from './tree';
import { openEntry } from './presentation';
import { nameDialog } from '../shared/desktop/dialogs';

export async function newEntry(
  data: FilesystemData,
  desktop: DesktopAPI,
  parentId: string,
  kind: Entry['kind'],
) {
  const name = await nameDialog(
    desktop,
    kind === 'folder' ? 'New Folder' : 'New Text File',
    kind === 'folder' ? 'Untitled Folder' : 'Untitled.txt',
  );
  if (!name) return;
  const result = await filesystemApi.create(
    parentId,
    name,
    kind,
    kind === 'file' ? '' : undefined,
  );
  await data.refresh();
  if (kind === 'file') openEntry(result.entry, desktop);
}

export async function renameEntry(
  data: FilesystemData,
  desktop: DesktopAPI,
  entry: Entry,
) {
  const name = await nameDialog(desktop, 'Rename', entry.name);
  if (!name || name === entry.name) return;
  const result = await filesystemApi.patch(entry.id, {
    name,
    revision: entry.revision,
  });
  data.metadataChanged(result.entry, entry.revision);
  await data.refresh();
  for (const window of desktop.windows) {
    if (
      window.params.entryId === entry.id ||
      window.params.folderId === entry.id
    )
      desktop.setTitle(window.id, name);
  }
}

export async function trashEntries(
  data: FilesystemData,
  desktop: DesktopAPI,
  ids: string[],
) {
  const live = data.entries.filter(
    (entry) => !entry.system && !isInTrash(entry, data.entries),
  );
  const entries = selectedRoots(live, ids);
  if (!entries.length) return;
  const affected = new Set(
    affectedEntries(
      live,
      entries.map((entry) => entry.id),
    ).map((entry) => entry.id),
  );
  for (const window of desktop.windows.filter((window) =>
    affected.has(window.params.entryId || ''),
  )) {
    // All dirty descendants must agree to close before the first mutation.
    if (!(await desktop.closeWindow(window.id))) return;
  }
  try {
    for (const entry of entries) await filesystemApi.action(entry.id, 'trash');
  } finally {
    await data.refresh();
  }
}

export function getInfo(
  desktop: DesktopAPI,
  entry: Entry,
  data?: FilesystemData,
): Promise<void> {
  data?.select('inspection', [entry.id]);
  const id = desktop.openApp('inspector', {}, { title: 'Inspector' });
  desktop.updateWindowState(id, { targetId: entry.id });
  return Promise.resolve();
}

export async function updateMetadata(
  data: FilesystemData,
  entry: Entry,
  patch: { name?: string; parentId?: string },
) {
  const result = await filesystemApi.patch(entry.id, {
    ...patch,
    revision: entry.revision,
  });
  data.metadataChanged(result.entry, entry.revision);
  return result.entry;
}

export async function pasteEntries(data: FilesystemData, parentId: string) {
  if (!data.clipboard) return;
  const roots = selectedRoots(
    data.entries.filter(
      (entry) => !entry.system && !isInTrash(entry, data.entries),
    ),
    data.clipboard.ids,
  );
  try {
    for (const entry of roots) {
      if (data.clipboard.mode === 'cut')
        await updateMetadata(data, entry, { parentId });
      else await filesystemApi.action(entry.id, 'duplicate', parentId);
    }
    if (data.clipboard.mode === 'cut') data.setClipboard(null);
  } finally {
    await data.refresh();
  }
}

export async function duplicateEntries(data: FilesystemData, ids: string[]) {
  const live = data.entries.filter((entry) => !isInTrash(entry, data.entries));
  try {
    for (const entry of selectedRoots(live, ids))
      await filesystemApi.action(entry.id, 'duplicate');
  } finally {
    await data.refresh();
  }
}

export async function moveEntries(
  data: FilesystemData,
  ids: string[],
  parentId: string,
) {
  const live = data.entries.filter((entry) => !isInTrash(entry, data.entries));
  try {
    for (const entry of selectedRoots(live, ids))
      await updateMetadata(data, entry, { parentId });
  } finally {
    await data.refresh();
  }
}
