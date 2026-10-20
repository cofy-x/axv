import { request } from '../../shared/api/http';
import type { VMProjectBinding } from './vm-api';

export interface VMTaskLimits {
  timeoutSeconds: number;
  cpus: number;
  memoryMiB: number;
  maxProcesses: number;
  maxLogBytes: number;
}

export interface VMTaskSpec {
  argv: string[];
  directory: string;
  limits: VMTaskLimits;
}

export interface VMTaskAttempt {
  id: string;
  taskId: string;
  number: number;
  binding: VMProjectBinding;
  root: string;
  rootIdentity: string;
  requestId: string;
  status: string;
  reason?: string;
  syncError?: string;
  exitCode?: number;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
  logBytes: number;
  cachedBytes: number;
  logLimited: boolean;
  cancelRequested: boolean;
}

export interface VMTaskResult {
  task: {
    id: string;
    hostId: string;
    projectId: string;
    requestId: string;
    spec: VMTaskSpec;
    currentAttemptId: string;
    createdAt: string;
  };
  attempt: VMTaskAttempt;
  attempts: VMTaskAttempt[];
}

export interface VMTaskSubmission {
  requestId: string;
  binding: VMProjectBinding;
  spec?: VMTaskSpec;
  taskId?: string;
}

export function validVMTaskSubmission(
  value: unknown,
  hostId: string,
  projectId: string,
): value is VMTaskSubmission {
  if (!value || typeof value !== 'object') return false;
  const saved = value as VMTaskSubmission;
  const binding = saved.binding;
  if (
    typeof saved.requestId !== 'string' ||
    saved.requestId.length < 8 ||
    saved.requestId.length > 128 ||
    !binding ||
    binding.hostId !== hostId ||
    binding.projectId !== projectId ||
    !['volumeId', 'machineId', 'bootId', 'instanceId'].every(
      (key) =>
        typeof binding[key as keyof VMProjectBinding] === 'string' &&
        !!binding[key as keyof VMProjectBinding],
    ) ||
    !Number.isSafeInteger(binding.projectRevision) ||
    binding.projectRevision < 1 ||
    !Number.isSafeInteger(binding.volumeRevision) ||
    binding.volumeRevision < 1
  )
    return false;
  if (saved.taskId !== undefined)
    return (
      typeof saved.taskId === 'string' &&
      !!saved.taskId &&
      saved.spec === undefined
    );
  const spec = saved.spec;
  if (
    !spec ||
    !Array.isArray(spec.argv) ||
    !spec.argv.length ||
    spec.argv.length > 128 ||
    !spec.argv[0] ||
    spec.argv.some((arg) => typeof arg !== 'string') ||
    typeof spec.directory !== 'string' ||
    !spec.limits
  )
    return false;
  const limits = spec.limits;
  return (
    [
      limits.timeoutSeconds,
      limits.cpus,
      limits.memoryMiB,
      limits.maxProcesses,
      limits.maxLogBytes,
    ].every(Number.isSafeInteger) &&
    limits.timeoutSeconds >= 1 &&
    limits.timeoutSeconds <= 86400 &&
    limits.cpus >= 1 &&
    limits.cpus <= 128 &&
    limits.memoryMiB >= 64 &&
    limits.memoryMiB <= 1048576 &&
    limits.maxProcesses >= 2 &&
    limits.maxProcesses <= 256 &&
    limits.maxLogBytes >= 1024 &&
    limits.maxLogBytes <= 8 << 20
  );
}

const path = (projectId: string, taskId?: string) =>
  `/compute/projects/${encodeURIComponent(projectId)}/tasks${taskId ? `/${encodeURIComponent(taskId)}` : ''}`;

function call<T>(
  projectId: string,
  hostId: string,
  suffix: string,
  values?: object,
  signal?: AbortSignal,
) {
  return request<T>(`${path(projectId)}${suffix}`, {
    headers: { 'X-AXV-Host-ID': hostId },
    ...(values ? { method: 'POST', body: JSON.stringify(values) } : {}),
    signal: signal
      ? AbortSignal.any([signal, AbortSignal.timeout(30000)])
      : AbortSignal.timeout(30000),
  });
}

export const vmTasksAPI = {
  recover: (projectId: string, hostId: string, requestId: string) =>
    request<VMTaskResult>(
      `/compute/projects/${encodeURIComponent(projectId)}/task-requests/${encodeURIComponent(requestId)}`,
      {
        headers: { 'X-AXV-Host-ID': hostId },
        signal: AbortSignal.timeout(30000),
      },
    ),
  list: (projectId: string, hostId: string, signal?: AbortSignal) =>
    call<{ tasks: VMTaskResult[] }>(projectId, hostId, '', undefined, signal),
  get: (
    projectId: string,
    hostId: string,
    taskId: string,
    signal?: AbortSignal,
  ) =>
    call<VMTaskResult>(
      projectId,
      hostId,
      `/${encodeURIComponent(taskId)}`,
      undefined,
      signal,
    ),
  submit: (projectId: string, submission: VMTaskSubmission) =>
    call<VMTaskResult>(
      projectId,
      submission.binding.hostId,
      submission.taskId
        ? `/${encodeURIComponent(submission.taskId)}/retry`
        : '',
      {
        requestId: submission.requestId,
        binding: submission.binding,
        ...(submission.spec ? { spec: submission.spec } : {}),
      },
    ),
  cancel: (projectId: string, attempt: VMTaskAttempt) =>
    call<VMTaskResult>(
      projectId,
      attempt.binding.hostId,
      `/${encodeURIComponent(attempt.taskId)}/attempts/${encodeURIComponent(attempt.id)}/cancel`,
      { binding: attempt.binding },
    ),
};

const displayLimit = 128 << 10;

/** Decode complete byte sequences across API pages; never execute terminal escapes. */
export async function readVMTaskLog(
  projectId: string,
  hostId: string,
  taskId: string,
  attemptId: string,
  signal: AbortSignal,
) {
  let offset = 0;
  let text = '';
  let logBytes: number;
  const decoder = new TextDecoder();
  do {
    const result = await call<{
      data: string | null;
      offset: number;
      nextOffset: number;
      logBytes: number;
    }>(
      projectId,
      hostId,
      `/${encodeURIComponent(taskId)}/attempts/${encodeURIComponent(attemptId)}/logs?offset=${offset}&limit=${Math.min(32768, displayLimit - offset)}`,
      undefined,
      signal,
    );
    const bytes = Uint8Array.from(atob(result.data || ''), (byte) =>
      byte.charCodeAt(0),
    );
    if (
      result.offset !== offset ||
      result.nextOffset !== offset + bytes.length ||
      !Number.isSafeInteger(result.logBytes) ||
      result.logBytes < result.nextOffset ||
      bytes.length > displayLimit - offset
    )
      throw new Error('The task log response has invalid byte offsets.');
    logBytes = result.logBytes;
    offset = result.nextOffset;
    text += decoder.decode(bytes, { stream: offset < logBytes });
    if (!bytes.length) break;
  } while (offset < logBytes && offset < displayLimit);
  return {
    text: text.replaceAll('\u001b', '␛'),
    displayedBytes: offset,
    logBytes,
  };
}

export function taskFinished(status: string) {
  return ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(status);
}

/** A recovered receipt can describe an older attempt than the task's current one. */
export function currentTaskAttempt(result: VMTaskResult) {
  return result.attempts.find(
    (attempt) => attempt.id === result.task.currentAttemptId,
  );
}
