import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { HostInfo, Machine, Operation, Volume } from './api';
import { machinesApi } from './api';
import { isDefiniteOperationRejection } from './operation-requests';

export type VMAction = 'start' | 'restart' | 'stop' | 'force-stop' | 'delete';

export function machineActionReason(
  machine: Machine,
  action: VMAction,
  operations: Operation[],
  volumes: Volume[],
  provider?: NonNullable<HostInfo['providers']>[number],
): string | undefined {
  if (machine.status === 'deleted') return 'This VM has been deleted.';
  const resources = [machine.id, machine.spec.volumeId].filter(
    (id): id is string => !!id,
  );
  if (
    operations.some(
      (operation) =>
        ['queued', 'running'].includes(operation.status) &&
        resources.some(
          (id) =>
            operation.machineId === id ||
            operation.volumeId === id ||
            operation.resourceId === id ||
            operation.locks?.includes(id),
        ),
    )
  )
    return 'Wait for the active VM or project-volume operation to finish.';
  if (
    ['queued', 'provisioning', 'starting', 'stopping', 'deleting'].includes(
      machine.status,
    )
  )
    return 'Wait for the current VM transition to finish.';
  if (action === 'delete')
    return volumes.some((volume) => volume.pendingMachineId === machine.id)
      ? 'Resolve the pending project-volume attachment before deleting this VM.'
      : undefined;
  if (action === 'stop' || action === 'force-stop') {
    if (action === 'force-stop' && !provider?.forceStop)
      return 'This provider does not support force stop.';
    return machine.status === 'stopped' && machine.desired === 'stopped'
      ? 'This VM is already stopped.'
      : undefined;
  }
  if (action === 'start' && machine.status === 'running')
    return 'This VM is already running; guest readiness is separate.';
  if (action === 'restart' && machine.status !== 'running')
    return 'Start this VM before restarting it.';
  if (provider && !provider.available)
    return provider.reason || 'This VM provider is unavailable.';
  if (machine.pendingSpec)
    return 'Apply the pending configuration before starting this VM.';
  if (machine.expiresAt && Date.parse(machine.expiresAt) <= Date.now())
    return 'Renew the expired task lease before starting this VM.';
  if (
    !volumes.some(
      (volume) =>
        volume.id === machine.spec.volumeId &&
        volume.machineId === machine.id &&
        volume.status === 'ready',
    )
  )
    return 'Attach a ready project volume before starting this VM.';
  return undefined;
}

// A lost response must resolve the original operation even if polling has
// already advanced the VM revision. Never turn that retry into another reboot.
export interface PendingMachineAction {
  machine: Machine;
  action: VMAction;
  requestId: string;
  uncertain: boolean;
}

export class MachineActionRequests {
  private pending = new Map<string, PendingMachineAction>();
  private inFlight = new Map<string, ReturnType<typeof machinesApi.action>>();
  private readonly listeners = new Set<() => void>();

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  private publish() {
    for (const listener of this.listeners) listener();
  }

  getPending(hostId: string, machineId: string) {
    return this.pending.get(JSON.stringify([hostId, machineId]));
  }

  execute(machine: Machine, action: VMAction) {
    const key = JSON.stringify([machine.hostId, machine.id]);
    const pending = this.pending.get(key);
    if (pending && pending.action !== action)
      return Promise.reject(
        new Error(
          `Resolve the original ${pending.action} request before changing this VM.`,
        ),
      );
    const active = this.inFlight.get(key);
    if (active) return active;
    const attempt = pending
      ? { ...pending, uncertain: false }
      : {
          machine,
          action,
          requestId: crypto.randomUUID(),
          uncertain: false,
        };
    this.pending.set(key, attempt);
    this.publish();
    const request = machinesApi
      .action(attempt.machine, action, attempt.requestId)
      .then((result) => {
        this.pending.delete(key);
        this.publish();
        return result;
      })
      .catch((error: unknown) => {
        if (isDefiniteOperationRejection(error)) this.pending.delete(key);
        else this.pending.set(key, { ...attempt, uncertain: true });
        this.publish();
        throw error;
      })
      .finally(() => this.inFlight.delete(key));
    this.inFlight.set(key, request);
    return request;
  }
}

export const machineActionRequests = new MachineActionRequests();

export const actionLabels: Record<VMAction, string> = {
  start: 'Start',
  restart: 'Restart',
  stop: 'Stop VM',
  'force-stop': 'Force Stop',
  delete: 'Delete VM',
};

export async function confirmVMAction(
  desktop: DesktopAPI,
  machine: Machine,
  action: VMAction,
) {
  if (action === 'start') return true;
  const name = `“${machine.spec.name}”`;
  const message = {
    restart: `Restart ${name}? All live guest sessions will end. Project data remains on disk.`,
    stop: `Stop ${name}? Guest sessions end. Project data remains on disk. If graceful shutdown fails, the VM state remains visible.`,
    'force-stop': `Power off ${name} immediately? Unsaved guest work may be lost and filesystems may require recovery.`,
    delete: `Delete ${name} and its root disk? ${machine.spec.retainVolume ? 'Its project volume will be retained.' : 'Its temporary project volume will also be deleted.'}`,
  }[action];
  const result = await desktop.dialog({
    title: `${actionLabels[action]}?`,
    message,
    buttons: [
      { label: 'Cancel', value: 'cancel', default: true, cancel: true },
      { label: actionLabels[action], value: 'continue' },
    ],
  });
  return result?.button === 'continue';
}
