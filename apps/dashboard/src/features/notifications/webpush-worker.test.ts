import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { describe, expect, it, vi } from 'vitest';

const worker = readFileSync(
  new URL('../../../public/axv-push/worker.js', import.meta.url),
  'utf8',
);
const origin = 'https://axv.example';
const current = {
  version: 1,
  storeId: 'a'.repeat(32),
  keyId: 'b'.repeat(32),
  installationId: '1'.repeat(32),
  subscriptionId: '2'.repeat(32),
  origin,
};
const payload = () => ({
  version: 1,
  storeId: current.storeId,
  keyId: current.keyId,
  installationId: current.installationId,
  subscriptionId: current.subscriptionId,
  notificationId: 'c'.repeat(32),
  expiresAt: new Date(Date.now() + 60000).toISOString(),
});

function fixture(saved: unknown = current) {
  const callbacks = new Map<string, (event: unknown) => void>();
  const show = vi.fn().mockResolvedValue(undefined);
  const clients = {
    matchAll: vi.fn().mockResolvedValue([]),
    openWindow: vi.fn().mockResolvedValue(null),
  };
  let remembered: unknown;
  const indexedDB = {
    open: () => {
      const request: { result: unknown; onsuccess?: () => void } = {
        result: {
          transaction: () => {
            const transaction: {
              objectStore: () => unknown;
              oncomplete?: () => void;
              onabort?: () => void;
              abort: () => void;
            } = {
              objectStore: () => ({
                get: (key: string) => {
                  const read: { result: unknown; onsuccess?: () => void } = {
                    result: key === 'current' ? saved : remembered,
                  };
                  queueMicrotask(() => {
                    read.onsuccess?.();
                    queueMicrotask(() => transaction.oncomplete?.());
                  });
                  return read;
                },
                put: (value: unknown, key: string) => {
                  if (key === 'open') remembered = value;
                },
              }),
              abort: () => {
                queueMicrotask(() => transaction.onabort?.());
              },
            };
            return transaction;
          },
          close: () => {},
        },
      };
      queueMicrotask(() => request.onsuccess?.());
      return request;
    },
  };
  runInNewContext(worker, {
    URL,
    Date,
    indexedDB,
    self: {
      location: { origin },
      clients,
      registration: { showNotification: show },
      addEventListener: (kind: string, callback: (event: unknown) => void) =>
        callbacks.set(kind, callback),
    },
  });

  async function dispatch(kind: string, fields: Record<string, unknown>) {
    let result: Promise<void> | undefined;
    callbacks.get(kind)?.({
      ...fields,
      waitUntil: (value: Promise<void>) => {
        result = value;
      },
    });
    await result;
  }

  return {
    show,
    clients,
    callbacks,
    remembered: () => remembered,
    push: (value: unknown) =>
      dispatch('push', { data: { text: () => JSON.stringify(value) } }),
    click: (data: unknown) =>
      dispatch('notificationclick', { notification: { data, close: vi.fn() } }),
  };
}

describe('metadata-only visible push worker', () => {
  it('displays from bounded encrypted metadata without any private fetch, cache or action', async () => {
    const f = fixture();
    const value = payload();
    await f.push(value);
    expect([...f.callbacks.keys()]).toEqual(['push', 'notificationclick']);
    expect(f.show).toHaveBeenCalledWith('AXV work update', {
      body: 'Open AXV to inspect the original work.',
      tag: `axv:${'a'.repeat(32)}:${'c'.repeat(32)}`,
      data: value,
    });
    expect(worker).not.toMatch(/\b(?:fetch|caches|skipWaiting|navigate)\s*\(/);
  });

  it('shows conservative visible alerts for expired, malformed, foreign or command-bearing messages', async () => {
    for (const value of [
      null,
      { ...payload(), expiresAt: 'invalid' },
      { ...payload(), expiresAt: new Date(Date.now() - 1).toISOString() },
      { ...payload(), storeId: 'd'.repeat(32) },
      {
        ...payload(),
        expiresAt: new Date(Date.now() + 17 * 60000).toISOString(),
      },
      { ...payload(), keyId: 'rotated-key' },
      { ...payload(), installationId: '3'.repeat(32) },
      { ...payload(), subscriptionId: '4'.repeat(32) },
      { ...payload(), command: 'approve' },
      { ...payload(), notificationId: '../new-run' },
      { ...payload(), notificationId: 'x'.repeat(4096) },
    ]) {
      const f = fixture();
      await f.push(value);
      expect(f.show).toHaveBeenCalledWith(
        'AXV notification',
        expect.objectContaining({ data: null, tag: 'axv:registration-review' }),
      );
    }
    const missing = fixture(null);
    await missing.push(payload());
    expect(missing.show).toHaveBeenCalledWith(
      'AXV notification',
      expect.objectContaining({ data: null }),
    );
  });

  it('focuses an existing window with an opaque message and never replaces its dirty page', async () => {
    const f = fixture();
    const client = {
      url: origin + '/',
      postMessage: vi.fn(),
      focus: vi.fn().mockResolvedValue(null),
      navigate: vi
        .fn()
        .mockRejectedValue(new Error('Dirty page must not navigate')),
    };
    f.clients.matchAll.mockResolvedValue([client]);
    await f.click(payload());
    expect(client.postMessage).toHaveBeenCalledWith({
      type: 'axv-push-open',
      reference: {
        storeId: 'a'.repeat(32),
        notificationId: 'c'.repeat(32),
      },
    });
    expect(f.remembered()).toEqual(
      expect.objectContaining({
        storeId: current.storeId,
        notificationId: 'c'.repeat(32),
        clickedAt: expect.any(Number),
      }),
    );
    expect(client.focus).toHaveBeenCalledOnce();
    expect(client.navigate).not.toHaveBeenCalled();
    expect(f.clients.openWindow).not.toHaveBeenCalled();
  });

  it('retains the exact reference when an already displayed alert is clicked after its delivery expiry', async () => {
    const f = fixture();
    await f.click({
      ...payload(),
      expiresAt: new Date(Date.now() - 60000).toISOString(),
    });
    expect(f.clients.openWindow).toHaveBeenCalledWith(
      origin + '/?notification=' + 'a'.repeat(32) + '%3A' + 'c'.repeat(32),
    );
    const revoked = fixture({ ...current, needsReview: true });
    await revoked.click(payload());
    expect(revoked.clients.openWindow).toHaveBeenCalledWith(origin + '/');
  });

  it('cold-opens only its own authenticated exact-event URL and never an arbitrary target', async () => {
    const f = fixture();
    await f.click(payload());
    expect(f.clients.openWindow).toHaveBeenCalledWith(
      origin + '/?notification=' + 'a'.repeat(32) + '%3A' + 'c'.repeat(32),
    );
    const foreign = fixture();
    await foreign.click({ ...payload(), url: 'http://private-host/control' });
    expect(foreign.clients.openWindow).toHaveBeenCalledWith(origin + '/');
  });
});
