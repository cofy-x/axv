import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { CloudflareStore } from './status-store';
import type { CloudflareConnection } from './types';

const info: CloudflareConnection = {
  local: true,
  cloudflare: { enabled: true, configured: true, state: 'listening' },
};
const cleanup: (() => void)[] = [];

function harness() {
  const api = {
    status: vi.fn<(_signal?: AbortSignal) => Promise<CloudflareConnection>>(
      async () => info,
    ),
  };
  const store = new CloudflareStore(api);
  const subscribe = () => {
    const unsubscribe = store.subscribe(() => undefined);
    cleanup.push(unsubscribe);
    return unsubscribe;
  };
  return { api, store, subscribe };
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  for (const unsubscribe of cleanup.splice(0)) unsubscribe();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('Cloudflare status observation', () => {
  it('starts on demand and stops when the utility is inactive', async () => {
    const { api, subscribe } = harness();
    await vi.advanceTimersByTimeAsync(10000);
    expect(api.status).not.toHaveBeenCalled();
    const unsubscribe = subscribe();
    await vi.advanceTimersByTimeAsync(5000);
    expect(api.status).toHaveBeenCalledTimes(2);
    unsubscribe();
    await vi.advanceTimersByTimeAsync(10000);
    expect(api.status).toHaveBeenCalledTimes(2);
  });

  it('retains earlier details as stale after failure, and recovers with a read', async () => {
    const { api, store, subscribe } = harness();
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    api.status.mockRejectedValueOnce(new Error('Cannot check status.'));
    await vi.advanceTimersByTimeAsync(5000);
    expect(store.getSnapshot()).toMatchObject({
      info,
      error: 'Cannot check status.',
      loading: false,
    });
    store.refresh();
    await vi.advanceTimersByTimeAsync(0);
    expect(store.getSnapshot()).toMatchObject({ info, error: undefined });
  });

  it('aborts inactive requests and cannot replace current status with a late reply', async () => {
    const { api, store, subscribe } = harness();
    let resolve!: (value: CloudflareConnection) => void;
    let signal!: AbortSignal;
    api.status.mockImplementationOnce((requestSignal) => {
      signal = requestSignal!;
      return new Promise((done) => {
        resolve = done;
      });
    });
    const unsubscribe = subscribe();
    unsubscribe();
    expect(signal.aborted).toBe(true);
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    resolve({ ...info, cloudflare: { ...info.cloudflare, state: 'error' } });
    await vi.advanceTimersByTimeAsync(0);
    expect(store.getSnapshot().info).toBe(info);
  });
});
