import { request } from '../../shared/api/http';
import type {
  ComputeBinding,
  ComputeEnvironment,
  DoctorReport,
  GuestTerminal,
  HostInfo,
  ImageImport,
  ImageInfo,
  LimaShellInfo,
  Machine,
  MachineSpec,
  Operation,
  PreparationCheck,
  PreparationJob,
  PreparationPlan,
  PreparationRequest,
  Volume,
} from './types';

export type * from './types';

const base = '/compute';

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
  doctor: (signal?: AbortSignal) =>
    request<DoctorReport>('/doctor', { signal }),
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
  limaShell: (machineId: string, signal?: AbortSignal) =>
    request<LimaShellInfo>(
      `${base}/preparation/lima-shell?machineId=${encodeURIComponent(machineId)}`,
      { signal: signal ?? AbortSignal.timeout(15000) },
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
  createTerminal: (
    id: string,
    requestId: string,
    hostId: string,
    directory?: string,
  ) =>
    request<GuestTerminal>(
      `${base}/machines/${encodeURIComponent(id)}/guest/terminals`,
      {
        method: 'POST',
        body: JSON.stringify({
          requestId,
          cols: 80,
          rows: 24,
          ...(directory ? { directory } : {}),
        }),
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
