import type { Locator, Page } from '@playwright/test';
import {
  bounds,
  dockEntry,
  expect,
  menuItem,
  persistedLayout,
  test,
} from './helpers';

const address = 'https://axv-workstation.example.ts.net';
// Match the safe summaries returned by server/connection.go.
const routineMessages = {
  disabled: 'AXV remote access is disabled in this profile.',
  starting: 'AXV remote access is starting.',
  login: 'Complete Tailscale enrollment on the computer running AXV.',
  https:
    'AXV is waiting for tailnet HTTPS availability. Check Tailscale HTTPS settings.',
};
const panel = (page: Page) => page.locator('[data-app-id="remote-access"]');
const remoteState = (page: Page) =>
  panel(page).locator('[aria-labelledby="tailscale-axv-title"]');

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
  await expect(
    panel(page).getByRole('heading', { name: 'Remote access', exact: true }),
  ).toBeVisible();
}

async function openPreferences(page: Page, touch = false) {
  await workspaceMenu(page, touch);
  await choose(menuItem(page, 'Preferences'), touch);
  const preferences = page.locator('[data-app-id="appearance"]');
  await expect(preferences).toBeVisible();
  return preferences;
}

function snapshot(local = true) {
  return {
    local,
    remote: {
      enabled: false,
      supported: true,
      state: 'disabled',
      message: '',
    },
  };
}

function authorization(state: ReturnType<typeof snapshot>, authUrl?: string) {
  return {
    instanceId: 'tailscale-browser-fixture',
    ...state.remote,
    ...(authUrl ? { authUrl } : {}),
  };
}

test('local Tailscale setup enables the AXV node, authorizes, and disables without a workstation restart', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = snapshot();
  state.remote.message = routineMessages.disabled;
  await page.addInitScript(() =>
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: async () => undefined },
    }),
  );
  let connectionReads = 0;
  let enrollmentReads = 0;
  const writes: boolean[] = [];
  const navigation: string[] = [];
  page.on('framenavigated', (frame) => {
    if (frame === page.mainFrame()) navigation.push(frame.url());
  });
  await page.route('**/api/connection', (route) => {
    connectionReads++;
    return route.fulfill({ json: state });
  });
  await page.route('**/api/remote/status', (route) => {
    enrollmentReads++;
    return route.fulfill({
      json: authorization(state, 'https://login.tailscale.com/a/example'),
    });
  });
  await page.route('**/api/remote', (route) => {
    expect(route.request().method()).toBe('PUT');
    const { enabled } = route.request().postDataJSON() as { enabled: boolean };
    writes.push(enabled);
    Object.assign(state.remote, {
      enabled,
      state: enabled ? 'needs-login' : 'disabled',
      message: enabled ? routineMessages.login : routineMessages.disabled,
      url: undefined,
    });
    return route.fulfill({ json: authorization(state) });
  });
  await page.reload();
  const initialNavigation = navigation.length;
  await expect(
    page.getByRole('region', { name: 'Desktop status', exact: true }),
  ).toHaveCount(0);
  expect(connectionReads).toBe(0);
  await openRemote(page, touch);
  await expect(remoteState(page).locator('.tailscale-state')).toHaveText('Off');
  await expect(
    panel(page).getByText(routineMessages.disabled, { exact: true }),
  ).toHaveCount(0);
  await expect(
    panel(page).getByText('Access AXV from your other devices.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(remoteState(page).locator('.tailscale-state')).toHaveAttribute(
    'role',
    'status',
  );
  await expect(remoteState(page).locator('.tailscale-state')).toHaveAttribute(
    'aria-atomic',
    'true',
  );
  const caption = panel(page).locator('.toolbar-caption');
  await expect(caption).toHaveText('Connection status');
  await expect(caption).not.toHaveAttribute('role', 'status');
  const systemHelp = panel(page).getByText(/No system installation is needed/);
  await expect(systemHelp).toBeHidden();
  await expect(
    panel(page).getByText('System Tailscale', { exact: true }),
  ).toHaveCount(0);
  await expect(
    panel(page).getByRole('button', { name: 'Enable', exact: true }),
  ).toBeEnabled();
  await choose(
    panel(page).getByRole('button', { name: 'Enable', exact: true }),
    touch,
  );
  await expect(remoteState(page).locator('.tailscale-state')).toHaveText(
    'Sign in',
  );
  await expect(
    panel(page).getByRole('link', { name: 'Authorize', exact: true }),
  ).toHaveAttribute('href', 'https://login.tailscale.com/a/example');
  await expect(
    panel(page).getByText(routineMessages.login, { exact: true }),
  ).toHaveCount(0);
  const actions = remoteState(page).locator('.tailscale-actions');
  const authorize = actions.getByRole('link', {
    name: 'Authorize',
    exact: true,
  });
  await expect(authorize).toHaveClass(/\bns-default\b/);
  await expect(
    actions.getByRole('button', { name: 'Disable', exact: true }),
  ).toBeVisible();
  await expect(
    panel(page).getByText('Sign in with your Tailscale account.', {
      exact: true,
    }),
  ).toBeVisible();
  const authorizeBounds = await bounds(authorize);
  const loginDisableBounds = await bounds(
    actions.getByRole('button', { name: 'Disable', exact: true }),
  );
  expect(
    Math.abs(
      authorizeBounds.y +
        authorizeBounds.height / 2 -
        loginDisableBounds.y -
        loginDisableBounds.height / 2,
    ),
  ).toBeLessThanOrEqual(1);
  expect(writes).toEqual([true]);
  expect(enrollmentReads).toBeGreaterThan(0);
  Object.assign(state.remote, {
    state: 'needs-https',
    message: routineMessages.https,
  });
  await choose(
    panel(page).getByRole('button', { name: 'Refresh', exact: true }),
    touch,
  );
  await expect(remoteState(page).locator('.tailscale-state')).toHaveText(
    'HTTPS required',
  );
  await expect(
    panel(page).getByText(routineMessages.https, { exact: true }),
  ).toHaveCount(0);
  await expect(
    panel(page).getByRole('link', {
      name: 'Tailscale DNS settings',
      exact: true,
    }),
  ).toHaveAttribute('href', 'https://login.tailscale.com/admin/dns');
  await expect(remoteState(page)).toContainText(
    'Enable MagicDNS and HTTPS certificates',
  );
  await expect(
    panel(page).getByText(
      'The hostname appears in public certificate logs. Access stays private.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    panel(page).getByRole('link', { name: 'Authorize', exact: true }),
  ).toHaveCount(0);
  Object.assign(state.remote, { state: 'ready', url: address, message: '' });
  await choose(
    panel(page).getByRole('button', { name: 'Refresh', exact: true }),
    touch,
  );
  await expect(remoteState(page).locator('.tailscale-state')).toHaveText(
    'Ready',
  );
  await expect(
    panel(page).getByRole('link', { name: address, exact: true }),
  ).toBeVisible();
  const heading = panel(page).getByRole('heading', {
    name: 'Remote access',
    exact: true,
  });
  const disable = panel(page).getByRole('button', {
    name: 'Disable',
    exact: true,
  });
  const copyButton = panel(page).getByRole('button', {
    name: 'Copy HTTPS URL',
    exact: true,
  });
  await copyButton.scrollIntoViewIfNeeded();
  const beforeCopy = await Promise.all([bounds(heading), bounds(disable)]);
  await choose(
    panel(page).getByRole('button', { name: 'Copy HTTPS URL', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByRole('status').filter({ hasText: 'HTTPS URL copied.' }),
  ).toBeVisible();
  const afterCopy = await Promise.all([bounds(heading), bounds(disable)]);
  for (const [index, before] of beforeCopy.entries()) {
    expect(Math.abs(afterCopy[index].x - before.x)).toBeLessThanOrEqual(1);
    expect(Math.abs(afterCopy[index].y - before.y)).toBeLessThanOrEqual(1);
  }
  await choose(
    panel(page)
      .locator('summary')
      .filter({ hasText: 'Connect another device' }),
    touch,
  );
  await expect(
    panel(page).getByRole('link', { name: 'Install Tailscale', exact: true }),
  ).toHaveAttribute('href', 'https://tailscale.com/download');
  await expect(systemHelp).toBeVisible();
  await expect(
    panel(page).getByText(
      'Connect with the same Tailscale account that authorized AXV.',
    ),
  ).toBeVisible();
  await choose(disable, touch);
  const confirmation = page.getByRole('dialog', {
    name: 'Disable remote access?',
    exact: true,
  });
  await expect(confirmation).toBeVisible();
  await expect(confirmation).toContainText(/host shells/i);
  await expect(confirmation).toContainText(/Codex.*detach/i);
  await expect(confirmation).toContainText(/VMs.*continue/i);
  await choose(
    confirmation.getByRole('button', { name: 'Cancel', exact: true }),
    touch,
  );
  await expect(confirmation).toBeHidden();
  await expect(disable).toBeFocused();
  expect(writes).toEqual([true]);
  await choose(disable, touch);
  await choose(
    confirmation.getByRole('button', { name: 'Disable', exact: true }),
    touch,
  );
  await expect(remoteState(page).locator('.tailscale-state')).toHaveText('Off');
  await expect(
    panel(page).getByRole('button', { name: 'Enable', exact: true }),
  ).toBeEnabled();
  await expect(
    panel(page).getByRole('link', { name: address, exact: true }),
  ).toHaveCount(0);
  expect(writes).toEqual([true, false]);
  expect(navigation).toHaveLength(initialNavigation);
});

test('failed Tailscale control stays visible and an enabled error can be retried', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = snapshot();
  let failWrite = true;
  const writes: boolean[] = [];
  await page.route('**/api/connection', (route) =>
    route.fulfill({ json: state }),
  );
  await page.route('**/api/remote', (route) => {
    const { enabled } = route.request().postDataJSON() as { enabled: boolean };
    writes.push(enabled);
    if (failWrite)
      return route.fulfill({
        status: 500,
        json: { error: 'Could not save remote access setting.' },
      });
    Object.assign(state.remote, {
      enabled,
      state: 'starting',
      message: routineMessages.starting,
    });
    return route.fulfill({ json: authorization(state) });
  });
  await page.reload();
  await openRemote(page, touch);
  await choose(
    panel(page).getByRole('button', { name: 'Enable', exact: true }),
    touch,
  );
  await expect(panel(page).getByRole('alert')).toContainText(
    'Could not save remote access setting.',
  );
  await expect(remoteState(page).locator('.tailscale-state')).toHaveText('Off');
  await expect(
    panel(page).getByRole('button', { name: 'Enable', exact: true }),
  ).toBeEnabled();
  Object.assign(state.remote, {
    enabled: true,
    state: 'error',
    message: 'The remote listener stopped.',
  });
  await choose(
    panel(page).getByRole('button', { name: 'Refresh', exact: true }),
    touch,
  );
  await expect(remoteState(page).locator('.tailscale-state')).toHaveText(
    'Needs attention',
  );
  await expect(
    panel(page).getByText('The remote listener stopped.', { exact: true }),
  ).toBeVisible();
  await expect(
    panel(page).getByRole('button', { name: 'Retry', exact: true }),
  ).toBeEnabled();
  failWrite = false;
  await choose(
    panel(page).getByRole('button', { name: 'Retry', exact: true }),
    touch,
  );
  await expect(remoteState(page).locator('.tailscale-state')).toHaveText(
    'Starting',
  );
  await expect(
    panel(page).getByText(routineMessages.starting, { exact: true }),
  ).toHaveCount(0);
  await expect(
    panel(page).getByRole('button', { name: 'Disable', exact: true }),
  ).toBeEnabled();
  expect(writes).toEqual([true, true]);
});

test('connection polling and local authorization belong to the active Tailscale view', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = snapshot(true);
  Object.assign(state.remote, { enabled: true, state: 'needs-login' });
  let reads = 0;
  let connectionReads = 0;
  let authUrl = 'https://login.tailscale.com/a/example';
  await page.route('**/api/connection', (route) => {
    connectionReads++;
    return route.fulfill({ json: state });
  });
  await page.route('**/api/remote/status', (route) => {
    reads++;
    return route.fulfill({ json: authorization(state, authUrl) });
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
  await expect(authorize).toHaveAttribute('href', authUrl);
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
});

test('unsupported AXV hosts explain availability without offering a control', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = snapshot();
  Object.assign(state.remote, {
    supported: false,
    message: 'AXV remote access requires macOS or Linux.',
  });
  const protectedRequests: string[] = [];
  page.on('request', (request) => {
    if (new URL(request.url()).pathname.startsWith('/api/remote'))
      protectedRequests.push(new URL(request.url()).pathname);
  });
  await page.route('**/api/connection', (route) =>
    route.fulfill({ json: state }),
  );
  await page.reload();
  await openRemote(page, touch);
  await expect(
    panel(page).getByText('Requires an AXV server running macOS or Linux.'),
  ).toBeVisible();
  for (const name of ['Enable', 'Disable', 'Retry'])
    await expect(
      panel(page).getByRole('button', { name, exact: true }),
    ).toHaveCount(0);
  expect(protectedRequests).toEqual([]);
});

test('remote Tailscale viewers can copy the address but cannot control or enroll the AXV node', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = snapshot(false);
  Object.assign(state.remote, { enabled: true, state: 'needs-login' });
  await page.addInitScript(() =>
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: async () => undefined },
    }),
  );
  const protectedRequests: string[] = [];
  page.on('request', (request) => {
    if (new URL(request.url()).pathname.startsWith('/api/remote'))
      protectedRequests.push(
        `${request.method()} ${new URL(request.url()).pathname}`,
      );
  });
  await page.route('**/api/connection', (route) =>
    route.fulfill({ json: state }),
  );
  await page.reload();
  await openRemote(page, touch);
  await expect(
    panel(page).getByText('Manage remote access on the computer running AXV.'),
  ).toBeVisible();
  await expect(
    panel(page).getByText('Manage remote access on the computer running AXV.', {
      exact: true,
    }),
  ).toHaveCount(1);
  await expect(
    panel(page).getByRole('link', { name: 'Authorize', exact: true }),
  ).toHaveCount(0);
  for (const name of ['Enable', 'Disable', 'Retry'])
    await expect(
      panel(page).getByRole('button', { name, exact: true }),
    ).toHaveCount(0);
  Object.assign(state.remote, { state: 'ready', url: address });
  await choose(
    panel(page).getByRole('button', { name: 'Refresh', exact: true }),
    touch,
  );
  await expect(remoteState(page).locator('.tailscale-state')).toHaveText(
    'Ready',
  );
  await expect(
    panel(page).getByRole('button', { name: 'Copy HTTPS URL', exact: true }),
  ).toBeVisible();
  await choose(
    panel(page).getByRole('button', { name: 'Copy HTTPS URL', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText('HTTPS URL copied.', { exact: true }),
  ).toBeVisible();
  expect(protectedRequests).toEqual([]);
});

test('failed polling stops reporting ready and copy errors remain visible', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = snapshot(false);
  Object.assign(state.remote, { enabled: true, state: 'ready', url: address });
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
  const heading = panel(page).getByRole('heading', {
    name: 'Remote access',
    exact: true,
  });
  const beforeCopy = await bounds(heading);
  await choose(
    panel(page).getByRole('button', { name: 'Copy HTTPS URL', exact: true }),
    touch,
  );
  await expect(panel(page).getByRole('alert')).toContainText(/Clipboard|copy/i);
  const afterCopy = await bounds(heading);
  expect(Math.abs(afterCopy.x - beforeCopy.x)).toBeLessThanOrEqual(1);
  expect(Math.abs(afterCopy.y - beforeCopy.y)).toBeLessThanOrEqual(1);
  failed = true;
  await choose(
    panel(page).getByRole('button', { name: 'Refresh', exact: true }),
    touch,
  );
  await expect(remoteState(page).locator('.tailscale-state')).toHaveText(
    'Unknown',
  );
  await expect(panel(page).locator('.toolbar-caption')).toHaveText(
    'Status unavailable',
  );
  await expect(
    panel(page).getByText(
      'Showing previous details. Current connection state is unknown.',
      { exact: true },
    ),
  ).toBeVisible();
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
  await expect(menuItem(page, 'Recycle')).toHaveCount(0);
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
