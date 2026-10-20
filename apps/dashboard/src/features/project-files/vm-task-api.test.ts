import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  currentTaskAttempt,
  readVMTaskLog,
  validVMTaskSubmission,
  vmTasksAPI,
} from './vm-task-api';
import type { VMTaskAttempt, VMTaskResult } from './vm-task-api';

afterEach(() => vi.restoreAllMocks());

describe('VM project task transport', () => {
  it('uses the current attempt for task actions when recovering an earlier receipt', () => {
    const original = { id: 'old', status: 'failed' } as VMTaskAttempt;
    const current = { id: 'new', status: 'running' } as VMTaskAttempt;
    const recovered = {
      task: { currentAttemptId: 'new' },
      attempt: original,
      attempts: [original, current],
    } as VMTaskResult;
    expect(recovered.attempt.id).toBe('old');
    expect(currentTaskAttempt(recovered)).toBe(current);
  });
  it('does not substitute the recovered attempt for a missing current identity', () => {
    const original = { id: 'old', status: 'failed' } as VMTaskAttempt;
    const recovered = {
      task: { currentAttemptId: 'missing' },
      attempt: original,
      attempts: [original],
    } as VMTaskResult;
    expect(currentTaskAttempt(recovered)).toBeUndefined();
  });

  it('blocks malformed recovery and bindings belonging to another host without rewriting them', () => {
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
    const value = { requestId: 'request-one', taskId: 'task', binding };
    expect(validVMTaskSubmission(value, 'host', 'project')).toBe(true);
    expect(validVMTaskSubmission(value, 'replacement-host', 'project')).toBe(
      false,
    );
    expect(
      validVMTaskSubmission(
        { ...value, binding: { ...binding, projectRevision: 0 } },
        'host',
        'project',
      ),
    ).toBe(false);
    expect(
      validVMTaskSubmission(
        { ...value, spec: { argv: ['/bin/sh'], directory: '', limits: {} } },
        'host',
        'project',
      ),
    ).toBe(false);
    expect(value.binding.hostId).toBe('host');
  });
  it('decodes UTF-8 spanning log pages and preserves escaped output as text', async () => {
    const bytes = new TextEncoder().encode('Build: 你好🙂\u001b[31m done\n');
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = new URL(String(input), 'http://axv.test');
      const offset = Number(url.searchParams.get('offset'));
      const end = Math.min(offset + 2, bytes.length);
      return Response.json({
        offset,
        nextOffset: end,
        logBytes: bytes.length,
        data: btoa(String.fromCharCode(...bytes.slice(offset, end))),
      });
    });
    const log = await readVMTaskLog(
      'project',
      'host',
      'task',
      'attempt',
      new AbortController().signal,
    );
    expect(log).toEqual({
      text: 'Build: 你好🙂␛[31m done\n',
      displayedBytes: bytes.length,
      logBytes: bytes.length,
    });
  });

  it('reports a partial cached log without inventing replacement characters at its end', async () => {
    const bytes = new TextEncoder().encode('好');
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const offset = Number(
        new URL(String(input), 'http://axv.test').searchParams.get('offset'),
      );
      return Response.json({
        offset,
        nextOffset: offset === 0 ? 1 : offset,
        logBytes: bytes.length,
        data: offset === 0 ? btoa(String.fromCharCode(bytes[0])) : '',
      });
    });
    const log = await readVMTaskLog(
      'project',
      'host',
      'task',
      'attempt',
      new AbortController().signal,
    );
    expect(log.text).toBe('');
    expect(log.displayedBytes).toBe(1);
    expect(log.logBytes).toBe(3);
  });

  it('rejects inconsistent byte offsets instead of displaying mixed log pages', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      Response.json({ offset: 5, nextOffset: 6, logBytes: 6, data: btoa('x') }),
    );
    await expect(
      readVMTaskLog(
        'project',
        'host',
        'task',
        'attempt',
        new AbortController().signal,
      ),
    ).rejects.toThrow('invalid byte offsets');
  });

  it('cancels only the captured attempt using its original binding', async () => {
    const binding = {
      hostId: 'old-host',
      projectId: 'project',
      projectRevision: 1,
      volumeId: 'volume',
      volumeRevision: 2,
      machineId: 'old-vm',
      bootId: 'old-boot',
      instanceId: 'old-guest',
    };
    const fetch = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(Response.json({}));
    await vmTasksAPI.cancel('project', {
      id: 'old-attempt',
      taskId: 'task',
      binding,
    } as VMTaskAttempt);
    expect(fetch).toHaveBeenCalledWith(
      '/api/compute/projects/project/tasks/task/attempts/old-attempt/cancel',
      expect.objectContaining({
        method: 'POST',
        headers: expect.objectContaining({ 'X-AXV-Host-ID': 'old-host' }),
        body: JSON.stringify({ binding }),
      }),
    );
  });
});
