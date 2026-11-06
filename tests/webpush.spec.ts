import { createHash } from 'node:crypto';
import type { Locator, Page } from '@playwright/test';
import {
  createEntry,
  expect,
  menuItem,
  openDocument,
  readContent,
  test,
} from './helpers';
import type { PushSnapshot } from '../apps/dashboard/src/features/notifications/webpush';
import type {
  NotificationEvent,
  NotificationReference,
  NotificationSnapshot,
} from '../apps/dashboard/src/features/notifications/types';

interface BrowserFixture {
  permission: NotificationPermission;
  requestResult: NotificationPermission;
  subscribed: boolean;
  registers: number;
  permissions: number;
  unsubscribes: number;
  endpoint: string;
  auth: string;
  emit: (reference: NotificationReference, trusted?: boolean) => void;
}

const panel = (page: Page) => page.locator('[data-app-id="notifications"]');
const browserPanel = (page: Page) =>
  panel(page).getByRole('region', { name: 'This browser notifications' });
const storeId = 'a'.repeat(32),
  keyId = 'b'.repeat(32),
  subscriptionId = 'c'.repeat(32),
  notificationId = 'd'.repeat(32);
const select = (control: Locator, touch: boolean) =>
  touch ? control.tap() : control.click();

async function fixture(
  page: Page,
  options: { denied?: boolean; ios?: boolean } = {},
) {
  const bytes = new Uint8Array(65);
  bytes[0] = 4;
  const snapshot: PushSnapshot = { storeId, revision: 0, subscriptions: [] };
  const history: NotificationSnapshot = {
    storeId,
    revision: 1,
    canConfigure: false,
    config: {
      enabled: false,
      tokenConfigured: false,
      applicationId: '',
      deviceId: '',
      returnOrigin: '',
      eventTypes: [],
    },
    events: [],
    intake: { gap: false },
  };
  const writes: { action: string; body: Record<string, unknown> }[] = [];
  let offline = false;
  await page.route('**/api/notifications**', async (route) => {
    const request = route.request(),
      url = new URL(request.url()),
      path = url.pathname.slice('/api/notifications'.length);
    if (path.startsWith('/push')) {
      if (offline)
        return route.fulfill({
          status: 503,
          json: { error: 'private-endpoint-secret' },
        });
      if (request.method() === 'POST') {
        const body = request.postDataJSON() as Record<string, unknown>;
        writes.push({ action: path, body });
        if (
          body.storeId !== snapshot.storeId ||
          body.revision !== snapshot.revision
        )
          return route.fulfill({
            status: 409,
            json: { error: 'private-endpoint-secret' },
          });
        if (path === '/push/prepare')
          Object.assign(snapshot, {
            revision: snapshot.revision + 1,
            keyId,
            publicKey: Buffer.from(bytes).toString('base64url'),
            contact: body.contact,
          });
        if (path === '/push/subscribe') {
          snapshot.revision++;
          snapshot.subscriptions = [
            {
              id: subscriptionId,
              installationId: body.installationId as string,
              origin: url.origin,
              fingerprint: createHash('sha256')
                .update(
                  `${(body.subscription as { endpoint: string }).endpoint}\0${(body.subscription as { keys: { p256dh: string } }).keys.p256dh}\0${(body.subscription as { keys: { auth: string } }).keys.auth}`,
                )
                .digest('hex'),
              label: body.label as string,
              enabled: true,
              status: 'active',
              eventTypes: body.eventTypes as string[],
              createdAt: new Date().toISOString(),
            },
          ];
        }
        if (path === '/push/disable') {
          snapshot.revision++;
          snapshot.subscriptions[0].enabled = false;
          snapshot.subscriptions[0].status = 'disabled';
        }
        if (path === '/push/unsubscribe') {
          snapshot.revision++;
          snapshot.subscriptions = [];
        }
        if (path === '/push/test')
          history.events = [
            {
              id: notificationId,
              createdAt: new Date().toISOString(),
              kind: 'test',
              state: 'test',
              status: 'disabled',
              attempts: 0,
              target: { kind: 'task', storeId: 'source-original' },
              pushDeliveries: [
                {
                  subscriptionId,
                  keyId,
                  status: 'accepted',
                  attempts: 1,
                  expiresAt: new Date(Date.now() + 15 * 60000).toISOString(),
                },
              ],
            } as NotificationEvent,
          ];
      }
      return route.fulfill({ json: snapshot });
    }
    if (!path) return route.fulfill({ json: history });
    const event = history.events.find((value) => path === '/' + value.id);
    if (event)
      return route.fulfill({
        json: {
          storeId,
          event,
          resolution: {
            available: false,
            current: false,
            state: 'test',
            target: event.target,
          },
        },
      });
    return route.fulfill({
      status: 404,
      json: { error: 'Original notification unavailable' },
    });
  });
  await page.addInitScript(({ denied, ios }) => {
    const state = {
      permission: 'default' as NotificationPermission,
      requestResult: (denied ? 'denied' : 'granted') as NotificationPermission,
      subscribed: false,
      registers: 0,
      permissions: 0,
      unsubscribes: 0,
      endpoint:
        'https://updates.push.services.mozilla.com/wpush/v2/synthetic-only',
      auth: 'synthetic-auth-key',
      emit: (_reference: unknown, _trusted?: boolean) => {},
    };
    const active = {
      scriptURL: location.origin + '/axv-push/worker.js',
      state: 'activated',
    };
    let registered =
      localStorage.getItem('axv-push-fixture-registered') === 'yes';
    const subscription = {
      unsubscribe: async () => {
        state.unsubscribes++;
        state.subscribed = false;
        return true;
      },
      options: {
        applicationServerKey: Uint8Array.from([4, ...new Array(64).fill(0)])
          .buffer,
        userVisibleOnly: true,
      },
      toJSON: () => ({
        endpoint: state.endpoint,
        expirationTime: null,
        keys: { p256dh: 'synthetic-public-key', auth: state.auth },
      }),
    };
    const registration = {
      scope: location.origin + '/axv-push/',
      active,
      pushManager: {
        getSubscription: async () => (state.subscribed ? subscription : null),
        subscribe: async () => {
          state.subscribed = true;
          return subscription;
        },
      },
    };
    const container = Object.assign(new EventTarget(), {
      getRegistration: async () => (registered ? registration : undefined),
      register: async () => {
        state.registers++;
        registered = true;
        localStorage.setItem('axv-push-fixture-registered', 'yes');
        return registration;
      },
    });
    state.emit = (reference: unknown, trusted = true) => {
      const event = new MessageEvent('message', {
        origin: location.origin,
        data: { type: 'axv-push-open', reference },
      });
      Object.defineProperty(event, 'source', { value: trusted ? active : {} });
      container.dispatchEvent(event);
    };
    Object.defineProperty(navigator, 'serviceWorker', {
      configurable: true,
      value: container,
    });
    Object.defineProperty(window, 'PushManager', {
      configurable: true,
      value: class {},
    });
    Object.defineProperty(window, 'Notification', {
      configurable: true,
      value: {
        get permission() {
          return state.permission;
        },
        requestPermission: async () => {
          state.permissions++;
          state.permission = state.requestResult;
          return state.permission;
        },
      },
    });
    if (ios) {
      Object.defineProperty(navigator, 'userAgent', {
        configurable: true,
        value: 'iPhone Safari',
      });
      Object.defineProperty(navigator, 'platform', {
        configurable: true,
        value: 'iPhone',
      });
    }
    (window as unknown as { __pushFixture: typeof state }).__pushFixture =
      state;
  }, options);
  await page.reload();
  return {
    snapshot,
    history,
    writes,
    setOffline: (value: boolean) => {
      offline = value;
    },
  };
}

async function open(page: Page, touch: boolean) {
  await select(
    page.getByRole('button', { name: 'Workspace menu', exact: true }),
    touch,
  );
  await select(menuItem(page, 'Applications'), touch);
  await select(menuItem(page, 'Notifications'), touch);
  await select(
    panel(page).getByRole('tab', { name: 'This browser', exact: true }),
    touch,
  );
  await expect(
    browserPanel(page).getByRole('button', { name: 'Refresh browser state' }),
  ).toBeEnabled();
}

async function subscribe(page: Page, touch: boolean) {
  await browserPanel(page)
    .getByLabel('Push service contact', { exact: true })
    .fill('mailto:owner@example.com');
  await select(
    browserPanel(page).getByRole('button', {
      name: 'Subscribe this browser',
      exact: true,
    }),
    touch,
  );
  await expect(
    browserPanel(page).getByText('Enabled', { exact: true }),
  ).toBeVisible();
}

async function browserState(page: Page) {
  return page.evaluate(() => {
    const value = (window as unknown as { __pushFixture: BrowserFixture })
      .__pushFixture;
    return {
      registers: value.registers,
      permissions: value.permissions,
      subscribed: value.subscribed,
      unsubscribes: value.unsubscribes,
      permission: value.permission,
    };
  });
}

test('browser Web Push is explicit and independent of local Pocket Alert settings, with test and separate disable/unsubscribe outcomes', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true,
    state = await fixture(page);
  await open(page, touch);
  await expect(
    panel(page).getByRole('tab', { name: 'Settings', exact: true }),
  ).toHaveCount(0);
  expect(await browserState(page)).toEqual({
    registers: 0,
    permissions: 0,
    subscribed: false,
    unsubscribes: 0,
    permission: 'default',
  });
  expect(state.writes).toEqual([]);
  await subscribe(page, touch);
  await browserPanel(page).evaluate((element) =>
    element.closest('.ns-tab-panel')?.scrollTo({ top: 0 }),
  );
  await panel(page).screenshot({
    path: info.outputPath('webpush-enabled-top.png'),
  });
  expect(state.writes.map((value) => value.action)).toEqual([
    '/push/prepare',
    '/push/subscribe',
  ]);
  expect(state.writes[1].body.subscription).toEqual({
    endpoint:
      'https://updates.push.services.mozilla.com/wpush/v2/synthetic-only',
    keys: { p256dh: 'synthetic-public-key', auth: 'synthetic-auth-key' },
  });
  expect(state.writes[1].body.installationId).toMatch(/^[a-f0-9]{32}$/);
  await select(
    browserPanel(page).getByRole('button', {
      name: 'Test this browser',
      exact: true,
    }),
    touch,
  );
  await expect(
    browserPanel(page).getByText(/Synthetic test recorded/),
  ).toBeVisible();
  await select(
    panel(page).getByRole('tab', { name: 'History', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText('Web Push: Accepted by push service', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(panel(page).getByText(/Pocket Alert:/)).toBeVisible();
  await select(
    panel(page).getByRole('tab', { name: 'This browser', exact: true }),
    touch,
  );
  await select(
    browserPanel(page).getByRole('button', {
      name: 'Disable server delivery',
      exact: true,
    }),
    touch,
  );
  await expect(
    browserPanel(page).getByText('Disabled', { exact: true }),
  ).toBeVisible();
  expect((await browserState(page)).subscribed).toBe(true);
  await select(
    browserPanel(page).getByRole('button', {
      name: 'Unsubscribe this browser',
      exact: true,
    }),
    touch,
  );
  await expect(
    browserPanel(page).getByText('Not subscribed', { exact: true }),
  ).toBeVisible();
  expect((await browserState(page)).permission).toBe('granted');
  expect(state.snapshot.subscriptions).toEqual([]);
  await panel(page).screenshot({
    path: info.outputPath('webpush-browser-lifecycle.png'),
  });
});

test('denied permission does not prepare keys or register a worker', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true,
    state = await fixture(page, { denied: true });
  await open(page, touch);
  await browserPanel(page)
    .getByLabel('Push service contact', { exact: true })
    .fill('mailto:owner@example.com');
  await select(
    browserPanel(page).getByRole('button', {
      name: 'Subscribe this browser',
      exact: true,
    }),
    touch,
  );
  await expect(
    browserPanel(page).getByText('denied', { exact: true }),
  ).toBeVisible();
  expect(state.writes).toEqual([]);
  expect((await browserState(page)).registers).toBe(0);
});

test('an ordinary iPhone tab explains the installed Home Screen requirement without requesting permission', async ({
  page,
}, info) => {
  const state = await fixture(page, { ios: true });
  await open(page, info.project.use.hasTouch === true);
  await expect(
    browserPanel(page).getByText(/add AXV to the Home Screen/),
  ).toBeVisible();
  await expect(
    browserPanel(page).getByRole('button', {
      name: 'Subscribe this browser',
      exact: true,
    }),
  ).toBeDisabled();
  expect(state.writes).toEqual([]);
  expect((await browserState(page)).permissions).toBe(0);
});

test('changed stores or sending keys invalidate the retained browser identity and require explicit removal', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true,
    state = await fixture(page);
  await open(page, touch);
  await subscribe(page, touch);
  state.snapshot.keyId = 'e'.repeat(32);
  await select(
    browserPanel(page).getByRole('button', { name: 'Refresh browser state' }),
    touch,
  );
  await expect(browserPanel(page).getByText(/has not adopted/)).toBeVisible();
  await expect(
    browserPanel(page).getByRole('button', {
      name: 'Test this browser',
      exact: true,
    }),
  ).toBeDisabled();
  const writes = state.writes.length;
  state.snapshot.keyId = keyId;
  await select(
    browserPanel(page).getByRole('button', { name: 'Refresh browser state' }),
    touch,
  );
  await expect(browserPanel(page).getByText(/has not adopted/)).toBeVisible();
  expect(state.writes).toHaveLength(writes);
  await select(
    browserPanel(page).getByRole('button', {
      name: 'Unsubscribe this browser',
      exact: true,
    }),
    touch,
  );
  await expect(
    browserPanel(page).getByText('Not subscribed', { exact: true }),
  ).toBeVisible();
});

test('browser unsubscribe works while AXV is unreachable and preserves identity for later server retirement', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true,
    state = await fixture(page);
  await open(page, touch);
  await subscribe(page, touch);
  state.setOffline(true);
  await select(
    browserPanel(page).getByRole('button', { name: 'Refresh browser state' }),
    touch,
  );
  await expect(browserPanel(page).getByRole('alert')).toBeVisible();
  await select(
    browserPanel(page).getByRole('button', {
      name: 'Unsubscribe this browser',
      exact: true,
    }),
    touch,
  );
  await expect(
    browserPanel(page).getByText(/server registration could not be checked/),
  ).toBeVisible();
  expect((await browserState(page)).subscribed).toBe(false);
  expect(state.snapshot.subscriptions).toHaveLength(1);
  state.setOffline(false);
  await select(
    browserPanel(page).getByRole('button', { name: 'Refresh browser state' }),
    touch,
  );
  await select(
    browserPanel(page).getByRole('button', {
      name: 'Unsubscribe this browser',
      exact: true,
    }),
    touch,
  );
  await expect.poll(() => state.snapshot.subscriptions.length).toBe(0);
});

test('a verified push click opens the exact notification without navigation or replacing a dirty document', async ({
  page,
  request,
}, info) => {
  const entry = await createEntry(
    request,
    'E2E push dirty document.md',
    'file',
    'documents',
    'Saved before notification',
  );
  const touch = info.project.use.hasTouch === true,
    state = await fixture(page);
  await open(page, touch);
  await subscribe(page, touch);
  const editor = await openDocument(page, entry, touch);
  const input = editor.getByRole('textbox', {
    name: 'Document text',
    exact: true,
  });
  await input.fill('Unsaved while work continues');
  await input.evaluate((element) =>
    element.setAttribute('data-push-input-witness', 'original'),
  );
  await editor.evaluate((element) => {
    element.setAttribute('data-push-mount-witness', 'original');
  });
  const originalUrl = page.url();
  const references = { storeId, notificationId };
  await page.evaluate(
    (reference) =>
      (
        window as unknown as { __pushFixture: BrowserFixture }
      ).__pushFixture.emit(reference, false),
    references,
  );
  await expect(
    panel(page).getByRole('heading', {
      name: 'Notification details',
      exact: true,
    }),
  ).toHaveCount(0);
  state.history.events = [
    {
      id: notificationId,
      createdAt: new Date().toISOString(),
      kind: 'test',
      state: 'test',
      status: 'disabled',
      attempts: 0,
      target: { kind: 'task', storeId: 'original-source' },
    },
  ];
  await page.evaluate(
    (reference) =>
      (
        window as unknown as { __pushFixture: BrowserFixture }
      ).__pushFixture.emit(reference),
    references,
  );
  await expect(
    panel(page).getByRole('heading', {
      name: 'Notification details',
      exact: true,
    }),
  ).toBeVisible();
  expect(page.url()).toBe(originalUrl);
  await expect(editor).toHaveAttribute('data-push-mount-witness', 'original');
  const retainedInput = editor.locator('textarea[aria-label="Document text"]');
  await expect(retainedInput).toHaveAttribute(
    'data-push-input-witness',
    'original',
  );
  await expect(retainedInput).toHaveValue('Unsaved while work continues');
  await expect(retainedInput).not.toBeFocused();
  expect(await readContent(request, entry.id)).toBe(
    'Saved before notification',
  );
});

test('a click retained during bootstrap opens its exact original event after the desktop becomes ready', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true,
    state = await fixture(page);
  await open(page, touch);
  await subscribe(page, touch);
  state.history.events = [
    {
      id: notificationId,
      createdAt: new Date().toISOString(),
      kind: 'test',
      state: 'test',
      status: 'disabled',
      attempts: 0,
      target: { kind: 'task', storeId: 'original-source' },
    },
  ];
  await page.evaluate(async (notificationId) => {
    const db = await new Promise<IDBDatabase>((resolve, reject) => {
      const open = indexedDB.open('axv-web-push', 1);
      open.onsuccess = () => resolve(open.result);
      open.onerror = () => reject(open.error);
    });
    await new Promise<void>((resolve, reject) => {
      const tx = db.transaction('binding', 'readwrite'),
        store = tx.objectStore('binding'),
        read = store.get('current');
      read.onsuccess = () => {
        const { origin: _origin, ...binding } = read.result;
        store.put(
          {
            ...binding,
            notificationId,
            expiresAt: new Date(Date.now() - 60000).toISOString(),
            clickedAt: Date.now(),
          },
          'open',
        );
      };
      tx.oncomplete = () => resolve();
      tx.onabort = () => reject(tx.error);
    });
    db.close();
  }, notificationId);
  await page.reload();
  await expect(
    panel(page).getByRole('heading', {
      name: 'Notification details',
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    panel(page).getByText('Test notification', { exact: true }).first(),
  ).toBeVisible();
});

test('a stale window cannot unsubscribe a newer browser binding created by another window', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true,
    state = await fixture(page);
  await open(page, touch);
  await subscribe(page, touch);
  await page.evaluate(async () => {
    const db = await new Promise<IDBDatabase>((resolve, reject) => {
      const open = indexedDB.open('axv-web-push', 1);
      open.onsuccess = () => resolve(open.result);
      open.onerror = () => reject(open.error);
    });
    await new Promise<void>((resolve, reject) => {
      const tx = db.transaction('binding', 'readwrite'),
        store = tx.objectStore('binding'),
        read = store.get('current');
      read.onsuccess = () =>
        store.put(
          { ...read.result, installationId: 'f'.repeat(32) },
          'current',
        );
      tx.oncomplete = () => resolve();
      tx.onabort = () => reject(tx.error);
    });
    db.close();
  });
  const writes = state.writes.length;
  await select(
    browserPanel(page).getByRole('button', {
      name: 'Unsubscribe this browser',
      exact: true,
    }),
    touch,
  );
  await expect
    .poll(async () =>
      browserPanel(page)
        .getByRole('button', { name: 'Refresh browser state' })
        .isEnabled(),
    )
    .toBe(true);
  expect((await browserState(page)).unsubscribes).toBe(0);
  expect((await browserState(page)).subscribed).toBe(true);
  expect(state.writes).toHaveLength(writes);
});

test('native browser endpoint or key rotation cannot silently confirm or adopt the old server registration', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true,
    state = await fixture(page);
  await open(page, touch);
  await subscribe(page, touch);
  const writes = state.writes.length,
    originalFingerprint = state.snapshot.subscriptions[0].fingerprint;
  await page.evaluate(() => {
    const browser = (window as unknown as { __pushFixture: BrowserFixture })
      .__pushFixture;
    browser.endpoint =
      'https://updates.push.services.mozilla.com/wpush/v2/rotated-only';
    browser.auth = 'rotated-synthetic-auth';
  });
  await select(
    browserPanel(page).getByRole('button', {
      name: 'Test this browser',
      exact: true,
    }),
    touch,
  );
  await expect(browserPanel(page).getByText(/has not adopted/)).toBeVisible();
  await expect(
    browserPanel(page).getByRole('button', {
      name: 'Test this browser',
      exact: true,
    }),
  ).toBeDisabled();
  expect(state.writes).toHaveLength(writes);
  expect(state.snapshot.subscriptions[0].fingerprint).toBe(originalFingerprint);
  await select(
    browserPanel(page).getByRole('button', {
      name: 'Unsubscribe this browser',
      exact: true,
    }),
    touch,
  );
  await expect.poll(() => state.snapshot.subscriptions.length).toBe(0);
  expect((await browserState(page)).subscribed).toBe(false);
});
