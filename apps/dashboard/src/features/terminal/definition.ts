import type { AppDefinition, DesktopAPI, MenuItem } from '@cofy-x/axv-nextstep';
import type {
  WindowCommand,
  WindowCommands,
} from '../../shared/desktop/window-commands';
import { TerminalWindow } from './TerminalWindow';

export function openTerminalWindow(desktop: DesktopAPI, projectPath?: string) {
  const titles = new Set(desktop.windows.map((window) => window.title));
  const project = projectPath?.split(/[\\/]/).filter(Boolean).at(-1);
  const baseTitle =
    projectPath === undefined
      ? 'Terminal'
      : `Terminal · ${project || projectPath || 'Project'}`;
  let number = 1;
  let title = baseTitle;
  while (titles.has(title)) title = `${baseTitle} ${++number}`;
  const params = {
    sessionId: crypto.randomUUID(),
    ...(projectPath === undefined ? {} : { projectPath }),
  };
  return desktop.openApp('terminal', params, { title });
}

export function terminalSessionMenu(
  commands: WindowCommands,
  windowId: string,
  { compact = false } = {},
): MenuItem[] {
  const command = (label: string, id: WindowCommand): MenuItem => ({
    label,
    disabled: !commands.canExecute(windowId, id),
    action: () => {
      void commands.execute(windowId, id);
    },
  });
  const items = [
    command('Clear', 'terminal:clear'),
    command('Details', 'terminal:details'),
    command('Paste text', 'terminal:paste-text'),
  ];
  if (!compact)
    items.push(
      { separator: true },
      command('Interrupt', 'terminal:interrupt'),
      command('New Session', 'terminal:new-session'),
    );
  return items;
}

export function createTerminalApp(commands: WindowCommands): AppDefinition {
  return {
    id: 'terminal',
    title: 'Terminal',
    icon: 'terminal',
    component: TerminalWindow,
    launch: (desktop) => {
      openTerminalWindow(desktop);
    },
    windowKey: (params) => params.sessionId || 'main',
    defaultSize: { width: 700, height: 460 },
    minSize: { width: 360, height: 280 },
    menu: (window, desktop) => {
      const command = (label: string, id: WindowCommand): MenuItem => ({
        label,
        disabled: !commands.canExecute(window.id, id),
        action: () => {
          void commands.execute(window.id, id);
        },
      });
      return [
        {
          id: 'file',
          label: 'File',
          items: [
            {
              label: 'New Terminal Window',
              action: () => {
                openTerminalWindow(desktop);
              },
            },
            {
              label: 'Close Window',
              shortcut: '⌘W',
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
            command('Copy', 'terminal:copy'),
            command('Paste', 'terminal:paste'),
            command('Paste text', 'terminal:paste-text'),
            command('Clear', 'terminal:clear'),
          ],
        },
        {
          id: 'session',
          label: 'Session',
          items: terminalSessionMenu(commands, window.id),
        },
      ];
    },
  };
}
