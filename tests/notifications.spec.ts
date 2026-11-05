import type { Locator, Page } from '@playwright/test';
import {
  expect,
  menuItem,
  persistedLayout,
  test,
  windowAction,
} from './helpers';
import type {
  NotificationConfigWrite,
  NotificationDetail,
  NotificationEvent,
  NotificationSnapshot,
} from '../apps/dashboard/src/features/notifications/types';

const storeId = '11111111111111111111111111111111';
const panel = (page: Page) => page.locator('[data-app-id="notifications"]');
const original = (page: Page) =>
  page.locator('[data-app-id="notification-work"]');
const token = 'fixture-pocket-alert-token';
const select = (control: Locator, touch: boolean) =>
  touch ? control.tap() : control.click();

function event(values: Partial<NotificationEvent> = {}): NotificationEvent {
  return {
    id: 'event-original',
    createdAt: '2026-11-06T00:00:00Z',
    kind: 'task_failed',
    state: 'failed',
    status: 'uncertain',
    attempts: 1,
    target: {
      kind: 'task',
      storeId: 'source-compute-store',
      hostId: 'source-host',
      projectId: 'source-project',
      taskId: 'source-task',
      attemptId: 'original-attempt',
    },
    ...values,
  };
}

async function fixture(page: Page, values: Partial<NotificationSnapshot> = {}) {
  const snapshot: NotificationSnapshot = {
    storeId,
    revision: 1,
    canConfigure: true,
    config: {
      enabled: false,
      tokenConfigured: false,
      applicationId: '',
      deviceId: '',
      returnOrigin: '',
      eventTypes: ['agent_failed', 'task_failed'],
    },
    events: [],
    intake: { gap: false },
    ...values,
  };
  const writes: { action: string; values: Record<string, unknown> }[] = [];
  let reads = 0;
  let available = true;
  let current = false;
  await page.route('**/api/notifications**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const suffix = url.pathname.slice('/api/notifications'.length);
    if (!suffix) {
      reads++;
      return route.fulfill({ json: snapshot });
    }
    if (request.method() === 'POST') {
      const body = request.postDataJSON() as Record<string, unknown>;
      writes.push({ action: suffix, values: body });
      if (
        body.storeId !== snapshot.storeId ||
        body.revision !== snapshot.revision
      )
        return route.fulfill({
          status: 409,
          json: { code: 'notification_conflict', error: token },
        });
      if (suffix === '/config') {
        const data = body as unknown as NotificationConfigWrite;
        snapshot.config = {
          enabled: data.enabled,
          tokenConfigured:
            data.tokenAction === 'clear'
              ? false
              : data.tokenAction === 'replace' ||
                snapshot.config.tokenConfigured,
          applicationId: data.applicationId,
          deviceId: data.deviceId,
          returnOrigin: data.returnOrigin,
          eventTypes: data.eventTypes,
        };
        snapshot.revision++;
      } else if (suffix === '/disable') {
        snapshot.config.enabled = false;
        snapshot.revision++;
      } else if (suffix === '/test')
        snapshot.events.unshift(
          event({
            id: 'test-delivery',
            kind: 'test',
            status: 'uncertain',
            nextAttemptAt: '2026-11-06T00:01:00Z',
          }),
        );
      return route.fulfill({ json: snapshot });
    }
    const item = snapshot.events.find(
      (item) => item.id === decodeURIComponent(suffix.slice(1)),
    );
    if (!item || url.searchParams.get('storeId') !== snapshot.storeId)
      return route.fulfill({
        status: 404,
        json: { code: 'notification_unavailable', error: token },
      });
    const detail: NotificationDetail = {
      storeId,
      event: item,
      resolution: {
        available,
        current,
        state: item.state,
        target: { ...item.target },
      },
    };
    return route.fulfill({ json: detail });
  });
  return {
    snapshot,
    writes,
    reads: () => reads,
    setAvailable: (value: boolean) => {
      available = value;
    },
    setCurrent: (value: boolean) => {
      current = value;
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
  await expect(panel(page)).toBeVisible();
}

test('Preferences opens optional Pocket Alert settings with explicit save, write-only token, test history and disable', async ({
  page,
  request,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = await fixture(page);
  await select(
    page.getByRole('button', { name: 'Workspace menu', exact: true }),
    touch,
  );
  await select(menuItem(page, 'Preferences'), touch);
  const preferences = page.locator('[data-app-id="appearance"]');
  await select(
    preferences.getByRole('button', {
      name: 'Open Notifications',
      exact: true,
    }),
    touch,
  );
  await panel(page).screenshot({
    path: info.outputPath('notifications-settings-top.png'),
  });
  await expect(
    panel(page).getByLabel('API token', { exact: true }),
  ).toHaveAttribute('type', 'password');
  await panel(page)
    .getByLabel('Application ID', { exact: true })
    .fill('application-one');
  await panel(page).getByLabel('Device ID', { exact: true }).fill('device-one');
  await panel(page).getByLabel('API token', { exact: true }).fill(token);
  await panel(page)
    .getByLabel('Return-link origin', { exact: true })
    .fill('https://axv.example');
  await panel(page).getByLabel('Enable Pocket Alert notifications').check();
  await expect(
    panel(page).getByRole('button', { name: 'Test', exact: true }),
  ).toBeDisabled();
  await select(
    panel(page).getByRole('button', { name: 'Save', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText('Settings saved.', { exact: true }),
  ).toBeVisible();
  expect(state.writes[0]).toEqual({
    action: '/config',
    values: {
      storeId,
      revision: 1,
      enabled: true,
      tokenAction: 'replace',
      token,
      applicationId: 'application-one',
      deviceId: 'device-one',
      returnOrigin: 'https://axv.example',
      eventTypes: ['agent_failed', 'task_failed'],
    },
  });
  await expect(panel(page).getByLabel('Replacement API token')).toHaveValue('');
  await panel(page).getByLabel('Device ID', { exact: true }).fill('device-two');
  await select(
    panel(page).getByRole('button', { name: 'Save', exact: true }),
    touch,
  );
  await expect.poll(() => state.writes.length).toBe(2);
  expect(state.writes[1].values.tokenAction).toBe('keep');
  expect(state.writes[1].values).not.toHaveProperty('token');
  await select(
    panel(page).getByRole('button', { name: 'Test', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText(
      'Test recorded. Check History for its delivery outcome.',
    ),
  ).toBeVisible();
  await select(
    panel(page).getByRole('tab', { name: 'History', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText('Delivery uncertain', { exact: true }),
  ).toBeVisible();
  await expect(panel(page).getByText(/bounded retry.*duplicate/)).toBeVisible();
  await expect(panel(page).getByText(token, { exact: false })).toHaveCount(0);
  const saved = JSON.stringify(await persistedLayout(page, request));
  expect(saved).not.toContain(token);
  expect(
    await page.evaluate(() =>
      JSON.stringify({
        local: { ...localStorage },
        session: { ...sessionStorage },
      }),
    ),
  ).not.toContain(token);
  await select(
    panel(page).getByRole('tab', { name: 'Settings', exact: true }),
    touch,
  );
  await select(
    panel(page).getByRole('button', { name: 'Disable', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText(
      'Notifications disabled. Previously accepted deliveries cannot be recalled.',
    ),
  ).toBeVisible();
  expect(state.writes.at(-1)?.action).toBe('/disable');
  await panel(page).screenshot({
    path: info.outputPath('notifications-settings.png'),
  });
});

test('notification return link inspects the original task attempt through GET only and never substitutes its newer retry', async ({
  page,
}, info) => {
  const item = event();
  const state = await fixture(page, { events: [item] });
  const sourceRequests: { method: string; path: string }[] = [];
  await page.route(
    '**/api/compute/projects/source-project/tasks/**',
    (route) => {
      const request = route.request();
      const url = new URL(request.url());
      sourceRequests.push({ method: request.method(), path: url.pathname });
      if (url.pathname.endsWith('/logs'))
        return route.fulfill({
          json: {
            data: Buffer.from('Original attempt failed.\n').toString('base64'),
            offset: 0,
            nextOffset: 25,
            logBytes: 25,
          },
        });
      return route.fulfill({
        json: {
          task: {
            id: 'source-task',
            hostId: 'source-host',
            projectId: 'source-project',
            currentAttemptId: 'newer-attempt',
          },
          attempt: { id: 'newer-attempt', status: 'running' },
          attempts: [
            {
              id: 'original-attempt',
              number: 1,
              status: 'failed',
              exitCode: 1,
            },
            { id: 'newer-attempt', number: 2, status: 'running' },
          ],
        },
      });
    },
  );
  await page.goto(
    `/?notification=${storeId}:${item.id}&approve=true&cancel=true&submit=true&dismiss=true`,
  );
  await expect(
    panel(page).getByRole('heading', { name: 'Notification details' }),
  ).toBeVisible();
  await expect(panel(page).getByText(/This event is stale/)).toBeVisible();
  expect(sourceRequests).toEqual([]);
  expect(state.writes).toEqual([]);
  await select(
    panel(page).getByRole('button', {
      name: 'Open original work',
      exact: true,
    }),
    info.project.use.hasTouch === true,
  );
  await expect(original(page).getByLabel('Original attempt output')).toHaveText(
    'Original attempt failed.\n',
  );
  await expect(
    original(page).getByText('original-attempt', { exact: true }),
  ).toBeVisible();
  await expect(
    original(page).getByText('failed', { exact: true }),
  ).toBeVisible();
  expect(sourceRequests.every((request) => request.method === 'GET')).toBe(
    true,
  );
  expect(
    sourceRequests.some((request) => request.path.includes('newer-attempt')),
  ).toBe(false);
  expect(state.writes).toEqual([]);
  await expect(original(page).getByRole('textbox')).toHaveCount(0);
  await expect(
    original(page).getByRole('button', {
      name: /Approve|Cancel|Dismiss|Submit|Retry/,
    }),
  ).toHaveCount(0);
  await original(page).screenshot({
    path: info.outputPath('notification-original-task.png'),
  });
  await original(page)
    .getByLabel('Original attempt output')
    .scrollIntoViewIfNeeded();
  await original(page).screenshot({
    path: info.outputPath('notification-original-task-output.png'),
  });
});

test('remote history exposes gaps and quota without a settings form, and hidden windows stop polling', async ({
  page,
}, info) => {
  const state = await fixture(page, {
    canConfigure: false,
    nextSendAt: '2026-11-06T00:02:00Z',
    intake: { gap: true, error: token },
    events: [event({ status: 'failed', error: 'quota_exceeded' })],
  });
  await open(page, info.project.use.hasTouch === true);
  await expect(panel(page).getByRole('tab', { name: 'Settings' })).toHaveCount(
    0,
  );
  await expect(
    panel(page).getByLabel('Application ID', { exact: true }),
  ).toBeHidden();
  await expect(panel(page).getByText(/Source history has a gap/)).toBeVisible();
  await expect(
    panel(page).getByText(/quota or rate limit reached/),
  ).toBeVisible();
  await expect(
    panel(page).getByText(/All queued deliveries wait until this time/),
  ).toBeVisible();
  await expect(
    panel(page).getByText('Pocket Alert quota cooldown.', { exact: true }),
  ).toBeVisible();
  await panel(page).screenshot({
    path: info.outputPath('notifications-quota-cooldown.png'),
  });
  await expect(panel(page).getByText(token, { exact: false })).toHaveCount(0);
  await windowAction(
    page,
    panel(page),
    'Shade',
    info.project.use.hasTouch === true,
  );
  const reads = state.reads();
  await page.waitForTimeout(5200);
  expect(state.reads()).toBe(reads);
  await windowAction(
    page,
    panel(page),
    'Unshade',
    info.project.use.hasTouch === true,
  );
  await expect.poll(state.reads).toBeGreaterThan(reads);
});

test('missing original targets keep their exact identity and never open another run', async ({
  page,
}, info) => {
  const item = event({
    kind: 'agent_waiting_approval',
    target: {
      kind: 'codex',
      storeId: 'original-codex-store',
      sessionId: 'old-session',
      runId: 'old-run',
      turnId: 'old-turn',
      eventId: 'old-approval',
    },
  });
  const state = await fixture(page, { events: [item] });
  state.setAvailable(false);
  await page.goto(`/?notification=${storeId}:${item.id}`);
  await expect(
    panel(page).getByText(/original work is unavailable/),
  ).toBeVisible();
  await expect(
    panel(page).getByRole('button', { name: 'Open original work' }),
  ).toBeDisabled();
  await select(
    panel(page).getByText('Recorded identity', { exact: true }),
    info.project.use.hasTouch === true,
  );
  await expect(panel(page).getByText('old-run', { exact: true })).toBeVisible();
  expect(state.writes).toEqual([]);
  await expect(original(page)).toHaveCount(0);
});

test('stale approval links inspect the exact Codex run without connecting input or dismissing its request', async ({
  page,
}, info) => {
  const item = event({
    kind: 'agent_waiting_approval',
    target: {
      kind: 'codex',
      storeId: 'original-codex-store',
      sessionId: 'old-session',
      runId: 'old-run',
      turnId: 'old-turn',
      eventId: 'old-approval',
    },
  });
  const state = await fixture(page, { events: [item] });
  const requests: { method: string; path: string }[] = [];
  const sockets: string[] = [];
  page.on('websocket', (socket) => sockets.push(socket.url()));
  await page.route('**/api/host/codex/**', (route) => {
    const request = route.request();
    requests.push({
      method: request.method(),
      path: new URL(request.url()).pathname,
    });
    return route.fulfill({
      json: {
        session: {
          id: 'old-session',
          runId: 'old-run',
          title: 'Original conversation',
          status: 'ended',
          activity: { state: 'completed' },
          continuation: { state: 'active', activeRunId: 'new-run' },
        },
      },
    });
  });
  await page.goto(`/?notification=${storeId}:${item.id}&approve=true`);
  await select(
    panel(page).getByRole('button', { name: 'Open original work' }),
    info.project.use.hasTouch === true,
  );
  await expect(
    original(page).getByText('Original conversation', { exact: true }),
  ).toBeVisible();
  await expect(
    original(page).getByText('old-run', { exact: true }),
  ).toBeVisible();
  await expect(
    original(page).getByText('old-approval', { exact: true }),
  ).toBeVisible();
  expect(requests.every((request) => request.method === 'GET')).toBe(true);
  expect(
    requests.filter((request) => request.path !== '/api/host/codex/info'),
  ).toEqual([
    {
      method: 'GET',
      path: '/api/host/codex/sessions/old-session/runs/old-run',
    },
  ]);
  expect(sockets.filter((url) => url.includes('codex'))).toEqual([]);
  expect(state.writes).toEqual([]);
  await expect(original(page).getByRole('textbox')).toHaveCount(0);
  await original(page).screenshot({
    path: info.outputPath('notification-original-codex.png'),
  });
});

test('a failed settings save preserves the draft and clears the password without echoing the provider error', async ({
  page,
}, info) => {
  await fixture(page);
  await page.route('**/api/notifications/config', (route) =>
    route.fulfill({
      status: 409,
      json: { code: 'notification_conflict', error: token },
    }),
  );
  await open(page, info.project.use.hasTouch === true);
  await panel(page)
    .getByLabel('Application ID', { exact: true })
    .fill('unsaved-application');
  await panel(page).getByLabel('API token', { exact: true }).fill(token);
  await select(
    panel(page).getByRole('button', { name: 'Save', exact: true }),
    info.project.use.hasTouch === true,
  );
  await expect(
    panel(page).getByText(/Notification settings or storage changed/),
  ).toBeVisible();
  await expect(
    panel(page).getByLabel('Application ID', { exact: true }),
  ).toHaveValue('unsaved-application');
  await expect(
    panel(page).getByLabel('API token', { exact: true }),
  ).toHaveValue('');
  await expect(panel(page).getByText(token, { exact: false })).toHaveCount(0);
  await expect(
    panel(page).getByText('Unsaved settings.', { exact: true }),
  ).toBeVisible();
});
