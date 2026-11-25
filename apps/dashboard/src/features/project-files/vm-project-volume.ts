import type { WindowParams } from '@cofy-x/axv-nextstep';
import type { Machine, Volume } from '../machines/api';

export interface VMProjectVolumeScope {
  hostId: string;
  machineId: string;
  volumeId: string;
}

export function vmProjectVolumeScope(
  params: WindowParams,
): VMProjectVolumeScope | undefined {
  const { hostId, machineId, vmProjectVolumeId } = params;
  if (hostId && machineId && vmProjectVolumeId)
    return { hostId, machineId, volumeId: vmProjectVolumeId };
}

export function canOpenVMProjectVolume(machine: Machine, volume: Volume) {
  return (
    volume.retain &&
    volume.status === 'ready' &&
    volume.machineId === machine.id &&
    machine.status === 'running' &&
    machine.desired === 'running' &&
    machine.ready &&
    !!machine.guestCapabilities?.includes('project-files')
  );
}
