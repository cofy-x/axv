import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { CloudflareDiagnosticsStore } from './diagnostics-store';
import type { CloudflareStatus } from './types';

const status: CloudflareStatus = {
  instanceId: 'diagnostics-fixture',
  enabled: true,
  configured: true,
  state: 'listening',
  listen: '127.0.0.1:3002',
};
const cleanup: (() => void)[] = [];

function harness() {
  const read = vi.fn<(_signal?: AbortSignal) => Promise<CloudflareStatus>>(
    async () => status,
  );
  const store = new CloudflareDiagnosticsStore(read);
  const subscribe = () => {
    const unsubscribe = store.subscribe(() => undefined);
    cleanup.push(unsubscribe);
    return unsubscribe;
  };
  return { read, store, subscribe };
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  for (const unsubscribe of cleanup.splice(0)) unsubscribe();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('local Cloudflare diagnostics observation', () => {
  it('lets a six-second read finish before scheduling another check', async () => {
    const { read, store, subscribe } = harness();
    read.mockImplementationOnce(
      () => new Promise((resolve) => setTimeout(() => resolve(status), 6000)),
    );
    await vi.advanceTimersByTimeAsync(5000);
    expect(read).not.toHaveBeenCalled();
    subscribe();
    await vi.advanceTimersByTimeAsync(6000);
    expect(read).toHaveBeenCalledTimes(1);
    expect(store.getSnapshot()).toMatchObject({ status, loading: false });
    await vi.advanceTimersByTimeAsync(5000);
    expect(read).toHaveBeenCalledTimes(2);
  });

  it('queues refresh once without aborting or overlapping the current read', async () => {
    const { read, store, subscribe } = harness();
    let resolve!: (value: CloudflareStatus) => void;
    let signal!: AbortSignal;
    read.mockImplementationOnce((requestSignal) => {
      signal = requestSignal!;
      return new Promise((done) => {
        resolve = done;
      });
    });
    subscribe();
    store.refresh();
    store.refresh();
    await vi.advanceTimersByTimeAsync(6000);
    expect(read).toHaveBeenCalledTimes(1);
    expect(signal.aborted).toBe(false);
    resolve(status);
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledTimes(2);
    expect(store.getSnapshot()).toMatchObject({ status, loading: false });
  });

  it('retains diagnostics during a read and marks failed updates stale until recovery', async () => {
    const { read, store, subscribe } = harness();
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    read.mockRejectedValueOnce(new Error('Could not read diagnostics.'));
    store.refresh();
    expect(store.getSnapshot()).toMatchObject({ status, loading: true });
    await vi.advanceTimersByTimeAsync(0);
    expect(store.getSnapshot()).toMatchObject({
      status,
      error: 'Could not read diagnostics.',
      loading: false,
    });
    store.refresh();
    await vi.advanceTimersByTimeAsync(0);
    expect(store.getSnapshot()).toMatchObject({
      status,
      error: undefined,
      loading: false,
    });
  });

  it('aborts when inactive, ignores a late result and stops polling', async () => {
    const { read, store, subscribe } = harness();
    let resolve!: (value: CloudflareStatus) => void;
    let signal!: AbortSignal;
    read.mockImplementationOnce((requestSignal) => {
      signal = requestSignal!;
      return new Promise((done) => {
        resolve = done;
      });
    });
    const unsubscribe = subscribe();
    store.refresh();
    unsubscribe();
    expect(signal.aborted).toBe(true);
    await vi.advanceTimersByTimeAsync(10000);
    expect(read).toHaveBeenCalledTimes(1);
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    resolve({ ...status, instanceId: 'obsolete-diagnostics' });
    await vi.advanceTimersByTimeAsync(0);
    expect(store.getSnapshot().status).toBe(status);
  });
});
