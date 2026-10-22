import { request } from '../../shared/api/http';
import type { VMProjectBinding } from './vm-api';
import { sameProjectBinding } from './vm-init-api';
import { validVMTaskSubmission } from './vm-task-api';

export interface VMProjectPreview {
  id: string;
  projectId: string;
  port: number;
  binding: VMProjectBinding;
  state: string;
  error?: string;
  url?: string;
}

export interface VMPreviewRegistration {
  instanceId: string;
  requestId: string;
  binding: VMProjectBinding;
  port: number;
}

export interface VMPreviewSnapshot {
  instanceId: string;
  supported: boolean;
  reason?: string;
  previews: VMProjectPreview[];
}

export interface PendingPreviewRegistration {
  version: 1;
  registration: VMPreviewRegistration;
}

export function validPendingPreview(
  value: unknown,
  hostId: string,
  projectId: string,
): value is PendingPreviewRegistration {
  if (!value || typeof value !== 'object') return false;
  const pending = value as PendingPreviewRegistration;
  const registration = pending.registration;
  return (
    pending.version === 1 &&
    !!registration &&
    typeof registration.instanceId === 'string' &&
    registration.instanceId.length > 0 &&
    registration.instanceId.length <= 128 &&
    Number.isInteger(registration.port) &&
    registration.port >= 1024 &&
    registration.port <= 65535 &&
    validVMTaskSubmission(
      {
        requestId: registration.requestId,
        binding: registration.binding,
        taskId: 'preview',
      },
      hostId,
      projectId,
    )
  );
}

export function verifyPreviewReceipt(
  preview: VMProjectPreview,
  registration: VMPreviewRegistration,
) {
  if (
    !preview.id ||
    preview.projectId !== registration.binding.projectId ||
    preview.port !== registration.port ||
    !sameProjectBinding(preview.binding, registration.binding)
  )
    throw new Error(
      'The preview receipt does not match the original registration. Its recovery record was preserved.',
    );
  return preview;
}

const path = (projectId: string) =>
  `/project-previews/${encodeURIComponent(projectId)}`;
export const vmPreviewsAPI = {
  list: (projectId: string, hostId: string, signal: AbortSignal) =>
    request<VMPreviewSnapshot>(path(projectId), {
      headers: { 'X-AXV-Host-ID': hostId },
      signal: AbortSignal.any([signal, AbortSignal.timeout(10000)]),
    }),
  register: (registration: VMPreviewRegistration) =>
    request<VMProjectPreview>(path(registration.binding.projectId), {
      method: 'POST',
      headers: { 'X-AXV-Host-ID': registration.binding.hostId },
      body: JSON.stringify(registration),
      signal: AbortSignal.timeout(30000),
    }),
  stop: (preview: VMProjectPreview) =>
    request<VMProjectPreview>(
      `${path(preview.projectId)}/${encodeURIComponent(preview.id)}/stop`,
      {
        method: 'POST',
        headers: { 'X-AXV-Host-ID': preview.binding.hostId },
        body: '{}',
        signal: AbortSignal.timeout(10000),
      },
    ),
};
