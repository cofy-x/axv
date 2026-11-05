import type { DesktopAPI, DesktopWindow, MenuItem } from '@cofy-x/axv-nextstep';
import type { Entry } from '../../filesystem/types';
import type { FilesystemData } from '../../filesystem/context';
import { downloadEntry, openEntry } from '../../filesystem/presentation';
import {
  newEntry,
  renameEntry,
  getInfo,
  trashEntries,
  pasteEntries,
  duplicateEntries,
} from '../../filesystem/commands';
import { runCommand } from '../../shared/desktop/dialogs';

export function createFilesMenu(
  data: FilesystemData,
  window: DesktopWindow,
  desktop: DesktopAPI,
) {
  const ids =
    data.selections[window.id] ||
    (Array.isArray(window.viewState.selectionIds)
      ? window.viewState.selectionIds.filter(
          (id): id is string => typeof id === 'string',
        )
      : []);
  const selected = data.entries.filter((entry) => ids.includes(entry.id));
  const first = selected[0];
  const command = (action: () => Promise<void>) => () => {
    void runCommand(desktop, action);
  };
  return [
    {
      id: 'file',
      label: 'File',
      items: [
        {
          label: 'New Folder',
          action: command(() =>
            newEntry(
              data,
              desktop,
              typeof window.viewState.folderId === 'string'
                ? window.viewState.folderId
                : window.params.folderId || 'documents',
              'folder',
            ),
          ),
        },
        {
          label: 'New Text File',
          action: command(() =>
            newEntry(
              data,
              desktop,
              typeof window.viewState.folderId === 'string'
                ? window.viewState.folderId
                : window.params.folderId || 'documents',
              'file',
            ),
          ),
        },
        { separator: true },
        {
          label: 'Open',
          disabled: !first,
          action: () => {
            if (first) openEntry(first, desktop);
          },
        },
        {
          label: 'Inspect',
          disabled: selected.length !== 1,
          action: command(() => getInfo(desktop, first!, data)),
        },
        {
          label: 'Rename',
          disabled: selected.length !== 1 || first?.system,
          action: command(() => renameEntry(data, desktop, first!)),
        },
        { separator: true },
        {
          label: 'Close Window',
          action: () => {
            void desktop.closeWindow(window.id);
          },
        },
      ],
    },
    {
      id: 'edit',
      label: 'Edit',
      items: [
        {
          label: 'Cut',
          disabled: !ids.length || selected.some((entry) => entry.system),
          action: () => data.setClipboard({ ids, mode: 'cut' }),
        },
        {
          label: 'Copy',
          disabled: !ids.length || selected.some((entry) => entry.system),
          action: () => data.setClipboard({ ids, mode: 'copy' }),
        },
        {
          label: 'Paste Here',
          disabled: !data.clipboard,
          action: command(() =>
            pasteEntries(
              data,
              typeof window.viewState.folderId === 'string'
                ? window.viewState.folderId
                : window.params.folderId || 'documents',
            ),
          ),
        },
        { separator: true },
        {
          label: 'Recycle',
          disabled: !ids.length || selected.some((entry) => entry.system),
          action: command(() => trashEntries(data, desktop, ids)),
        },
      ],
    },
  ];
}

export interface FileMenuOptions {
  data: FilesystemData;
  desktop: DesktopAPI;
  folderId: string;
  selected: string[];
  selection: Entry[];
  view: 'columns' | 'list';
  onViewChange: (view: 'columns' | 'list') => void;
  onImport: () => void;
}

function createSelectionMenu(
  { data, desktop, selected, selection }: FileMenuOptions,
  entry?: Entry,
): MenuItem[] {
  const command = (action: () => Promise<void>) => {
    void runCommand(desktop, action);
  };
  const targets = entry && !selected.includes(entry.id) ? [entry] : selection;
  const ids = targets.map((item) => item.id);
  const first = entry || targets[0];
  const protectedSelection = targets.some((item) => item.system);
  return [
    {
      id: 'open',
      label: 'Open',
      disabled: !first,
      action: () => {
        if (first) openEntry(first, desktop);
      },
    },
    {
      id: 'inspect',
      label: 'Inspect',
      disabled: !first,
      action: () => {
        if (first) command(() => getInfo(desktop, first, data));
      },
    },
    { separator: true },
    {
      id: 'rename',
      label: 'Rename',
      disabled: targets.length !== 1 || first?.system,
      action: () => {
        if (first) command(() => renameEntry(data, desktop, first));
      },
    },
    {
      id: 'duplicate',
      label: 'Duplicate',
      disabled: !targets.length || protectedSelection,
      action: () => command(() => duplicateEntries(data, ids)),
    },
    {
      id: 'copy',
      label: 'Copy',
      disabled: !targets.length || protectedSelection,
      action: () => data.setClipboard({ ids, mode: 'copy' }),
    },
    {
      id: 'cut',
      label: 'Cut',
      disabled: !targets.length || protectedSelection,
      action: () => data.setClipboard({ ids, mode: 'cut' }),
    },
    {
      id: 'download',
      label: 'Download',
      disabled: first?.kind !== 'file',
      action: () => {
        if (first) downloadEntry(first);
      },
    },
    { separator: true },
    {
      id: 'recycle',
      label: 'Recycle',
      disabled: !targets.length || protectedSelection,
      action: () => command(() => trashEntries(data, desktop, ids)),
    },
  ];
}

export function createFilesActionMenu(options: FileMenuOptions): MenuItem[] {
  return [
    ...createSelectionMenu(options).slice(3),
    { separator: true },
    ...createFilesContextMenu(options).filter((item) =>
      ['paste', 'refresh'].includes(item.id || ''),
    ),
  ];
}

export function createFilesNewMenu(options: FileMenuOptions): MenuItem[] {
  return createFilesContextMenu(options).filter((item) =>
    ['new-folder', 'new-file'].includes(item.id || ''),
  );
}

export function createFilesContextMenu(
  {
    data,
    desktop,
    folderId,
    selected,
    selection,
    view,
    onViewChange,
    onImport,
  }: FileMenuOptions,
  entry?: Entry,
): MenuItem[] {
  const command = (action: () => Promise<void>) => {
    void runCommand(desktop, action);
  };
  if (entry)
    return createSelectionMenu(
      {
        data,
        desktop,
        folderId,
        selected,
        selection,
        view,
        onViewChange,
        onImport,
      },
      entry,
    );
  return [
    {
      id: 'new-folder',
      label: 'New Folder',
      action: () => command(() => newEntry(data, desktop, folderId, 'folder')),
    },
    {
      id: 'new-file',
      label: 'New Text File',
      action: () => command(() => newEntry(data, desktop, folderId, 'file')),
    },
    { label: 'Import Files', action: () => onImport() },
    {
      id: 'paste',
      label: 'Paste Here',
      disabled: !data.clipboard,
      action: () => command(() => pasteEntries(data, folderId)),
    },
    { separator: true },
    {
      id: 'view',
      label: 'View',
      children: [
        {
          label: 'as Columns',
          checked: view === 'columns',
          action: () => onViewChange('columns'),
        },
        {
          label: 'as List',
          checked: view === 'list',
          action: () => onViewChange('list'),
        },
      ],
    },
    {
      id: 'refresh',
      label: 'Refresh Folder',
      action: () => command(data.refresh),
    },
  ];
}
