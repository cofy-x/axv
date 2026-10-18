import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type {
  ComputeBinding,
  ComputeEnvironment,
  HostInfo,
  ImageInfo,
  Machine,
  Volume,
} from './api';
import { ComputeStore } from './compute-store';

const environment: ComputeEnvironment = {
  platform: 'darwin',
  configured: true,
  configPath: '/profile/axv.json',
  profileHome: '/profile',
  executablePath: '/bin/axv',
  requiredLimaVersion: '2.2.1',
  automaticSetupSupported: true,
};
const info: HostInfo = { configured: true, hostId: 'host-original' };
const machine = { id: 'vm-original', hostId: 'host-original' } as Machine;
const subscriptions: (() => void)[] = [];

function harness() {
  const api = {
    environment: vi.fn(async () => environment),
    info: vi.fn<
      (_signal?: AbortSignal, _binding?: ComputeBinding) => Promise<HostInfo>
    >(async () => info),
    machines: vi.fn(
      async (_signal?: AbortSignal, _binding?: ComputeBinding) => ({
        machines: [machine],
      }),
    ),
    volumes: vi.fn(async () => ({ volumes: [] as Volume[] })),
    operations: vi.fn(async () => ({ operations: [] })),
    images: vi.fn(async () => ({ images: [] as ImageInfo[] })),
  };
  const store = new ComputeStore(api);
  const subscribe = () => {
    const listener = vi.fn();
    const unsubscribe = store.subscribe(listener);
    subscriptions.push(unsubscribe);
    return { listener, unsubscribe };
  };
  return { store, api, subscribe };
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  for (const unsubscribe of subscriptions.splice(0)) unsubscribe();
  vi.useRealTimers();
});

describe('shared compute inventory', () => {
  it('shares one poller across windows and probes dependencies only on explicit refresh', async () => {
    const { store, api, subscribe } = harness();
    const first = subscribe();
    const second = subscribe();
    expect(store.getSnapshot().loading).toBe(true);
    expect(store.getSnapshot().refreshing).toBe(true);
    await vi.advanceTimersByTimeAsync(0);
    expect(api.info).toHaveBeenCalledOnce();
    expect(api.machines).toHaveBeenCalledOnce();
    expect(first.listener).toHaveBeenCalled();
    expect(second.listener).toHaveBeenCalled();
    expect(store.getSnapshot()).toMatchObject({
      environment,
      machines: [machine],
      loading: false,
      refreshing: false,
    });
    await vi.advanceTimersByTimeAsync(5000);
    expect(api.machines).toHaveBeenCalledTimes(2);
    expect(api.info).toHaveBeenCalledOnce();
    expect(api.environment).toHaveBeenCalledTimes(4);
    store.refresh();
    await vi.advanceTimersByTimeAsync(0);
    expect(api.info).toHaveBeenCalledTimes(2);
    expect(api.machines).toHaveBeenCalledTimes(3);
    first.unsubscribe();
    await vi.advanceTimersByTimeAsync(5000);
    expect(api.machines).toHaveBeenCalledTimes(4);
    second.unsubscribe();
    await vi.advanceTimersByTimeAsync(20000);
    expect(api.machines).toHaveBeenCalledTimes(4);
  });

  it('keeps the last complete inventory and timestamp while the host is offline', async () => {
    const { store, api, subscribe } = harness();
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    const lastUpdated = store.getSnapshot().lastUpdated;
    api.volumes.mockRejectedValueOnce(new Error('Host is offline'));
    api.machines.mockResolvedValueOnce({ machines: [] });
    await vi.advanceTimersByTimeAsync(5000);
    expect(store.getSnapshot()).toMatchObject({
      machines: [machine],
      lastUpdated,
      loading: false,
      refreshing: false,
      error: 'Host is offline',
    });
    await vi.advanceTimersByTimeAsync(5000);
    expect(store.getSnapshot().error).toBeUndefined();
    expect(store.getSnapshot().lastUpdated).toBeGreaterThan(lastUpdated!);
    expect(api.info).toHaveBeenCalledOnce();
  });

  it('rechecks provider capabilities once when VM windows reopen after all subscriptions close', async () => {
    const { store, api, subscribe } = harness();
    const unavailable = {
      provider: 'lima',
      available: false,
      images: false,
    } as NonNullable<HostInfo['providers']>[number];
    api.info.mockResolvedValue({ ...info, providers: [unavailable] });
    const first = subscribe();
    const second = subscribe();
    await vi.advanceTimersByTimeAsync(0);
    first.unsubscribe();
    await vi.advanceTimersByTimeAsync(5000);
    expect(api.info).toHaveBeenCalledOnce();
    expect(api.images).not.toHaveBeenCalled();
    second.unsubscribe();
    await vi.advanceTimersByTimeAsync(20000);
    expect(api.machines).toHaveBeenCalledTimes(2);

    const repaired = { ...unavailable, available: true, images: true };
    api.info.mockResolvedValue({ ...info, providers: [repaired] });
    subscribe();
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    expect(api.info).toHaveBeenCalledTimes(2);
    expect(api.environment).toHaveBeenCalledTimes(6);
    expect(api.images).toHaveBeenCalledOnce();
    expect(store.getSnapshot().info?.providers).toEqual([repaired]);
    await vi.advanceTimersByTimeAsync(5000);
    expect(api.info).toHaveBeenCalledTimes(2);
    expect(api.images).toHaveBeenCalledTimes(2);
  });

  it('coalesces refresh clicks during a request without overlapping inventory fetches', async () => {
    const { store, api, subscribe } = harness();
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    let resolve!: (value: { machines: Machine[] }) => void;
    api.machines.mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done;
        }),
    );
    await vi.advanceTimersByTimeAsync(5000);
    store.refresh();
    store.refresh();
    expect(api.machines).toHaveBeenCalledTimes(2);
    resolve({ machines: [machine] });
    await vi.advanceTimersByTimeAsync(0);
    expect(api.info).toHaveBeenCalledTimes(2);
    expect(api.machines).toHaveBeenCalledTimes(3);
    expect(store.getSnapshot().refreshing).toBe(false);
  });

  it('aborts on the final detach and ignores a late response from an older subscription', async () => {
    const { store, api, subscribe } = harness();
    let resolve!: (value: { machines: Machine[] }) => void;
    let signal!: AbortSignal;
    api.machines.mockImplementationOnce((nextSignal) => {
      signal = nextSignal!;
      return new Promise((done) => {
        resolve = done;
      });
    });
    const first = subscribe();
    await vi.advanceTimersByTimeAsync(0);
    first.unsubscribe();
    expect(signal.aborted).toBe(true);
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    resolve({ machines: [{ id: 'stale-vm' } as Machine] });
    await vi.advanceTimersByTimeAsync(0);
    expect(store.getSnapshot().machines).toEqual([machine]);
  });

  it('exposes setup metadata without listing inventory when no host is configured', async () => {
    const { store, api, subscribe } = harness();
    api.environment.mockResolvedValue({ ...environment, configured: false });
    api.info.mockResolvedValue({
      configured: false,
      reason: 'No host configured',
    });
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    expect(store.getSnapshot().environment?.configured).toBe(false);
    expect(store.getSnapshot().info?.reason).toBe('No host configured');
    expect(store.getSnapshot().error).toBeUndefined();
    expect(api.machines).not.toHaveBeenCalled();
  });

  it('keeps environment metadata available when the first host probe fails', async () => {
    const { store, api, subscribe } = harness();
    api.info.mockRejectedValueOnce(new Error('Host socket unavailable'));
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    expect(store.getSnapshot()).toMatchObject({
      environment,
      loading: false,
      refreshing: false,
      error: 'Host socket unavailable',
    });
    await vi.advanceTimersByTimeAsync(5000);
    expect(store.getSnapshot().machines).toEqual([machine]);
    expect(store.getSnapshot().error).toBeUndefined();
  });

  it('updates ready image choices from inventory polls without another dependency probe', async () => {
    const { store, api, subscribe } = harness();
    const configured = {
      id: 'configured-image',
      status: 'configured',
    } as ImageInfo;
    const imported = { id: 'imported-image', status: 'queued' } as ImageInfo;
    api.info.mockResolvedValue({
      ...info,
      providers: [
        { images: true } as NonNullable<HostInfo['providers']>[number],
      ],
      images: [configured],
    });
    api.images.mockResolvedValue({ images: [configured, imported] });
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    expect(store.getSnapshot().info?.images).toEqual([configured]);
    expect(store.getSnapshot().images).toEqual([configured, imported]);
    const ready = { ...imported, status: 'ready' };
    api.images.mockResolvedValue({ images: [configured, ready] });
    await vi.advanceTimersByTimeAsync(5000);
    expect(api.info).toHaveBeenCalledOnce();
    expect(store.getSnapshot().info?.images).toEqual([configured, ready]);
    api.images.mockResolvedValue({
      images: [{ ...ready, status: 'deleting' }],
    });
    await vi.advanceTimersByTimeAsync(5000);
    expect(store.getSnapshot().info?.images).toEqual([]);
  });

  it('marks retained inventory unavailable if the desktop restarts without its host configuration', async () => {
    const { store, api, subscribe } = harness();
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    const lastUpdated = store.getSnapshot().lastUpdated;
    api.info.mockResolvedValue({
      configured: false,
      reason: 'No host configured',
    });
    api.environment.mockResolvedValue({ ...environment, configured: false });
    store.refresh();
    await vi.advanceTimersByTimeAsync(0);
    expect(store.getSnapshot()).toMatchObject({
      machines: [machine],
      error: 'No host configured',
      lastUpdated,
      info: { configured: true, hostId: 'host-original' },
    });
    expect(api.machines).toHaveBeenCalledOnce();
  });

  it('binds all inventory requests to the observed runtime and rejects a generation change before publication', async () => {
    const { store, api, subscribe } = harness();
    const runtime: NonNullable<ComputeEnvironment['runtime']> = {
      state: 'ready',
      mode: 'managed',
      hostId: 'host-original',
      generation: 1,
      readOnly: false,
    };
    const original = { ...environment, runtime };
    api.environment.mockResolvedValue(original);
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    const complete = store.getSnapshot();
    expect(api.info.mock.calls[0][1]).toEqual(runtime);
    expect(api.machines.mock.calls[0][1]).toEqual(runtime);
    const replacement = {
      ...environment,
      runtime: { ...runtime, hostId: 'host-new', generation: 2 },
    };
    api.environment
      .mockResolvedValueOnce(original)
      .mockResolvedValue(replacement);
    api.machines.mockResolvedValueOnce({ machines: [] });
    await vi.advanceTimersByTimeAsync(5000);
    expect(store.getSnapshot()).toMatchObject({
      environment: complete.environment,
      info: complete.info,
      machines: [machine],
      lastUpdated: complete.lastUpdated,
      error: expect.stringContaining('connection changed'),
    });
    const replacementMachine = { ...machine, hostId: 'host-new' };
    api.info.mockResolvedValue({ ...info, hostId: 'host-new' });
    api.machines.mockResolvedValue({ machines: [replacementMachine] });
    await vi.advanceTimersByTimeAsync(5000);
    expect(api.info).toHaveBeenCalledTimes(2);
    expect(store.getSnapshot()).toMatchObject({
      environment: replacement,
      info: { hostId: 'host-new' },
      machines: [replacementMachine],
    });
    expect(store.getSnapshot().error).toBeUndefined();
  });

  it('never publishes new host metadata beside retained inventory when an explicit refresh partially fails', async () => {
    const { store, api, subscribe } = harness();
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    const original = store.getSnapshot();
    api.info.mockResolvedValue({ ...info, hostId: 'host-other' });
    api.volumes.mockRejectedValue(new Error('New host inventory unavailable'));
    api.machines.mockResolvedValue({ machines: [] });
    store.refresh();
    await vi.advanceTimersByTimeAsync(0);
    expect(store.getSnapshot()).toMatchObject({
      info: original.info,
      machines: original.machines,
      lastUpdated: original.lastUpdated,
      error: 'New host inventory unavailable',
    });
  });

  it('rejects machine records belonging to a different host', async () => {
    const { store, api, subscribe } = harness();
    api.machines.mockResolvedValue({
      machines: [{ ...machine, hostId: 'unexpected-host' }],
    });
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    expect(store.getSnapshot().machines).toEqual([]);
    expect(store.getSnapshot().error).toContain('different host');
    expect(store.getSnapshot().lastUpdated).toBeUndefined();
  });
});
