import type { Machine, Operation, Volume } from './api';

export function humanize(value: string) {
  const words = value.replaceAll('-', ' ');
  return words.charAt(0).toUpperCase() + words.slice(1);
}

export function operationTarget(
  operation: Operation,
  machines: Machine[],
  volumes: Volume[],
) {
  return (
    machines.find((item) => item.id === operation.machineId)?.spec.name ||
    volumes.find((item) => item.id === operation.volumeId)?.name ||
    operation.machineId ||
    operation.volumeId ||
    operation.resourceId ||
    'Host'
  );
}

export function machineGuidance(
  machine: Machine,
  operations: Operation[],
): string | undefined {
  const operation = operations.find(
    (op) =>
      (op.machineId === machine.id || op.locks?.includes(machine.id)) &&
      (op.status === 'queued' || op.status === 'running'),
  );
  if (operation)
    return `${humanize(operation.action)} is ${operation.status}${operation.phase ? ` · ${humanize(operation.phase)}` : ''}. Open Activity for progress.`;
  if (machine.pendingSpec)
    return 'Configuration change pending. Review or retry the change.';
  if (!machine.spec.volumeId)
    return 'Attach a project volume before starting this VM.';
  if (machine.status === 'running' && machine.guestState === 'not-installed')
    return 'Basic VM management only. This image has no guest enhancements.';
  if (machine.status === 'running' && machine.guestState === 'incompatible')
    return 'Guest protocol incompatible. Guest enhancements require a compatible AXV image.';
  if (machine.status === 'running' && !machine.ready)
    return 'Guest not ready. Terminals require a guest connection.';
  if (machine.ready && !machine.guestCapabilities?.includes('terminals'))
    return 'Guest terminals are unavailable in this image.';
  return machine.reason || undefined;
}

export function guestTerminalReason(machine?: Machine) {
  if (!machine) return 'Select a VM first.';
  if (machine.status !== 'running' || machine.desired !== 'running')
    return 'Start this VM before opening a guest terminal.';
  if (machine.guestState === 'not-installed')
    return 'This image does not include AXV guest enhancements.';
  if (machine.guestState === 'incompatible')
    return 'The AXV guest protocol is incompatible. Select a compatible image.';
  if (!machine.ready) return 'The AXV guest is not ready.';
  if (!machine.guestCapabilities?.includes('terminals'))
    return 'This image does not provide guest terminals.';
  return undefined;
}
