import type { DesktopAPI, DesktopIcon, MenuItem } from '@cofy-x/axv-nextstep';
import type { Machine } from './api';
import type { ComputeSnapshot } from './compute-store';

export function machineDesktopId(machine: Pick<Machine, 'hostId' | 'id'>) {
  return `machine:${JSON.stringify([machine.hostId, machine.id])}`;
}

export function openMachineInspector(desktop: DesktopAPI, machine: Machine) {
  desktop.openApp(
    'machine-inspector',
    { hostId: machine.hostId, machineId: machine.id },
    { title: `${machine.spec.name} — Inspector` },
  );
}

function machineStatus(
  machine: Machine,
  unavailable: boolean,
): DesktopIcon['status'] {
  if (unavailable) return { label: 'Unavailable', tone: 'warning' };
  if (machine.status === 'running')
    return { label: machine.ready ? 'Ready' : 'Running', tone: 'success' };
  if (machine.status === 'stopped')
    return { label: 'Stopped', tone: 'neutral' };
  if (machine.status === 'failed') return { label: 'Failed', tone: 'error' };
  return {
    label: machine.status
      ? machine.status[0].toUpperCase() + machine.status.slice(1)
      : 'Unknown',
    tone: 'warning',
  };
}

/** Stable host-bound projections of the shared inventory; these are not documents. */
export function createMachineDesktopIcons(
  snapshot: ComputeSnapshot,
  getDesktop: () => DesktopAPI | null,
): DesktopIcon[] {
  return snapshot.machines
    .filter((machine) => machine.hostId && machine.status !== 'deleted')
    .sort(
      (a, b) =>
        a.createdAt.localeCompare(b.createdAt) ||
        machineDesktopId(a).localeCompare(machineDesktopId(b)),
    )
    .map((machine) => ({
      id: machineDesktopId(machine),
      label: machine.spec.name,
      icon: 'virtual-machine',
      appId: 'machine-inspector',
      params: { hostId: machine.hostId, machineId: machine.id },
      status: machineStatus(
        machine,
        !!snapshot.error || snapshot.info?.hostId !== machine.hostId,
      ),
      onOpen: () => {
        const desktop = getDesktop();
        if (desktop) openMachineInspector(desktop, machine);
      },
    }));
}

export function createMachineDesktopMenu(
  icons: DesktopIcon[],
  ids: string[],
): MenuItem[] | undefined {
  const selected = icons.filter((icon) => ids.includes(icon.id));
  if (!selected.length || selected.length !== ids.length) return;
  const open = () => selected.forEach((icon) => icon.onOpen?.());
  return [
    { label: 'Open', action: open },
    { label: 'Inspect', action: open },
  ];
}
