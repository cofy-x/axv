import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SerialPoller } from './serial-poller';
import type { PollContext } from './serial-poller';

function deferred() {
  let resolve!: () => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<void>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

const pollers: SerialPoller[] = [];

function harness(
  overrides: Partial<ConstructorParameters<typeof SerialPoller>[0]> = {},
) {
  const options = {
    read: vi.fn(async (_context: PollContext) => {}),
    onStart: vi.fn(),
    onError: vi.fn(),
    onSettled: vi.fn(),
    timeoutMs: 15000,
    intervalMs: 5000,
    ...overrides,
  };
  const poller = new SerialPoller(options);
  pollers.push(poller);
  return { poller, options };
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  for (const poller of pollers.splice(0)) poller.stop();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('serial read lifecycle', () => {
  it('keeps one-shot resources idle until an explicit refresh and ignores refresh after stop', async () => {
    const { poller, options } = harness({ intervalMs: undefined });
    poller.start();
    await vi.advanceTimersByTimeAsync(30000);
    expect(options.read).toHaveBeenCalledOnce();
    expect(vi.getTimerCount()).toBe(0);
    poller.refresh();
    await vi.advanceTimersByTimeAsync(30000);
    expect(options.read).toHaveBeenCalledTimes(2);
    poller.stop();
    poller.refresh();
    await vi.advanceTimersByTimeAsync(30000);
    expect(options.read).toHaveBeenCalledTimes(2);
  });

  it('starts on demand, coalesces refreshes and measures polling from completion', async () => {
    const first = deferred();
    const read = vi
      .fn<(_context: PollContext) => Promise<void>>()
      .mockImplementationOnce(() => first.promise)
      .mockResolvedValue(undefined);
    const { poller } = harness({ read });
    poller.refresh();
    await vi.advanceTimersByTimeAsync(5000);
    expect(read).not.toHaveBeenCalled();
    poller.start();
    poller.start();
    poller.refresh();
    poller.refresh();
    await vi.advanceTimersByTimeAsync(10000);
    expect(read).toHaveBeenCalledOnce();
    first.resolve();
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(4999);
    expect(read).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(read).toHaveBeenCalledTimes(3);
    poller.stop();
    await vi.advanceTimersByTimeAsync(10000);
    expect(read).toHaveBeenCalledTimes(3);
  });

  it('retires pending reads on stop and ignores their late success or failure after restart', async () => {
    const first = deferred();
    const second = deferred();
    const read = vi
      .fn<(_context: PollContext) => Promise<void>>()
      .mockImplementationOnce(() => first.promise)
      .mockImplementationOnce(() => second.promise)
      .mockResolvedValue(undefined);
    const { poller, options } = harness({ read });
    poller.start();
    const old = read.mock.calls[0][0];
    poller.refresh();
    poller.stop();
    expect(old.signal.aborted).toBe(true);
    expect(old.isCurrent()).toBe(false);
    poller.start();
    first.resolve();
    await vi.advanceTimersByTimeAsync(0);
    expect(options.onSettled).not.toHaveBeenCalled();
    expect(read.mock.calls[1][0].isCurrent()).toBe(true);
    poller.stop();
    poller.start();
    second.reject(new Error('Old connection failed'));
    await vi.advanceTimersByTimeAsync(0);
    expect(options.onError).not.toHaveBeenCalled();
    expect(options.onSettled).toHaveBeenCalledOnce();
    expect(read).toHaveBeenCalledTimes(3);
  });

  it('does not leave a timer behind when a completion subscriber refreshes immediately', async () => {
    let refresh = true;
    const { poller, options } = harness({
      onSettled: () => {
        if (refresh) {
          refresh = false;
          poller.refresh();
        }
      },
    });
    poller.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(options.read).toHaveBeenCalledTimes(2);
    expect(vi.getTimerCount()).toBe(1);
    poller.stop();
    expect(vi.getTimerCount()).toBe(0);
    await vi.advanceTimersByTimeAsync(10000);
    expect(options.read).toHaveBeenCalledTimes(2);
  });

  it('does not schedule an old generation after a completion subscriber stops and restarts', async () => {
    let restart = true;
    const { poller, options } = harness({
      onSettled: () => {
        if (restart) {
          restart = false;
          poller.stop();
          poller.start();
        }
      },
    });
    poller.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(options.read).toHaveBeenCalledTimes(2);
    expect(vi.getTimerCount()).toBe(1);
    poller.stop();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('honors a stop from the loading notification before beginning transport work', async () => {
    const { poller, options } = harness({ onStart: () => poller.stop() });
    poller.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(options.read).not.toHaveBeenCalled();
    expect(options.onSettled).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('reports read errors and runs a queued recovery before returning to the current interval', async () => {
    let interval = 1000;
    const read = vi
      .fn<(_context: PollContext) => Promise<void>>()
      .mockRejectedValueOnce(new Error('Offline'))
      .mockResolvedValue(undefined);
    const { poller, options } = harness({
      read,
      intervalMs: () => interval,
      onError: () => {
        interval = 5000;
        poller.refresh();
      },
    });
    poller.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledTimes(2);
    expect(options.onSettled).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(4999);
    expect(read).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(read).toHaveBeenCalledTimes(3);
  });

  it('reports a timeout without accepting a late successful read', async () => {
    const timeout = new AbortController();
    vi.spyOn(AbortSignal, 'timeout').mockReturnValue(timeout.signal);
    const read = vi.fn(async ({ signal }: PollContext) => {
      await Promise.resolve();
      timeout.abort(new DOMException('Read timed out', 'TimeoutError'));
      signal.throwIfAborted();
    });
    const { poller, options } = harness({ read });
    poller.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(options.onError).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'TimeoutError' }),
      expect.objectContaining({
        signal: expect.objectContaining({ aborted: true }),
      }),
    );
    expect(options.onSettled).toHaveBeenCalledOnce();
  });
});
