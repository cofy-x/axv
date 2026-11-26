import type { ImageInfo, Machine, Operation, Volume } from './api';
import type { VMAction } from './lifecycle';
import { isProjectBackup } from './volume-policy';

export const linuxVMSetupGuide =
  'https://github.com/cofy-x/axv/blob/main/docs/startup.md#linux-privilege-boundary';

export function humanize(value: string) {
  const words = value.replaceAll('-', ' ');
  return words.charAt(0).toUpperCase() + words.slice(1);
}

export function operationLabel(operation: Operation, volumes: Volume[]) {
  if (operation.action === 'copy-volume') {
    const volume = volumes.find((item) => item.id === operation.volumeId);
    if (volume?.restoredFrom) return 'Restore volume';
    if (volume && isProjectBackup(volume)) return 'Back up volume';
  }
  return humanize(operation.action);
}

export function operationPhase({ action, phase }: Operation) {
  if (!phase || phase === 'complete' || phase === action) return;
  return humanize(phase).replace(/\bvm\b/gi, 'VM');
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

const actionProgress: Partial<Record<string, string>> = {
  create: 'Creating…',
  start: 'Starting…',
  restart: 'Restarting…',
  stop: 'Stopping…',
  'force-stop': 'Stopping…',
  delete: 'Deleting…',
  configure: 'Updating configuration…',
};

export function machineTransition(
  machine: Machine,
  operation?: Operation,
  sending?: VMAction,
): string | undefined {
  if (sending) return actionProgress[sending];
  if (operation)
    return (
      (operation.machineId === machine.id &&
        actionProgress[operation.action]) ||
      'Updating VM storage…'
    );
  switch (machine.status) {
    case 'queued':
    case 'provisioning':
      return 'Preparing VM…';
    case 'starting':
      return 'Starting…';
    case 'stopping':
      return 'Stopping…';
    case 'deleting':
      return 'Deleting…';
  }
}

export function latestVMFailure(machine: Machine, operations: Operation[]) {
  const latest = operations.find(
    (op) =>
      op.machineId === machine.id &&
      ['create', 'start', 'restart', 'stop', 'force-stop', 'delete'].includes(
        op.action,
      ),
  );
  return latest?.status === 'failed' ? latest : undefined;
}

export function machineGuidance(machine: Machine): string | undefined {
  if (machine.status === 'unknown')
    return 'Power state is unconfirmed. Stop the VM before trying to start it again.';
  if (machine.pendingSpec)
    return 'Configuration change pending. Review or retry the change.';
  if (!machine.spec.volumeId)
    return 'Attach a project volume before starting this VM.';
  if (machine.status === 'running' && machine.guestState === 'not-installed')
    return 'Basic VM management only. This image has no guest enhancements.';
  if (machine.status === 'running' && machine.guestState === 'incompatible')
    return 'Guest protocol incompatible. Guest enhancements require a compatible AXV image.';
  if (machine.status === 'running' && !machine.ready)
    return machine.guestReason || 'Waiting for the guest service.';
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

export function imageTerminalSummary(image?: ImageInfo): string {
  if (!image) return 'Select an image to review guest capabilities.';
  const capabilities = image.guest?.capabilities ?? [];
  if (capabilities.includes('terminals')) {
    return 'Browser terminal included (AXV guest enhancements preinstalled).';
  }
  if (image.guest) {
    return 'AXV guest installed without browser terminal support.';
  }
  return 'No browser terminal (basic VM lifecycle only; local Lima shell available on macOS).';
}

export function machineGuestStatus(machine: Machine): string | undefined {
  if (machine.status !== 'running') return undefined;
  if (machine.guestState === 'not-installed') return 'Basic VM (no guest)';
  if (machine.guestState === 'incompatible') return 'Guest incompatible';
  if (!machine.ready) return 'Guest starting…';
  if (!machine.guestCapabilities?.includes('terminals'))
    return 'Guest ready (no terminal)';
  return 'Guest ready';
}

export function canCopyLocalLimaShell(
  machine: Machine | undefined,
  localOwner: boolean,
): boolean {
  if (
    !localOwner ||
    !machine ||
    machine.spec.provider !== 'lima' ||
    machine.status !== 'running' ||
    machine.desired !== 'running'
  ) {
    return false;
  }
  return (
    machine.guestState === 'not-installed' ||
    (machine.ready && !machine.guestCapabilities?.includes('terminals'))
  );
}
