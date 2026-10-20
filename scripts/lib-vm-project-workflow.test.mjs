import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, expect, it, vi } from 'vitest';
import { verifyProjectWorkflow } from './lib-vm-project-workflow.mjs';

const clock = vi.hoisted(() => ({ delay: vi.fn() }));
vi.mock('node:timers/promises', () => ({ setTimeout: clock.delay }));

const directories = [];
afterEach(async () => {
  vi.useRealTimers();
  vi.resetAllMocks();
  await Promise.all(
    directories
      .splice(0)
      .map((path) => rm(path, { recursive: true, force: true })),
  );
});

it.each([
  ['missing durable tail', 1, undefined, /log tail was not fully recovered/],
  ['wrong byte range', 2, { offset: 1, nextOffset: 3 }, /1 !== 0/],
  ['wrong attempt identity', 2, { attemptId: 'replacement' }, /replacement/],
])(
  'does not accept a completed task with %s',
  async (_name, cachedBytes, overrides, message) => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(0);
    clock.delay.mockImplementation(async () => vi.setSystemTime(600000));
    const directory = await mkdtemp(join(tmpdir(), 'axv-workflow-'));
    directories.push(directory);
    const bindingPath = join(directory, 'binding.json');
    await writeFile(
      bindingPath,
      JSON.stringify({ binding: { hostId: 'host-original' } }),
    );
    const cliJSON = vi.fn().mockResolvedValue({
      task: { id: 'task-original' },
      attempt: { id: 'attempt-original' },
    });
    const request = vi.fn(async (path) =>
      path.endsWith('/logs?offset=0&limit=65536')
        ? {
            taskId: 'task-original',
            attemptId: 'attempt-original',
            offset: 0,
            data: Buffer.from('ok').toString('base64'),
            nextOffset: 2,
            ...overrides,
          }
        : {
            attempt: {
              id: 'attempt-original',
              status: 'succeeded',
              exitCode: 0,
              logBytes: 2,
              cachedBytes,
            },
          },
    );
    await expect(
      verifyProjectWorkflow({
        cliJSON,
        request,
        project: { id: 'project-original' },
        bindingPath,
        directory,
        token: 'test',
      }),
    ).rejects.toThrow(message);
    expect(cliJSON).toHaveBeenCalledTimes(1);
  },
);
