import type { AppDefinition, DesktopAPI, MenuItem } from '@cofy-x/axv-nextstep';
import type {
  WindowCommand,
  WindowCommands,
} from '../../shared/desktop/window-commands';
import { CodexApp } from './CodexApp';
import { deferWindow } from '../../shared/desktop/defer-window';
import { runCommand } from '../../shared/desktop/dialogs';
import { sessionWindowTitle } from './api';
import type { CodexSessionRecord } from './api';

const CodexEnvironment = deferWindow(() =>
  import('./CodexEnvironment').then((module) => module.CodexEnvironment),
);

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

export function createCodexApp(commands: WindowCommands): AppDefinition {
  return {
    id: 'codex',
    title: 'Codex',
    icon: 'companion',
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
      const isRun = !!(window.params.sessionId && window.params.runId);
      const command = (label: string, id: WindowCommand): MenuItem => ({
        id,
        label,
        disabled: !commands.canExecute(window.id, id),
        action: () => {
          void commands.execute(window.id, id);
        },
      });
      return [
        {
          id: 'file',
          label: 'Sessions',
          items: [
            ...(!isRun
              ? [
                  command('New Session', 'codex:new-session'),
                  ...(!window.params.vmProjectId
                    ? [command('New in Folder', 'codex:new-in-folder')]
                    : []),
                  command('Refresh', 'codex:refresh'),
                  { separator: true },
                ]
              : []),
            {
              label: 'All Sessions',
              checked: !isRun && !window.params.vmProjectId,
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
          ],
        },
        ...(isRun
          ? [
              {
                id: 'edit',
                label: 'Terminal',
                items: codexEditMenu(commands, window.id),
              },
              {
                id: 'session',
                label: 'Session',
                items: codexSessionMenu(commands, window.id),
              },
              {
                id: 'project',
                label: 'Project',
                items: (
                  [
                    ['Attachments', 'codex:attachments'],
                    ['Project Files', 'codex:project-files'],
                    ['Tasks', 'codex:tasks'],
                    ['Host Terminal', 'codex:terminal'],
                  ] as const
                ).map(([label, id]) => ({
                  id,
                  label,
                  disabled: !commands.canExecute(window.id, id),
                  action: () => {
                    void runCommand(desktop, async () => {
                      await commands.execute(window.id, id);
                    });
                  },
                })),
              },
            ]
          : []),
      ];
    },
  };
}
