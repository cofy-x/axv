import { afterEach, describe, expect, it, vi } from 'vitest';
import { APIError } from '../../shared/api/http';
import { notificationError, notificationsAPI } from './api';
import { notificationLink } from './definition';
import { deliveryExplanation } from './presentation';
import type { NotificationEvent } from './types';

afterEach(() => vi.unstubAllGlobals());

describe('notification identity and safe errors', () => {
  it('accepts the opaque store and notification identity without accepting action parameters', () => {
    expect(
      notificationLink(
        '?notification=0123456789abcdef0123456789abcdef:notification-one&approve=yes',
      ),
    ).toEqual({
      storeId: '0123456789abcdef0123456789abcdef',
      notificationId: 'notification-one',
    });
    for (const search of [
      '?notification=store',
      '?notification=store:one:two',
      '?notification=store:../run',
      '?notification=store:%2Frun',
      '?notification=:run',
    ])
      expect(notificationLink(search)).toBeUndefined();
  });

  it('rejects replacement notification identities even in a successful response', async () => {
    const fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          storeId: 'another-store',
          event: { id: 'original' },
        }),
        { status: 200 },
      ),
    );
    vi.stubGlobal('fetch', fetch);
    await expect(
      notificationsAPI.detail({
        storeId: 'original-store',
        notificationId: 'original',
      }),
    ).rejects.toThrow('identity changed');
    expect(fetch).toHaveBeenCalledWith(
      '/api/notifications/original?storeId=original-store',
      expect.objectContaining({ cache: 'no-store' }),
    );
  });

  it('never exposes raw provider or transport errors', () => {
    const secret = 'sensitive-token-and-provider-response';
    for (const error of [
      new Error(secret),
      new APIError(secret, 503, secret),
      new APIError(secret, 409, secret),
      new APIError(secret, 429, secret),
    ]) {
      expect(notificationError(error)).not.toContain(secret);
      expect(notificationError(error)).not.toContain('503');
    }
    expect(notificationError(new APIError(secret, 409, secret))).toContain(
      'settings or storage changed',
    );
    expect(notificationError(new APIError(secret, 429, secret))).toContain(
      'quota',
    );
  });

  it('distinguishes acceptance, uncertainty and scheduled duplicate risk', () => {
    const event = { status: 'uncertain' } as NotificationEvent;
    expect(deliveryExplanation(event)).toContain('No automatic retry');
    expect(
      deliveryExplanation({ ...event, nextAttemptAt: '2026-11-06T00:01:00Z' }),
    ).toContain('duplicate');
    expect(deliveryExplanation({ ...event, status: 'accepted' })).toContain(
      'does not confirm',
    );
    expect(
      deliveryExplanation({
        ...event,
        status: 'failed',
        error: 'quota_exceeded',
      }),
    ).toContain('quota');
  });
});
