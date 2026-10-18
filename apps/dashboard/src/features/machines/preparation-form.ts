import type { PreparationRequest } from './api';
import { integer } from './form-state';

export interface PreparationDraft {
  imageSource: 'official' | 'bundle' | 'ubuntu-base';
  imageBundle: string;
  storageRoot: string;
  limaHome: string;
  cpus: string;
  memoryMiB: string;
  diskGiB: string;
  machines: string;
  connection: 'configuration' | 'socket';
  hostConfig: string;
  socket: string;
}

export function preparationDraft(
  defaults: PreparationRequest,
): PreparationDraft {
  return {
    imageSource:
      defaults.imageSource || (defaults.imageBundle ? 'bundle' : 'ubuntu-base'),
    imageBundle: defaults.imageBundle || '',
    storageRoot: defaults.storageRoot || '',
    limaHome: defaults.limaHome || '',
    cpus: defaults.cpus === undefined ? '' : String(defaults.cpus),
    memoryMiB:
      defaults.memoryMiB === undefined ? '' : String(defaults.memoryMiB),
    diskGiB: defaults.diskGiB === undefined ? '' : String(defaults.diskGiB),
    machines: defaults.machines === undefined ? '' : String(defaults.machines),
    connection:
      defaults.socket && !defaults.hostConfig ? 'socket' : 'configuration',
    hostConfig: defaults.hostConfig || '/etc/axv/host.json',
    socket: defaults.socket || '/run/axv-host/control.sock',
  };
}

function absolutePath(value: string) {
  return value.startsWith('/') && !/[\r\n\0]/.test(value);
}

export function preparationDraftError(
  draft: PreparationDraft,
  platform: string,
  configured: boolean,
) {
  if (configured) return '';
  if (platform === 'linux')
    return absolutePath(
      draft.connection === 'socket' ? draft.socket : draft.hostConfig,
    )
      ? ''
      : 'Enter an absolute path on the machine running AXV.';
  if (platform !== 'darwin')
    return 'VM preparation is available on macOS and Linux.';
  if (draft.imageSource === 'bundle' && !absolutePath(draft.imageBundle))
    return 'Enter an absolute path to an AXV image bundle manifest.';
  if (!absolutePath(draft.storageRoot) || !absolutePath(draft.limaHome))
    return 'Enter absolute storage paths on the machine running AXV.';
  if (!integer(draft.cpus, 1, 4096))
    return 'Enter a whole CPU budget between 1 and 4,096.';
  if (!integer(draft.memoryMiB, 256, 2 ** 30))
    return 'Enter a whole memory budget of at least 256 MiB.';
  if (!integer(draft.diskGiB, 1, 2 ** 30))
    return 'Enter a whole disk budget of at least 1 GiB.';
  if (!integer(draft.machines, 1, 100))
    return 'Enter a VM limit between 1 and 100.';
  return '';
}

export function preparationRequest(
  draft: PreparationDraft,
  platform: string,
  configured: boolean,
  defaults: PreparationRequest,
): PreparationRequest {
  if (configured) return structuredClone(defaults);
  if (platform === 'linux')
    return draft.connection === 'socket'
      ? { socket: draft.socket }
      : { hostConfig: draft.hostConfig };
  return {
    imageSource: draft.imageSource,
    ...(draft.imageSource === 'bundle'
      ? { imageBundle: draft.imageBundle }
      : {}),
    storageRoot: draft.storageRoot,
    limaHome: draft.limaHome,
    cpus: Number(draft.cpus),
    memoryMiB: Number(draft.memoryMiB),
    diskGiB: Number(draft.diskGiB),
    machines: Number(draft.machines),
  };
}

export function preparationSize(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return 'Size not yet known';
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)} GiB`;
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(1)} MiB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${bytes} bytes`;
}
