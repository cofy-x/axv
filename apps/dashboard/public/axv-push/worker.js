/* global self, indexedDB, URL */
'use strict';

// This worker owns visible push alerts only. It never intercepts requests,
// fetches private state, caches responses, or replaces a live application page.
const privateId = (value) =>
  typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);

function binding() {
  return new Promise((resolve) => {
    const open = indexedDB.open('axv-web-push', 1);
    open.onupgradeneeded = () => open.transaction.abort();
    open.onerror = () => resolve(null);
    open.onblocked = () => resolve(null);
    open.onsuccess = () => {
      const database = open.result;
      try {
        const transaction = database.transaction('binding', 'readonly');
        const read = transaction.objectStore('binding').get('current');
        transaction.oncomplete = () => {
          database.close();
          resolve(read.result || null);
        };
        transaction.onabort = () => {
          database.close();
          resolve(null);
        };
      } catch {
        database.close();
        resolve(null);
      }
    };
  });
}

function validPayload(value) {
  return (
    value &&
    typeof value === 'object' &&
    !Array.isArray(value) &&
    Object.keys(value).sort().join(',') ===
      'expiresAt,installationId,keyId,notificationId,storeId,subscriptionId,version' &&
    value.version === 1 &&
    privateId(value.storeId) &&
    privateId(value.keyId) &&
    privateId(value.installationId) &&
    privateId(value.subscriptionId) &&
    privateId(value.notificationId) &&
    typeof value.expiresAt === 'string' &&
    Number.isFinite(Date.parse(value.expiresAt))
  );
}

function matches(current, value) {
  return (
    current?.version === 1 &&
    !current.needsReview &&
    current.origin === self.location.origin &&
    current.storeId === value.storeId &&
    current.keyId === value.keyId &&
    current.installationId === value.installationId &&
    current.subscriptionId === value.subscriptionId
  );
}

function rememberClick(value) {
  return new Promise((resolve) => {
    const open = indexedDB.open('axv-web-push', 1);
    open.onupgradeneeded = () => open.transaction.abort();
    open.onerror = () => resolve(false);
    open.onblocked = () => resolve(false);
    open.onsuccess = () => {
      const database = open.result;
      try {
        const transaction = database.transaction('binding', 'readwrite');
        const store = transaction.objectStore('binding');
        const read = store.get('current');
        read.onsuccess = () => {
          if (matches(read.result, value))
            store.put({ ...value, clickedAt: Date.now() }, 'open');
          else transaction.abort();
        };
        transaction.oncomplete = () => {
          database.close();
          resolve(true);
        };
        transaction.onabort = () => {
          database.close();
          resolve(false);
        };
      } catch {
        database.close();
        resolve(false);
      }
    };
  });
}

self.addEventListener('push', (event) => {
  event.waitUntil(
    (async () => {
      let value;
      try {
        const text = event.data?.text();
        if (text && text.length <= 2048) value = JSON.parse(text);
      } catch {
        /* An unrecognized push still produces a conservative visible alert. */
      }
      const current = await binding();
      const accepted =
        validPayload(value) &&
        Date.parse(value.expiresAt) > Date.now() &&
        Date.parse(value.expiresAt) <= Date.now() + 16 * 60000 &&
        matches(current, value);
      await self.registration.showNotification(
        accepted ? 'AXV work update' : 'AXV notification',
        {
          body: accepted
            ? 'Open AXV to inspect the original work.'
            : 'Open AXV to review notification access. This alert could not be matched to the current browser registration.',
          tag: accepted
            ? `axv:${value.storeId}:${value.notificationId}`
            : 'axv:registration-review',
          data: accepted ? value : null,
        },
      );
    })(),
  );
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  event.waitUntil(
    (async () => {
      const value = event.notification.data;
      const current = await binding();
      const reference =
        validPayload(value) && matches(current, value)
          ? { storeId: value.storeId, notificationId: value.notificationId }
          : null;
      const windows = await self.clients.matchAll({
        type: 'window',
        includeUncontrolled: true,
      });
      for (const client of windows) {
        const url = new URL(client.url);
        if (url.origin !== self.location.origin || url.pathname !== '/')
          continue;
        const remembered = reference ? await rememberClick(value) : true;
        if (reference) client.postMessage({ type: 'axv-push-open', reference });
        if (!remembered)
          await self.registration.showNotification('AXV work update', {
            body: 'Open AXV and select this alert again to inspect the original work.',
            tag: `axv:${value.storeId}:${value.notificationId}`,
            data: value,
          });
        await client.focus();
        return;
      }
      const url = new URL('/', self.location.origin);
      if (reference)
        url.searchParams.set(
          'notification',
          `${reference.storeId}:${reference.notificationId}`,
        );
      await self.clients.openWindow(url.href);
    })(),
  );
});
