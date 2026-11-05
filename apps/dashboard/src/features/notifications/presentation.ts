import { notificationKinds } from './types';
import type { NotificationEvent } from './types';

export const kindLabel = (kind: string) =>
  notificationKinds.find(([id]) => id === kind)?.[1] ||
  (kind === 'test'
    ? 'Test notification'
    : kind === 'gap'
      ? 'Source history gap'
      : 'Notification');

export function deliveryLabel(event: NotificationEvent) {
  return (
    {
      pending: 'Pending',
      sending: 'Sending',
      accepted: 'Accepted by Pocket Alert',
      uncertain: 'Delivery uncertain',
      failed: 'Delivery failed',
      disabled: 'Disabled before delivery',
      superseded: 'Superseded before delivery',
    }[event.status] || 'Delivery unavailable'
  );
}

export function deliveryExplanation(event: NotificationEvent) {
  if (event.status === 'uncertain')
    return event.nextAttemptAt
      ? 'Pocket Alert may have accepted this notification. A bounded retry is scheduled and may deliver a duplicate.'
      : 'Pocket Alert may have accepted this notification. No automatic retry is scheduled.';
  if (event.status === 'accepted')
    return 'Provider acceptance does not confirm that a device displayed or read the notification.';
  if (event.error && /quota|rate_limit/i.test(event.error))
    return 'Pocket Alert quota or rate limit reached. The saved delivery state remains visible here.';
  if (event.status === 'failed')
    return 'Delivery could not be completed. Check the saved settings and provider account.';
  if (event.status === 'pending')
    return 'Queued for delivery. Superseded requests are checked against the original work before sending.';
  return '';
}
