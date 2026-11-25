import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import type { BrowserContext, Locator, Page } from '@playwright/test';
import type { DesktopLayout } from '@cofy-x/axv-nextstep';
import {
  activate,
  expect,
  expectedDownloadFilename,
  launch,
  openDockOptions,
  persistedLayout,
  test,
} from './helpers';
import { expectEditorText } from './code-editor-helpers';
import { openVMEditorFixture } from './vm-editor-fixture';

async function choose(control: Locator, touch: boolean) {
  if (touch) await control.tap();
  else await control.click();
}

function layoutGeometry(layout: DesktopLayout) {
  return {
    windows: layout.windows.map(
      ({ id, x, y, width, height, restoreBounds }) => ({
        id,
        x,
        y,
        width,
        height,
        restoreBounds,
      }),
    ),
    icons: layout.icons,
    compactIcons: layout.compactIcons,
  };
}

async function downloadedDraft(
  page: Page,
  window: Locator,
  menu: string,
  filename: string,
  expected: string,
  browserName: string,
  touch: boolean,
) {
  const downloading = page.waitForEvent('download');
  await choose(window.getByRole('button', { name: menu, exact: true }), touch);
  await choose(
    page.locator('.ns-menu-layer').getByRole('menuitem', {
      name: /^Download Draft(?:\s|$)/,
    }),
    touch,
  );
  const download = await downloading;
  expect(download.suggestedFilename()).toBe(
    expectedDownloadFilename(filename, browserName),
  );
  expect(await download.failure()).toBeNull();
  expect(await readFile((await download.path())!, 'utf8')).toBe(expected);
}

function probeOnFocus(page: Page) {
  return page.evaluate(() => globalThis.dispatchEvent(new Event('focus')));
}

function storedVMFileDrafts(page: Page) {
  return page.evaluate(() =>
    Object.entries(sessionStorage).filter(([key]) =>
      key.includes('axv-vm-project-draft'),
    ),
  );
}

async function removeClientIdentity(context: BrowserContext) {
  const changing = await context.newPage();
  try {
    await changing.route('**/ui-storage-change', (route) =>
      route.fulfill({
        contentType: 'text/html',
        body: '<!doctype html><title>Storage change</title>',
      }),
    );
    await changing.goto('/ui-storage-change');
    await changing.evaluate(() =>
      localStorage.removeItem('nextstep-browser-id'),
    );
  } finally {
    await changing.close();
  }
}

test('online installation uses stable metadata, original icons and no service worker', async ({
  page,
  request,
}) => {
  const link = page.locator('link[rel="manifest"]');
  await expect(link).toHaveAttribute('href', '/manifest.webmanifest');
  await expect(link).toHaveAttribute('crossorigin', 'use-credentials');
  await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute(
    'content',
    '#252729',
  );
  const response = await request.get('/manifest.webmanifest');
  expect(response.ok()).toBeTruthy();
  expect(response.headers()['content-type']).toBe('application/manifest+json');
  expect(response.headers()['cache-control']).toBe('no-cache');
  expect(await response.json()).toEqual({
    id: '/',
    name: 'AXV',
    short_name: 'AXV',
    description: 'A personal cloud workstation.',
    lang: 'en',
    start_url: '/',
    scope: '/',
    display: 'standalone',
    background_color: '#505b64',
    theme_color: '#252729',
    icons: [
      {
        src: '/icons/axv-192.png',
        sizes: '192x192',
        type: 'image/png',
        purpose: 'any',
      },
      {
        src: '/icons/axv-512.png',
        sizes: '512x512',
        type: 'image/png',
        purpose: 'any',
      },
    ],
  });
  await expect(page.locator('link[rel="apple-touch-icon"]')).toHaveAttribute(
    'href',
    '/icons/apple-touch-icon.png',
  );
  for (const [path, size] of [
    ['/icons/axv-192.png', 192],
    ['/icons/axv-512.png', 512],
    ['/icons/apple-touch-icon.png', 180],
  ] as const) {
    expect(
      await page.evaluate(async (src) => {
        const image = new Image();
        image.src = src;
        await image.decode();
        return [image.naturalWidth, image.naturalHeight];
      }, path),
    ).toEqual([size, size]);
  }
  expect(
    await page.evaluate(async () => {
      if (!('serviceWorker' in navigator))
        return { registrations: 0, controlled: false };
      return {
        registrations: (await navigator.serviceWorker.getRegistrations())
          .length,
        controlled: navigator.serviceWorker.controller !== null,
      };
    }),
  ).toEqual({ registrations: 0, controlled: false });
});

test('short desktop notices can be reached and scrolled with the keyboard without changing saved work', async ({
  page,
  context,
}) => {
  const clientId = await page.evaluate(() =>
    localStorage.getItem('nextstep-browser-id'),
  );
  await removeClientIdentity(context);
  await page.setViewportSize({ width: 390, height: 260 });
  const notices = page.getByRole('region', {
    name: 'Desktop notices',
    exact: true,
  });
  await expect(notices).toContainText(
    'Browser storage changed or is unavailable',
  );
  await expect(notices).toHaveAttribute('tabindex', '0');
  await expect
    .poll(() =>
      notices.evaluate(
        (element) => element.scrollHeight > element.clientHeight,
      ),
    )
    .toBe(true);
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .focus();
  for (let index = 0; index < 90; index++) {
    if (await notices.evaluate((element) => element === document.activeElement))
      break;
    await page.keyboard.press('Tab');
  }
  await expect(notices).toBeFocused();
  await notices.evaluate((element) => {
    element.scrollTop = 0;
  });
  await page.keyboard.press('End');
  // Let native scrolling reach the end before reversing its direction.
  await expect
    .poll(() =>
      notices.evaluate((element) =>
        Math.abs(
          element.scrollHeight - element.clientHeight - element.scrollTop,
        ),
      ),
    )
    .toBeLessThan(1);
  await page.keyboard.press('Home');
  await expect
    .poll(() => notices.evaluate((element) => element.scrollTop))
    .toBe(0);
  await page.keyboard.press('ArrowDown');
  await expect
    .poll(() => notices.evaluate((element) => element.scrollTop))
    .toBeGreaterThan(0);
  await expect(notices).toBeFocused();
  expect(
    await notices
      .locator('.persistence-warning')
      .evaluate((element) => getComputedStyle(element).fontSize),
  ).toBe('12px');
  expect(clientId).toBeTruthy();
  expect(
    await page.evaluate(() => localStorage.getItem('nextstep-browser-id')),
  ).toBeNull();
});

test('a replaced desktop database freezes its requests and keeps the original page and VM draft', async ({
  page,
  context,
  request,
  hasTouch,
}, testInfo) => {
  const { window, editor, saved, writes } = await openVMEditorFixture(page, {
    touch: hasTouch,
  });
  const originalContent = saved();
  const draft = 'Keep the VM draft open when the desktop database changes.\n';
  await editor.fill(draft);
  await editor.evaluate((element) => {
    element.dataset.mountProbe = 'original-editor';
  });
  const timeOrigin = await page.evaluate(() => performance.timeOrigin);
  const clientId = await page.evaluate(() =>
    localStorage.getItem('nextstep-browser-id'),
  );
  await expect
    .poll(async () => (await storedVMFileDrafts(page))[0]?.[1])
    .toContain('Keep the VM draft open');
  const recovery = await storedVMFileDrafts(page);
  await expect
    .poll(async () => {
      const layout = (await persistedLayout(page, request)) as DesktopLayout;
      return layout?.windows.some((win) =>
        win.id.startsWith('project-files:vm:'),
      );
    })
    .toBe(true);
  const originalGeometry = layoutGeometry(await persistedLayout(page, request));
  const bootstrap = await request.get('/api/bootstrap');
  const { databaseId, preferences } = await bootstrap.json();
  let rejected = 0;
  await page.route('**/api/health', async (route) => {
    rejected++;
    expect(route.request().headers()['x-axv-database-id']).toBe(databaseId);
    await route.fulfill({
      status: 409,
      json: {
        code: 'database_changed',
        error: 'The original database was replaced.',
      },
    });
  });
  await probeOnFocus(page);
  const overlay = page.locator('.ns-status-overlay');
  const banner = page.locator('.desktop-warnings .persistence-warning');
  await expect(banner).toContainText('The AXV desktop database changed.');
  await expect(banner).toContainText('Keep this page open');
  expect(rejected).toBeGreaterThan(0);
  const rejectedBeforeFreeze = rejected;
  const laterRequests: string[] = [];
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname;
    if (
      /^\/api\/(bootstrap|health|layout|preferences|projects|host\/codex)(\/|$)/.test(
        path,
      )
    )
      laterRequests.push(`${request.method()} ${path}`);
  });

  await launch(page, 'appearance');
  const settings = page.locator('[data-app-id="appearance"]');
  await choose(
    settings.getByRole('button', { name: 'Solid Gray', exact: true }),
    hasTouch,
  );
  const failure = page.getByRole('dialog', {
    name: 'Action failed',
    exact: true,
  });
  await expect(failure).toBeVisible();
  await expect(banner).toBeHidden();
  await expect(overlay).toHaveAttribute('inert', '');
  await choose(
    failure.getByRole('button', { name: 'OK', exact: true }),
    hasTouch,
  );
  await expect(settings).toContainText('Not saved.');
  await expect(banner).toBeVisible();
  await expect(overlay).not.toHaveAttribute('inert', '');
  await activate(page, window, hasTouch);

  const originalReservation = await overlay.evaluate(
    (element) => element.getBoundingClientRect().height,
  );
  await openDockOptions(page, 'project-files', hasTouch);
  await expect(banner).toBeHidden();
  await expect(overlay).toHaveAttribute('inert', '');
  await banner.locator('a').evaluate((link) => link.focus());
  expect(
    await overlay.evaluate((element) =>
      element.contains(document.activeElement),
    ),
  ).toBe(false);
  expect(
    await overlay.evaluate((element) => element.getBoundingClientRect().height),
  ).toBe(originalReservation);
  await page.keyboard.press('Escape');
  await expect(banner).toBeVisible();
  await choose(
    page.getByRole('button', { name: 'Window Overview', exact: true }),
    hasTouch,
  );
  await expect(page.locator('.ns-overview-backdrop')).toBeVisible();
  await expect(banner).toBeHidden();
  await expect(overlay).toHaveAttribute('inert', '');
  await page.keyboard.press('Escape');
  await expect(banner).toBeVisible();
  await probeOnFocus(page);
  await page.evaluate(() =>
    document.dispatchEvent(new Event('visibilitychange')),
  );
  await expectEditorText(editor, draft);
  await expect(editor).toHaveAttribute('data-mount-probe', 'original-editor');
  expect(await page.evaluate(() => performance.timeOrigin)).toBe(timeOrigin);
  expect(await storedVMFileDrafts(page)).toEqual(recovery);
  expect(layoutGeometry(await persistedLayout(page, request))).toEqual(
    originalGeometry,
  );
  expect(
    (await (await request.get('/api/bootstrap')).json()).preferences,
  ).toEqual(preferences);
  expect(
    await page.evaluate(() => localStorage.getItem('nextstep-browser-id')),
  ).toBe(clientId);
  expect(laterRequests).toEqual([]);
  expect(rejected).toBe(rejectedBeforeFreeze);
  expect(writes).toEqual([]);
  expect(saved()).toBe(originalContent);

  const reopen = banner.getByRole('link', {
    name: 'Open current AXV',
    exact: true,
  });
  await expect(reopen).toHaveAttribute('href', '/');
  await expect(reopen).toHaveAttribute('target', '_blank');
  await expect(reopen).toHaveAttribute('rel', 'noopener');
  expect(
    await reopen.evaluate((link) => link.getBoundingClientRect().height),
  ).toBeGreaterThanOrEqual(
    await reopen.evaluate(() =>
      parseFloat(
        getComputedStyle(document.documentElement).getPropertyValue(
          '--ns-control-height',
        ),
      ),
    ),
  );
  const originalViewport = page.viewportSize()!;
  const viewports = hasTouch
    ? [
        { width: 390, height: 844 },
        { width: 844, height: 390 },
      ]
    : [originalViewport];
  for (const viewport of viewports) {
    await page.setViewportSize(viewport);
    await expect(banner).toBeInViewport();
    await expect(reopen).toBeInViewport();
    await expect
      .poll(async () => {
        const notice = (await overlay.boundingBox())!;
        const activeWindow = (await window.boundingBox())!;
        return activeWindow.y + activeWindow.height <= notice.y;
      })
      .toBe(true);
    await expect
      .poll(async () => {
        const bounds = (await banner.boundingBox())!;
        const dock = (await page
          .getByRole('complementary', { name: 'Application Dock' })
          .boundingBox())!;
        return (
          bounds.x + bounds.width <= dock.x ||
          bounds.y + bounds.height <= dock.y
        );
      })
      .toBe(true);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= globalThis.innerWidth,
      ),
    ).toBe(true);
    const name = `database-warning-${viewport.width}x${viewport.height}`;
    const path = testInfo.outputPath(`${name}.png`);
    await page.screenshot({ path });
    await testInfo.attach(name, { path, contentType: 'image/png' });
    await openDockOptions(page, 'project-files', hasTouch);
    await expect(banner).toBeHidden();
    await page.keyboard.press('Escape');
    await expect(banner).toBeVisible();
  }
  await page.setViewportSize(originalViewport);
  const opened = context.waitForEvent('page');
  await choose(reopen, hasTouch);
  const current = await opened;
  try {
    await expect(
      current.getByRole('complementary', { name: 'Application Dock' }),
    ).toBeVisible();
    expect(await current.evaluate(() => globalThis.opener === null)).toBe(true);
    await expect(banner).toBeVisible();
    await expectEditorText(editor, draft);
  } finally {
    await current.close();
  }
});

test('a retryable status reserves space without rewriting queued geometry or remounting editors', async ({
  page,
  request,
  hasTouch,
}) => {
  const { window, editor } = await openVMEditorFixture(page, {
    touch: hasTouch,
  });
  const draft = 'Keep this local text while retrying layout';
  await editor.fill(draft);
  await editor.evaluate((element) => {
    element.dataset.mountProbe = 'status-editor';
  });
  await expect
    .poll(async () => {
      const layout = (await persistedLayout(page, request)) as DesktopLayout;
      return layout?.windows.some((win) =>
        win.id.startsWith('project-files:vm:'),
      );
    })
    .toBe(true);
  const originalGeometry = layoutGeometry(await persistedLayout(page, request));
  const clientId = await page.evaluate(() =>
    localStorage.getItem('nextstep-browser-id'),
  );
  const welcomeId = 'welcome:main';
  const welcome = page.locator(`[data-window-id="${welcomeId}"]`);
  const writes: unknown[] = [];
  let fail = true;
  await page.route('**/api/layout*', async (route) => {
    if (route.request().method() !== 'PUT') return route.continue();
    const payload = route.request().postDataJSON() as {
      layout: DesktopLayout;
    };
    // Editor initialization can still be saving its layout. Inject the
    // outage on the Welcome activation this test intends to retry.
    if (!writes.length && payload.layout.activeWindowId !== welcomeId)
      return route.continue();
    writes.push(payload);
    if (fail)
      return route.fulfill({
        status: 503,
        json: { error: 'The layout store is temporarily unavailable.' },
      });
    return route.continue();
  });
  await expect(page.locator('.ns-status-overlay')).toHaveCount(0);
  await activate(page, welcome, hasTouch);
  const banner = page.locator('.desktop-warnings .persistence-warning');
  await expect(banner).toContainText('temporarily unavailable');
  expect(writes).toHaveLength(1);
  await expect
    .poll(async () => {
      const notice = (await banner.boundingBox())!;
      const bounds = (await welcome.boundingBox())!;
      return bounds.y + bounds.height <= notice.y;
    })
    .toBe(true);
  fail = false;
  await choose(
    banner.getByRole('button', { name: 'Retry', exact: true }),
    hasTouch,
  );
  await expect(page.locator('.ns-status-overlay')).toHaveCount(0);
  expect(writes).toHaveLength(2);
  expect(writes[1]).toEqual(writes[0]);
  expect(layoutGeometry(await persistedLayout(page, request))).toEqual(
    originalGeometry,
  );
  expect(
    await page.evaluate(() => localStorage.getItem('nextstep-browser-id')),
  ).toBe(clientId);
  await activate(page, window, hasTouch);
  await expect(editor).toHaveAttribute('data-mount-probe', 'status-editor');
  await expectEditorText(editor, draft);
});

test('a VM editor downloads its current offline text and retains its original recovery binding', async ({
  page,
  context,
  browserName,
  hasTouch,
}) => {
  const { window, editor, binding, filename, saved, writes } =
    await openVMEditorFixture(page, { touch: hasTouch });
  const original = saved();
  await editor.fill('First VM draft');
  const latest = 'const draft = "最新 🐱";\n';
  await editor.fill(latest);
  await expectEditorText(editor, latest);
  await expect
    .poll(async () => (await storedVMFileDrafts(page)).length)
    .toBe(1);
  const recovery = await storedVMFileDrafts(page);
  expect(JSON.parse(recovery[0]![1])).toMatchObject({
    binding,
    editor: {
      text: latest,
      saved: original,
      sha256: createHash('sha256').update(original).digest('hex'),
    },
  });
  await page.route('**/api/compute/**', (route) => {
    if (new URL(route.request().url()).pathname.endsWith('/write'))
      writes.push(route.request().postDataJSON());
    return route.abort('internetdisconnected');
  });
  await context.setOffline(true);
  try {
    await downloadedDraft(
      page,
      window,
      'More VM project actions',
      `${filename}.draft.txt`,
      latest,
      browserName,
      hasTouch,
    );
    await expectEditorText(editor, latest);
    expect(await storedVMFileDrafts(page)).toEqual(recovery);
    expect(writes).toEqual([]);
    expect(saved()).toBe(original);
    await expect(
      window.getByRole('status').filter({ hasText: ' · Unsaved changes' }),
    ).toBeVisible();
    await expect(
      window.getByRole('button', { name: 'Save', exact: true }),
    ).toBeEnabled();
  } finally {
    await context.setOffline(false);
  }
});

test('expired authorization has a recoverable notice and retains editor focus and identity', async ({
  page,
  hasTouch,
}) => {
  const { window, editor, saved, writes } = await openVMEditorFixture(page, {
    touch: hasTouch,
  });
  const original = saved();
  const draft = 'Keep typing while authorization is restored';
  await editor.fill(draft);
  await editor.evaluate((element) => {
    element.dataset.mountProbe = 'authorization-editor';
  });
  const clientId = await page.evaluate(() =>
    localStorage.getItem('nextstep-browser-id'),
  );
  const timeOrigin = await page.evaluate(() => performance.timeOrigin);
  let bootstraps = 0;
  page.on('request', (request) => {
    if (new URL(request.url()).pathname === '/api/bootstrap') bootstraps++;
  });
  await page.route('**/api/health', (route) =>
    route.fulfill({
      status: 401,
      json: {
        code: 'cloudflare_unauthorized',
        error: 'Authorization required',
      },
    }),
  );
  await probeOnFocus(page);
  const notices = page.getByRole('region', { name: 'Desktop notices' });
  await expect(notices).toContainText('Authorization is required.');
  await expect(editor).toBeFocused();
  await expectEditorText(editor, draft);
  await expect(
    window.getByRole('button', { name: 'Save', exact: true }),
  ).toBeEnabled();
  await page.unroute('**/api/health');
  await choose(
    notices.getByRole('button', { name: 'Retry', exact: true }),
    hasTouch,
  );
  await expect(notices).toHaveCount(0);
  await expect(editor).toHaveAttribute(
    'data-mount-probe',
    'authorization-editor',
  );
  await expect(editor).toBeFocused();
  await expectEditorText(editor, draft);
  expect(
    await page.evaluate(() => localStorage.getItem('nextstep-browser-id')),
  ).toBe(clientId);
  expect(await page.evaluate(() => performance.timeOrigin)).toBe(timeOrigin);
  expect(bootstraps).toBe(0);
  expect(writes).toEqual([]);
  expect(saved()).toBe(original);
});

test('a slow periodic health probe can report authorization expiry', async ({
  page,
}) => {
  await page.clock.install();
  await page.reload();
  await expect(
    page.getByRole('complementary', { name: 'Application Dock' }),
  ).toBeVisible();
  let probes = 0;
  let finish = () => {};
  const pending = new Promise<void>((resolve) => {
    finish = resolve;
  });
  await page.route('**/api/health', async (route) => {
    if (++probes === 1) {
      await pending;
      await route.fulfill({
        status: 401,
        json: { code: 'cloudflare_unauthorized' },
      });
      return;
    }
    await route.fulfill({ json: { status: 'ok' } });
  });
  try {
    await page.clock.fastForward(5000);
    await expect.poll(() => probes).toBe(1);
    await page.clock.fastForward(5000);
    finish();
    await expect(
      page.getByRole('region', { name: 'Desktop notices' }),
    ).toContainText('Authorization is required.');
    expect(probes).toBe(1);
  } finally {
    finish();
  }
});

for (const expired of [false, true]) {
  test(`a late health probe cannot override ${expired ? 'newer authorization expiry' : 'a newer healthy desktop response'}`, async ({
    page,
  }) => {
    const status = page.locator('.ns-desktop-status').getByRole('status');
    const notices = page.getByRole('region', { name: 'Desktop notices' });
    const expectedStatus = expired ? 'Offline' : 'Connected';
    let probes = 0;
    let releaseOlder = () => {};
    const older = new Promise<void>((resolve) => {
      releaseOlder = resolve;
    });
    await page.route('**/api/health', async (route) => {
      if (probes++ === 0) {
        await older;
        return route.fulfill(
          expired
            ? { json: { status: 'ok' } }
            : { status: 503, json: { error: 'Old probe failed.' } },
        );
      }
      return route.fulfill(
        expired
          ? { status: 401, json: { code: 'cloudflare_unauthorized' } }
          : { json: { status: 'ok' } },
      );
    });
    try {
      await probeOnFocus(page);
      await expect.poll(() => probes).toBe(1);
      await probeOnFocus(page);
      await expect.poll(() => probes).toBeGreaterThan(1);
      if (expired)
        await expect(notices).toContainText('Authorization is required.');
      await expect(status).toHaveText(expectedStatus);
      const delivered = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === '/api/health' &&
          response.status() === (expired ? 200 : 503),
      );
      releaseOlder();
      await delivered;
      await page.evaluate(
        () =>
          new Promise<void>((resolve) =>
            requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
          ),
      );
      await expect(status).toHaveText(expectedStatus);
      if (expired)
        await expect(notices).toContainText('Authorization is required.');
    } finally {
      releaseOlder();
    }
  });
}

test('removing one focused notice restores the editor without reclaiming deliberately moved focus', async ({
  page,
  context,
  hasTouch,
}) => {
  const { window, editor, saved, writes } = await openVMEditorFixture(page, {
    touch: hasTouch,
  });
  const original = saved();
  const draft = 'Keep this editor and its focus through notice recovery.';
  await editor.fill(draft);
  const element = await editor.elementHandle();
  await removeClientIdentity(context);
  const notices = page.getByRole('region', { name: 'Desktop notices' });
  const authentication = notices
    .locator('.persistence-warning')
    .filter({ hasText: 'Authorization is required.' });
  let denied = true;
  await page.route('**/api/health', (route) =>
    denied
      ? route.fulfill({
          status: 401,
          json: {
            code: 'cloudflare_unauthorized',
            error: 'Authorization required',
          },
        })
      : route.continue(),
  );
  await editor.focus();
  await probeOnFocus(page);
  await expect(authentication).toBeVisible();
  await expect(notices.locator('.persistence-warning')).toHaveCount(2);
  denied = false;
  await choose(
    authentication.getByRole('button', { name: 'Retry', exact: true }),
    hasTouch,
  );
  await expect(authentication).toHaveCount(0);
  await expect(notices.locator('.persistence-warning')).toHaveCount(1);
  await expect(editor).toBeFocused();

  denied = true;
  await probeOnFocus(page);
  await expect(authentication).toBeVisible();
  await authentication
    .getByRole('button', { name: 'Retry', exact: true })
    .focus();
  const workspaceMenu = page.getByRole('button', {
    name: 'Workspace menu',
    exact: true,
  });
  await workspaceMenu.focus();
  denied = false;
  await probeOnFocus(page);
  await expect(authentication).toHaveCount(0);
  await expect(workspaceMenu).toBeFocused();

  denied = true;
  await probeOnFocus(page);
  await expect(authentication).toBeVisible();
  await authentication
    .getByRole('button', { name: 'Retry', exact: true })
    .focus();
  await choose(page.locator('.ns-connection'), hasTouch);
  expect(
    await page.evaluate(() => document.activeElement === document.body),
  ).toBe(true);
  denied = false;
  await probeOnFocus(page);
  await expect(authentication).toHaveCount(0);
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      ),
  );
  expect(
    await page.evaluate(() => document.activeElement === document.body),
  ).toBe(true);
  expect(await element!.evaluate((node) => node.isConnected)).toBe(true);
  await expectEditorText(editor, draft);
  await expect(
    window.getByRole('status').filter({ hasText: ' · Unsaved changes' }),
  ).toBeVisible();
  expect(writes).toEqual([]);
  expect(saved()).toBe(original);
});
