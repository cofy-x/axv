import type { DesktopAPI, DesktopIcon, MenuItem } from '@cofy-x/axv-nextstep';
import type { FilesystemData } from '../filesystem/context';
import { isInTrash } from '../filesystem/tree';
import {
  downloadEntry,
  entryIcon,
  openEntry,
} from '../filesystem/presentation';
import {
  newEntry,
  renameEntry,
  getInfo,
  trashEntries,
  pasteEntries,
} from '../filesystem/commands';
import { runCommand } from '../shared/desktop/dialogs';
import { openTerminalWindow } from '../features/terminal/definition';

export function createDesktopIcons(
  data: FilesystemData,
  getDesktop: () => DesktopAPI | null,
  codexInstalled = false,
): DesktopIcon[] {
  const { entries } = data;
  return [
    {
      id: 'system:computer',
      label: 'Workspace',
      icon: 'computer',
      appId: 'files',
      params: { folderId: 'root' },
    },
    {
      id: 'system:documents',
      label: 'Documents',
      icon: 'folder',
      appId: 'files',
      params: { folderId: 'documents' },
    },
    {
      id: 'system:notes',
      label: 'Text Editor',
      icon: 'notes',
      appId: 'notes',
      applicationLauncher: true,
    },
    {
      id: 'system:terminal',
      label: 'Terminal',
      icon: 'terminal',
      appId: 'terminal',
      applicationLauncher: true,
      onOpen: () => {
        const desktop = getDesktop();
        if (desktop) openTerminalWindow(desktop);
      },
    },
    {
      id: 'system:project-files',
      label: 'Project Files',
      icon: 'project',
      appId: 'project-files',
      applicationLauncher: true,
    },
    {
      id: 'system:remote-access',
      label: 'Tailscale',
      icon: 'mesh-network',
      appId: 'remote-access',
      applicationLauncher: true,
    },
    {
      id: 'system:appearance',
      label: 'Preferences',
      icon: 'appearance',
      appId: 'appearance',
      applicationLauncher: true,
    },
    ...(codexInstalled
      ? [
          {
            id: 'system:codex',
            label: 'Codex',
            icon: 'companion' as const,
            appId: 'codex',
            applicationLauncher: true,
          },
        ]
      : []),
    {
      id: 'system:machines',
      label: 'Virtual Machines',
      icon: 'virtual-machines',
      appId: 'machines',
      applicationLauncher: true,
    },
    {
      id: 'system:gallery',
      label: 'Component Gallery',
      icon: 'gallery',
      appId: 'gallery',
      applicationLauncher: true,
    },
    ...entries
      .filter(
        (entry) => entry.parentId === 'desktop' && !isInTrash(entry, entries),
      )
      .map<DesktopIcon>((entry) => ({
        id: entry.id,
        label: entry.name,
        icon: entryIcon(entry),
        trashable: !entry.system,
        onOpen: () => {
          const desktop = getDesktop();
          if (desktop) openEntry(entry, desktop);
        },
      })),
    {
      id: 'system:trash',
      label: 'Recycler',
      icon: 'trash',
      appId: 'trash',
      isTrash: true,
    },
  ];
}

export function createDesktopMenu(
  data: FilesystemData,
  ids: string[],
  desktop: DesktopAPI,
  icons: DesktopIcon[] = [],
): MenuItem[] {
  const { entries, clipboard } = data;
  const selected = entries.filter(
    (entry) => ids.includes(entry.id) && !entry.system,
  );
  const command = (action: () => Promise<void>) => () => {
    void runCommand(desktop, action);
  };
  if (selected.length && selected.length === ids.length)
    return [
      {
        label: 'Open',
        action: () => selected.forEach((entry) => openEntry(entry, desktop)),
      },
      {
        label: 'Inspect',
        disabled: selected.length !== 1,
        action: command(() => getInfo(desktop, selected[0]!, data)),
      },
      {
        label: 'Rename',
        disabled: selected.length !== 1,
        action: command(() => renameEntry(data, desktop, selected[0]!)),
      },
      {
        label: 'Download',
        disabled: selected.length !== 1 || selected[0]?.kind === 'folder',
        action: () => downloadEntry(selected[0]!),
      },
      { separator: true },
      {
        label: 'Recycle',
        action: command(() => trashEntries(data, desktop, ids)),
      },
    ];
  const selectedIcons = icons.filter((icon) => ids.includes(icon.id));
  if (selectedIcons.length)
    return [
      {
        label: 'Open',
        action: () =>
          selectedIcons.forEach((icon) => {
            if (icon.onOpen) icon.onOpen();
            else if (icon.appId) desktop.openApp(icon.appId, icon.params);
          }),
      },
    ];
  return [
    {
      label: 'New Folder',
      action: command(() => newEntry(data, desktop, 'desktop', 'folder')),
    },
    {
      label: 'New Text File',
      action: command(() => newEntry(data, desktop, 'desktop', 'file')),
    },
    {
      label: 'Paste',
      disabled: !clipboard,
      action: command(() => pasteEntries(data, 'desktop')),
    },
    { separator: true },
    { label: 'Arrange Icons', action: desktop.arrangeIcons },
    { label: 'Preferences', action: () => desktop.openApp('appearance') },
  ];
}
