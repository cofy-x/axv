import { describe, expect, it, vi } from 'vitest';
import { APIError } from '../../shared/api/http';
import type {
  VMTaskAttempt,
  VMTaskResult,
  VMTaskSubmission,
} from './vm-task-api';
import { createVMTaskRequests } from './vm-task-requests';

const recoveryKey = 'task recovery';
const binding = {
  hostId: 'host',
  projectId: 'project',
  projectRevision: 1,
  volumeId: 'volume',
  volumeRevision: 2,
  machineId: 'vm',
  bootId: 'boot',
  instanceId: 'guest',
};
const spec = {
  argv: ['/bin/sh', '-lc', 'printf hello'],
  directory: '',
  limits: {
    timeoutSeconds: 900,
    cpus: 1,
    memoryMiB: 512,
    maxProcesses: 64,
    maxLogBytes: 1024,
  },
};
const original = { binding, spec, requestId: 'original-request' };
const later = { ...original, requestId: 'different-request' };

function result(submission: VMTaskSubmission = original): VMTaskResult {
  const attempt: VMTaskAttempt = {
    id: 'attempt-one',
    taskId: 'task-one',
    requestId: submission.requestId,
    number: 1,
    binding: submission.binding,
    status: 'running',
    root: 'repo',
    rootIdentity: 'root-id',
    guestFinal: false,
    logBytes: 0,
    cachedBytes: 0,
    logLimited: false,
    cancelRequested: false,
    createdAt: '',
  };
  return {
    task: {
      id: attempt.taskId,
      hostId: binding.hostId,
      projectId: binding.projectId,
      requestId: submission.requestId,
      spec,
      currentAttemptId: attempt.id,
      createdAt: '',
    },
    attempt,
    attempts: [attempt],
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

function harness(saved: VMTaskSubmission | null = original) {
  const records = new Map<string, string>();
  if (saved) records.set(recoveryKey, JSON.stringify(saved));
  const storage = {
    getItem: (key: string) => records.get(key) ?? null,
    setItem: vi.fn((key: string, raw: string) => {
      records.set(key, raw);
    }),
    removeItem: vi.fn((key: string) => {
      records.delete(key);
    }),
  };
  const options = {
    hostId: binding.hostId,
    projectId: binding.projectId,
    recoveryKey,
    isOpen: vi.fn(() => true),
    onSend: vi.fn(),
    onError: vi.fn(),
  };
  const api = {
    submit: vi.fn(async (_id: string, submission: VMTaskSubmission) =>
      result(submission),
    ),
    recover: vi.fn(async () => result()),
    cancel: vi.fn(async () => result()),
  };
  const actions = createVMTaskRequests(options, api, () => storage);
  const pending = () => {
    const { recovery } = actions.getSnapshot();
    if (recovery.kind !== 'pending')
      throw new Error('Expected a pending submission.');
    return recovery;
  };
  return {
    records,
    storage,
    options,
    api,
    actions,
    pending,
    accepted: vi.fn(),
  };
}

describe('VM task browser request ownership', () => {
  it('persists the full request before sending and blocks work if storage fails', async () => {
    const { actions, api, options, storage, accepted, records } = harness(null);
    storage.setItem.mockImplementationOnce(() => {
      throw new Error('Storage unavailable');
    });
    await actions.submit({ binding, spec }, accepted);
    expect(api.submit).not.toHaveBeenCalled();
    expect(options.onError).toHaveBeenCalledWith(
      expect.stringContaining('Storage unavailable'),
    );

    api.submit.mockImplementationOnce(async (_id, submission) => {
      expect(JSON.parse(records.get(recoveryKey)!)).toEqual(submission);
      return result(submission);
    });
    await actions.submit({ binding, spec }, accepted);
    expect(api.submit).toHaveBeenCalledOnce();
    expect(accepted).toHaveBeenCalledOnce();
    expect(records.has(recoveryKey)).toBe(false);
  });

  it.each([
    ['not_found', true],
    ['http_error', false],
  ] as const)(
    'handles missing receipt code %s with the original request only',
    async (code, replay) => {
      const { actions, api, accepted, pending, records } = harness();
      api.recover.mockRejectedValueOnce(
        new APIError('Missing receipt', 404, code),
      );
      await actions.recover(pending(), accepted);
      expect(api.recover).toHaveBeenCalledExactlyOnceWith(
        'project',
        'host',
        original.requestId,
      );
      if (replay) {
        expect(api.submit).toHaveBeenCalledExactlyOnceWith('project', original);
        expect(accepted).toHaveBeenCalledOnce();
        expect(records.has(recoveryKey)).toBe(false);
      } else {
        expect(api.submit).not.toHaveBeenCalled();
        expect(accepted).not.toHaveBeenCalled();
        expect(records.get(recoveryKey)).toBe(JSON.stringify(original));
      }
    },
  );

  it('rechecks the saved bytes before replaying after an unconfirmed lookup', async () => {
    const { actions, api, accepted, pending, records } = harness();
    const lookup = deferred<VMTaskResult>();
    api.recover.mockReturnValueOnce(lookup.promise);
    const recovering = actions.recover(pending(), accepted);
    records.set(recoveryKey, JSON.stringify(later));
    lookup.reject(new APIError('Missing receipt', 404, 'not_found'));
    await recovering;
    expect(api.submit).not.toHaveBeenCalled();
    expect(accepted).not.toHaveBeenCalled();
    expect(pending().submission).toEqual(later);
  });

  it('acknowledges a late verified receipt while keeping a newer recovery record', async () => {
    const { actions, api, accepted, pending, records, options } = harness();
    const receipt = deferred<VMTaskResult>();
    api.recover.mockReturnValueOnce(receipt.promise);
    const recovering = actions.recover(pending(), accepted);
    const laterRaw = JSON.stringify(later);
    records.set(recoveryKey, laterRaw);
    receipt.resolve(result());
    await recovering;
    expect(api.submit).not.toHaveBeenCalled();
    expect(accepted).toHaveBeenCalledExactlyOnceWith(result());
    expect(records.get(recoveryKey)).toBe(laterRaw);
    expect(pending().submission).toEqual(later);
    expect(options.onError).toHaveBeenCalledWith(
      expect.stringContaining('Task accepted'),
    );
  });

  it('keeps the original record when receipt recovery returns another request or binding', async () => {
    const { actions, api, accepted, pending, records, options } = harness();
    api.recover.mockResolvedValueOnce(result(later));
    await actions.recover(pending(), accepted);
    api.recover.mockResolvedValueOnce(
      result({ ...original, binding: { ...binding, bootId: 'later-boot' } }),
    );
    await actions.recover(pending(), accepted);
    expect(api.submit).not.toHaveBeenCalled();
    expect(accepted).not.toHaveBeenCalled();
    expect(records.get(recoveryKey)).toBe(JSON.stringify(original));
    expect(options.onError).toHaveBeenCalledTimes(2);
  });

  it('reserves confirmation immediately and retires an unconfirmed cancel when the view closes', async () => {
    const { actions, api, accepted, options } = harness();
    const answer = deferred<boolean>();
    const confirm = vi.fn(() => answer.promise);
    const target = result().attempt;
    const first = actions.cancel(target, confirm, accepted);
    await actions.cancel(target, confirm, accepted);
    expect(confirm).toHaveBeenCalledOnce();
    expect(actions.getSnapshot().command.phase).toBe('confirming');
    options.isOpen.mockReturnValue(false);
    answer.resolve(true);
    await first;
    expect(api.cancel).not.toHaveBeenCalled();
    expect(accepted).not.toHaveBeenCalled();
  });

  it('sends cancellation against the original attempt and rejects a later attempt as its acknowledgement', async () => {
    const { actions, api, accepted, options } = harness();
    const target = result().attempt;
    const wrong = result();
    wrong.attempt = { ...target, id: 'attempt-two', number: 2 };
    api.cancel.mockResolvedValueOnce(wrong);
    await actions.cancel(target, async () => true, accepted);
    expect(api.cancel).toHaveBeenCalledExactlyOnceWith('project', target);
    expect(accepted).not.toHaveBeenCalled();
    expect(options.onError).toHaveBeenCalledWith(
      expect.stringContaining('different attempt'),
    );
  });
});
