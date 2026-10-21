import type { VMProjectBinding } from '../project-files/vm-api';
import { APIError } from '../../shared/api/http';

// These refusals follow request replay lookup and precede run reservation.
// Shutdown, transport and reconciliation failures can describe an existing run.
const preLaunchRefusals = new Map<string, readonly number[]>([
  ['codex_limit', [503]],
  ['codex_unavailable', [503]],
  ['vm_project_unavailable', [503]],
  ['vm_project_changed', [409]],
  ['conversation_active', [409]],
  ['run_changed', [409]],
  ['project_changed', [409]],
  ['workspace_changed', [409]],
  ['history_unavailable', [409, 503]],
]);

export function launchRejectedBeforeStart(error: unknown) {
  return (
    error instanceof APIError &&
    preLaunchRefusals.get(error.code)?.includes(error.status) === true
  );
}

export interface VMProjectRequest {
  requestId: string;
  binding: VMProjectBinding;
  expectedRunId?: string;
}

export function validVMProjectBinding(
  value: unknown,
): value is VMProjectBinding {
  if (!value || typeof value !== 'object') return false;
  const binding = value as VMProjectBinding;
  return (
    [
      'hostId',
      'projectId',
      'volumeId',
      'machineId',
      'bootId',
      'instanceId',
    ].every(
      (key) =>
        typeof binding[key as keyof VMProjectBinding] === 'string' &&
        !!binding[key as keyof VMProjectBinding],
    ) &&
    Number.isSafeInteger(binding.projectRevision) &&
    binding.projectRevision > 0 &&
    Number.isSafeInteger(binding.volumeRevision) &&
    binding.volumeRevision > 0
  );
}

/** An uncertain launch retains the entire reviewed environment, across reloads. */
export class VMProjectRequests {
  constructor(
    private readonly key: string,
    private readonly hostId: string,
    private readonly projectId: string,
    private readonly storage?: Pick<
      Storage,
      'getItem' | 'setItem' | 'removeItem'
    >,
  ) {}

  private persistence() {
    return this.storage || globalThis.sessionStorage;
  }

  read(): VMProjectRequest | undefined {
    const raw = this.persistence().getItem(this.key);
    if (raw === null) return;
    let value: VMProjectRequest;
    try {
      value = JSON.parse(raw) as VMProjectRequest;
      if (
        !value ||
        typeof value !== 'object' ||
        typeof value.requestId !== 'string' ||
        value.requestId.length < 8 ||
        value.requestId.length > 128 ||
        !validVMProjectBinding(value.binding) ||
        value.binding.hostId !== this.hostId ||
        value.binding.projectId !== this.projectId ||
        (value.expectedRunId !== undefined &&
          (typeof value.expectedRunId !== 'string' || !value.expectedRunId))
      )
        throw new Error('Invalid request');
    } catch {
      throw new Error(
        'The saved Codex launch request could not be read. Preserve this tab’s recovery data and inspect the session list before clearing it.',
      );
    }
    return value;
  }

  capture(binding: VMProjectBinding, expectedRunId?: string) {
    const pending = this.read();
    if (pending) return pending;
    if (
      !validVMProjectBinding(binding) ||
      binding.hostId !== this.hostId ||
      binding.projectId !== this.projectId
    )
      throw new Error('The reviewed VM project environment is invalid.');
    const request: VMProjectRequest = {
      requestId: crypto.randomUUID(),
      binding: { ...binding },
      ...(expectedRunId ? { expectedRunId } : {}),
    };
    this.persistence().setItem(this.key, JSON.stringify(request));
    return request;
  }

  complete(requestId: string) {
    if (this.read()?.requestId === requestId)
      this.persistence().removeItem(this.key);
  }
}
