import { createHash } from 'node:crypto';
import { afterEach, describe, expect, it, vi } from 'vitest';
import * as pushModule from './webpush';
import { APIError } from '../../shared/api/http';
import { notificationFromPushMessage } from './webpush-message';
import {
  pushApplicationKey,
  pushBindingMatches,
  pushEnvironment,
  pushSubscriptionFingerprint,
  pushSubscriptionKey,
  validBrowserPushBinding,
  webPushAPI,
  webPushError,
} from './webpush';
import type { BrowserPushBinding } from './webpush';

const binding: BrowserPushBinding = {
  version: 1,
  installationId: '1'.repeat(32),
  storeId: 'store-original',
  keyId: 'key-original',
  subscriptionId: '2'.repeat(32),
  origin: 'https://axv.example',
};

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('Web Push browser identity and transport', () => {
  it('matches the persistent store, key and exact origin independently of layout identity', () => {
    const snapshot = {
      storeId: binding.storeId,
      revision: 1,
      keyId: binding.keyId,
      subscriptions: [],
    };
    expect(validBrowserPushBinding(binding)).toBe(true);
    expect(pushBindingMatches(binding, snapshot, binding.origin)).toBe(true);
    for (const changed of [
      { ...snapshot, storeId: 'replacement' },
      { ...snapshot, keyId: 'rotated' },
    ])
      expect(pushBindingMatches(binding, changed, binding.origin)).toBe(false);
    expect(
      pushBindingMatches(binding, snapshot, 'https://another.example'),
    ).toBe(false);
    for (const invalid of [
      { ...binding, installationId: 'layout-client-id' },
      { ...binding, subscriptionId: '../run' },
      { ...binding, origin: 'https://axv.example/path' },
      { ...binding, version: 2 },
    ])
      expect(validBrowserPushBinding(invalid)).toBe(false);
  });

  it('keeps iPhone tabs distinct from installed clients and reports unsupported APIs', () => {
    vi.stubGlobal('isSecureContext', true);
    vi.stubGlobal('navigator', {
      userAgent: 'iPhone',
      platform: 'iPhone',
      maxTouchPoints: 5,
    });
    vi.stubGlobal('matchMedia', () => ({ matches: false }));
    expect(pushEnvironment()).toContain('Home Screen');
    vi.stubGlobal('navigator', {
      userAgent: 'Desktop browser',
      platform: 'Linux',
      maxTouchPoints: 0,
    });
    expect(pushEnvironment()).toContain('does not support');
    vi.stubGlobal('isSecureContext', false);
    expect(pushEnvironment()).toContain('HTTPS');
  });

  it('accepts only a correctly shaped uncompressed public application key', () => {
    const bytes = new Uint8Array(65);
    bytes[0] = 4;
    const key = Buffer.from(bytes).toString('base64url');
    expect(pushApplicationKey(key)).toEqual(bytes);
    for (const invalid of [
      '',
      'http://private/key',
      Buffer.from(new Uint8Array(64)).toString('base64url'),
    ])
      expect(() => pushApplicationKey(invalid)).toThrow();
  });

  it('compares the actual native endpoint, encryption keys and application key without exposing capabilities', async () => {
    const bytes = Uint8Array.from([4, ...new Array(64).fill(0)]);
    const value = {
      endpoint:
        'https://updates.push.services.mozilla.com/wpush/v2/synthetic-only',
      keys: { p256dh: 'synthetic-public', auth: 'synthetic-auth' },
    };
    const subscription = {
      toJSON: () => value,
      options: { applicationServerKey: bytes.buffer },
    } as unknown as PushSubscription;
    const expected = createHash('sha256')
      .update(`${value.endpoint}\0${value.keys.p256dh}\0${value.keys.auth}`)
      .digest('hex');
    expect(await pushSubscriptionFingerprint(subscription)).toBe(expected);
    expect(pushSubscriptionKey(subscription)).toBe(
      Buffer.from(bytes).toString('base64url'),
    );
    const canonical = {
      toJSON: () => ({
        endpoint: 'https://fcm.googleapis.com/fcm/send/synthetic',
        keys: {
          p256dh:
            'BGsX0fLhLEJH-Lzm5WOkQPJ3A32BLeszoPShOUXYmMKWT-NC4v4af5uO5-tKfA-eFivOM1drMV7Oy7ZAaDe_UfU',
          auth: 'MDEyMzQ1Njc4OWFiY2RlZg',
        },
      }),
    } as unknown as PushSubscription;
    expect(await pushSubscriptionFingerprint(canonical)).toBe(
      'ab11b7897018eb2cb5145542821594115a745f51c54ab70dd8ec1c323b964a63',
    );
    const original = await pushSubscriptionFingerprint(subscription);
    value.keys.auth = 'rotated-auth';
    expect(await pushSubscriptionFingerprint(subscription)).not.toBe(original);
  });

  it('uses an independent exact subscription identity for server disable and hides native/provider errors', async () => {
    const fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          storeId: 'store-original',
          revision: 3,
          subscriptions: [],
        }),
      ),
    );
    vi.stubGlobal('fetch', fetch);
    const identity = {
      storeId: binding.storeId,
      revision: 2,
      keyId: binding.keyId,
      installationId: binding.installationId,
      subscriptionId: binding.subscriptionId,
    };
    await webPushAPI.disable(identity);
    expect(fetch).toHaveBeenCalledWith(
      '/api/notifications/push/disable',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify(identity),
      }),
    );
    for (const cause of [
      new Error('secret-endpoint'),
      new APIError('secret-endpoint', 400, 'secret'),
      new APIError('secret-endpoint', 409, 'secret'),
      new APIError('secret-endpoint', 503, 'secret'),
    ])
      expect(webPushError(cause)).not.toContain('secret');
    expect(
      webPushError(
        new APIError('secret-endpoint', 400, 'push_service_unsupported'),
      ),
    ).toContain('push service is not supported');
  });

  it('opens only a typed opaque reference from the exact active worker, without page navigation', async () => {
    const active = { scriptURL: 'https://axv.example/axv-push/worker.js' };
    vi.stubGlobal('location', { origin: binding.origin });
    vi.stubGlobal('navigator', {
      serviceWorker: {
        getRegistration: vi.fn().mockResolvedValue({
          scope: 'https://axv.example/axv-push/',
          active,
        }),
      },
    });
    vi.spyOn(pushModule, 'readBrowserPushBinding').mockResolvedValue({
      ...binding,
      storeId: 'a'.repeat(32),
    });
    const event = {
      origin: binding.origin,
      source: active,
      data: {
        type: 'axv-push-open',
        reference: {
          storeId: 'a'.repeat(32),
          notificationId: 'b'.repeat(32),
        },
      },
    } as unknown as MessageEvent;
    expect(await notificationFromPushMessage(event)).toEqual(
      event.data.reference,
    );
    for (const changed of [
      { ...event, origin: 'https://foreign.example' },
      { ...event, source: {} },
      { ...event, data: { ...event.data, command: 'approve' } },
      {
        ...event,
        data: {
          type: 'axv-push-open',
          reference: { storeId: binding.storeId, notificationId: '../new-run' },
        },
      },
    ])
      expect(
        await notificationFromPushMessage(changed as MessageEvent),
      ).toBeUndefined();
  });
});
