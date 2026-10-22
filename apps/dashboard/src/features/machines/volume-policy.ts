import type { MachineSpec, Operation, Volume } from './api';
import { resourceBusy } from './form-state';

export type VolumePolicyAction =
  'attach' | 'detach' | 'rename' | 'resize' | 'retain' | 'copy' | 'delete';

export function isProjectBackup(volume: Volume) {
  return !!volume.backup && !volume.restoredFrom;
}

/** Provenance restrictions supplement provider, revision and lifecycle checks. */
export function volumeActionAllowed(
  volume: Volume,
  action: VolumePolicyAction,
) {
  if (action === 'delete') return true;
  if (isProjectBackup(volume)) return false;
  return action !== 'copy' || (!volume.backup && !volume.restoredFrom);
}

export function volumeAvailableForMachine(
  volume: Volume,
  provider: string | undefined,
  kind: MachineSpec['kind'],
  operations: Operation[],
) {
  return (
    volumeActionAllowed(volume, 'attach') &&
    !volume.machineId &&
    !volume.pendingMachineId &&
    !volume.pendingSizeGiB &&
    volume.status === 'ready' &&
    volume.provider === provider &&
    (kind !== 'development' || volume.retain) &&
    !resourceBusy(operations, volume.id)
  );
}
