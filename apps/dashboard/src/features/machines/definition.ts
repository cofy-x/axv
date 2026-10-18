import type { AppDefinition, MenuItem } from '@cofy-x/axv-nextstep';
import type {
  WindowCommand,
  WindowCommands,
} from '../../shared/desktop/window-commands';
import { Machines } from './Machines';
import { CreateMachine } from './CreateMachine';
import { MachineInspector } from './MachineInspector';
import { GuestTerminalApp } from './GuestTerminalApp';

export const machinesApp: AppDefinition = {
  id: 'machines',
  title: 'Virtual Machines',
  icon: 'virtual-machines',
  component: Machines,
  windowKey: () => 'main',
  defaultSize: { width: 800, height: 620 },
  minSize: { width: 320, height: 300 },
};

export const createMachineApp: AppDefinition = {
  id: 'machine-create',
  title: 'New VM',
  icon: 'virtual-machine',
  component: CreateMachine,
  launchable: false,
  windowKey: () => 'main',
  defaultSize: { width: 470, height: 600 },
  minSize: { width: 320, height: 350 },
};

export const machineInspectorApp: AppDefinition = {
  id: 'machine-inspector',
  title: 'VM Inspector',
  icon: 'virtual-machine',
  component: MachineInspector,
  launchable: false,
  kind: 'panel',
  windowKey: (params) =>
    params.hostId
      ? JSON.stringify([params.hostId, params.machineId])
      : params.machineId || 'main',
  defaultSize: { width: 420, height: 620 },
  minSize: { width: 300, height: 300 },
};

export function guestTerminalSessionMenu(
  commands: WindowCommands,
  windowId: string,
): MenuItem[] {
  const command = (label: string, id: WindowCommand): MenuItem => ({
    label,
    disabled: !commands.canExecute(windowId, id),
    action: () => {
      void commands.execute(windowId, id);
    },
  });
  return [
    command('Clear', 'terminal:clear'),
    command('Details', 'terminal:details'),
    command('Reconnect', 'guest-terminal:reconnect'),
    { separator: true },
    command('End Session', 'guest-terminal:end'),
  ];
}

export function createGuestTerminalApp(
  commands: WindowCommands,
): AppDefinition {
  return {
    id: 'guest-terminal',
    title: 'Guest Terminal',
    icon: 'terminal',
    component: GuestTerminalApp,
    launchable: false,
    windowKey: (params) =>
      params.hostId
        ? JSON.stringify([params.hostId, params.machineId, params.terminalId])
        : `${params.machineId}:${params.terminalId}`,
    defaultSize: { width: 740, height: 470 },
    minSize: { width: 320, height: 300 },
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
            command('Copy', 'terminal:copy'),
            command('Paste', 'terminal:paste'),
            command('Clear', 'terminal:clear'),
            command('Interrupt', 'terminal:interrupt'),
          ],
        },
        {
          id: 'session',
          label: 'Session',
          items: guestTerminalSessionMenu(commands, window.id),
        },
      ];
    },
  };
}
