import type {
  DesktopAPI,
  DesktopWindow,
  MenuGroup,
  MenuItem,
} from '@cofy-x/axv-nextstep';
import type {
  WindowCommand,
  WindowCommands,
} from '../../shared/desktop/window-commands';
import type { VMAction } from './lifecycle';

export type MachineMenuAction = VMAction | 'terminal' | 'inspect';

/** A stale menu cannot dispatch its selected-machine command to a new target. */
export function machineMenuCommand(
  action: MachineMenuAction,
  hostId: string,
  machineId: string,
): WindowCommand {
  return `machines:${action}:${JSON.stringify([hostId, machineId])}`;
}

export function createMachinesMenu(
  commands: WindowCommands,
  window: DesktopWindow,
  desktop: DesktopAPI,
): MenuGroup[] {
  const hostId = String(window.viewState.machineHostId || '');
  const machineId = String(window.viewState.machineId || '');
  const command = (label: string, id: WindowCommand): MenuItem => ({
    id,
    label,
    disabled: !commands.canExecute(window.id, id),
    action: () => {
      void commands.execute(window.id, id);
    },
  });
  const selected = (label: string, action: MachineMenuAction) =>
    command(label, machineMenuCommand(action, hostId, machineId));
  const environment =
    !commands.canExecute(window.id, 'machines:view:machines') ||
    window.viewState.vmEnvironment === true;
  return [
    {
      id: 'machines',
      label: 'Machines',
      items: [
        command('New VM', 'machines:new'),
        command('Refresh', 'machines:refresh'),
      ],
    },
    {
      id: 'machine',
      label: 'Selected VM',
      items: [
        selected('Inspect', 'inspect'),
        selected('New Terminal', 'terminal'),
        { separator: true },
        selected('Start', 'start'),
        selected('Stop', 'stop'),
        selected('Restart', 'restart'),
        selected('Force Stop', 'force-stop'),
        { separator: true },
        selected('Delete', 'delete'),
      ],
    },
    {
      id: 'view',
      label: 'View',
      items: [
        ...(
          [
            ['VMs', 'machines'],
            ['Volumes', 'volumes'],
            ['Images', 'images'],
            ['Activity', 'operations'],
          ] as const
        ).map(([label, tab]) => ({
          ...command(label, `machines:view:${tab}`),
          checked:
            !environment && (window.viewState.vmTab || 'machines') === tab,
        })),
        { separator: true },
        {
          label: 'Environment',
          checked: environment,
          action: () =>
            desktop.updateWindowState(window.id, {
              vmEnvironment: !environment,
            }),
        },
      ],
    },
  ];
}
