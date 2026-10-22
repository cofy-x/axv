import { request } from '../../shared/api/http';
import type { VMProject, VMProjectBinding } from './vm-api';
import type { VMTaskResult } from './vm-task-api';
import { validVMTaskSubmission } from './vm-task-api';

export interface VMProjectInitialization {
  requestId: string;
  binding: VMProjectBinding;
  directory: string;
  repository: string;
  name: string;
}

export interface PendingInitialization {
  version: 1;
  hostId: string;
  volumeId: string;
  volumeRevision: number;
  machineId: string;
  bootId: string;
  instanceId: string;
  requestId: string;
  rootRequestId: string;
  directory: string;
  repository: string;
  name: string;
  submission?: VMProjectInitialization;
}

const bytes = (value: string) => new TextEncoder().encode(value).length;
const identifier = (value: unknown): value is string =>
  typeof value === 'string' &&
  bytes(value) > 0 &&
  bytes(value) <= 128 &&
  !/[\p{Cc}\s]/u.test(value);

export function initializationDirectoryAllowed(value: string) {
  return (
    !!value &&
    bytes(value) <= 255 &&
    !value.includes('/') &&
    !value.startsWith('-') &&
    !['.', '..'].includes(value) &&
    !value.includes('\\') &&
    !/^\.axv(?:-|$)/u.test(value) &&
    !/\p{Cc}/u.test(value)
  );
}

export function initializationNameAllowed(name: string, directory: string) {
  const value = name || directory;
  return (
    !!value &&
    value.trim() === value &&
    bytes(value) <= 80 &&
    !/\p{Cc}/u.test(value)
  );
}

export function sameProjectBinding(a: VMProjectBinding, b: VMProjectBinding) {
  return (
    [
      'hostId',
      'projectId',
      'projectRevision',
      'volumeId',
      'volumeRevision',
      'machineId',
      'bootId',
      'instanceId',
    ] as const
  ).every((key) => a?.[key] === b?.[key]);
}

export function verifyInitializationReceipt(
  receipt: VMTaskResult,
  submission: VMProjectInitialization,
) {
  const { task, attempt } = receipt;
  const intent = task?.spec?.initialization;
  const expectedRoot = [attempt?.root, submission.directory]
    .filter(Boolean)
    .join('/');
  if (
    !task?.id ||
    !attempt?.id ||
    task.hostId !== submission.binding.hostId ||
    task.projectId !== submission.binding.projectId ||
    task.requestId !== submission.requestId ||
    task.currentAttemptId !== attempt.id ||
    attempt.taskId !== task.id ||
    attempt.requestId !== submission.requestId ||
    !sameProjectBinding(attempt.binding, submission.binding) ||
    !intent ||
    intent.directory !== submission.directory ||
    (intent.repository || '') !== submission.repository ||
    (intent.name || '') !== submission.name ||
    task.spec.argv.length !== 0 ||
    task.spec.directory !== '' ||
    (attempt.createdRoot &&
      (attempt.createdRoot.root !== expectedRoot ||
        !attempt.createdRoot.rootIdentity)) ||
    (attempt.status === 'succeeded' &&
      (!attempt.guestFinal || !attempt.createdRoot))
  )
    throw new Error(
      'The task receipt does not match the original typed initialization. Its recovery record was preserved.',
    );
  return receipt;
}

export function validPendingInitialization(
  value: unknown,
): value is PendingInitialization {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const pending = value as PendingInitialization;
  if (
    pending.version !== 1 ||
    ![
      'hostId',
      'volumeId',
      'machineId',
      'bootId',
      'instanceId',
      'requestId',
      'rootRequestId',
    ].every((key) => identifier(pending[key as keyof PendingInitialization])) ||
    bytes(pending.requestId) < 8 ||
    bytes(pending.rootRequestId) < 8 ||
    typeof pending.directory !== 'string' ||
    !initializationDirectoryAllowed(pending.directory) ||
    typeof pending.name !== 'string' ||
    !initializationNameAllowed(pending.name, pending.directory) ||
    typeof pending.repository !== 'string' ||
    !repositoryURLAllowed(pending.repository) ||
    !Number.isSafeInteger(pending.volumeRevision) ||
    pending.volumeRevision < 1
  )
    return false;
  const submission = pending.submission;
  return (
    submission === undefined ||
    (submission !== null &&
      submission.requestId === pending.requestId &&
      submission.directory === pending.directory &&
      submission.repository === pending.repository &&
      submission.name === pending.name &&
      validVMTaskSubmission(
        {
          requestId: submission.requestId,
          binding: submission.binding,
          taskId: 'initialization',
        },
        pending.hostId,
        submission.binding?.projectId || '',
      ) &&
      submission.binding.volumeId === pending.volumeId &&
      submission.binding.volumeRevision === pending.volumeRevision &&
      submission.binding.machineId === pending.machineId &&
      submission.binding.bootId === pending.bootId &&
      submission.binding.instanceId === pending.instanceId)
  );
}

export function repositoryURLAllowed(value: string) {
  if (!value) return true;
  if (
    bytes(value) > 4096 ||
    value.trim() !== value ||
    Array.from(value).some(
      (character) =>
        character.charCodeAt(0) <= 32 || character.charCodeAt(0) === 127,
    )
  )
    return false;
  if (/^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[A-Za-z0-9_./~%-]+$/u.test(value))
    return true;
  try {
    const url = new URL(value);
    return (
      ['https:', 'ssh:'].includes(url.protocol) &&
      !!url.hostname &&
      !!url.pathname &&
      !url.search &&
      !value.includes('?') &&
      !url.hash &&
      !value.includes('#') &&
      !url.password &&
      !(
        url.protocol === 'https:' &&
        (url.username ||
          value
            .slice(value.indexOf('://') + 3)
            .split('/')[0]
            .includes('@'))
      ) &&
      /^\w+:\/\/[^/]+\/.+/u.test(value)
    );
  } catch {
    return false;
  }
}

export const vmInitializationAPI = {
  health: (hostId: string, machineId: string) =>
    request<{
      machineId: string;
      bootId: string;
      instanceId: string;
      ready: boolean;
    }>(`/compute/machines/${encodeURIComponent(machineId)}/guest/health`, {
      headers: { 'X-AXV-Host-ID': hostId },
      signal: AbortSignal.timeout(15000),
    }),
  submit: (values: VMProjectInitialization) =>
    request<VMTaskResult>(
      `/compute/projects/${encodeURIComponent(values.binding.projectId)}/initialize`,
      {
        method: 'POST',
        headers: { 'X-AXV-Host-ID': values.binding.hostId },
        body: JSON.stringify(values),
        signal: AbortSignal.timeout(30000),
      },
    ),
  finish: (values: VMProjectInitialization) =>
    request<{ project: VMProject }>(
      `/compute/projects/${encodeURIComponent(values.binding.projectId)}/initializations/${encodeURIComponent(values.requestId)}/finish`,
      {
        method: 'POST',
        headers: { 'X-AXV-Host-ID': values.binding.hostId },
        body: JSON.stringify({ binding: values.binding }),
        signal: AbortSignal.timeout(30000),
      },
    ),
};
