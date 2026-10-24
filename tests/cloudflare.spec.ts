import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import type { Locator, Page, Route } from '@playwright/test';
import type { CloudflarePublication } from '../apps/dashboard/src/features/cloudflare/types';
import { activate, bounds, dockEntry, expect, menuItem, test } from './helpers';

const address = 'https://workstation.example.com';
const panel = (page: Page) => page.locator('[data-app-id="cloudflare"]');
const stateLabel = (page: Page) => panel(page).locator('.cloudflare-state');
const diagnosticsSummary = (page: Page) =>
  panel(page)
    .locator('summary')
    .filter({ hasText: /^Diagnostics$/ });

async function choose(target: Locator, touch = false) {
  if (touch) await target.tap();
  else await target.click();
}

async function openCloudflare(page: Page, touch = false) {
  await choose(
    page.getByRole('button', { name: 'Workspace menu', exact: true }),
    touch,
  );
  await choose(menuItem(page, 'Applications'), touch);
  await choose(menuItem(page, 'Cloudflare'), touch);
  await expect(panel(page)).toBeVisible();
  await expect(panel(page)).toHaveAccessibleName('Cloudflare');
}

async function cancelSetupReview(
  page: Page,
  opener: Locator,
  title: string,
  touch: boolean,
) {
  for (const method of ['enter', 'button'] as const) {
    await choose(opener, touch);
    const review = page.getByRole('dialog', { name: title, exact: true });
    const cancel = review.getByRole('button', { name: 'Cancel', exact: true });
    await expect(cancel).toHaveAttribute('data-default', 'true');
    await expect(cancel).toBeFocused();
    if (method === 'enter') await page.keyboard.press('Enter');
    else await choose(cancel, touch);
    await expect(review).toBeHidden();
    await expect(opener).toBeFocused();
  }
}

function snapshot(local = true, configured = true) {
  return {
    local,
    remote: { enabled: false, supported: true, state: 'disabled' },
    cloudflare: {
      enabled: false,
      configured,
      state: 'disabled',
      url: undefined as string | undefined,
      message: '',
    },
  };
}

function configuration() {
  return {
    instanceId: 'cf-instance',
    revision: 'revision-1',
    access: {
      enabled: false,
      listen: '127.0.0.1:3002',
      origin: address,
      issuer: 'https://team.cloudflareaccess.com',
      audience: 'desktop-aud',
      ownerSubject: 'owner-sub',
    },
    account: {
      configured: false,
      hasToken: false,
      accountId: '',
      zoneId: '',
      tunnelId: '',
      domain: '',
      listen: '127.0.0.1:3003',
      audience: '',
    },
  };
}

function retainedPublication(
  visibility: 'owner' | 'public',
): CloudflarePublication {
  const generation = '0123456789abcdef0123456789abcdef';
  return {
    id: 'original-publication',
    requestId: 'original-request',
    name: 'editor',
    generation,
    domain: 'example.net',
    hostname: `editor-${generation}.example.net`,
    binding: {
      hostId: 'original-host',
      projectId: 'original-project',
      projectRevision: 1,
      volumeId: 'original-volume',
      volumeRevision: 2,
      machineId: 'original-vm',
      bootId: 'original-boot',
      instanceId: 'original-instance',
    },
    port: 3000,
    visibility,
    state: 'error',
    phase: 'ingress',
    cleanupPending: false,
    error: 'The original ingress update needs attention.',
    createdAt: '2026-10-24T00:00:00Z',
    updatedAt: '2026-10-24T00:00:00Z',
  };
}

test.beforeEach(async ({ page }) => {
  await page.route('**/api/cloudflare/capabilities', (route) =>
    route.fulfill({
      json: { configure: true, publish: true, health: true, traffic: true },
    }),
  );
  await page.route('**/api/cloudflare/health*', (route) =>
    route.fulfill({
      json: {
        state: 'unconfigured',
        checkedAt: '2026-10-24T00:00:00Z',
        message: 'Account publishing is not configured.',
      },
    }),
  );
  await page.route('**/api/cloudflare/config', (route) =>
    route.fulfill({ json: configuration() }),
  );
  await page.route('**/api/cloudflare-publications', (route) =>
    route.fulfill({
      json: {
        instanceId: 'cf-instance',
        supported: false,
        domain: '',
        reason: 'Connect an account in Setup.',
        publications: [],
      },
    }),
  );
});

test('the default Cloudflare desktop icon opens one utility without polling while closed', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  let reads = 0;
  await page.route('**/api/connection', (route) => {
    reads++;
    return route.fulfill({ json: snapshot() });
  });
  await page.reload();
  for (const app of ['files', 'welcome'])
    await choose(
      page
        .locator(`[data-app-id="${app}"]`)
        .getByRole('button', { name: /^Close / }),
      touch,
    );
  const canvas = page.getByRole('group', { name: 'Desktop icons' });
  const launcher = canvas.getByRole('button', {
    name: 'Cloudflare',
    exact: true,
  });
  await expect(launcher).toHaveAttribute(
    'data-icon-id',
    'application:cloudflare',
  );
  await expect(launcher).toBeInViewport();
  await expect(dockEntry(page, 'cloudflare')).toHaveCount(0);
  await page.waitForTimeout(5500);
  expect(reads).toBe(0);
  await launcher.click();
  await page.keyboard.press('Enter');
  await expect(panel(page)).toBeVisible();
  await expect(panel(page)).toHaveAttribute(
    'data-window-id',
    'cloudflare:main',
  );
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
  await expect(panel(page)).toHaveCount(1);
  await expect(panel(page)).toHaveAttribute(
    'data-window-id',
    'cloudflare:main',
  );
});

test('first-time setup opens real local settings without enabling or reading diagnostics', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  let detailReads = 0;
  let configReads = 0;
  const writes: string[] = [];
  await page.route('**/api/connection', (route) =>
    route.fulfill({ json: snapshot(true, false) }),
  );
  await page.route('**/api/cloudflare/status', (route) => {
    detailReads++;
    return route.fulfill({ json: {} });
  });
  await page.route('**/api/cloudflare/config', (route) => {
    configReads++;
    return route.fulfill({ json: configuration() });
  });
  page.on('request', (request) => {
    if (request.method() === 'PUT')
      writes.push(new URL(request.url()).pathname);
  });
  await page.reload();
  await openCloudflare(page, touch);
  await expect(stateLabel(page)).toHaveText('Setup required');
  expect(configReads).toBe(0);
  const setup = panel(page).getByRole('button', {
    name: 'Open setup',
    exact: true,
  });
  if (touch) await setup.tap();
  else {
    await setup.focus();
    await page.keyboard.press('Enter');
  }
  await expect(
    panel(page).getByRole('tab', { name: 'Setup', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await expect(
    panel(page).getByRole('group', { name: 'Desktop access', exact: true }),
  ).toBeVisible();
  await expect(
    panel(page).getByLabel('Desktop origin', { exact: true }),
  ).toHaveValue(address);
  await expect(
    panel(page).getByRole('button', { name: 'Validate access', exact: true }),
  ).toBeEnabled();
  expect(configReads).toBe(1);
  expect(detailReads).toBe(0);
  expect(writes.filter((path) => path.startsWith('/api/cloudflare'))).toEqual(
    [],
  );
  const manual = panel(page)
    .locator('summary')
    .filter({ hasText: /^Manual setup guide$/ });
  await choose(manual, touch);
  const instructions = panel(page).getByRole('region', {
    name: 'Cloudflare setup guide',
    exact: true,
  });
  await expect(instructions).toBeVisible();
  await expect(instructions).toContainText('AXV_HOME/config.json');
  await expect(instructions).toContainText('127.0.0.1:3002');
  await choose(
    instructions.getByRole('button', { name: 'Close guide', exact: true }),
    touch,
  );
  await expect(instructions).toBeHidden();
});

test('a configured but disabled entrypoint shows its origin address only in local diagnostics', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  let detailReads = 0;
  await page.route('**/api/connection', (route) =>
    route.fulfill({ json: snapshot() }),
  );
  await page.route('**/api/cloudflare/status', (route) => {
    detailReads++;
    return route.fulfill({
      json: {
        instanceId: 'disabled-fixture',
        ...snapshot().cloudflare,
        listen: '127.0.0.1:3002',
      },
    });
  });
  await page.reload();
  await openCloudflare(page, touch);
  await expect(stateLabel(page)).toHaveText('Off');
  await expect(
    panel(page).getByRole('button', { name: 'Enable', exact: true }),
  ).toBeEnabled();
  await expect(
    panel(page).getByText('Cloudflare access is configured but not enabled.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    panel(page).getByText('Listening locally', { exact: true }),
  ).toHaveCount(0);
  await expect(
    panel(page).getByRole('link', { name: address, exact: true }),
  ).toHaveCount(0);
  expect(detailReads).toBe(0);
  await choose(diagnosticsSummary(page), touch);
  await expect(
    panel(page).getByText('Origin address', { exact: true }),
  ).toBeVisible();
  await expect(
    panel(page).getByText('127.0.0.1:3002', { exact: true }),
  ).toBeVisible();
  await expect(stateLabel(page)).toHaveText('Off');
  await expect(
    panel(page).getByText('Configured address; the entrypoint is off.', {
      exact: true,
    }),
  ).toBeVisible();
  expect(detailReads).toBeGreaterThan(0);
});

test('local Cloudflare utility enables an entrypoint and confirms disabling only its connections', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = snapshot(true, false);
  const writes: boolean[] = [];
  const pendingWrites: Route[] = [];
  let detailReads = 0;
  await page.addInitScript(() =>
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: async () => undefined },
    }),
  );
  await page.route('**/api/connection', (route) =>
    route.fulfill({ json: state }),
  );
  await page.route('**/api/cloudflare/status', (route) => {
    detailReads++;
    return route.fulfill({
      json: {
        instanceId: 'cloudflare-fixture',
        ...state.cloudflare,
        listen: '127.0.0.1:3002',
      },
    });
  });
  await page.route('**/api/cloudflare', (route) => {
    expect(route.request().method()).toBe('PUT');
    const body = route.request().postDataJSON();
    expect(Object.keys(body)).toEqual(['enabled']);
    writes.push(body.enabled);
    pendingWrites.push(route);
  });
  await page.reload();
  await openCloudflare(page, touch);
  await expect(stateLabel(page)).toHaveText('Setup required');
  await expect(
    panel(page).getByRole('button', { name: 'Open setup', exact: true }),
  ).toBeVisible();
  const enable = panel(page).getByRole('button', {
    name: 'Enable',
    exact: true,
  });
  expect(detailReads).toBe(0);
  state.cloudflare.configured = true;
  await choose(
    panel(page).getByRole('button', { name: 'Refresh', exact: true }),
    touch,
  );
  await expect(enable).toBeEnabled();
  await choose(enable, touch);
  await expect.poll(() => pendingWrites.length).toBe(1);
  await expect(
    panel(page).getByText('Enabling…', { exact: true }),
  ).toBeVisible();
  await expect(enable).toBeDisabled();
  Object.assign(state.cloudflare, {
    enabled: true,
    state: 'listening',
    url: address,
  });
  await pendingWrites[0]!.fulfill({ json: state.cloudflare });
  await expect(stateLabel(page)).toHaveText('Listening locally');
  await expect(
    panel(page).getByText('Public access has not been verified.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(panel(page).locator('.cloudflare-status-dot')).not.toHaveClass(
    /is-ready/,
  );
  await choose(
    panel(page).getByRole('button', { name: 'Copy Desktop URL', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText('Desktop URL copied.', { exact: true }),
  ).toBeVisible();
  await choose(diagnosticsSummary(page), touch);
  await expect(
    panel(page).getByText('127.0.0.1:3002', { exact: true }),
  ).toBeVisible();
  expect(detailReads).toBeGreaterThan(0);
  const disable = panel(page).getByRole('button', {
    name: 'Disable',
    exact: true,
  });
  await choose(disable, touch);
  const confirmation = page.getByRole('dialog', {
    name: 'Disable Cloudflare access?',
    exact: true,
  });
  await expect(confirmation).toContainText(/host shells will end/);
  await expect(confirmation).toContainText(/Codex views will detach/);
  await expect(confirmation).toContainText(/Codex runs and VMs will continue/);
  await expect(confirmation).toContainText(
    /Local and Tailscale connections stay open/,
  );
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
  await expect.poll(() => pendingWrites.length).toBe(2);
  await expect(
    panel(page).getByText('Disabling…', { exact: true }),
  ).toBeVisible();
  await expect(disable).toBeDisabled();
  Object.assign(state.cloudflare, {
    enabled: false,
    state: 'disabled',
    url: undefined,
  });
  await pendingWrites[1]!.fulfill({ json: state.cloudflare });
  await expect(stateLabel(page)).toHaveText('Off');
  expect(writes).toEqual([true, false]);
  await expect(
    panel(page).getByRole('link', { name: address, exact: true }),
  ).toHaveCount(0);
});

test('control errors remain visible and refresh never replays a Cloudflare mutation', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = snapshot();
  let fail = true;
  let detailReads = 0;
  const writes: boolean[] = [];
  await page.route('**/api/connection', (route) =>
    route.fulfill({ json: state }),
  );
  await page.route('**/api/cloudflare', (route) => {
    writes.push(route.request().postDataJSON().enabled);
    if (fail)
      return route.fulfill({
        status: 500,
        json: { error: 'Could not save Cloudflare access.' },
      });
    Object.assign(state.cloudflare, { enabled: true, state: 'starting' });
    return route.fulfill({ json: state.cloudflare });
  });
  await page.route('**/api/cloudflare/status', (route) => {
    detailReads++;
    return route.fulfill({
      json: {
        instanceId: 'error-fixture',
        ...state.cloudflare,
        listen: '127.0.0.1:3002',
        error: 'The configured origin address is already in use.',
      },
    });
  });
  await page.reload();
  await openCloudflare(page, touch);
  await choose(
    panel(page).getByRole('button', { name: 'Enable', exact: true }),
    touch,
  );
  await expect(panel(page).getByRole('alert')).toContainText(
    'Could not save Cloudflare access.',
  );
  await choose(
    panel(page).getByRole('button', { name: 'Refresh', exact: true }),
    touch,
  );
  expect(writes).toEqual([true]);
  Object.assign(state.cloudflare, {
    enabled: true,
    state: 'error',
    message: 'Check Cloudflare setup locally.',
  });
  await choose(
    panel(page).getByRole('button', { name: 'Refresh', exact: true }),
    touch,
  );
  await expect(stateLabel(page)).toHaveText('Needs attention');
  expect(detailReads).toBe(0);
  await choose(
    panel(page).getByRole('button', { name: 'View diagnostics', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText('The configured origin address is already in use.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(diagnosticsSummary(page).locator('..')).toHaveAttribute(
    'open',
    '',
  );
  expect(detailReads).toBeGreaterThan(0);
  expect(writes).toEqual([true]);
  fail = false;
  await choose(
    panel(page).getByRole('button', { name: 'Retry', exact: true }),
    touch,
  );
  await expect(stateLabel(page)).toHaveText('Starting');
  expect(writes).toEqual([true, true]);
});

test('remote Cloudflare viewers receive only the summary and address', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = snapshot(false);
  Object.assign(state.cloudflare, {
    enabled: true,
    state: 'listening',
    url: address,
  });
  const protectedRequests: string[] = [];
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname;
    if (
      [
        '/api/cloudflare',
        '/api/cloudflare/status',
        '/api/cloudflare/config',
        '/api/cloudflare/wizard',
        '/api/cloudflare/token',
      ].includes(path)
    )
      protectedRequests.push(path);
  });
  await page.route('**/api/connection', (route) =>
    route.fulfill({ json: state }),
  );
  await page.reload();
  await openCloudflare(page, touch);
  await expect(
    panel(page).getByRole('link', { name: address, exact: true }),
  ).toBeVisible();
  await expect(
    panel(page).getByText('Desktop URL', { exact: true }),
  ).toBeVisible();
  await expect(
    panel(page).getByText('Manage Cloudflare on the computer running AXV.', {
      exact: true,
    }),
  ).toBeVisible();
  for (const name of ['Enable', 'Disable', 'Retry'])
    await expect(
      panel(page).getByRole('button', { name, exact: true }),
    ).toHaveCount(0);
  await expect(diagnosticsSummary(page)).toHaveCount(0);
  await expect(
    panel(page).getByRole('button', { name: 'Open setup', exact: true }),
  ).toHaveCount(0);
  await expect(
    panel(page).getByRole('button', { name: 'View diagnostics', exact: true }),
  ).toHaveCount(0);
  await choose(
    panel(page)
      .locator('summary')
      .filter({ hasText: /^Help$/ }),
    touch,
  );
  await expect(
    panel(page).getByText(/your Access session expires/),
  ).toBeVisible();
  await expect(
    panel(page).getByText('Origin address', { exact: true }),
  ).toHaveCount(0);
  expect(protectedRequests).toEqual([]);
});

test('expired authorization preserves the desktop while a later status refresh recovers', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = snapshot(false);
  Object.assign(state.cloudflare, {
    enabled: true,
    state: 'listening',
    url: address,
  });
  let expired = false;
  const writes: string[] = [];
  await page.route('**/api/connection', (route) =>
    expired
      ? route.fulfill({
          status: 401,
          contentType: 'text/html',
          body: 'Cloudflare Access authorization required.',
        })
      : route.fulfill({ json: state }),
  );
  page.on('request', (request) => {
    if (
      request.method() === 'PUT' &&
      new URL(request.url()).pathname === '/api/cloudflare'
    )
      writes.push(request.method());
  });
  await page.reload();
  await openCloudflare(page, touch);
  await expect(stateLabel(page)).toHaveText('Listening locally');
  let navigations = 0;
  page.on('framenavigated', (frame) => {
    if (frame === page.mainFrame()) navigations++;
  });
  const refresh = panel(page).getByRole('button', {
    name: 'Refresh',
    exact: true,
  });
  expired = true;
  await choose(refresh, touch);
  await expect(stateLabel(page)).toHaveText('Unknown');
  await expect(panel(page).getByRole('alert')).toContainText(
    'Keep this desktop open to preserve drafts, sign in again in another tab, then retry.',
  );
  await expect(
    panel(page).getByRole('link', { name: address, exact: true }),
  ).toHaveCount(0);
  await expect(panel(page)).toHaveAttribute(
    'data-window-id',
    'cloudflare:main',
  );
  expect(navigations).toBe(0);
  expect(writes).toEqual([]);
  expired = false;
  await choose(refresh, touch);
  await expect(stateLabel(page)).toHaveText('Listening locally');
  await expect(
    panel(page).getByRole('link', { name: address, exact: true }),
  ).toBeVisible();
  await expect(panel(page).getByRole('alert')).toHaveCount(0);
  expect(navigations).toBe(0);
  expect(writes).toEqual([]);
});

test('failed status hides a stale address and inactive windows stop polling without disabling', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = snapshot();
  Object.assign(state.cloudflare, {
    enabled: true,
    state: 'listening',
    url: address,
  });
  let failed = false;
  let reads = 0;
  let detailReads = 0;
  const writes: string[] = [];
  await page.route('**/api/connection', (route) => {
    reads++;
    return failed
      ? route.fulfill({
          status: 503,
          json: { error: 'Connection status unavailable.' },
        })
      : route.fulfill({ json: state });
  });
  await page.route('**/api/cloudflare/status', (route) => {
    detailReads++;
    return route.fulfill({
      json: {
        instanceId: 'fixture',
        ...state.cloudflare,
        listen: '127.0.0.1:3002',
      },
    });
  });
  page.on('request', (request) => {
    if (
      request.method() === 'PUT' &&
      new URL(request.url()).pathname === '/api/cloudflare'
    )
      writes.push(request.method());
  });
  await page.reload();
  expect(reads).toBe(0);
  await openCloudflare(page, touch);
  await expect(stateLabel(page)).toHaveText('Listening locally');
  failed = true;
  await choose(
    panel(page).getByRole('button', { name: 'Refresh', exact: true }),
    touch,
  );
  await expect(stateLabel(page)).toHaveText('Unknown');
  await expect(
    panel(page).getByText(
      'Showing previous details. Current connection state is unknown.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    panel(page).getByRole('link', { name: address, exact: true }),
  ).toHaveCount(0);
  await expect(
    panel(page).getByRole('button', { name: 'Disable', exact: true }),
  ).toBeDisabled();
  failed = false;
  await choose(
    panel(page).getByRole('button', { name: 'Refresh', exact: true }),
    touch,
  );
  await expect(stateLabel(page)).toHaveText('Listening locally');
  await choose(diagnosticsSummary(page), touch);
  await expect(
    panel(page).getByText('127.0.0.1:3002', { exact: true }),
  ).toBeVisible();
  await choose(
    panel(page).getByRole('button', { name: /^Miniaturize / }),
    touch,
  );
  await expect(panel(page)).toBeHidden();
  const hidden = { reads, detailReads };
  await page.waitForTimeout(5500);
  expect({ reads, detailReads }).toEqual(hidden);
  expect(writes).toEqual([]);
  await openCloudflare(page, touch);
  await expect(panel(page)).toHaveAttribute(
    'data-window-id',
    'cloudflare:main',
  );
  await expect(stateLabel(page)).toHaveText('Listening locally');
  await choose(panel(page).getByRole('button', { name: /^Close / }), touch);
  expect(writes).toEqual([]);
});

test('Preferences opens the same Cloudflare utility and controls fit desktop and narrow screens', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = snapshot();
  Object.assign(state.cloudflare, {
    enabled: true,
    state: 'listening',
    url: address,
  });
  let reads = 0;
  await page.route('**/api/connection', (route) => {
    reads++;
    return route.fulfill({ json: state });
  });
  await page.reload();
  await expect(dockEntry(page, 'cloudflare')).toHaveCount(0);
  await choose(
    page.getByRole('button', { name: 'Workspace menu', exact: true }),
    touch,
  );
  await choose(menuItem(page, 'Preferences'), touch);
  const preferences = page.locator('[data-app-id="appearance"]');
  expect(reads).toBe(0);
  const remoteAccess = preferences.getByRole('group', {
    name: 'Remote access',
    exact: true,
  });
  const tailscaleButton = remoteAccess.getByRole('button', {
    name: 'Open Tailscale',
    exact: true,
  });
  const cloudflareButton = remoteAccess.getByRole('button', {
    name: 'Open Cloudflare',
    exact: true,
  });
  await expect(tailscaleButton).toBeVisible();
  await expect(cloudflareButton).toBeVisible();
  if (!touch) {
    const frame = await bounds(preferences);
    expect(frame.width).toBe(440);
    expect(frame.height).toBe(500);
    const visibility = await preferences
      .locator('.app-pad')
      .evaluate((content) => {
        const area = content.getBoundingClientRect();
        const buttons = Array.from(content.querySelectorAll('button'))
          .filter((button) =>
            ['Open Tailscale', 'Open Cloudflare'].includes(
              button.getAttribute('aria-label') || '',
            ),
          )
          .map((button) => {
            const rect = button.getBoundingClientRect();
            return { top: rect.top, bottom: rect.bottom };
          });
        return {
          scrollTop: content.scrollTop,
          top: area.top,
          bottom: area.bottom,
          buttons,
        };
      });
    expect(visibility.scrollTop).toBe(0);
    expect(visibility.buttons).toHaveLength(2);
    const group = await bounds(remoteAccess);
    expect(group.y).toBeGreaterThanOrEqual(visibility.top);
    expect(group.y + group.height).toBeLessThanOrEqual(visibility.bottom);
    for (const button of visibility.buttons) {
      expect(button.top).toBeGreaterThanOrEqual(visibility.top);
      expect(button.bottom).toBeLessThanOrEqual(visibility.bottom);
    }
  }
  await choose(tailscaleButton, touch);
  const tailscale = page.locator('[data-app-id="remote-access"]');
  await expect(tailscale).toHaveAttribute(
    'data-window-id',
    'remote-access:main',
  );
  await choose(tailscale.getByRole('button', { name: /^Close / }), touch);
  await choose(cloudflareButton, touch);
  await expect(panel(page)).toHaveAttribute(
    'data-window-id',
    'cloudflare:main',
  );
  await expect(stateLabel(page)).toHaveText('Listening locally');
  const captureDir = join(
    process.env.AXV_TEST_TMPDIR || '.data/test',
    'cloudflare-ui',
  );
  await mkdir(captureDir, { recursive: true, mode: 0o700 });
  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 390, height: 844 },
    { width: 320, height: 720 },
  ]) {
    await page.setViewportSize(viewport);
    const refresh = panel(page).getByRole('button', {
      name: 'Refresh',
      exact: true,
    });
    await expect(refresh).toBeInViewport();
    await expect(
      panel(page).getByRole('button', { name: /^Close / }),
    ).toBeInViewport();
    const frame = await bounds(panel(page));
    expect(frame.x).toBeGreaterThanOrEqual(0);
    expect(frame.x + frame.width).toBeLessThanOrEqual(viewport.width);
    await panel(page).screenshot({
      path: join(captureDir, `${info.project.name}-${viewport.width}.png`),
    });
    const heading = panel(page).getByRole('heading', {
      name: 'Desktop access',
      exact: true,
    });
    const before = await bounds(heading);
    await choose(refresh, touch);
    await expect(stateLabel(page)).toHaveText('Listening locally');
    const after = await bounds(heading);
    expect(Math.abs(after.x - before.x)).toBeLessThanOrEqual(1);
    expect(Math.abs(after.y - before.y)).toBeLessThanOrEqual(1);
  }
  await choose(
    panel(page).getByRole('button', { name: /^Miniaturize / }),
    touch,
  );
  await openCloudflare(page, touch);
  await expect(panel(page)).toHaveAttribute(
    'data-window-id',
    'cloudflare:main',
  );
  await expect(panel(page)).toHaveCount(1);
});

test('slow diagnostics finish across summary polls and collapse cancels pending details', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = snapshot();
  const pending: Route[] = [];
  let summaryReads = 0;
  await page.route('**/api/connection', (route) => {
    summaryReads++;
    return route.fulfill({ json: state });
  });
  await page.route('**/api/cloudflare/status', (route) => {
    pending.push(route);
  });
  await page.reload();
  await openCloudflare(page, touch);
  const details = diagnosticsSummary(page);
  await choose(details, touch);
  await expect.poll(() => pending.length).toBe(1);
  await page.waitForTimeout(6000);
  expect(summaryReads).toBeGreaterThanOrEqual(2);
  expect(pending).toHaveLength(1);
  const diagnostics = {
    instanceId: 'slow-diagnostics',
    ...state.cloudflare,
    listen: '127.0.0.1:3002',
  };
  await pending[0]!.fulfill({ json: diagnostics });
  await expect(
    panel(page).getByText('slow-diagnostics', { exact: true }),
  ).toBeVisible();
  const refresh = panel(page).getByRole('button', {
    name: 'Refresh',
    exact: true,
  });
  await expect(refresh).toBeEnabled();
  await choose(refresh, touch);
  await expect.poll(() => pending.length).toBe(2);
  await expect(
    panel(page).getByText('slow-diagnostics', { exact: true }),
  ).toBeVisible();
  await pending[1]!.fulfill({
    status: 503,
    json: { error: 'Could not refresh diagnostics.' },
  });
  await expect(panel(page).getByRole('alert')).toContainText(
    'Could not refresh diagnostics.',
  );
  await expect(
    panel(page).getByText(/Showing previous diagnostics/),
  ).toBeVisible();
  await expect(refresh).toBeEnabled();
  await choose(refresh, touch);
  await expect.poll(() => pending.length).toBe(3);
  const cancelled = page.waitForEvent('requestfailed', {
    predicate: (request) =>
      new URL(request.url()).pathname === '/api/cloudflare/status',
  });
  await choose(details, touch);
  await cancelled;
  await expect(
    panel(page).getByText('Origin address', { exact: true }),
  ).toBeHidden();
  await choose(details, touch);
  await expect.poll(() => pending.length).toBe(4);
  await pending[2]!.fulfill({
    json: { ...diagnostics, instanceId: 'obsolete-diagnostics' },
  });
  await pending[3]!.fulfill({
    json: { ...diagnostics, instanceId: 'current-diagnostics' },
  });
  await expect(
    panel(page).getByText('current-diagnostics', { exact: true }),
  ).toBeVisible();
  await expect(
    panel(page).getByText('obsolete-diagnostics', { exact: true }),
  ).toHaveCount(0);
  await expect(panel(page).getByRole('alert')).toHaveCount(0);
});

test('Access validation and revision conflicts preserve drafts until explicit reload', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const config = configuration();
  let savedOrigin = 'https://changed-elsewhere.example.com';
  const writes: unknown[] = [];
  let configReads = 0;
  await page.route('**/api/connection', (route) =>
    route.fulfill({ json: snapshot() }),
  );
  await page.route('**/api/cloudflare/config', (route) => {
    if (route.request().method() === 'PUT') {
      writes.push(route.request().postDataJSON());
      config.revision = 'revision-2';
      config.access.origin = savedOrigin;
      return route.fulfill({
        status: 409,
        json: {
          error: 'Configuration changed. Reload saved settings to continue.',
          code: 'configuration_conflict',
        },
      });
    }
    configReads++;
    return route.fulfill({ json: config });
  });
  await page.route('**/api/cloudflare/wizard', (route) => {
    expect(Object.keys(route.request().postDataJSON()).sort()).toEqual([
      'access',
      'accountId',
      'domain',
      'step',
      'tunnelId',
      'zoneId',
    ]);
    return route.fulfill({
      json: { ok: true, message: 'Desktop access settings are valid.' },
    });
  });
  await page.reload();
  await openCloudflare(page, touch);
  await choose(
    panel(page).getByRole('tab', { name: 'Setup', exact: true }),
    touch,
  );
  await choose(
    panel(page).getByRole('button', { name: 'Desktop access', exact: true }),
    touch,
  );
  const origin = panel(page).getByLabel('Desktop origin', { exact: true });
  await origin.fill('https://draft.example.com');
  await choose(
    panel(page).getByRole('button', { name: 'Validate access', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText('Desktop access settings are valid.', {
      exact: true,
    }),
  ).toBeVisible();
  const save = panel(page).getByRole('button', {
    name: 'Save settings',
    exact: true,
  });
  await cancelSetupReview(page, save, 'Save Cloudflare settings?', touch);
  await expect(origin).toHaveValue('https://draft.example.com');
  expect(writes).toEqual([]);
  await choose(save, touch);
  expect(writes).toEqual([]);
  await choose(
    page
      .getByRole('dialog')
      .getByRole('button', { name: 'Save settings', exact: true }),
    touch,
  );
  await expect(panel(page).getByRole('alert')).toContainText(
    'Configuration changed.',
  );
  await expect(origin).toHaveValue('https://draft.example.com');
  expect(writes).toEqual([
    expect.objectContaining({
      instanceId: 'cf-instance',
      expectedRevision: 'revision-1',
      access: expect.objectContaining({ origin: 'https://draft.example.com' }),
      tokenAction: 'keep',
    }),
  ]);
  await choose(
    panel(page).getByRole('tab', { name: 'Overview', exact: true }),
    touch,
  );
  await choose(
    panel(page).getByRole('tab', { name: 'Setup', exact: true }),
    touch,
  );
  await expect(origin).toHaveValue('https://draft.example.com');
  await expect(
    panel(page).getByText(/Saved settings changed elsewhere/),
  ).toBeVisible();
  const reload = panel(page).getByRole('button', {
    name: 'Reload saved settings',
    exact: true,
  });
  const readsBeforeCancel = configReads;
  await cancelSetupReview(page, reload, 'Replace unsaved settings?', touch);
  await expect(origin).toHaveValue('https://draft.example.com');
  // Each dismissed modal resumes one background observation, without reloading the draft.
  await expect.poll(() => configReads).toBe(readsBeforeCancel + 2);
  expect(writes).toHaveLength(1);
  savedOrigin = 'https://latest-saved.example.com';
  config.access.origin = savedOrigin;
  await choose(
    panel(page).getByRole('button', {
      name: 'Reload saved settings',
      exact: true,
    }),
    touch,
  );
  await choose(
    page.getByRole('button', { name: 'Reload settings', exact: true }),
    touch,
  );
  await expect(origin).toHaveValue(savedOrigin);
  await expect(
    panel(page).getByText('Settings saved', { exact: true }),
  ).toBeVisible();
});

test('the account wizard validates existing resources and saves a token only on explicit review', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const config = configuration();
  const steps: string[] = [];
  const writes: Record<string, unknown>[] = [];
  const token = 'fixture-scoped-api-token';
  const leaked: string[] = [];
  page.on('request', (request) => {
    if (
      (request.postData() || '').includes(token) &&
      !['/api/cloudflare/wizard', '/api/cloudflare/config'].includes(
        new URL(request.url()).pathname,
      )
    )
      leaked.push(request.url());
  });
  await page.route('**/api/connection', (route) =>
    route.fulfill({ json: snapshot() }),
  );
  await page.route('**/api/cloudflare/config', (route) => {
    if (route.request().method() === 'PUT') {
      const value = route.request().postDataJSON();
      writes.push(value);
      Object.assign(config, {
        revision: 'revision-2',
        access: { ...value.access, enabled: false },
        account: { ...value.account, configured: true, hasToken: true },
      });
    }
    return route.fulfill({ json: config });
  });
  await page.route('**/api/cloudflare/wizard', (route) => {
    const value = route.request().postDataJSON();
    steps.push(value.step);
    expect(value.token).toBe(token);
    expect(value).not.toHaveProperty('listen');
    expect(value).not.toHaveProperty('audience');
    return route.fulfill({
      json: {
        ok: true,
        message: `${value.step} validated.`,
        accounts: [{ id: 'account-id', name: 'Personal account' }],
        zones: [{ id: 'zone-id', name: 'example.net' }],
        tunnels: [
          {
            id: 'tunnel-id',
            name: 'Workstation',
            status: 'healthy',
            remoteConfig: true,
          },
        ],
        warnings:
          value.step === 'tunnel' ? ['Run cloudflared separately.'] : [],
      },
    });
  });
  await page.reload();
  await openCloudflare(page, touch);
  await choose(
    panel(page).getByRole('tab', { name: 'Setup', exact: true }),
    touch,
  );
  await choose(
    panel(page).getByRole('button', { name: 'Connect account', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText('Not saved', { exact: true }),
  ).toBeHidden();
  await panel(page)
    .getByRole('combobox', { name: 'API token action', exact: true })
    .selectOption('replace');
  const tokenField = panel(page).getByLabel('Cloudflare API token', {
    exact: true,
  });
  await tokenField.fill(token);
  await expect(tokenField).toHaveAttribute('type', 'password');
  const next = panel(page).getByRole('button', {
    name: 'Validate and continue',
    exact: true,
  });
  await choose(next, touch);
  await panel(page)
    .getByRole('combobox', { name: 'Choose account', exact: true })
    .selectOption('account-id');
  await choose(next, touch);
  await panel(page)
    .getByRole('combobox', { name: 'Choose zone', exact: true })
    .selectOption('zone-id');
  await choose(next, touch);
  await panel(page)
    .getByRole('combobox', { name: 'Choose tunnel', exact: true })
    .selectOption('tunnel-id');
  await choose(next, touch);
  await panel(page)
    .getByLabel('Base domain', { exact: true })
    .fill('example.net');
  await choose(next, touch);
  await expect(
    panel(page).getByText('Step 6 of 6: Review', { exact: true }),
  ).toBeVisible();
  await expect(
    panel(page).getByRole('button', { name: 'Save settings', exact: true }),
  ).toHaveCount(1);
  await panel(page)
    .getByLabel('Apps application AUD (owner access)', { exact: true })
    .fill('apps-aud');
  expect(steps).toEqual(['token', 'account', 'zone', 'tunnel', 'domain']);
  expect(writes).toEqual([]);
  await choose(
    panel(page).getByRole('button', { name: 'Save settings', exact: true }),
    touch,
  );
  await expect(page.getByRole('dialog')).not.toContainText(token);
  await choose(
    page
      .getByRole('dialog')
      .getByRole('button', { name: 'Save settings', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText(/Settings saved. Enable desktop access/),
  ).toBeVisible();
  expect(writes).toEqual([
    expect.objectContaining({
      expectedRevision: 'revision-1',
      tokenAction: 'replace',
      token,
      account: expect.objectContaining({
        accountId: 'account-id',
        zoneId: 'zone-id',
        tunnelId: 'tunnel-id',
        domain: 'example.net',
        audience: 'apps-aud',
      }),
    }),
  ]);
  await expect(tokenField).toHaveCount(0);
  expect(
    await page.evaluate(() => JSON.stringify({ ...localStorage })),
  ).not.toContain(token);
  expect(leaked).toEqual([]);
});

test('narrow setup keeps the current step and original draft reachable across rotation', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  await page.route('**/api/connection', (route) =>
    route.fulfill({ json: snapshot() }),
  );
  await page.reload();
  await openCloudflare(page, touch);
  await choose(
    panel(page).getByRole('tab', { name: 'Setup', exact: true }),
    touch,
  );
  await choose(
    panel(page).getByRole('button', { name: 'Connect account', exact: true }),
    touch,
  );
  const token = panel(page).getByLabel('Cloudflare API token', { exact: true });
  await token.fill('retained-draft-token');
  const content = panel(page).locator('.cloudflare-content:visible');
  const next = panel(page).getByRole('button', {
    name: 'Validate and continue',
    exact: true,
  });
  for (const viewport of [
    { width: 390, height: 844 },
    { width: 320, height: 568 },
    { width: 568, height: 320 },
    { width: 844, height: 390 },
  ]) {
    await page.setViewportSize(viewport);
    await content.evaluate((element) => {
      element.scrollTop = 0;
    });
    await expect(
      panel(page).getByText('Step 1 of 6: Token', { exact: true }),
    ).toBeInViewport();
    const geometry = await content.evaluate((element) => ({
      width: element.clientWidth,
      scrollWidth: element.scrollWidth,
      height: element.clientHeight,
    }));
    expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.width + 1);
    if (viewport.width === 568) expect(geometry.height).toBeGreaterThan(150);
    await next.scrollIntoViewIfNeeded();
    await expect(next).toBeInViewport();
    const button = await bounds(next);
    const area = await bounds(content);
    expect(button.y).toBeGreaterThanOrEqual(area.y - 1);
    expect(button.y + button.height).toBeLessThanOrEqual(
      area.y + area.height + 1,
    );
    await expect(token).toHaveValue('retained-draft-token');
    await expect(panel(page)).toHaveAttribute(
      'data-window-id',
      'cloudflare:main',
    );
  }
  await choose(
    panel(page).getByRole('button', { name: 'Close wizard', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText('Not saved', { exact: true }),
  ).toBeVisible();
  await choose(
    panel(page).getByRole('button', { name: 'Connect account', exact: true }),
    touch,
  );
  await expect(token).toHaveValue('retained-draft-token');
});

test('token rotation keeps enabled access and unrelated unsaved settings', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const state = snapshot();
  state.cloudflare.enabled = true;
  state.cloudflare.state = 'listening';
  state.cloudflare.url = address;
  const config = configuration();
  config.access.enabled = true;
  config.account.hasToken = true;
  const writes: unknown[] = [];
  const events: string[] = [];
  await page.route('**/api/connection', (route) =>
    route.fulfill({ json: state }),
  );
  await page.route('**/api/cloudflare/config', (route) =>
    route.fulfill({ json: config }),
  );
  await page.route('**/api/cloudflare/wizard', (route) => {
    events.push('validate');
    expect(route.request().postDataJSON()).toEqual({
      step: 'token',
      token: 'replacement-fixture-token',
    });
    return route.fulfill({ json: { ok: true, message: 'Token valid.' } });
  });
  await page.route('**/api/cloudflare/token', (route) => {
    events.push('rotate');
    writes.push(route.request().postDataJSON());
    config.revision = 'rotated-revision';
    return route.fulfill({ json: config });
  });
  await page.reload();
  await openCloudflare(page, touch);
  await choose(
    panel(page).getByRole('tab', { name: 'Setup', exact: true }),
    touch,
  );
  await choose(
    panel(page).getByRole('button', { name: 'Desktop access', exact: true }),
    touch,
  );
  const origin = panel(page).getByLabel('Desktop origin', { exact: true });
  await origin.fill('https://unsaved.example.com');
  await expect(
    panel(page).getByRole('button', { name: 'Save settings', exact: true }),
  ).toBeDisabled();
  await choose(
    panel(page).getByRole('button', { name: 'App publishing', exact: true }),
    touch,
  );
  await choose(
    panel(page).getByRole('button', { name: 'Rotate token', exact: true }),
    touch,
  );
  await panel(page)
    .getByLabel('Replacement API token', { exact: true })
    .fill('replacement-fixture-token');
  const rotate = panel(page).getByRole('button', {
    name: 'Validate and rotate token',
    exact: true,
  });
  await cancelSetupReview(page, rotate, 'Rotate Cloudflare API token?', touch);
  await expect(
    panel(page).getByLabel('Replacement API token', { exact: true }),
  ).toHaveValue('replacement-fixture-token');
  expect(events).toEqual([]);
  expect(writes).toEqual([]);
  await choose(rotate, touch);
  expect(events).toEqual([]);
  expect(writes).toEqual([]);
  await choose(
    page
      .getByRole('dialog')
      .getByRole('button', { name: 'Rotate token', exact: true }),
    touch,
  );
  await expect.poll(() => writes.length).toBe(1);
  expect(events).toEqual(['validate', 'rotate']);
  await choose(
    panel(page).getByRole('button', { name: 'Desktop access', exact: true }),
    touch,
  );
  await expect(origin).toHaveValue('https://unsaved.example.com');
  await expect(
    panel(page).getByText('Unsaved settings', { exact: true }),
  ).toBeVisible();
  expect(writes).toEqual([
    {
      instanceId: 'cf-instance',
      expectedRevision: 'revision-1',
      token: 'replacement-fixture-token',
    },
  ]);
  await expect(
    panel(page).getByLabel('Replacement API token', { exact: true }),
  ).toHaveCount(0);
});

test('remote owners can inspect safe features without privileged configuration requests', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const protectedRequests: string[] = [];
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname;
    if (
      [
        '/api/cloudflare/config',
        '/api/cloudflare/wizard',
        '/api/cloudflare/token',
        '/api/cloudflare/status',
      ].includes(path)
    )
      protectedRequests.push(path);
  });
  await page.route('**/api/connection', (route) =>
    route.fulfill({ json: snapshot(false) }),
  );
  await page.route('**/api/cloudflare/capabilities', (route) =>
    route.fulfill({
      json: { configure: false, publish: true, health: true, traffic: true },
    }),
  );
  await page.route('**/api/cloudflare/traffic*', (route) =>
    route.fulfill({
      json: {
        supported: false,
        reason: 'Analytics are not available on this plan.',
        since: '2026-10-23T00:00:00Z',
        until: '2026-10-24T00:00:00Z',
        requests: 0,
        bytes: 0,
        cachedRequests: 0,
        series: [],
      },
    }),
  );
  await page.reload();
  await openCloudflare(page, touch);
  await choose(
    panel(page).getByRole('tab', { name: 'Setup', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText(/Configure Cloudflare on the computer running AXV/),
  ).toBeVisible();
  await expect(
    panel(page).getByLabel('Desktop origin', { exact: true }),
  ).toHaveCount(0);
  await choose(
    panel(page).getByRole('tab', { name: 'Traffic', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText('Analytics are not available on this plan.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(panel(page).getByRole('table')).toHaveCount(0);
  await panel(page)
    .getByRole('combobox', { name: 'Time range', exact: true })
    .selectOption('720');
  await expect(
    panel(page).getByText('Analytics are not available on this plan.', {
      exact: true,
    }),
  ).toBeVisible();
  expect(protectedRequests).toEqual([]);
});

test('publishing reviews public access and retains the original target across uncertain receipts and revocation', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const binding = {
    hostId: 'publication-host',
    projectId: 'publication-project',
    projectRevision: 1,
    volumeId: 'publication-volume',
    volumeRevision: 2,
    machineId: 'publication-vm',
    bootId: 'original-boot',
    instanceId: 'cf-instance',
  };
  const project = {
    id: binding.projectId,
    hostId: binding.hostId,
    kind: 'vm',
    name: 'Publication project',
    volumeId: binding.volumeId,
    root: 'repo',
    rootIdentity: 'root-1',
    revision: 1,
    createdAt: '2026-10-24T00:00:00Z',
  };
  let bindingReads = 0;
  let mismatch = true;
  let reject = true;
  let listDomain = 'example.org';
  const generation = '0123456789abcdef0123456789abcdef';
  let publication: Record<string, unknown> | undefined;
  const submissions: Record<string, unknown>[] = [];
  const actions: string[] = [];
  await page.route('**/api/connection', (route) =>
    route.fulfill({ json: snapshot() }),
  );
  await page.route('**/api/compute/environment', (route) =>
    route.fulfill({
      json: {
        runtime: { state: 'ready', hostId: binding.hostId, generation: 4 },
      },
    }),
  );
  await page.route('**/api/compute/projects', (route) => {
    expect(route.request().headers()['x-axv-host-id']).toBe(binding.hostId);
    expect(route.request().headers()['x-axv-compute-generation']).toBe('4');
    return route.fulfill({ json: { projects: [project] } });
  });
  await page.route('**/api/compute/projects/*/binding', (route) => {
    bindingReads++;
    return route.fulfill({ json: { project, binding } });
  });
  await page.route('**/api/cloudflare-publications', (route) => {
    if (route.request().method() === 'POST') {
      const value = route.request().postDataJSON();
      submissions.push(value);
      expect(route.request().headers()['x-axv-host-id']).toBe(binding.hostId);
      if (reject)
        return route.fulfill({
          status: 400,
          json: {
            code: 'invalid_publication_request',
            error: 'Publication settings were rejected.',
          },
        });
      publication = {
        ...value,
        id: 'publication-id',
        domain: listDomain,
        generation,
        hostname: `${value.name}-${generation}.${listDomain}`,
        state: 'provisioning',
        phase: 'dns',
        cleanupPending: false,
        createdAt: '2026-10-24T00:00:00Z',
        updatedAt: '2026-10-24T00:00:00Z',
      };
      return route.fulfill({
        status: 202,
        json: mismatch
          ? {
              ...publication,
              binding: { ...binding, bootId: 'incorrect-boot' },
            }
          : publication,
      });
    }
    return route.fulfill({
      json: {
        instanceId: 'cf-instance',
        supported: !publication,
        reason: publication
          ? 'This registered domain has already been reserved.'
          : undefined,
        domain: listDomain,
        isolation: {
          mode: 'dedicated-site',
          domain: listDomain,
          maxOwnerSessionSeconds: 900,
          uniqueHostnames: true,
          retiredSitesReusable: false,
        },
        publications: publication ? [publication] : [],
      },
    });
  });
  await page.route('**/api/cloudflare-publications/*/*', (route) => {
    const action = new URL(route.request().url()).pathname.split('/').at(-1)!;
    actions.push(action);
    expect(route.request().headers()['x-axv-host-id']).toBe(binding.hostId);
    publication = {
      ...publication,
      state: action === 'unpublish' ? 'removing' : 'provisioning',
      cleanupPending: action === 'unpublish',
      url: undefined,
    };
    return route.fulfill({ status: 202, json: publication });
  });
  await page.reload();
  await openCloudflare(page, touch);
  await choose(
    panel(page).getByRole('tab', { name: 'Publishing', exact: true }),
    touch,
  );
  await choose(
    panel(page).getByRole('button', { name: 'New publication', exact: true }),
    touch,
  );
  await panel(page)
    .getByRole('combobox', { name: 'VM project', exact: true })
    .selectOption(project.id);
  await panel(page).getByLabel('Service port', { exact: true }).fill('3000');
  await choose(
    panel(page).getByRole('button', {
      name: 'Use project target',
      exact: true,
    }),
    touch,
  );
  await panel(page).getByLabel('Name', { exact: true }).fill('Public');
  const access = panel(page).getByRole('combobox', {
    name: 'Access',
    exact: true,
  });
  await expect(access).toHaveValue('owner');
  await access.selectOption('public');
  await choose(
    panel(page).getByRole('button', {
      name: 'Review publication',
      exact: true,
    }),
    touch,
  );
  await expect(
    panel(page).getByText(/Anyone on the internet can reach this service/),
  ).toBeVisible();
  const review = panel(page).getByRole('group', {
    name: 'Review publication',
    exact: true,
  });
  const details = review.locator('details');
  await expect(details.getByText(binding.bootId, { exact: true })).toBeHidden();
  await choose(details.locator('summary'), touch);
  await expect(
    details.getByText(binding.bootId, { exact: true }),
  ).toBeVisible();
  await choose(details.locator('summary'), touch);
  await expect(review).toContainText(
    'A unique URL is assigned when you publish.',
  );
  await expect(review).toContainText('example.org');
  listDomain = 'example.net';
  await choose(
    panel(page).getByRole('button', {
      name: 'Refresh publications',
      exact: true,
    }),
    touch,
  );
  await expect(review.getByText(/Publishing settings changed/)).toBeVisible();
  await expect(
    review.getByRole('button', { name: 'Publish', exact: true }),
  ).toBeDisabled();
  await choose(
    review.getByRole('button', { name: 'Back', exact: true }),
    touch,
  );
  await expect(panel(page).getByLabel('Name', { exact: true })).toHaveValue(
    'Public',
  );
  await choose(
    panel(page).getByRole('button', {
      name: 'Review publication',
      exact: true,
    }),
    touch,
  );
  await expect(review).toContainText('example.net');
  expect(submissions).toEqual([]);
  await choose(
    panel(page).getByRole('button', { name: 'Publish', exact: true }),
    touch,
  );
  await expect(panel(page).getByRole('alert')).toContainText(
    'Publication settings were rejected.',
  );
  await expect(panel(page).getByText(/Publication is unconfirmed/)).toHaveCount(
    0,
  );
  const original = submissions[0]!;
  expect(original).not.toHaveProperty('hostname');
  reject = false;
  await choose(
    panel(page).getByRole('button', { name: 'Publish', exact: true }),
    touch,
  );
  await expect(panel(page).getByRole('alert')).toContainText(
    'different publication receipt',
  );
  await expect(panel(page).getByText(/Request ID:/)).toContainText(
    String(original.requestId),
  );
  reject = true;
  await choose(
    panel(page).getByRole('button', { name: 'Publish', exact: true }),
    touch,
  );
  await expect(panel(page).getByRole('alert')).toContainText(
    'Publication settings were rejected.',
  );
  await expect(
    panel(page).getByText(/Publication is unconfirmed/),
  ).toContainText(String(original.requestId));
  reject = false;
  mismatch = false;
  await choose(
    panel(page).getByRole('button', { name: 'Publish', exact: true }),
    touch,
  );
  await expect(panel(page).getByText(/Publication accepted/)).toBeVisible();
  expect(submissions).toEqual([original, original, original, original]);
  expect(original).toEqual(
    expect.objectContaining({
      binding,
      port: 3000,
      name: 'public',
      domain: 'example.net',
      visibility: 'public',
    }),
  );
  expect(bindingReads).toBe(1);
  await expect(
    panel(page).getByRole('link', {
      name: `Open public-${generation}.example.net`,
      exact: true,
    }),
  ).toHaveCount(0);
  publication = {
    ...publication,
    state: 'published',
    phase: 'complete',
    url: `https://public-${generation}.example.net`,
  };
  await choose(
    panel(page).getByRole('button', {
      name: 'Refresh publications',
      exact: true,
    }),
    touch,
  );
  await expect(
    panel(page).getByRole('link', {
      name: `Open public-${generation}.example.net`,
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    panel(page).getByRole('button', { name: 'New publication', exact: true }),
  ).toBeDisabled();
  publication = {
    ...publication,
    state: 'unavailable',
    phase: 'restart-required',
    url: undefined,
  };
  await choose(
    panel(page).getByRole('button', {
      name: 'Refresh publications',
      exact: true,
    }),
    touch,
  );
  const resume = panel(page).getByRole('button', {
    name: 'Resume',
    exact: true,
  });
  await choose(resume, touch);
  const resumeDialog = page.getByRole('dialog');
  await expect(resumeDialog).toContainText(`public-${generation}.example.net`);
  await expect(resumeDialog).toContainText(binding.bootId);
  await expect(resumeDialog).toContainText('Anyone on the internet');
  expect(actions).toEqual([]);
  await choose(
    resumeDialog.getByRole('button', { name: 'Cancel', exact: true }),
    touch,
  );
  expect(actions).toEqual([]);
  await choose(resume, touch);
  await choose(
    page
      .getByRole('dialog')
      .getByRole('button', { name: 'Resume', exact: true }),
    touch,
  );
  await expect.poll(() => actions).toEqual(['retry']);
  publication = {
    ...publication,
    state: 'published',
    phase: 'complete',
    url: `https://public-${generation}.example.net`,
  };
  await choose(
    panel(page).getByRole('button', {
      name: 'Refresh publications',
      exact: true,
    }),
    touch,
  );
  await expect(
    panel(page).getByRole('link', {
      name: `Open public-${generation}.example.net`,
      exact: true,
    }),
  ).toBeVisible();
  await choose(
    panel(page).getByRole('button', { name: 'Unpublish', exact: true }),
    touch,
  );
  await expect(page.getByRole('dialog')).toContainText(
    'The VM and its service keep running.',
  );
  expect(actions).toEqual(['retry']);
  await choose(
    page
      .getByRole('dialog')
      .getByRole('button', { name: 'Unpublish', exact: true }),
    touch,
  );
  await expect(
    panel(page).getByText('Removing…', { exact: true }),
  ).toBeVisible();
  await expect(
    panel(page).getByRole('link', {
      name: `Open public-${generation}.example.net`,
      exact: true,
    }),
  ).toHaveCount(0);
  expect(actions).toEqual(['retry', 'unpublish']);
});

for (const visibility of ['owner', 'public'] as const) {
  test(`stale ${visibility} Retry reviews the original publication after another client succeeds and the server restarts`, async ({
    page,
    context,
  }, info) => {
    const touch = info.project.use.hasTouch === true;
    const original = retainedPublication(visibility);
    let publication = { ...original };
    let holdPrimaryReads = false;
    let failPrimaryReads = false;
    const heldReads: Route[] = [];
    const targetReads: string[] = [];
    const actions: {
      client: string;
      path: string;
      host?: string;
      body: unknown;
    }[] = [];
    const other = await context.newPage();
    for (const [client, name] of [
      [page, 'original'],
      [other, 'other'],
    ] as const) {
      await client.route('**/api/connection', (route) =>
        route.fulfill({ json: snapshot() }),
      );
      await client.route('**/api/cloudflare/capabilities', (route) =>
        route.fulfill({
          json: { configure: true, publish: true, health: true, traffic: true },
        }),
      );
      await client.route('**/api/cloudflare/health*', (route) =>
        route.fulfill({
          json: { state: 'unconfigured', message: 'Fixture account.' },
        }),
      );
      await client.route('**/api/compute/projects**', (route) => {
        targetReads.push(route.request().url());
        return route.fulfill({
          status: 409,
          json: { error: 'Do not select a newer target.' },
        });
      });
      await client.route('**/api/compute/environment', (route) =>
        route.fulfill({
          json: {
            runtime: { state: 'ready', hostId: 'newer-host', generation: 99 },
          },
        }),
      );
      await client.route('**/api/cloudflare-publications', (route) => {
        expect(route.request().method()).toBe('GET');
        if (name === 'original' && failPrimaryReads)
          return route.fulfill({
            status: 503,
            json: { error: 'Publication reads unavailable.' },
          });
        if (name === 'original' && holdPrimaryReads) {
          heldReads.push(route);
          return;
        }
        return route.fulfill({
          json: {
            instanceId: 'cf-instance',
            domain: original.domain,
            supported: false,
            reason: 'This registered domain has already been reserved.',
            publications: [publication],
          },
        });
      });
      await client.route('**/api/cloudflare-publications/*/*', (route) => {
        actions.push({
          client: name,
          path: new URL(route.request().url()).pathname,
          host: route.request().headers()['x-axv-host-id'],
          body: route.request().postDataJSON(),
        });
        publication = {
          ...publication,
          state: 'published',
          phase: 'complete',
          error: undefined,
          url: `https://${original.hostname}`,
        };
        return route.fulfill({ status: 202, json: publication });
      });
    }
    await page.reload();
    await openCloudflare(page, touch);
    await choose(
      panel(page).getByRole('tab', { name: 'Publishing', exact: true }),
      touch,
    );
    const retry = panel(page).getByRole('button', {
      name: 'Retry',
      exact: true,
    });
    await expect(retry).toBeEnabled();
    await expect(panel(page)).toContainText(original.error!);
    await choose(
      panel(page).getByRole('tab', { name: 'Overview', exact: true }),
      touch,
    );

    // The first client retains its error while a second client publishes successfully.
    await other.goto(page.url());
    const otherReceipt = await other.evaluate(
      async ({ id, hostId }) => {
        const response = await fetch(
          `/api/cloudflare-publications/${id}/retry`,
          {
            method: 'POST',
            headers: {
              'Content-Type': 'application/json',
              'X-AXV-Host-ID': hostId,
            },
            body: '{}',
          },
        );
        if (response.status !== 202)
          throw new Error('The other client could not retry.');
        return response.json();
      },
      { id: original.id, hostId: original.binding.hostId },
    );
    expect(otherReceipt).toMatchObject({
      id: original.id,
      state: 'published',
      hostname: original.hostname,
    });
    await other.close();
    expect(actions.map((action) => action.client)).toEqual(['other']);

    // Restart closes the server route; the old client resumes with its next GET held.
    publication = {
      ...publication,
      state: 'unavailable',
      phase: 'restart-required',
      url: undefined,
    };
    holdPrimaryReads = true;
    await choose(
      panel(page).getByRole('tab', { name: 'Publishing', exact: true }),
      touch,
    );
    await expect.poll(() => heldReads.length).toBeGreaterThan(0);
    await expect(retry).toBeEnabled();
    await expect(panel(page)).toContainText(original.error!);
    await choose(retry, touch);
    const review = page.getByRole('dialog', {
      name: 'Resume publication?',
      exact: true,
    });
    for (const value of [
      original.hostname,
      original.binding.hostId,
      original.binding.projectId,
      original.binding.machineId,
      original.binding.bootId,
      String(original.port),
    ])
      await expect(review).toContainText(value);
    await expect(review).toContainText(
      visibility === 'public'
        ? 'Anyone on the internet can reach this service'
        : 'Owner only — Cloudflare Access required',
    );
    await expect(review).not.toContainText('newer-host');
    const cancel = review.getByRole('button', { name: 'Cancel', exact: true });
    await expect(cancel).toHaveAttribute('data-default', 'true');
    await expect(cancel).toBeFocused();
    await info.attach('stale-publication-resume-review', {
      body: await review.screenshot(),
      contentType: 'image/png',
    });
    expect(actions.map((action) => action.client)).toEqual(['other']);
    failPrimaryReads = true;
    holdPrimaryReads = false;
    await page.keyboard.press('Enter');
    for (const route of heldReads)
      await route.fulfill({
        status: 503,
        json: { error: 'Publication reads unavailable.' },
      });
    await expect(review).toBeHidden();
    await expect(retry).toBeFocused();
    await expect(panel(page)).toContainText(
      'Showing previous publications. Status is unknown; links are hidden.',
    );
    expect(actions.map((action) => action.client)).toEqual(['other']);

    // Failed refreshes still require review, and explicit cancellation sends no mutation.
    await choose(retry, touch);
    await choose(cancel, touch);
    await expect(review).toBeHidden();
    expect(actions.map((action) => action.client)).toEqual(['other']);
    await choose(retry, touch);
    await choose(
      review.getByRole('button', { name: 'Resume', exact: true }),
      touch,
    );
    await expect
      .poll(() => actions)
      .toEqual([
        {
          client: 'other',
          path: `/api/cloudflare-publications/${original.id}/retry`,
          host: original.binding.hostId,
          body: {},
        },
        {
          client: 'original',
          path: `/api/cloudflare-publications/${original.id}/retry`,
          host: original.binding.hostId,
          body: {},
        },
      ]);
    expect(targetReads).toEqual([]);
    expect(publication).toMatchObject({
      id: original.id,
      requestId: original.requestId,
      generation: original.generation,
      hostname: original.hostname,
      binding: original.binding,
      port: original.port,
      visibility,
    });
  });
}

test('cleanup Retry names removal and keeps access closed without a Resume review', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const original = {
    ...retainedPublication('public'),
    phase: 'remove-dns',
    cleanupPending: true,
  };
  let publication = { ...original };
  const actions: string[] = [];
  await page.route('**/api/connection', (route) =>
    route.fulfill({ json: snapshot() }),
  );
  await page.route('**/api/cloudflare-publications', (route) =>
    route.fulfill({
      json: {
        instanceId: 'cf-instance',
        domain: original.domain,
        supported: false,
        reason: 'This registered domain has already been reserved.',
        publications: [publication],
      },
    }),
  );
  await page.route('**/api/cloudflare-publications/*/retry', (route) => {
    actions.push(new URL(route.request().url()).pathname);
    expect(route.request().headers()['x-axv-host-id']).toBe(
      original.binding.hostId,
    );
    expect(route.request().postDataJSON()).toEqual({});
    // Completed removal is permanent; retrying a stale cleanup receipt never restores it.
    publication = {
      ...publication,
      state: 'removed',
      phase: 'complete',
      cleanupPending: false,
    };
    return route.fulfill({ status: 202, json: publication });
  });
  await page.reload();
  await openCloudflare(page, touch);
  await choose(
    panel(page).getByRole('tab', { name: 'Publishing', exact: true }),
    touch,
  );
  const row = panel(page)
    .locator('article')
    .filter({ hasText: original.hostname });
  await expect(row).toContainText(
    'Retry cleanup removes only this publication’s cloud resources; AXV access stays closed.',
  );
  await expect(
    row.getByRole('button', { name: 'Resume', exact: true }),
  ).toHaveCount(0);
  await choose(
    row.getByRole('button', { name: 'Retry cleanup', exact: true }),
    touch,
  );
  await expect
    .poll(() => actions)
    .toEqual([`/api/cloudflare-publications/${original.id}/retry`]);
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(row).toContainText('Removed');
  await expect(row.getByRole('link')).toHaveCount(0);
});

test('project preview publishing opens the singleton and never replaces a dirty target draft silently', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const binding = {
    hostId: 'context-host',
    projectId: 'context-project',
    projectRevision: 1,
    volumeId: 'context-volume',
    volumeRevision: 2,
    machineId: 'context-vm',
    bootId: 'captured-boot',
    instanceId: 'cf-instance',
  };
  const project = {
    id: binding.projectId,
    hostId: binding.hostId,
    kind: 'vm',
    name: 'Context project',
    volumeId: binding.volumeId,
    root: 'repo',
    rootIdentity: 'root-1',
    revision: 1,
    createdAt: '2026-10-24T00:00:00Z',
  };
  let bindingReads = 0;
  const writes: string[] = [];
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      new URL(request.url()).pathname.startsWith('/api/cloudflare-publications')
    )
      writes.push(request.url());
  });
  await page.route('**/api/connection', (route) =>
    route.fulfill({ json: snapshot() }),
  );
  await page.route('**/api/cloudflare-publications', (route) =>
    route.fulfill({
      json: {
        instanceId: 'cf-instance',
        supported: true,
        domain: 'example.net',
        isolation: {
          mode: 'dedicated-site',
          domain: 'example.net',
          maxOwnerSessionSeconds: 900,
          uniqueHostnames: true,
          retiredSitesReusable: false,
        },
        publications: [],
      },
    }),
  );
  await page.route('**/api/project-previews/*', (route) =>
    route.fulfill({
      json: { instanceId: 'cf-instance', supported: true, previews: [] },
    }),
  );
  await page.route('**/api/compute/**', (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/compute/info')
      return route.fulfill({
        json: {
          configured: true,
          hostId: binding.hostId,
          providers: [],
          images: [],
        },
      });
    if (path === '/api/compute/machines')
      return route.fulfill({ json: { machines: [] } });
    if (path === '/api/compute/volumes')
      return route.fulfill({ json: { volumes: [] } });
    if (path === '/api/compute/projects')
      return route.fulfill({ json: { projects: [project] } });
    if (path.endsWith('/binding')) {
      bindingReads++;
      return route.fulfill({ json: { project, binding } });
    }
    if (path.endsWith('/files/list'))
      return route.fulfill({ json: { project, binding, entries: [] } });
    return route.continue();
  });
  await page.reload();
  await choose(
    page.getByRole('button', { name: 'Workspace menu', exact: true }),
    touch,
  );
  await choose(menuItem(page, 'Applications'), touch);
  await choose(menuItem(page, 'Project Files'), touch);
  const launcher = page.locator('[data-window-id="project-files:projects"]');
  await choose(launcher.getByRole('tab', { name: 'VM', exact: true }), touch);
  await choose(
    launcher.getByRole('button', { name: /^Context project ·/ }),
    touch,
  );
  const projectWindow = page.locator(
    `[data-window-id="project-files:vm:${binding.hostId}:${binding.projectId}"]`,
  );
  await choose(
    projectWindow.getByRole('tab', { name: 'Previews', exact: true }),
    touch,
  );
  const previews = projectWindow.getByRole('region', {
    name: 'Project previews',
    exact: true,
  });
  const publish = previews.getByRole('button', {
    name: 'Publish',
    exact: true,
  });
  await expect(publish).toBeEnabled();
  await choose(publish, touch);
  await expect(panel(page)).toHaveAttribute(
    'data-window-id',
    'cloudflare:main',
  );
  await expect(
    panel(page).getByRole('tab', { name: 'Publishing', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  const hostname = panel(page).getByLabel('Name', { exact: true });
  await hostname.fill('draft');
  await expect(
    panel(page).getByText(
      /Project context-project · VM context-vm · port 5173/,
      { exact: false },
    ),
  ).toBeVisible();
  await activate(page, projectWindow);
  await previews.getByLabel('Port', { exact: true }).fill('6000');
  await choose(publish, touch);
  await expect(
    panel(page).getByText(/A project requested publishing/),
  ).toBeVisible();
  await expect(hostname).toHaveValue('draft');
  await choose(
    panel(page).getByRole('button', {
      name: 'Keep current draft',
      exact: true,
    }),
    touch,
  );
  await expect(hostname).toHaveValue('draft');
  await activate(page, projectWindow);
  await previews.getByLabel('Port', { exact: true }).fill('7000');
  await choose(publish, touch);
  await choose(
    panel(page).getByRole('button', {
      name: 'Use requested project',
      exact: true,
    }),
    touch,
  );
  await expect(hostname).toHaveValue('');
  await expect(
    panel(page).getByText(
      'Project context-project · VM context-vm · port 7000',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(panel(page)).toHaveCount(1);
  expect(bindingReads).toBe(1);
  expect(writes).toEqual([]);
});
