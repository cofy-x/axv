import type { AppDefinition, DesktopAPI, MenuItem } from '@cofy-x/axv-nextstep';
import type {
  WindowCommand,
  WindowCommands,
} from '../../shared/desktop/window-commands';
import { CodexApp } from './CodexApp';
import { CodexEnvironment } from './CodexEnvironment';
import { sessionWindowTitle } from './api';
import type { CodexSessionRecord } from './api';

export function codexEditMenu(
  commands: WindowCommands,
  windowId: string,
): MenuItem[] {
  const command = (label: string, id: WindowCommand): MenuItem => ({
    id,
    label,
    disabled: !commands.canExecute(windowId, id),
    action: () => {
      void commands.execute(windowId, id);
    },
  });
  return [
    command('Copy', 'codex:copy'),
    command('Paste', 'codex:paste'),
    command('Paste text', 'codex:paste-text'),
    command('Clear', 'codex:clear'),
  ];
}

export function codexSessionMenu(
  commands: WindowCommands,
  windowId: string,
): MenuItem[] {
  const archived = commands.canExecute(windowId, 'codex:unarchive');
  const command = (label: string, id: WindowCommand): MenuItem => ({
    id,
    label,
    disabled: !commands.canExecute(windowId, id),
    action: () => {
      void commands.execute(windowId, id);
    },
  });
  return [
    ...(commands.canExecute(windowId, 'codex:open-running')
      ? [command('Open Running Session', 'codex:open-running')]
      : [command('Continue', 'codex:continue')]),
    { separator: true },
    command('Rename', 'codex:rename'),
    command(
      archived ? 'Unarchive' : 'Archive',
      archived ? 'codex:unarchive' : 'codex:archive',
    ),
    command('Host Terminal', 'codex:terminal'),
    { separator: true },
    command('Details', 'codex:details'),
    command('Clear', 'codex:clear'),
    command('Interrupt', 'codex:interrupt'),
    command('Reconnect', 'codex:reconnect'),
    { separator: true },
    command('End Session', 'codex:end'),
  ];
}

export function codexMoreMenu(
  commands: WindowCommands,
  windowId: string,
  actions: {
    attachments: () => void;
    projectFiles: () => void;
    projectFilesAvailable: boolean;
  },
): MenuItem[] {
  const sessionItems = codexSessionMenu(commands, windowId);
  const management = sessionItems.filter((item) =>
    [
      'codex:rename',
      'codex:archive',
      'codex:unarchive',
      'codex:terminal',
      'codex:details',
    ].includes(item.id || ''),
  );
  return [
    ...codexEditMenu(commands, windowId),
    { separator: true },
    { label: 'Attachments', action: actions.attachments },
    {
      label: 'Project Files',
      disabled: !actions.projectFilesAvailable,
      action: actions.projectFiles,
    },
    { separator: true },
    ...management,
    { separator: true },
    ...sessionItems.filter((item) => item.id === 'codex:end'),
  ];
}

export function openCodexSession(
  desktop: DesktopAPI,
  session: CodexSessionRecord,
) {
  return desktop.openApp(
    'codex',
    { sessionId: session.id, runId: session.runId },
    { title: sessionWindowTitle(session) },
  );
}

export function openCodexEnvironment(desktop: DesktopAPI) {
  return desktop.openApp('codex-environment');
}

export function openVMProjectCodex(
  desktop: Pick<DesktopAPI, 'openApp'>,
  hostId: string,
  projectId: string,
  name?: string,
) {
  return desktop.openApp(
    'codex',
    { hostId, vmProjectId: projectId },
    { title: `Codex Sessions · ${name || 'VM project'}` },
  );
}

export const codexEnvironmentApp: AppDefinition = {
  id: 'codex-environment',
  title: 'Codex Environment',
  icon: 'info',
  kind: 'panel',
  launchable: false,
  component: CodexEnvironment,
  windowKey: () => 'main',
  defaultSize: { width: 430, height: 380 },
  minSize: { width: 300, height: 240 },
};

export function createCodexApp(
  commands: WindowCommands,
  installed: boolean,
): AppDefinition {
  return {
    id: 'codex',
    title: 'Codex',
    icon: 'companion',
    launchable: installed,
    component: CodexApp,
    windowKey: (params) =>
      params.sessionId && params.runId
        ? `${params.sessionId}:${params.runId}`
        : params.vmProjectId
          ? `vm:${params.hostId}:${params.vmProjectId}`
          : 'manager',
    launch: (desktop) => {
      desktop.openApp('codex');
    },
    defaultSize: { width: 820, height: 580 },
    minSize: { width: 360, height: 320 },
    menu: (window, desktop) => {
      return [
        {
          id: 'file',
          label: 'File',
          items: [
            {
              label: 'Codex Sessions',
              action: () => {
                desktop.openApp('codex');
              },
            },
            {
              label: 'Environment',
              action: () => {
                openCodexEnvironment(desktop);
              },
            },
            { separator: true },
            {
              label: 'Close Window',
              shortcut: '⌘W',
              action: () => {
                void desktop.closeWindow(window.id);
              },
            },
          ],
        },
        ...(window.params.sessionId && window.params.runId
          ? [
              {
                id: 'edit',
                label: 'Edit',
                items: codexEditMenu(commands, window.id),
              },
              {
                id: 'session',
                label: 'Session',
                items: codexSessionMenu(commands, window.id),
              },
            ]
          : []),
      ];
    },
  };
}
