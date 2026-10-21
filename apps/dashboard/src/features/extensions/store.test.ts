import { afterEach, describe, expect, it, vi } from 'vitest';
import { createExtensionStore } from './store';
import type { ExtensionCatalog, ExtensionRecord } from './types';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((yes) => {
    resolve = yes;
  });
  return { promise, resolve };
}

const app: ExtensionRecord = {
  id: 'example.tool',
  title: 'Example Tool',
  icon: 'checklist',
  version: '1.0.0',
  apiVersion: 1,
  capabilities: ['storage'],
  digest: 'a'.repeat(64),
  generation: 1,
  installed: true,
  enabled: true,
};
const catalog = { storeId: 'store-one', apps: [app] };
const definitionApps = [
  { id: app.id, title: app.title, icon: app.icon, installed: app.installed },
];

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('extension catalog store', () => {
  it('keeps the last complete catalog on query failure and supports explicit retry', async () => {
    const read = vi.fn(async (): Promise<ExtensionCatalog> => catalog);
    const store = createExtensionStore(read);
    await store.refresh();
    read.mockRejectedValueOnce(new Error('Offline'));
    await store.refresh();
    expect(store.getSnapshot()).toEqual({
      ...catalog,
      definitionApps,
      loading: false,
      error: 'Offline',
    });
    const changed = {
      ...catalog,
      apps: [{ ...app, enabled: false, generation: 2 }],
    };
    read.mockResolvedValueOnce(changed);
    await store.refresh();
    expect(store.getSnapshot()).toEqual({
      ...changed,
      definitionApps,
      loading: false,
      error: '',
    });
  });

  it('makes initial failures visible without claiming an initialized catalog', async () => {
    const store = createExtensionStore(async () => {
      throw new Error('Cannot load applications');
    });
    await store.refresh();
    expect(store.getSnapshot()).toEqual({
      storeId: '',
      apps: [],
      definitionApps: [],
      loading: false,
      error: 'Cannot load applications',
    });
  });

  it('coalesces overlapping refreshes instead of racing catalog snapshots', async () => {
    const pending = deferred<ExtensionCatalog>();
    const read = vi.fn(() => pending.promise);
    const store = createExtensionStore(read);
    const first = store.refresh();
    const second = store.refresh();
    expect(second).toBe(first);
    expect(read).toHaveBeenCalledOnce();
    pending.resolve(catalog);
    await first;
    expect(store.getSnapshot().apps).toEqual([app]);
  });

  it('forces a fresh catalog read after an older in-flight query following a mutation', async () => {
    const oldRead = deferred<ExtensionCatalog>();
    const newRead = deferred<ExtensionCatalog>();
    const read = vi
      .fn(() => oldRead.promise)
      .mockReturnValueOnce(oldRead.promise)
      .mockReturnValueOnce(newRead.promise);
    const store = createExtensionStore(read);
    const originalRefresh = store.refresh();
    expect(store.refresh()).toBe(originalRefresh);
    const changed = {
      ...catalog,
      apps: [{ ...app, enabled: false, generation: 2 }],
    };
    const forcedRefresh = store.refresh(true);
    let forcedComplete = false;
    void forcedRefresh.then(() => {
      forcedComplete = true;
    });
    expect(read).toHaveBeenCalledOnce();
    oldRead.resolve(catalog);
    await originalRefresh;
    expect(read).toHaveBeenCalledTimes(2);
    expect(forcedComplete).toBe(false);
    expect(store.getSnapshot().apps).toEqual(catalog.apps);
    newRead.resolve(changed);
    await forcedRefresh;
    expect(forcedComplete).toBe(true);
    expect(store.getSnapshot()).toEqual({
      ...changed,
      definitionApps,
      loading: false,
      error: '',
    });
    expect(read).toHaveBeenCalledTimes(2);
  });

  it('refreshes on subscription, focus and polling and removes owned listeners after unsubscribe', async () => {
    vi.useFakeTimers();
    const addEventListener = vi.fn();
    const removeEventListener = vi.fn();
    vi.stubGlobal('addEventListener', addEventListener);
    vi.stubGlobal('removeEventListener', removeEventListener);
    const read = vi.fn(async () => catalog);
    const store = createExtensionStore(read);
    const listener = vi.fn();
    const unsubscribe = store.subscribe(listener);
    await store.refresh();
    expect(read).toHaveBeenCalledOnce();
    expect(listener).toHaveBeenCalledOnce();
    const focus = addEventListener.mock.calls[0][1] as () => void;
    focus();
    await store.refresh();
    expect(read).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(15_000);
    expect(read).toHaveBeenCalledTimes(3);
    unsubscribe();
    expect(removeEventListener).toHaveBeenCalledWith('focus', focus);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(read).toHaveBeenCalledTimes(3);
  });

  it('retains only unavailable definition metadata when a replacement store no longer contains a seen application', async () => {
    const replacement: ExtensionCatalog = { storeId: 'store-two', apps: [] };
    const read = vi.fn(async (): Promise<ExtensionCatalog> => catalog);
    const store = createExtensionStore(read);
    await store.refresh();
    read.mockResolvedValueOnce(replacement);
    await store.refresh();
    expect(store.getSnapshot()).toEqual({
      ...replacement,
      definitionApps: [
        { id: app.id, title: app.title, icon: app.icon, installed: false },
      ],
      loading: false,
      error: '',
    });
    read.mockRejectedValueOnce(new Error('Offline'));
    await store.refresh();
    expect(store.getSnapshot().apps).toEqual([]);
    expect(store.getSnapshot().definitionApps).toEqual([
      { id: app.id, title: app.title, icon: app.icon, installed: false },
    ]);
    const reinstalled = {
      ...app,
      title: 'Reinstalled Tool',
      icon: 'document' as const,
      generation: 1,
    };
    read.mockResolvedValueOnce({ storeId: 'store-two', apps: [reinstalled] });
    await store.refresh();
    expect(store.getSnapshot().apps).toEqual([reinstalled]);
    expect(store.getSnapshot().definitionApps).toEqual([
      {
        id: app.id,
        title: 'Reinstalled Tool',
        icon: 'document',
        installed: true,
      },
    ]);
  });
});
