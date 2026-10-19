import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ConnectionStore } from './connection-store';
import type { ConnectionInfo } from './types';

const info: ConnectionInfo = {
  local: true,
  remote: { state: 'disabled' },
  host: { installed: true, state: 'connected', ips: ['100.65.1.2'] },
};
const cleanup: (() => void)[] = [];

function harness() {
  const api = {
    status: vi.fn<(_signal?: AbortSignal) => Promise<ConnectionInfo>>(
      async () => info,
    ),
  };
  const store = new ConnectionStore(api);
  const subscribe = () => {
    const listener = vi.fn();
    const unsubscribe = store.subscribe(listener);
    cleanup.push(unsubscribe);
    return { listener, unsubscribe };
  };
  return { store, api, subscribe };
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  for (const unsubscribe of cleanup.splice(0)) unsubscribe();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('shared read-only connection status', () => {
  it('starts on demand, shares active subscriptions and stops after the last detach', async () => {
    const { store, api, subscribe } = harness();
    await vi.advanceTimersByTimeAsync(15000);
    expect(api.status).not.toHaveBeenCalled();
    const view = subscribe();
    const panel = subscribe();
    await vi.advanceTimersByTimeAsync(0);
    expect(api.status).toHaveBeenCalledOnce();
    expect(store.getSnapshot()).toMatchObject({ info, loading: false });
    await vi.advanceTimersByTimeAsync(5000);
    expect(api.status).toHaveBeenCalledTimes(2);
    panel.unsubscribe();
    await vi.advanceTimersByTimeAsync(5000);
    expect(api.status).toHaveBeenCalledTimes(3);
    view.unsubscribe();
    await vi.advanceTimersByTimeAsync(15000);
    expect(api.status).toHaveBeenCalledTimes(3);
  });

  it('coalesces manual refreshes without overlapping requests', async () => {
    const { store, api, subscribe } = harness();
    let resolve!: (value: ConnectionInfo) => void;
    api.status.mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done;
        }),
    );
    subscribe();
    store.refresh();
    store.refresh();
    expect(api.status).toHaveBeenCalledOnce();
    resolve(info);
    await vi.advanceTimersByTimeAsync(0);
    expect(api.status).toHaveBeenCalledTimes(2);
    expect(store.getSnapshot().loading).toBe(false);
  });

  it('retains useful last details while clearly reporting failure, then recovers', async () => {
    const { store, api, subscribe } = harness();
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    const updatedAt = store.getSnapshot().updatedAt;
    api.status.mockRejectedValueOnce(
      new Error('The workstation is unavailable.'),
    );
    await vi.advanceTimersByTimeAsync(5000);
    expect(store.getSnapshot()).toMatchObject({
      info,
      updatedAt,
      loading: false,
      error: 'The workstation is unavailable.',
    });
    await vi.advanceTimersByTimeAsync(5000);
    expect(store.getSnapshot().error).toBeUndefined();
    expect(store.getSnapshot().updatedAt).toBeGreaterThan(updatedAt!);
  });

  it('aborts the final subscription and rejects late responses from an earlier view', async () => {
    const { store, api, subscribe } = harness();
    let resolve!: (value: ConnectionInfo) => void;
    let signal!: AbortSignal;
    api.status.mockImplementationOnce((requestSignal) => {
      signal = requestSignal!;
      return new Promise((done) => {
        resolve = done;
      });
    });
    const first = subscribe();
    first.unsubscribe();
    expect(signal.aborted).toBe(true);
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    resolve({
      ...info,
      remote: { state: 'ready', url: 'https://old.example.ts.net' },
    });
    await vi.advanceTimersByTimeAsync(0);
    expect(store.getSnapshot().info).toBe(info);
  });

  it('bounds a hung status fetch and retries on the next poll', async () => {
    const { store, api, subscribe } = harness();
    const deadline = new AbortController();
    const timeout = vi
      .spyOn(AbortSignal, 'timeout')
      .mockReturnValue(deadline.signal);
    api.status.mockImplementationOnce(
      (signal) =>
        new Promise((_resolve, reject) => {
          signal!.addEventListener('abort', () => reject(signal!.reason), {
            once: true,
          });
        }),
    );
    subscribe();
    expect(timeout).toHaveBeenCalledWith(8000);
    deadline.abort(new DOMException('Timed out', 'TimeoutError'));
    await vi.advanceTimersByTimeAsync(0);
    expect(store.getSnapshot()).toMatchObject({
      loading: false,
      error: 'The connection status check timed out. Refresh to retry.',
    });
    timeout.mockRestore();
    await vi.advanceTimersByTimeAsync(5000);
    expect(store.getSnapshot()).toMatchObject({
      info,
      loading: false,
      error: undefined,
    });
  });
});
