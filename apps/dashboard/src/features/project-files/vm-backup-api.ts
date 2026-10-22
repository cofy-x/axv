import { request } from '../../shared/api/http';
import type { MachineSpec, Volume, Operation } from '../machines/api';
import type { VMProject } from './vm-api';

export type ProjectBackup = Volume;

interface BackupScope {
  hostId: string;
  projectId: string;
}

export type BackupAction = BackupScope &
  (
    | {
        kind: 'backup';
        values: {
          requestId: string;
          projectRevision: number;
          volumeRevision: number;
          machineId: string;
          machineRevision: number;
          name: string;
        };
      }
    | {
        kind: 'restore';
        backupId: string;
        values: { requestId: string; revision: number; name: string };
      }
    | {
        kind: 'bind';
        backupId: string;
        values: {
          requestId: string;
          sourceProjectId: string;
          volumeId: string;
          volumeRevision: number;
          machineId: string;
          name: string;
        };
      }
    | {
        kind: 'stop';
        machineId: string;
        values: { requestId: string; revision: number; action: 'stop' };
      }
    | {
        kind: 'create-vm';
        backupId: string;
        values: { requestId: string; spec: MachineSpec; start: true };
      }
  );

export interface BackupRecovery {
  version: 1;
  action: BackupAction;
  operationId?: string;
}

export const backupActionLabel = (action: BackupAction) =>
  ({
    backup: 'Create backup',
    restore: 'Restore to new volume',
    bind: 'Open as new project',
    stop: 'Stop source VM',
    'create-vm': 'Create restored VM',
  })[action.kind];

function record(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

const text = (value: unknown): value is string =>
  typeof value === 'string' &&
  value.length > 0 &&
  value.length <= 4096 &&
  Array.from(value).every(
    (c) => c.charCodeAt(0) > 31 && c.charCodeAt(0) !== 127,
  );
const positive = (value: unknown) =>
  typeof value === 'number' && Number.isSafeInteger(value) && value > 0;
const keys = (value: Record<string, unknown>, expected: string[]) =>
  Object.keys(value).every((key) => expected.includes(key));

export function readBackupRecovery(
  raw: string,
  hostId: string,
  projectId: string,
): BackupRecovery {
  const saved: unknown = JSON.parse(raw);
  if (
    !record(saved) ||
    saved.version !== 1 ||
    !keys(saved, ['version', 'action', 'operationId']) ||
    (saved.operationId !== undefined && !text(saved.operationId)) ||
    !record(saved.action)
  )
    throw new Error('Invalid backup recovery record.');
  const action = saved.action;
  const values = action.values;
  if (
    action.hostId !== hostId ||
    action.projectId !== projectId ||
    !record(values) ||
    !text(values.requestId) ||
    values.requestId.length < 8 ||
    values.requestId.length > 128 ||
    !keys(action, [
      'hostId',
      'projectId',
      'kind',
      'values',
      'backupId',
      'machineId',
    ])
  )
    throw new Error(
      'Backup recovery belongs to another target or has an invalid request.',
    );
  let valid = false;
  switch (action.kind) {
    case 'backup':
      valid =
        keys(values, [
          'requestId',
          'projectRevision',
          'volumeRevision',
          'machineId',
          'machineRevision',
          'name',
        ]) &&
        positive(values.projectRevision) &&
        positive(values.volumeRevision) &&
        text(values.machineId) &&
        positive(values.machineRevision) &&
        text(values.name);
      break;
    case 'restore':
      valid =
        text(action.backupId) &&
        keys(values, ['requestId', 'revision', 'name']) &&
        positive(values.revision) &&
        text(values.name);
      break;
    case 'bind':
      valid =
        text(action.backupId) &&
        keys(values, [
          'requestId',
          'sourceProjectId',
          'volumeId',
          'volumeRevision',
          'machineId',
          'name',
        ]) &&
        values.sourceProjectId === projectId &&
        text(values.volumeId) &&
        positive(values.volumeRevision) &&
        text(values.machineId) &&
        text(values.name);
      break;
    case 'stop':
      valid =
        text(action.machineId) &&
        keys(values, ['requestId', 'revision', 'action']) &&
        positive(values.revision) &&
        values.action === 'stop';
      break;
    case 'create-vm': {
      const spec = values.spec;
      valid =
        text(action.backupId) &&
        keys(values, ['requestId', 'spec', 'start']) &&
        values.start === true &&
        record(spec) &&
        keys(spec, [
          'name',
          'provider',
          'imageId',
          'kind',
          'resources',
          'volumeId',
          'projectGiB',
          'retainVolume',
        ]) &&
        text(spec.name) &&
        text(spec.provider) &&
        text(spec.imageId) &&
        text(spec.volumeId) &&
        spec.kind === 'development' &&
        spec.retainVolume === true &&
        positive(spec.projectGiB) &&
        record(spec.resources) &&
        keys(spec.resources, ['cpus', 'memoryMiB', 'diskGiB']) &&
        positive(spec.resources.cpus) &&
        positive(spec.resources.memoryMiB) &&
        positive(spec.resources.diskGiB);
      break;
    }
  }
  if (!valid) throw new Error('Invalid backup recovery payload.');
  return saved as unknown as BackupRecovery;
}

function actionPath(action: BackupAction) {
  switch (action.kind) {
    case 'backup':
      return `projects/${encodeURIComponent(action.projectId)}/backups`;
    case 'restore':
    case 'bind':
      return `project-backups/${encodeURIComponent(action.backupId)}/${action.kind}`;
    case 'stop':
      return `machines/${encodeURIComponent(action.machineId)}/actions`;
    case 'create-vm':
      return 'machines';
  }
}

export function submitBackupAction(action: BackupAction) {
  return request<{
    project?: VMProject;
    volume?: ProjectBackup;
    operation?: Operation;
  }>(`/compute/${actionPath(action)}`, {
    method: 'POST',
    headers: { 'X-AXV-Host-ID': action.hostId },
    body: JSON.stringify(action.values),
    signal: AbortSignal.timeout(30000),
  });
}

export function backupOperation(
  hostId: string,
  id: string,
  signal: AbortSignal,
) {
  return request<Operation>(`/compute/operations/${encodeURIComponent(id)}`, {
    headers: { 'X-AXV-Host-ID': hostId },
    signal,
  });
}

export function restoredMachineSpec(
  template: MachineSpec,
  volume: ProjectBackup,
  name: string,
): MachineSpec {
  return {
    name,
    provider: volume.provider,
    imageId: template.imageId,
    kind: 'development',
    resources: { ...template.resources },
    volumeId: volume.id,
    projectGiB: volume.sizeGiB,
    retainVolume: true,
  };
}

export function projectBackups(hostId: string) {
  return request<{ backups: ProjectBackup[] }>('/compute/project-backups', {
    headers: { 'X-AXV-Host-ID': hostId },
  });
}
