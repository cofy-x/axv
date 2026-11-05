import { APIError, request } from '../../shared/api/http';
import type {
  NotificationConfigWrite,
  NotificationDetail,
  NotificationReference,
  NotificationSnapshot,
} from './types';

export function notificationError(error: unknown) {
  if (error instanceof APIError) {
    if (error.status === 409)
      return 'Notification settings or storage changed. Refresh and review the saved settings before trying again.';
    if (error.status === 403 || error.status === 401)
      return 'Manage notification settings on the computer running AXV. Restore authorization to read history.';
    if (error.status === 404 || error.status === 410)
      return 'The original notification or work is unavailable. Its identity will not be replaced with newer work.';
    if (error.status === 429)
      return 'Pocket Alert quota or rate limit reached. Check the delivery history before trying again.';
    if (error.status === 400)
      return 'Check the application, device, token and return-link origin, then save again.';
  }
  return 'Could not confirm the notification operation. Refresh to check its saved state before trying again.';
}

export const notificationsAPI = {
  snapshot: (signal?: AbortSignal) =>
    request<NotificationSnapshot>('/notifications', {
      signal,
      cache: 'no-store',
    }),
  save: (values: NotificationConfigWrite, signal?: AbortSignal) =>
    request<NotificationSnapshot>('/notifications/config', {
      method: 'POST',
      body: JSON.stringify(values),
      signal,
    }),
  test: (
    values: Pick<NotificationSnapshot, 'storeId' | 'revision'>,
    signal?: AbortSignal,
  ) =>
    request<NotificationSnapshot>('/notifications/test', {
      method: 'POST',
      body: JSON.stringify(values),
      signal,
    }),
  disable: (
    values: Pick<NotificationSnapshot, 'storeId' | 'revision'>,
    signal?: AbortSignal,
  ) =>
    request<NotificationSnapshot>('/notifications/disable', {
      method: 'POST',
      body: JSON.stringify(values),
      signal,
    }),
  async detail(reference: NotificationReference, signal?: AbortSignal) {
    const value = await request<NotificationDetail>(
      `/notifications/${encodeURIComponent(reference.notificationId)}?storeId=${encodeURIComponent(reference.storeId)}`,
      { signal, cache: 'no-store' },
    );
    if (
      value.storeId !== reference.storeId ||
      value.event.id !== reference.notificationId
    )
      throw new Error('Notification identity changed.');
    return value;
  },
};
