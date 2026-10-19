import type { Locator, Page } from '@playwright/test';
import { dockEntry, expect, menuItem, persistedLayout, test } from './helpers';

const address = 'https://axv-workstation.example.ts.net';
const panel = (page: Page) => page.locator('[data-app-id="remote-access"]');
const remoteState = (page: Page) =>
  panel(page).locator('[aria-labelledby="tailscale-axv-title"]');
const hostState = (page: Page) =>
  panel(page).locator('[aria-labelledby="tailscale-host-title"]');

async function choose(target: Locator, touch = false) {
  if (touch) await target.tap();
  else await target.click();
}

async function workspaceMenu(page: Page, touch = false) {
  await choose(
    page.getByRole('button', { name: 'Workspace menu', exact: true }),
    touch,
  );
}

async function openRemote(page: Page, touch = false) {
  await workspaceMenu(page, touch);
  await choose(menuItem(page, 'Applications'), touch);
  await choose(menuItem(page, 'Tailscale'), touch);
  await expect(panel(page)).toBeVisible();
  await expect(panel(page)).toHaveAccessibleName('Tailscale');
}

async function openPreferences(page: Page, touch = false) {
  await workspaceMenu(page, touch);
  await choose(menuItem(page, 'Preferences'), touch);
  const preferences = page.locator('[data-app-id="appearance"]');
  await expect(preferences).toBeVisible();
  return preferences;
}

function snapshot(local = false) {
  return {
    local,
    remote: {
      state: 'disabled',
      message: 'AXV remote access is disabled in this profile.',
    },
    host: {
      state: 'connected',
      installed: true,
      daemonAvailable: true,
      hostname: 'workstation',
      ips: ['100.64.0.20', 'fd7a:115c:a1e0::20'],
    },
  };
}

test('Tailscale distinguishes AXV and system Tailscale and refreshes without host changes', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = snapshot();
  await page.addInitScript(() =>
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: async () => undefined },
    }),
  );
  let enrollmentReads = 0;
  let connectionReads = 0;
  const writes: string[] = [];
  page.on('request', (request) => {
    if (request.url().includes('/api/remote/status')) enrollmentReads++;
    if (request.url().includes('/api/connection')) {
      connectionReads++;
      if (request.method() !== 'GET') writes.push(request.method());
    }
  });
  await page.route('**/api/connection', (route) =>
    route.fulfill({ json: state }),
  );
  await page.reload();
  await expect(
    page.getByRole('region', { name: 'Desktop status', exact: true }),
  ).toHaveCount(0);
  expect(connectionReads).toBe(0);
  await openRemote(page, touch);
  await expect(
    remoteState(page).getByRole('heading', { name: 'AXV Remote Access' }),
  ).toBeVisible();
  await expect(remoteState(page).locator('.tailscale-state')).toHaveText('Off');
  await expect(hostState(page).locator('.tailscale-state')).toHaveText(
    'Connected',
  );
  await expect(
    hostState(page).getByText('100.64.0.20', { exact: true }),
  ).toBeVisible();
  await expect(panel(page).getByRole('link', { name: address })).toHaveCount(0);
  Object.assign(state.remote, { state: 'ready', message: '', url: address });
  Object.assign(state.host, { state: 'disconnected' });
  await choose(
    panel(page).getByRole('button', { name: 'Refresh', exact: true }),
    touch,
  );
  await expect(remoteState(page).locator('.tailscale-state')).toHaveText(
    'Ready',
  );
  await expect(hostState(page).locator('.tailscale-state')).toHaveText(
    'Disconnected',
  );
  await expect(
    panel(page).getByRole('link', { name: address, exact: true }),
  ).toBeVisible();
  await choose(
    panel(page).getByRole('button', { name: 'Copy HTTPS URL', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText('HTTPS URL copied.', { exact: true }),
  ).toBeVisible();
  expect(enrollmentReads).toBe(0);
  expect(writes).toEqual([]);
});

test('connection polling and local authorization belong to the active Tailscale view', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = snapshot(true);
  state.remote.state = 'needs-login';
  let reads = 0;
  let connectionReads = 0;
  let authUrl = 'https://login.tailscale.com/a/example';
  await page.route('**/api/connection', (route) => {
    connectionReads++;
    return route.fulfill({ json: state });
  });
  await page.route('**/api/remote/status', (route) => {
    reads++;
    return route.fulfill({
      json: {
        state: 'needs-login',
        authUrl,
      },
    });
  });
  await page.reload();
  expect(connectionReads).toBe(0);
  expect(reads).toBe(0);
  await openRemote(page, touch);
  await expect(remoteState(page).locator('.tailscale-state')).toHaveText(
    'Sign in',
  );
  const authorize = panel(page).getByRole('link', {
    name: 'Authorize',
    exact: true,
  });
  await expect(authorize).toHaveAttribute(
    'href',
    'https://login.tailscale.com/a/example',
  );
  await authorize.focus();
  authUrl = 'https://login.tailscale.com/a/updated';
  await expect(authorize).toHaveAttribute('href', authUrl, { timeout: 9000 });
  await expect(authorize).toBeFocused();
  await choose(
    panel(page).getByRole('button', { name: /^Miniaturize / }),
    touch,
  );
  await expect(panel(page)).toBeHidden();
  await expect(
    panel(page).locator('a[href*="login.tailscale.com/a/"]'),
  ).toHaveCount(0);
  const hiddenReads = { connections: connectionReads, authorizations: reads };
  // Leave enough time for a five-second polling tick to expose a hidden reader.
  await page.waitForTimeout(5500);
  expect({ connections: connectionReads, authorizations: reads }).toEqual(
    hiddenReads,
  );
  state.local = false;
  reads = 0;
  await page.reload();
  await openRemote(page, touch);
  await choose(
    panel(page).getByRole('button', { name: 'Refresh', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText(
      'Complete AXV authorization on the computer running AXV.',
    ),
  ).toBeVisible();
  await expect(authorize).toHaveCount(0);
  await choose(
    panel(page).getByRole('button', { name: 'Refresh', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText(
      'Complete AXV authorization on the computer running AXV.',
    ),
  ).toBeVisible();
  expect(reads).toBe(0);
});

test('failed polling stops reporting ready and copy errors remain visible', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = snapshot(false);
  Object.assign(state.remote, { state: 'ready', message: '', url: address });
  let failed = false;
  await page.route('**/api/connection', (route) =>
    failed
      ? route.fulfill({
          status: 503,
          json: { error: 'Connection status unavailable.' },
        })
      : route.fulfill({ json: state }),
  );
  await page.addInitScript(() =>
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: async () => {
          throw new Error('Clipboard denied');
        },
      },
    }),
  );
  await page.reload();
  await openRemote(page, touch);
  await expect(remoteState(page).locator('.tailscale-state')).toHaveText(
    'Ready',
  );
  await choose(
    panel(page).getByRole('button', { name: 'Copy HTTPS URL', exact: true }),
    touch,
  );
  await expect(panel(page).getByRole('alert')).toContainText(/Clipboard|copy/i);
  failed = true;
  await choose(
    panel(page).getByRole('button', { name: 'Refresh', exact: true }),
    touch,
  );
  await expect(remoteState(page).locator('.tailscale-state')).toHaveText(
    'Unknown',
  );
  await expect(hostState(page).locator('.tailscale-state')).toHaveText(
    'Unknown',
  );
  await expect(
    panel(page).getByRole('link', { name: address, exact: true }),
  ).toHaveCount(0);
  failed = false;
  await choose(
    panel(page).getByRole('button', { name: 'Refresh', exact: true }),
    touch,
  );
  await expect(remoteState(page).locator('.tailscale-state')).toHaveText(
    'Ready',
  );
});

test('Preferences and optional Dock launchers share the Tailscale panel without a status strip', async ({
  page,
  request,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  let connectionReads = 0;
  await page.route('**/api/connection', (route) => {
    connectionReads++;
    return route.fulfill({ json: snapshot() });
  });
  await page.reload();
  await expect(dockEntry(page, 'remote-access')).toHaveCount(0);
  await expect(
    page.getByRole('region', { name: 'Desktop status', exact: true }),
  ).toHaveCount(0);
  const preferences = await openPreferences(page, touch);
  expect(connectionReads).toBe(0);
  const open = preferences
    .getByRole('group', { name: 'Tailscale', exact: true })
    .getByRole('button', { name: 'Open', exact: true });
  await choose(open, touch);
  await expect(panel(page)).toBeVisible();
  const id = await panel(page).getAttribute('data-window-id');
  await choose(
    panel(page).getByRole('button', { name: /^Miniaturize / }),
    touch,
  );
  await openPreferences(page, touch);
  await choose(open, touch);
  await expect(panel(page)).toHaveAttribute('data-window-id', id!);
  await expect(panel(page)).toHaveCount(1);
  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 390, height: 844 },
  ]) {
    await page.setViewportSize(viewport);
    await expect(
      panel(page).getByRole('button', { name: /^Close / }),
    ).toBeInViewport();
    await expect(
      panel(page).getByRole('button', { name: 'Refresh', exact: true }),
    ).toBeInViewport();
    const frame = await panel(page).boundingBox();
    const dock = await page.locator('.ns-dock').boundingBox();
    expect(frame && dock).toBeTruthy();
    const dockIcon = await dockEntry(page, 'remote-access')
      .locator('.ns-dock-tile svg')
      .boundingBox();
    const dockOptions = await dockEntry(page, 'remote-access')
      .locator('.ns-dock-options')
      .boundingBox();
    expect(dockIcon && dockOptions).toBeTruthy();
    expect(dockIcon!.y + dockIcon!.height).toBeLessThanOrEqual(dockOptions!.y);
    if (viewport.width < 600)
      expect(frame!.y + frame!.height).toBeLessThanOrEqual(dock!.y);
    else expect(frame!.x + frame!.width).toBeLessThanOrEqual(dock!.x);
  }
  await choose(panel(page).getByRole('button', { name: /^Close / }), touch);
  await choose(preferences.getByRole('button', { name: /^Close / }), touch);
  await expect(dockEntry(page, 'remote-access')).toHaveCount(0);
  await choose(
    page.getByRole('button', { name: 'Add application to Dock', exact: true }),
    touch,
  );
  await choose(menuItem(page, 'Tailscale'), touch);
  await expect(dockEntry(page, 'remote-access')).toBeVisible();
  await expect
    .poll(async () =>
      (await persistedLayout(page, request))?.dock.some(
        (entry: { appId: string; pinned: boolean }) =>
          entry.appId === 'remote-access' && entry.pinned,
      ),
    )
    .toBe(true);
  await choose(
    dockEntry(page, 'remote-access').locator('.ns-dock-tile'),
    touch,
  );
  await expect(panel(page)).toBeVisible();
  await choose(panel(page).getByRole('button', { name: /^Close / }), touch);
  await expect(dockEntry(page, 'remote-access')).toBeVisible();
  await page.reload();
  await expect(dockEntry(page, 'remote-access')).toBeVisible();
});

test('desktop Tailscale launcher opens one panel and stays outside document deletion', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  let connectionReads = 0;
  const documentWrites: string[] = [];
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname;
    if (path.startsWith('/api/entries') && request.method() !== 'GET')
      documentWrites.push(`${request.method()} ${path}`);
  });
  await page.route('**/api/connection', (route) => {
    connectionReads++;
    return route.fulfill({ json: snapshot() });
  });
  await page.reload();
  for (const app of ['files', 'welcome']) {
    await choose(
      page
        .locator(`[data-app-id="${app}"]`)
        .getByRole('button', { name: /^Close / }),
      touch,
    );
  }
  const canvas = page.getByRole('group', { name: 'Desktop icons' });
  const launcher = canvas.getByRole('button', {
    name: 'Tailscale',
    exact: true,
  });
  await expect(launcher).toHaveAttribute(
    'data-icon-id',
    'system:remote-access',
  );
  await expect(launcher).toBeInViewport();
  await expect(dockEntry(page, 'remote-access')).toHaveCount(0);
  // A visible launcher does not own a background connection-status poller.
  await page.waitForTimeout(5500);
  expect(connectionReads).toBe(0);
  await launcher.click({ button: 'right' });
  await expect(menuItem(page, 'Open')).toBeVisible();
  await expect(menuItem(page, 'Rename')).toHaveCount(0);
  await expect(menuItem(page, 'Move to Recycler')).toHaveCount(0);
  await expect(menuItem(page, 'Delete')).toHaveCount(0);
  await page.keyboard.press('Escape');
  // A hardware keyboard activates only the single selected launcher.
  await launcher.click();
  await expect(launcher).toHaveAttribute('aria-pressed', 'true');
  await expect(canvas.locator('[aria-pressed="true"]')).toHaveCount(1);
  await page.keyboard.press('Delete');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(launcher).toBeVisible();
  expect(documentWrites).toEqual([]);
  await page.keyboard.press('Enter');
  await expect(panel(page)).toBeVisible();
  await expect(panel(page)).toHaveAttribute(
    'data-window-id',
    'remote-access:main',
  );
  await expect(panel(page)).toHaveAccessibleName('Tailscale');
  await expect(remoteState(page).locator('.tailscale-state')).toHaveText('Off');
  const id = await panel(page).getAttribute('data-window-id');
  await choose(
    panel(page).getByRole('button', { name: /^Miniaturize / }),
    touch,
  );
  await expect(panel(page)).toBeHidden();
  if (touch) {
    await launcher.tap();
    await launcher.tap();
  } else await launcher.dblclick();
  await expect(panel(page)).toBeVisible();
  await expect(panel(page)).toHaveAttribute('data-window-id', id!);
  await expect(panel(page)).toHaveCount(1);
  await expect(launcher).toBeAttached();
  expect(documentWrites).toEqual([]);
});
