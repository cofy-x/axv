import type { AppDefinition } from '@cofy-x/axv-nextstep';
import type { FilesystemData } from '../../filesystem/context';
import { newEntry } from '../../filesystem/commands';
import { runCommand } from '../../shared/desktop/dialogs';
import type { WindowCommands } from '../../shared/desktop/window-commands';
import { deferWindow } from '../../shared/desktop/defer-window';
import { desktopDatabase } from '../../shared/api/desktop';

const Notes = deferWindow(() =>
  import('./Notes').then((module) => module.Notes),
);

export function createNotesApp(
  data: FilesystemData,
  commands: WindowCommands,
): AppDefinition {
  return {
    id: 'notes',
    title: 'Text Editor',
    icon: 'notes',
    component: Notes,
    launchable: true,
    launch: (desktop) => {
      desktop.openApp('notes');
    },
    windowKey: (params) => params.draftId || params.entryId || 'launcher',
    defaultSize: { width: 490, height: 370 },
    minSize: { width: 300, height: 230 },
    menu: (window, desktop) => [
      {
        id: 'file',
        label: 'File',
        items: [
          {
            label: 'New Document',
            disabled: !!desktopDatabase.getSnapshot(),
            action: () => {
              void runCommand(desktop, async () => {
                desktopDatabase.assertActive();
                await newEntry(data, desktop, 'documents', 'file');
              });
            },
          },
          {
            label: 'Save',
            shortcut: '⌘S',
            disabled: !commands.canExecute(window.id, 'save'),
            action: () => {
              void commands.execute(window.id, 'save');
            },
          },
          {
            label: 'New View of Document',
            disabled: !window.params.entryId,
            action: () => {
              desktop.openApp(
                'notes',
                {
                  entryId: window.params.entryId!,
                  draftId: crypto.randomUUID(),
                },
                { title: window.title },
              );
            },
          },
          {
            label: 'Close Window',
            action: () => {
              void desktop.closeWindow(window.id);
            },
          },
        ],
      },
    ],
  };
}
