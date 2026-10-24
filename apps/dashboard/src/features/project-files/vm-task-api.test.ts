import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  currentTaskAttempt,
  readVMTaskLog,
  validVMTaskSubmission,
  vmTaskLogPageBytes,
  vmTasksAPI,
} from './vm-task-api';
import type { VMTaskAttempt, VMTaskResult } from './vm-task-api';

afterEach(() => vi.restoreAllMocks());

function serveLog(bytes: Uint8Array, cachedBytes = () => bytes.length) {
  return vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const url = new URL(String(input), 'http://axv.test');
    const offset = Number(url.searchParams.get('offset'));
    const limit = Number(url.searchParams.get('limit'));
    const end = Math.max(offset, Math.min(offset + limit, cachedBytes()));
    return Response.json({
      offset,
      nextOffset: end,
      logBytes: bytes.length,
      data: btoa(String.fromCharCode(...bytes.subarray(offset, end))),
    });
  });
}

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
      requestedOffset: 0,
      offset: 0,
      nextOffset: bytes.length,
      displayedBytes: bytes.length,
      logBytes: bytes.length,
    });
  });

  it('reads final errors beyond the first page using the requested byte offset', async () => {
    const prefix = 'x'.repeat(vmTaskLogPageBytes);
    const final = 'Build failed: 依赖 unavailable\n';
    const bytes = new TextEncoder().encode(prefix + final);
    const fetch = vi
      .spyOn(globalThis, 'fetch')
      .mockImplementation(async (input) => {
        const url = new URL(String(input), 'http://axv.test');
        const offset = Number(url.searchParams.get('offset'));
        const limit = Number(url.searchParams.get('limit'));
        const end = Math.min(offset + limit, bytes.length);
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
      vmTaskLogPageBytes,
    );
    expect(log).toEqual({
      text: final,
      requestedOffset: vmTaskLogPageBytes,
      offset: vmTaskLogPageBytes,
      nextOffset: bytes.length,
      displayedBytes: bytes.length - vmTaskLogPageBytes,
      logBytes: bytes.length,
    });
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(String(fetch.mock.calls[0][0])).toContain(
      `offset=${vmTaskLogPageBytes - 3}&limit=32768`,
    );
  });

  it.each([
    ['好', 1],
    ['好', 2],
    ['🙂', 1],
    ['🙂', 2],
    ['🙂', 3],
  ])(
    'keeps %s whole across adjacent pages at byte %i',
    async (character, split) => {
      const prefix = 'x'.repeat(vmTaskLogPageBytes - split);
      const source = prefix + character + ' final error';
      const bytes = new TextEncoder().encode(source);
      const fetch = serveLog(bytes);
      const first = await readVMTaskLog(
        'project',
        'host',
        'task',
        'attempt',
        new AbortController().signal,
      );
      expect(first).toEqual({
        text: prefix,
        requestedOffset: 0,
        offset: 0,
        nextOffset: prefix.length,
        displayedBytes: prefix.length,
        logBytes: bytes.length,
      });
      const next = await readVMTaskLog(
        'project',
        'host',
        'task',
        'attempt',
        new AbortController().signal,
        first.nextOffset,
      );
      expect(next.text).toBe(character + ' final error');
      expect(next.offset).toBe(first.nextOffset);
      expect(next.nextOffset).toBe(bytes.length);
      expect(first.text + next.text).toBe(source);
      expect(
        fetch.mock.calls.every(
          ([input]) =>
            Number(
              new URL(String(input), 'http://axv.test').searchParams.get(
                'limit',
              ),
            ) <= 32768,
        ),
      ).toBe(true);
    },
  );

  it.each([
    ['好', 1],
    ['好', 2],
    ['🙂', 1],
    ['🙂', 2],
    ['🙂', 3],
  ])(
    'aligns a tail offset inside %s at byte %i to its actual source boundary',
    async (character, split) => {
      const prefix = 'Earlier output: ';
      const bytes = new TextEncoder().encode(
        prefix + character + ' final error',
      );
      serveLog(bytes);
      const requestedOffset = prefix.length + split;
      const log = await readVMTaskLog(
        'project',
        'host',
        'task',
        'attempt',
        new AbortController().signal,
        requestedOffset,
      );
      expect(log).toEqual({
        text: character + ' final error',
        requestedOffset,
        offset: prefix.length,
        nextOffset: bytes.length,
        displayedBytes: bytes.length - prefix.length,
        logBytes: bytes.length,
      });
    },
  );

  it('keeps tail pages bounded when UTF-8 alignment adds preceding context', async () => {
    const source = '🙂' + 'x'.repeat(vmTaskLogPageBytes) + ' end';
    const bytes = new TextEncoder().encode(source);
    serveLog(bytes);
    const log = await readVMTaskLog(
      'project',
      'host',
      'task',
      'attempt',
      new AbortController().signal,
      3,
    );
    expect(log.requestedOffset).toBe(3);
    expect(log.offset).toBe(0);
    expect(log.displayedBytes).toBe(vmTaskLogPageBytes + 3);
    expect(log.nextOffset).toBe(vmTaskLogPageBytes + 3);
    expect(log.text).toBe('🙂' + 'x'.repeat(vmTaskLogPageBytes - 1));
  });

  it('leaves genuinely invalid UTF-8 visible instead of treating it as a page boundary', async () => {
    const prefix = new TextEncoder().encode('x'.repeat(vmTaskLogPageBytes - 2));
    const bytes = new Uint8Array(vmTaskLogPageBytes + 2);
    bytes.set(prefix);
    bytes.set([0xe0, 0x80, 0x61, 0xe5], prefix.length);
    serveLog(bytes);
    const first = await readVMTaskLog(
      'project',
      'host',
      'task',
      'attempt',
      new AbortController().signal,
    );
    expect(first.nextOffset).toBe(vmTaskLogPageBytes);
    expect(first.text).toBe('x'.repeat(prefix.length) + '��');
    const next = await readVMTaskLog(
      'project',
      'host',
      'task',
      'attempt',
      new AbortController().signal,
      first.nextOffset,
    );
    expect(next.offset).toBe(first.nextOffset);
    expect(next.text).toBe('a�');
  });

  it('waits for incomplete cached boundary bytes without inventing characters', async () => {
    const prefix = 'x'.repeat(vmTaskLogPageBytes - 1);
    const bytes = new TextEncoder().encode(prefix + '🙂 final error');
    let cachedBytes = vmTaskLogPageBytes;
    serveLog(bytes, () => cachedBytes);
    const first = await readVMTaskLog(
      'project',
      'host',
      'task',
      'attempt',
      new AbortController().signal,
    );
    expect(first.text).toBe(prefix);
    expect(first.nextOffset).toBe(prefix.length);
    const partial = await readVMTaskLog(
      'project',
      'host',
      'task',
      'attempt',
      new AbortController().signal,
      first.nextOffset,
    );
    expect(partial.text).toBe('');
    expect(partial.nextOffset).toBe(cachedBytes);
    cachedBytes = bytes.length;
    const complete = await readVMTaskLog(
      'project',
      'host',
      'task',
      'attempt',
      new AbortController().signal,
      first.nextOffset,
    );
    expect(complete.text).toBe('🙂 final error');
  });

  it('supports an explicit zero offset for an empty log', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      Response.json({ offset: 0, nextOffset: 0, logBytes: 0, data: '' }),
    );
    await expect(
      readVMTaskLog(
        'project',
        'host',
        'task',
        'attempt',
        new AbortController().signal,
        0,
      ),
    ).resolves.toEqual({
      text: '',
      requestedOffset: 0,
      offset: 0,
      nextOffset: 0,
      displayedBytes: 0,
      logBytes: 0,
    });
  });

  it.each([
    -1,
    0.5,
    Number.NaN,
    Number.POSITIVE_INFINITY,
    Number.MAX_SAFE_INTEGER + 1,
  ])(
    'rejects invalid starting offset %s before requesting a log',
    async (offset) => {
      const fetch = vi.spyOn(globalThis, 'fetch');
      await expect(
        readVMTaskLog(
          'project',
          'host',
          'task',
          'attempt',
          new AbortController().signal,
          offset,
        ),
      ).rejects.toThrow('nonnegative safe integer');
      expect(fetch).not.toHaveBeenCalled();
    },
  );

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

  it.each([
    { offset: 0, nextOffset: 1, logBytes: 0, data: btoa('x') },
    { offset: 0, nextOffset: 2, logBytes: 2, data: btoa('x') },
    { offset: 0, nextOffset: 1, logBytes: 1.5, data: btoa('x') },
    {
      offset: 0,
      nextOffset: 1,
      logBytes: Number.MAX_SAFE_INTEGER + 1,
      data: btoa('x'),
    },
    {
      offset: 0,
      nextOffset: 32769,
      logBytes: 32769,
      data: btoa('x'.repeat(32769)),
    },
  ])('rejects malformed or overlarge log chunks %#', async (response) => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(Response.json(response));
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

  it('rejects invalid byte data instead of coercing it to an empty log', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      Response.json({ offset: 0, nextOffset: 0, logBytes: 0, data: false }),
    );
    await expect(
      readVMTaskLog(
        'project',
        'host',
        'task',
        'attempt',
        new AbortController().signal,
      ),
    ).rejects.toThrow('invalid byte data');
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
        body: JSON.stringify({ binding }),
      }),
    );
    expect(
      new Headers(fetch.mock.calls[0]?.[1]?.headers).get('X-AXV-Host-ID'),
    ).toBe('old-host');
  });
});
