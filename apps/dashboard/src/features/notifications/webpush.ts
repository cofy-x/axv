import { APIError, request } from '../../shared/api/http';

export const pushWorkerPath = '/axv-push/worker.js';
export const pushWorkerScope = '/axv-push/';

export interface BrowserPushBinding {
  version: 1;
  installationId: string;
  origin: string;
  storeId: string;
  keyId: string;
  subscriptionId: string;
  needsReview?: boolean;
}

export interface PushSubscriptionState {
  id: string;
  installationId: string;
  origin: string;
  fingerprint?: string;
  label: string;
  enabled: boolean;
  status: string;
  eventTypes: string[];
  createdAt: string;
  nextSendAt?: string;
  error?: string;
}

export interface PushSnapshot {
  storeId: string;
  revision: number;
  keyId?: string;
  publicKey?: string;
  contact?: string;
  subscriptions: PushSubscriptionState[];
  error?: string;
}

interface PushIdentity {
  storeId: string;
  revision: number;
  keyId: string;
  installationId: string;
  subscriptionId: string;
}

const opaque = (value: unknown): value is string =>
  typeof value === 'string' && /^[A-Za-z0-9._-]{1,128}$/.test(value);

export function validBrowserPushBinding(
  value: unknown,
): value is BrowserPushBinding {
  if (!value || typeof value !== 'object') return false;
  const binding = value as BrowserPushBinding;
  return (
    binding.version === 1 &&
    (binding.needsReview === undefined ||
      typeof binding.needsReview === 'boolean') &&
    typeof binding.installationId === 'string' &&
    /^[a-f0-9]{32}$/.test(binding.installationId) &&
    opaque(binding.storeId) &&
    opaque(binding.keyId) &&
    typeof binding.subscriptionId === 'string' &&
    (binding.subscriptionId === '' ||
      /^[a-f0-9]{32}$/.test(binding.subscriptionId)) &&
    typeof binding.origin === 'string' &&
    exactOrigin(binding.origin)
  );
}

function exactOrigin(value: string) {
  try {
    const url = new URL(value);
    return (
      url.origin === value &&
      (url.protocol === 'https:' || url.protocol === 'http:')
    );
  } catch {
    return false;
  }
}

export function pushBindingMatches(
  binding: BrowserPushBinding | undefined,
  snapshot: PushSnapshot,
  origin: string,
) {
  return (
    !!binding &&
    !binding.needsReview &&
    binding.origin === origin &&
    binding.storeId === snapshot.storeId &&
    binding.keyId === snapshot.keyId
  );
}

export function pushEnvironment() {
  const appleMobile =
    /iPad|iPhone|iPod/.test(navigator.userAgent) ||
    (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
  const installed =
    matchMedia('(display-mode: standalone)').matches ||
    (navigator as Navigator & { standalone?: boolean }).standalone === true;
  if (!globalThis.isSecureContext)
    return 'Use AXV at its authenticated HTTPS address or on localhost.';
  if (appleMobile && !installed)
    return 'On iPhone or iPad, add AXV to the Home Screen and open that installed app before subscribing.';
  if (
    !('serviceWorker' in navigator) ||
    !('PushManager' in globalThis) ||
    !('Notification' in globalThis)
  )
    return 'This browser does not support the required Web Push APIs.';
  if (!navigator.locks || !('indexedDB' in globalThis))
    return 'This browser cannot safely store and coordinate notification subscriptions. Allow browser storage or use a supported browser.';
  return '';
}

export function webPushError(error: unknown) {
  if (error instanceof APIError) {
    if (error.code === 'push_service_unsupported')
      return 'This browser’s push service is not supported. AXV currently supports the reviewed Chrome, Firefox and Apple push-service endpoints.';
    if (error.status === 401 || error.status === 403)
      return 'Owner authorization is required. Reconnect to AXV, then retry.';
    if (error.status === 409)
      return 'The notification store, key or registration changed. Refresh and review this browser before subscribing again.';
    if (error.status === 400)
      return 'Check the push service contact, browser subscription and selected event types.';
  }
  return 'Could not confirm this Web Push operation. Refresh to review the browser and server states before trying again.';
}

export const webPushAPI = {
  snapshot: (signal?: AbortSignal) =>
    request<PushSnapshot>('/notifications/push', { signal, cache: 'no-store' }),
  prepare: (
    values: Pick<PushSnapshot, 'storeId' | 'revision'> & { contact: string },
    signal?: AbortSignal,
  ) =>
    request<PushSnapshot>('/notifications/push/prepare', {
      method: 'POST',
      body: JSON.stringify(values),
      signal,
    }),
  subscribe: (
    values: {
      storeId: string;
      revision: number;
      keyId: string;
      installationId: string;
      label: string;
      eventTypes: string[];
      subscription: PushSubscriptionJSON;
    },
    signal?: AbortSignal,
  ) =>
    request<PushSnapshot>('/notifications/push/subscribe', {
      method: 'POST',
      body: JSON.stringify(values),
      signal,
    }),
  test: (values: PushIdentity, signal?: AbortSignal) =>
    request<PushSnapshot>('/notifications/push/test', {
      method: 'POST',
      body: JSON.stringify(values),
      signal,
    }),
  disable: (values: PushIdentity, signal?: AbortSignal) =>
    request<PushSnapshot>('/notifications/push/disable', {
      method: 'POST',
      body: JSON.stringify(values),
      signal,
    }),
  unsubscribe: (values: PushIdentity, signal?: AbortSignal) =>
    request<PushSnapshot>('/notifications/push/unsubscribe', {
      method: 'POST',
      body: JSON.stringify(values),
      signal,
    }),
};

function bindingDatabase() {
  return new Promise<IDBDatabase>((resolve, reject) => {
    const open = indexedDB.open('axv-web-push', 1);
    open.onupgradeneeded = () => open.result.createObjectStore('binding');
    open.onsuccess = () => resolve(open.result);
    open.onerror = () =>
      reject(new Error('Browser notification storage is unavailable.'));
    open.onblocked = () =>
      reject(new Error('Browser notification storage is blocked.'));
  });
}

export async function readBrowserPushBinding(): Promise<
  BrowserPushBinding | undefined
> {
  const database = await bindingDatabase();
  try {
    return await new Promise((resolve, reject) => {
      const transaction = database.transaction('binding', 'readonly');
      const read = transaction.objectStore('binding').get('current');
      transaction.oncomplete = () => {
        if (read.result !== undefined && !validBrowserPushBinding(read.result))
          reject(
            new Error(
              'Browser notification identity is invalid and was preserved.',
            ),
          );
        else resolve(read.result as BrowserPushBinding | undefined);
      };
      transaction.onabort = () =>
        reject(new Error('Browser notification storage is unavailable.'));
    });
  } finally {
    database.close();
  }
}

export async function writeBrowserPushBinding(
  expected: BrowserPushBinding | undefined,
  value: BrowserPushBinding | undefined,
) {
  if (value && !validBrowserPushBinding(value))
    throw new Error('Invalid browser notification identity.');
  const database = await bindingDatabase();
  try {
    await new Promise<void>((resolve, reject) => {
      const transaction = database.transaction('binding', 'readwrite');
      const store = transaction.objectStore('binding');
      const read = store.get('current');
      read.onsuccess = () => {
        if (JSON.stringify(read.result) !== JSON.stringify(expected)) {
          transaction.abort();
          return;
        }
        if (value) store.put(value, 'current');
        else {
          store.delete('current');
          store.delete('open');
        }
      };
      transaction.oncomplete = () => resolve();
      transaction.onabort = () =>
        reject(
          new Error(
            'Browser notification identity changed. Refresh before continuing.',
          ),
        );
    });
  } finally {
    database.close();
  }
}

export async function browserPushSubscription() {
  const registration =
    await navigator.serviceWorker.getRegistration(pushWorkerScope);
  if (
    !registration ||
    registration.scope !== new URL(pushWorkerScope, location.origin).href
  )
    return { registration: undefined, subscription: null };
  const worker =
    registration.active || registration.waiting || registration.installing;
  if (worker?.scriptURL !== new URL(pushWorkerPath, location.origin).href)
    throw new Error('Another worker owns the notification scope.');
  return {
    registration,
    subscription: await registration.pushManager.getSubscription(),
  };
}

export function pushApplicationKey(value: string) {
  if (!/^[A-Za-z0-9_-]+$/.test(value))
    throw new Error('Invalid push application key.');
  const decoded = atob(value.replaceAll('-', '+').replaceAll('_', '/'));
  if (decoded.length !== 65 || decoded.charCodeAt(0) !== 4)
    throw new Error('Invalid push application key.');
  return Uint8Array.from(decoded, (character) => character.charCodeAt(0));
}

export function newPushInstallationId() {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (value) =>
    value.toString(16).padStart(2, '0'),
  ).join('');
}

// A click can reach a window while its authenticated application is still
// bootstrapping. Retain only the public exact reference until the desktop is ready.
export async function takePendingPushOpen(expected?: {
  storeId: string;
  notificationId: string;
}) {
  if (!('serviceWorker' in navigator)) return;
  const registration =
    await navigator.serviceWorker.getRegistration(pushWorkerScope);
  if (
    !registration ||
    registration.scope !== new URL(pushWorkerScope, location.origin).href ||
    registration.active?.scriptURL !==
      new URL(pushWorkerPath, location.origin).href
  )
    return;
  const database = await bindingDatabase();
  try {
    return await new Promise<
      { storeId: string; notificationId: string } | undefined
    >((resolve, reject) => {
      const transaction = database.transaction('binding', 'readwrite');
      const store = transaction.objectStore('binding');
      const read = store.get('open');
      let reference: { storeId: string; notificationId: string } | undefined;
      read.onsuccess = () => {
        const pending = read.result as Record<string, unknown> | undefined;
        if (
          !pending ||
          typeof pending !== 'object' ||
          Object.keys(pending).sort().join(',') !==
            'clickedAt,expiresAt,installationId,keyId,notificationId,storeId,subscriptionId,version' ||
          pending.version !== 1 ||
          typeof pending.expiresAt !== 'string' ||
          pending.expiresAt.length > 40 ||
          !Number.isFinite(Date.parse(pending.expiresAt)) ||
          (expected !== undefined &&
            (pending.storeId !== expected.storeId ||
              pending.notificationId !== expected.notificationId)) ||
          typeof pending.clickedAt !== 'number' ||
          !Number.isSafeInteger(pending.clickedAt) ||
          pending.clickedAt > Date.now() + 60000 ||
          pending.clickedAt < Date.now() - 15 * 60000 ||
          ![
            pending.storeId,
            pending.keyId,
            pending.installationId,
            pending.subscriptionId,
            pending.notificationId,
          ].every(
            (value) =>
              typeof value === 'string' && /^[a-f0-9]{32}$/.test(value),
          )
        )
          return;
        const current = store.get('current');
        current.onsuccess = () => {
          const binding = current.result as BrowserPushBinding | undefined;
          if (
            !validBrowserPushBinding(binding) ||
            binding.needsReview ||
            binding.origin !== location.origin ||
            binding.storeId !== pending.storeId ||
            binding.keyId !== pending.keyId ||
            binding.installationId !== pending.installationId ||
            binding.subscriptionId !== pending.subscriptionId
          )
            return;
          reference = {
            storeId: pending.storeId as string,
            notificationId: pending.notificationId as string,
          };
          store.delete('open');
        };
      };
      transaction.oncomplete = () => resolve(reference);
      transaction.onabort = () =>
        reject(new Error('Pending notification access is unavailable.'));
    });
  } finally {
    database.close();
  }
}

export async function pushSubscriptionFingerprint(
  subscription: PushSubscription,
) {
  const value = subscription.toJSON();
  if (!value.endpoint || !value.keys?.p256dh || !value.keys.auth)
    throw new Error('Browser subscription is incomplete.');
  const bytes = new TextEncoder().encode(
    `${value.endpoint}\0${value.keys.p256dh}\0${value.keys.auth}`,
  );
  const digest = await crypto.subtle.digest('SHA-256', bytes);
  return Array.from(new Uint8Array(digest), (value) =>
    value.toString(16).padStart(2, '0'),
  ).join('');
}

export function pushSubscriptionKey(subscription: PushSubscription) {
  const key = subscription.options.applicationServerKey;
  if (!key) return '';
  return btoa(String.fromCharCode(...new Uint8Array(key)))
    .replaceAll('+', '-')
    .replaceAll('/', '_')
    .replace(/=+$/, '');
}
