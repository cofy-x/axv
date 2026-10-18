import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type {
  PreparationCheck,
  PreparationJob,
  PreparationPlan,
  machinesApi,
} from './api';
import { PreparationStore } from './preparation-store';

const check: PreparationCheck = {
  cacheHome: '/fixture/cache',
  officialImageAvailable: false,
  platform: 'darwin',
  arch: 'arm64',
  configured: false,
  configurationSource: 'default profile',
  automaticSetupSupported: true,
  requiredLimaVersion: '2.2.1',
  instanceId: 'server-one',
  defaults: { storageRoot: '/profile/compute' },
  checks: [],
};
const plan: PreparationPlan = {
  id: 'plan-one',
  createdAt: '2026-10-17T00:00:00Z',
  expiresAt: '2026-10-17T01:00:00Z',
  cacheHome: '/fixture/cache',
  platform: 'darwin',
  arch: 'arm64',
  provider: 'lima',
  request: check.defaults,
  actions: ['Prepare private Lima'],
  downloads: [],
  checks: [],
  canStart: true,
  configPath: '/profile/config.json',
  hostConfigPath: '/profile/host.json',
  activation: 'ready',
};
const job: PreparationJob = {
  id: 'job-one',
  requestId: 'request-one',
  planId: plan.id,
  revision: 1,
  status: 'running',
  attempt: 1,
  phase: 'download',
  steps: [],
  createdAt: plan.createdAt,
  updatedAt: plan.createdAt,
  committed: false,
  canCancel: true,
  canRetry: false,
  plan,
};
const subscriptions: (() => void)[] = [];

function harness() {
  const api = {
    preparationCheck: vi.fn<
      (_signal?: AbortSignal) => Promise<PreparationCheck>
    >(async () => structuredClone(check)),
    preparationPlan: vi.fn<typeof machinesApi.preparationPlan>(
      async () => plan,
    ),
    preparationJob: vi.fn<typeof machinesApi.preparationJob>(async () => job),
    startPreparation: vi.fn<typeof machinesApi.startPreparation>(
      async () => job,
    ),
    preparationAction: vi.fn<typeof machinesApi.preparationAction>(
      async () => job,
    ),
  };
  const changed = vi.fn();
  const store = new PreparationStore(api, changed);
  const subscribe = () => {
    const unsubscribe = store.subscribe(vi.fn());
    subscriptions.push(unsubscribe);
    return unsubscribe;
  };
  return { api, store, changed, subscribe };
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  for (const unsubscribe of subscriptions.splice(0)) unsubscribe();
  vi.useRealTimers();
});

describe('shared preparation jobs', () => {
  it('only reads on discovery, shares one job poller and resumes after all windows close', async () => {
    const { api, store, subscribe } = harness();
    api.preparationCheck.mockResolvedValue({ ...check, activeJob: job });
    const first = subscribe();
    const second = subscribe();
    await vi.advanceTimersByTimeAsync(0);
    expect(api.preparationCheck).toHaveBeenCalledOnce();
    expect(api.preparationPlan).not.toHaveBeenCalled();
    expect(api.startPreparation).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1000);
    expect(api.preparationJob).toHaveBeenCalledOnce();
    expect(store.getSnapshot().job?.id).toBe(job.id);
    first();
    second();
    await vi.advanceTimersByTimeAsync(10000);
    expect(api.preparationJob).toHaveBeenCalledOnce();
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    expect(api.preparationCheck).toHaveBeenCalledTimes(2);
    expect(store.getSnapshot().job?.id).toBe(job.id);
    expect(api.preparationAction).not.toHaveBeenCalled();
  });

  it('coalesces start clicks and keeps the original plan and server identity after a lost response', async () => {
    const { api, store, subscribe } = harness();
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    api.startPreparation.mockRejectedValueOnce(
      new TypeError('Connection lost'),
    );
    const first = store.start(plan);
    expect(store.start({ ...plan, id: 'different-plan' })).toBe(first);
    await first;
    expect(store.getSnapshot()).toMatchObject({
      error: 'Connection lost',
      pending: { kind: 'start' },
    });
    api.preparationCheck.mockResolvedValue({
      ...check,
      instanceId: 'server-two',
      activeJob: job,
    });
    store.refresh();
    await vi.advanceTimersByTimeAsync(0);
    await store.resolve();
    expect(api.startPreparation).toHaveBeenCalledTimes(2);
    expect(api.startPreparation.mock.calls[1]).toEqual(
      api.startPreparation.mock.calls[0],
    );
    expect(api.startPreparation.mock.calls[1]).toEqual([
      plan.id,
      expect.any(String),
      'server-one',
    ]);
    expect(store.getSnapshot().pending).toBeUndefined();
  });

  it('retries the exact failed job revision while later polls advance the visible job', async () => {
    const { api, store, subscribe } = harness();
    const failed = {
      ...job,
      status: 'failed' as const,
      revision: 7,
      canRetry: true,
    };
    api.preparationCheck.mockResolvedValue({ ...check, latestJob: failed });
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    api.preparationAction.mockRejectedValueOnce(new TypeError('Response lost'));
    await store.retry(failed);
    api.preparationCheck.mockResolvedValue({
      ...check,
      activeJob: { ...job, revision: 10, attempt: 2 },
    });
    store.refresh();
    await vi.advanceTimersByTimeAsync(0);
    await store.resolve();
    expect(api.preparationAction.mock.calls[1]).toEqual(
      api.preparationAction.mock.calls[0],
    );
    expect(api.preparationAction.mock.calls[1]).toEqual([
      job.id,
      'retry',
      7,
      expect.any(String),
      'server-one',
    ]);
    expect(store.getSnapshot().job?.revision).toBe(10);
  });

  it('does not cancel accepted work when the last window closes during submission', async () => {
    const { api, store, subscribe } = harness();
    const detach = subscribe();
    await vi.advanceTimersByTimeAsync(0);
    let finish!: (value: PreparationJob) => void;
    api.startPreparation.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    const submit = store.start(plan);
    await Promise.resolve();
    detach();
    finish(job);
    await submit;
    expect(store.getSnapshot().job?.id).toBe(job.id);
    expect(api.preparationAction).not.toHaveBeenCalled();
  });

  it('retains the accepted job through connection loss and refreshes inventory on completion', async () => {
    const { api, store, changed, subscribe } = harness();
    api.preparationCheck.mockResolvedValue({ ...check, activeJob: job });
    subscribe();
    await vi.advanceTimersByTimeAsync(0);
    api.preparationJob.mockRejectedValueOnce(new Error('Service offline'));
    await vi.advanceTimersByTimeAsync(1000);
    expect(store.getSnapshot()).toMatchObject({
      job,
      connectionError: 'Service offline',
    });
    const completed = {
      ...job,
      status: 'succeeded' as const,
      revision: 2,
      committed: true,
      canCancel: false,
    };
    api.preparationJob.mockResolvedValue(completed);
    api.preparationCheck.mockResolvedValue({
      ...check,
      configured: true,
      latestJob: completed,
    });
    await vi.advanceTimersByTimeAsync(5000);
    expect(store.getSnapshot().job).toEqual(completed);
    expect(store.getSnapshot().connectionError).toBeUndefined();
    expect(changed).toHaveBeenCalledOnce();
    expect(api.preparationCheck).toHaveBeenCalledTimes(2);
  });
});
