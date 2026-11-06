import type { NotificationReference } from './types';
import {
  pushWorkerPath,
  pushWorkerScope,
  readBrowserPushBinding,
} from './webpush';

export async function notificationFromPushMessage(
  event: MessageEvent,
): Promise<NotificationReference | undefined> {
  if (
    event.origin !== location.origin ||
    !event.source ||
    !event.data ||
    typeof event.data !== 'object' ||
    Array.isArray(event.data)
  )
    return;
  const value = event.data as { type?: unknown; reference?: unknown };
  if (
    Object.keys(value).sort().join(',') !== 'reference,type' ||
    value.type !== 'axv-push-open' ||
    !value.reference ||
    typeof value.reference !== 'object' ||
    Array.isArray(value.reference)
  )
    return;
  const reference = value.reference as NotificationReference;
  if (
    Object.keys(reference).sort().join(',') !== 'notificationId,storeId' ||
    typeof reference.storeId !== 'string' ||
    typeof reference.notificationId !== 'string' ||
    !/^[a-f0-9]{32}$/.test(reference.storeId) ||
    !/^[a-f0-9]{32}$/.test(reference.notificationId)
  )
    return;
  const registration =
    await navigator.serviceWorker.getRegistration(pushWorkerScope);
  if (
    !registration ||
    registration.scope !== new URL(pushWorkerScope, location.origin).href ||
    registration.active !== event.source ||
    registration.active?.scriptURL !==
      new URL(pushWorkerPath, location.origin).href
  )
    return;
  const binding = await readBrowserPushBinding();
  if (
    !binding ||
    binding.needsReview ||
    binding.origin !== location.origin ||
    binding.storeId !== reference.storeId ||
    !binding.subscriptionId
  )
    return;
  return {
    storeId: reference.storeId,
    notificationId: reference.notificationId,
  };
}
