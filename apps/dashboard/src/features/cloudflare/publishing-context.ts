import type { CloudflarePublishTarget } from './types';

export interface RequestedPublication extends CloudflarePublishTarget {
  id: string;
}

/** Desktop view state may be restored from storage; accept only a complete captured target. */
export function requestedPublication(
  value: unknown,
): RequestedPublication | undefined {
  if (!value || typeof value !== 'object') return undefined;
  const request = value as Partial<RequestedPublication>;
  const binding = request.binding;
  if (
    typeof request.id !== 'string' ||
    !request.id ||
    !binding ||
    !Number.isInteger(request.port) ||
    request.port! < 1024 ||
    request.port! > 65535
  )
    return undefined;
  for (const key of [
    'hostId',
    'projectId',
    'volumeId',
    'machineId',
    'bootId',
    'instanceId',
  ] as const)
    if (typeof binding[key] !== 'string' || !binding[key]) return undefined;
  if (
    !Number.isInteger(binding.projectRevision) ||
    !Number.isInteger(binding.volumeRevision)
  )
    return undefined;
  return { id: request.id, port: request.port!, binding: { ...binding } };
}
