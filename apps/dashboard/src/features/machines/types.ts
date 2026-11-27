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

export interface HostServiceStatus {
  supported: boolean;
  installed: boolean;
  enabled: boolean;
  active: boolean;
  state: string;
  socket?: string;
  socketReachable: boolean;
  installedBinary?: string;
  installedVersion?: string;
  currentVersion?: string;
  versionDrift?: boolean;
}

export interface DoctorCheck {
  id: string;
  category: string;
  label: string;
  status: 'ok' | 'info' | 'warn' | 'fail';
  detail: string;
  fix?: string;
}

export interface DoctorReport {
  instanceId?: string;
  platform: string;
  arch: string;
  version: string;
  executable?: string;
  profileHome: string;
  checks: DoctorCheck[];
  ok: number;
  info: number;
  warn: number;
  fail: number;
}

export interface ComputeEnvironment {
  preparation?: Omit<PreparationCheck, 'instanceId'>;
  hostService?: HostServiceStatus;
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
  code?: string;
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

export type HostUnitState = 'not_installed' | 'inactive' | 'failed' | 'active';

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
  localService?: {
    status: 'missing' | 'permission_denied' | 'unavailable' | 'ready';
    systemdState?: HostUnitState;
    socket: string;
    detail: string;
  };
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
  guest?: {
    imageVersion?: number;
    protocolVersion?: number;
    capabilities?: string[];
  };
  source?: string;
  reason?: string;
}

export interface LimaShellInfo {
  binary: string;
  limaHome: string;
  instance: string;
  command: string;
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
  sequence?: number;
}

export interface ComputeBinding {
  hostId?: string;
  generation?: number;
}
