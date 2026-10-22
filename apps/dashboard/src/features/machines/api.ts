import { request } from '../../shared/api/http';
import type { VMProject } from '../project-files/vm-api';

export interface Resources {
  cpus: number;
  memoryMiB: number;
  diskGiB: number;
}

export interface MachineSpec {
  name: string;
  provider: string;
  imageId: string;
  kind: 'development' | 'task';
  resources: Resources;
  projectGiB: number;
  volumeId?: string;
  retainVolume: boolean;
  ttlSeconds?: number;
}

export interface Machine {
  id: string;
  hostId: string;
  spec: MachineSpec;
  pendingSpec?: MachineSpec;
  revision: number;
  desired: string;
  status: string;
  ready: boolean;
  guestState: string;
  guestReason?: string;
  guestCapabilities?: string[];
  reason?: string;
  reserved: boolean;
  createdAt: string;
  updatedAt: string;
  expiresAt?: string;
}

export interface VolumeBackup {
  hostId: string;
  sourceVolumeId: string;
  sourceVolumeRevision: number;
  sourceMachineId: string;
  sourceMachineRevision: number;
  projects: VMProject[];
  createdAt: string;
}

export interface Volume {
  id: string;
  name: string;
  revision: number;
  provider: string;
  sizeGiB: number;
  pendingSizeGiB?: number;
  transientGiB?: number;
  pendingAction?: string;
  pendingMachineId?: string;
  retain: boolean;
  machineId?: string;
  status: string;
  reason?: string;
  backup?: VolumeBackup;
  restoredFrom?: string;
}

export interface Operation {
  requestId?: string;
  packageSource?: 'official' | 'aliyun';
  id: string;
  machineId: string;
  volumeId?: string;
  resourceId?: string;
  resourceKind?: string;
  locks?: string[];
  action: string;
  status: string;
  error?: string;
  phase?: string;
  createdAt: string;
}

export interface ComputeEnvironment {
  preparation?: Omit<PreparationCheck, 'instanceId'>;
  configurationSource?: string;
  availableActions?: ('review' | 'retry' | 'cancel')[];
  platform: string;
  configured: boolean;
  configPath: string;
  profileHome: string;
  logPath?: string;
  loggingError?: string;
  executablePath: string;
  requiredLimaVersion: string;
  automaticSetupSupported: boolean;
  runtime?: {
    state: 'not_configured' | 'starting' | 'ready' | 'failed';
    mode: string;
    error?: string;
    hostId?: string;
    generation: number;
    readOnly: boolean;
  };
}

export interface PreparationRequest {
  storageRoot?: string;
  limaHome?: string;
  cpus?: number;
  memoryMiB?: number;
  diskGiB?: number;
  machines?: number;
  socket?: string;
  hostConfig?: string;
  imageSource?: 'official' | 'bundle' | 'ubuntu-base';
  imageBundle?: string;
}

export interface PreparationRequirement {
  id: string;
  label: string;
  status: 'ready' | 'needed' | 'blocked';
  detail: string;
}

export interface PreparationDownload {
  originUrl?: string;
  id: string;
  name: string;
  url: string;
  sha256: string;
  bytes: number;
  cached: boolean;
  cacheState?: string;
  cachePath?: string;
}

export interface PreparationPlan {
  cacheHome: string;
  id: string;
  createdAt: string;
  expiresAt: string;
  platform: string;
  arch: string;
  provider: string;
  request: PreparationRequest;
  actions: string[];
  downloads: PreparationDownload[];
  checks: PreparationRequirement[];
  canStart: boolean;
  configPath: string;
  hostConfigPath: string;
  activation: 'ready' | 'configured';
}

export interface PreparationStep {
  id: string;
  label: string;
  status: 'pending' | 'running' | 'succeeded' | 'failed';
  detail?: string;
  bytesDone?: number;
  bytesTotal?: number;
}

export interface PreparationJob {
  id: string;
  requestId: string;
  planId: string;
  revision: number;
  status:
    'queued' | 'running' | 'interrupted' | 'failed' | 'succeeded' | 'cancelled';
  attempt: number;
  phase: string;
  steps: PreparationStep[];
  createdAt: string;
  updatedAt: string;
  committed: boolean;
  canCancel: boolean;
  canRetry: boolean;
  result?: 'configured' | 'ready';
  error?: { code: string; message: string; retryable: boolean };
  binding?: {
    hostId: string;
    protocolVersion: number;
    configFingerprint: string;
  };
  plan: PreparationPlan;
}

export interface PreparationCheck {
  officialImageAvailable: boolean;
  cacheHome: string;
  instanceId: string;
  platform: string;
  arch: string;
  configured: boolean;
  configurationSource: string;
  lockedReason?: string;
  automaticSetupSupported: boolean;
  requiredLimaVersion: string;
  defaults: PreparationRequest;
  checks: PreparationRequirement[];
  activeJob?: PreparationJob;
  latestJob?: PreparationJob;
}

export interface CreationDefaults {
  spec: MachineSpec;
  start: boolean;
  taskTtlSeconds: number;
  taskRetainVolume: boolean;
}

export interface HostInfo {
  configured: boolean;
  reason?: string;
  hostId?: string;
  hostArch?: string;
  capacity?: Resources & { machines: number; workers: number };
  providers?: {
    provider: string;
    available: boolean;
    reason?: string;
    persistentVolumes: boolean;
    snapshots: boolean;
    guestExecution: boolean;
    version?: string;
    configuration?: boolean;
    forceStop?: boolean;
    snapshotReason?: string;
    volumes?: boolean;
    volumeCopy?: boolean;
    images?: boolean;
  }[];
  images?: ImageInfo[];
  defaults?: CreationDefaults;
}

export interface ImageInfo {
  id: string;
  name: string;
  provider: string;
  arch: string;
  status?: string;
  managed?: boolean;
  revision?: number;
  sha256?: string;
  sizeBytes?: number;
  minRootGiB?: number;
  source?: string;
  reason?: string;
}

export interface ImageImport {
  name: string;
  arch: string;
  sha256: string;
  url?: string;
}

export interface GuestTerminal {
  id: string;
  bootId: string;
  status: string;
}

const base = '/compute';

export interface ComputeBinding {
  hostId?: string;
  generation?: number;
}

function bindingHeaders(binding?: ComputeBinding): Record<string, string> {
  return {
    ...(binding?.hostId ? { 'X-AXV-Host-ID': binding.hostId } : {}),
    ...(binding?.generation !== undefined
      ? { 'X-AXV-Compute-Generation': String(binding.generation) }
      : {}),
  };
}

export const machinesApi = {
  environment: (signal?: AbortSignal) =>
    request<ComputeEnvironment>(`${base}/environment`, { signal }),
  preparationCheck: (signal?: AbortSignal) =>
    request<PreparationCheck>(`${base}/preparation/check`, { signal }),
  preparationPlan: (
    spec: PreparationRequest,
    instanceId: string,
    signal?: AbortSignal,
  ) =>
    request<PreparationPlan>(`${base}/preparation/plan`, {
      method: 'POST',
      body: JSON.stringify(spec),
      headers: { 'X-AXV-Instance-ID': instanceId },
      signal: AbortSignal.any([
        ...(signal ? [signal] : []),
        AbortSignal.timeout(30000),
      ]),
    }),
  startPreparation: (planId: string, requestId: string, instanceId: string) =>
    request<PreparationJob>(`${base}/preparation/jobs`, {
      method: 'POST',
      body: JSON.stringify({ planId, requestId, confirmed: true }),
      headers: { 'X-AXV-Instance-ID': instanceId },
      signal: AbortSignal.timeout(15000),
    }),
  preparationJob: (id: string, signal?: AbortSignal) =>
    request<PreparationJob>(
      `${base}/preparation/jobs/${encodeURIComponent(id)}`,
      { signal },
    ),
  preparationAction: (
    id: string,
    action: 'retry' | 'cancel',
    expectedRevision: number,
    requestId: string,
    instanceId: string,
  ) =>
    request<PreparationJob>(
      `${base}/preparation/jobs/${encodeURIComponent(id)}/${action}`,
      {
        method: 'POST',
        body: JSON.stringify({ requestId, expectedRevision }),
        headers: { 'X-AXV-Instance-ID': instanceId },
        signal: AbortSignal.timeout(15000),
      },
    ),
  info: (signal?: AbortSignal, binding?: ComputeBinding) =>
    request<HostInfo>(`${base}/info`, {
      signal,
      headers: bindingHeaders(binding),
    }),
  machines: (signal?: AbortSignal, binding?: ComputeBinding) =>
    request<{ machines: Machine[] }>(`${base}/machines`, {
      signal,
      headers: bindingHeaders(binding),
    }),
  volumes: (signal?: AbortSignal, binding?: ComputeBinding) =>
    request<{ volumes: Volume[] }>(`${base}/volumes`, {
      signal,
      headers: bindingHeaders(binding),
    }),
  operations: (signal?: AbortSignal, binding?: ComputeBinding) =>
    request<{ operations: Operation[] }>(`${base}/operations`, {
      signal,
      headers: bindingHeaders(binding),
    }),
  images: (signal?: AbortSignal, binding?: ComputeBinding) =>
    request<{ images: ImageInfo[] }>(`${base}/images`, {
      signal,
      headers: bindingHeaders(binding),
    }),
  downloadImage: (
    spec: ImageImport,
    requestId: string,
    binding?: ComputeBinding,
  ) =>
    request<{ image: ImageInfo; operation: Operation }>(`${base}/images`, {
      method: 'POST',
      body: JSON.stringify({ ...spec, requestId }),
      headers: bindingHeaders(binding),
      signal: AbortSignal.timeout(15000),
    }),
  importImage: (
    file: File,
    spec: ImageImport,
    requestId: string,
    signal?: AbortSignal,
    binding?: ComputeBinding,
  ) => {
    const body = new FormData();
    body.append('metadata', JSON.stringify({ ...spec, requestId }));
    body.append('file', file);
    return request<{ image: ImageInfo; operation: Operation }>(
      `${base}/images/import`,
      {
        method: 'POST',
        body,
        headers: bindingHeaders(binding),
        signal: AbortSignal.any([
          ...(signal ? [signal] : []),
          AbortSignal.timeout(30 * 60 * 1000),
        ]),
      },
    );
  },
  deleteImage: (
    image: ImageInfo,
    requestId: string,
    binding?: ComputeBinding,
  ) =>
    request<Operation>(
      `${base}/images/${encodeURIComponent(image.id)}/delete`,
      {
        method: 'POST',
        body: JSON.stringify({ requestId, revision: image.revision }),
        headers: bindingHeaders(binding),
        signal: AbortSignal.timeout(15000),
      },
    ),
  create: (
    spec: MachineSpec,
    start: boolean,
    requestId: string,
    binding?: ComputeBinding,
  ) =>
    request<{ machine: Machine; operation: Operation }>(`${base}/machines`, {
      method: 'POST',
      body: JSON.stringify({ spec, start, requestId }),
      headers: bindingHeaders(binding),
      signal: AbortSignal.timeout(15000),
    }),
  action: (machine: Machine, action: string, requestId: string) =>
    request<{ machine: Machine; operation: Operation }>(
      `${base}/machines/${encodeURIComponent(machine.id)}/actions`,
      {
        method: 'POST',
        body: JSON.stringify({ action, revision: machine.revision, requestId }),
        headers: bindingHeaders({ hostId: machine.hostId }),
        signal: AbortSignal.timeout(15000),
      },
    ),
  update: (
    machine: Machine,
    spec: MachineSpec,
    renewLease: boolean,
    requestId: string,
  ) =>
    request<{ machine: Machine; operation: Operation }>(
      `${base}/machines/${encodeURIComponent(machine.id)}`,
      {
        method: 'PATCH',
        headers: bindingHeaders({ hostId: machine.hostId }),
        body: JSON.stringify({
          spec,
          renewLease,
          revision: machine.revision,
          requestId,
        }),
        signal: AbortSignal.timeout(15000),
      },
    ),
  createVolume: (
    spec: {
      name: string;
      provider: string;
      sizeGiB: number;
      retain: boolean;
      sourceVolumeId?: string;
    },
    requestId: string,
    binding?: ComputeBinding,
  ) =>
    request<{ volume: Volume; operation: Operation }>(`${base}/volumes`, {
      method: 'POST',
      body: JSON.stringify({ ...spec, requestId }),
      headers: bindingHeaders(binding),
      signal: AbortSignal.timeout(15000),
    }),
  volumeAction: (
    volume: Volume,
    change: {
      action: 'rename' | 'retain' | 'resize' | 'attach' | 'detach';
      name?: string;
      sizeGiB?: number;
      retain?: boolean;
      machineId?: string;
      machineRevision?: number;
    },
    requestId: string,
    binding?: ComputeBinding,
  ) =>
    request<{ volume: Volume; operation: Operation }>(
      `${base}/volumes/${encodeURIComponent(volume.id)}/actions`,
      {
        method: 'POST',
        headers: bindingHeaders(binding),
        body: JSON.stringify({
          ...change,
          revision: volume.revision,
          requestId,
        }),
        signal: AbortSignal.timeout(15000),
      },
    ),
  deleteVolume: (volume: Volume, requestId: string, binding?: ComputeBinding) =>
    request<Operation>(
      `${base}/volumes/${encodeURIComponent(volume.id)}/delete`,
      {
        method: 'POST',
        body: JSON.stringify({ requestId, revision: volume.revision }),
        headers: bindingHeaders(binding),
        signal: AbortSignal.timeout(15000),
      },
    ),
  createTerminal: (id: string, requestId: string, hostId: string) =>
    request<GuestTerminal>(
      `${base}/machines/${encodeURIComponent(id)}/guest/terminals`,
      {
        method: 'POST',
        body: JSON.stringify({ requestId, cols: 80, rows: 24 }),
        headers: { 'X-AXV-Host-ID': hostId },
        signal: AbortSignal.timeout(15000),
      },
    ),
  terminals: (id: string, hostId: string, signal?: AbortSignal) =>
    request<{ terminals: GuestTerminal[] }>(
      `${base}/machines/${encodeURIComponent(id)}/guest/terminals`,
      { signal, headers: { 'X-AXV-Host-ID': hostId } },
    ),
  terminal: (
    machineId: string,
    terminalId: string,
    hostId: string,
    signal?: AbortSignal,
  ) =>
    request<GuestTerminal>(
      `${base}/machines/${encodeURIComponent(machineId)}/guest/terminals/${encodeURIComponent(terminalId)}`,
      { signal, headers: { 'X-AXV-Host-ID': hostId } },
    ),
  endTerminal: (machineId: string, terminalId: string, hostId: string) =>
    request<GuestTerminal>(
      `${base}/machines/${encodeURIComponent(machineId)}/guest/terminals/${encodeURIComponent(terminalId)}`,
      {
        method: 'DELETE',
        signal: AbortSignal.timeout(15000),
        headers: { 'X-AXV-Host-ID': hostId },
      },
    ),
};
