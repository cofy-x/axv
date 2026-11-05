import { readFile } from 'node:fs/promises';
import type { Locator, Page } from '@playwright/test';
import type { DesktopLayout } from '@cofy-x/axv-nextstep';
import type { DraftRecord } from '../apps/dashboard/src/shared/drafts/storage';
import type {
  VMProject,
  VMProjectBinding,
} from '../apps/dashboard/src/features/project-files/vm-api';
import {
  actionMenu,
  activate,
  createEntry,
  expect,
  expectedDownloadFilename,
  filesWindow,
  launch,
  openDocument,
  openDockOptions,
  persistedLayout,
  readContent,
  test,
} from './helpers';
import { codeEditor, expectEditorText } from './code-editor-helpers';

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

async function storedDrafts(page: Page, entryId: string) {
  return page.evaluate(async (id) => {
    const database = await new Promise<IDBDatabase>((resolve, reject) => {
      const request = indexedDB.open('nextstep-drafts', 1);
      request.onsuccess = () => resolve(request.result);
      request.onerror = () =>
        reject(new Error('Could not read draft storage.'));
    });
    try {
      return await new Promise<DraftRecord[]>((resolve, reject) => {
        const transaction = database.transaction('drafts', 'readonly');
        const request = transaction.objectStore('drafts').getAll();
        transaction.oncomplete = () =>
          resolve(
            (request.result as DraftRecord[]).filter(
              (draft) => draft.entryId === id,
            ),
          );
        transaction.onabort = () =>
          reject(new Error('Draft read was aborted.'));
        transaction.onerror = () => reject(new Error('Draft read failed.'));
      });
    } finally {
      database.close();
    }
  }, entryId);
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

test('a document downloads the latest offline draft without saving or clearing recovery', async ({
  page,
  context,
  request,
  browserName,
  hasTouch,
}) => {
  const saved = 'Saved server version\n';
  const entry = await createEntry(
    request,
    'E2E Download Draft.md',
    'file',
    'documents',
    saved,
  );
  await page.reload();
  const window = await openDocument(page, entry, hasTouch);
  const text = window.getByRole('textbox', { name: 'Document text' });
  await text.fill('First local draft');
  const latest = '# Latest unsaved text\n\n中文 and emoji 🐱\n';
  await text.fill(latest);
  await expect
    .poll(async () => (await storedDrafts(page, entry.id))[0]?.text)
    .toBe(latest);
  const recovery = await storedDrafts(page, entry.id);
  const writes: string[] = [];
  page.on('request', (request) => {
    if (
      new URL(request.url()).pathname === `/api/entries/${entry.id}` &&
      request.method() !== 'GET'
    )
      writes.push(request.method());
  });
  await context.setOffline(true);
  try {
    await downloadedDraft(
      page,
      window,
      'More document actions',
      `${entry.name}.draft.txt`,
      latest,
      browserName,
      hasTouch,
    );
    await expect(text).toHaveValue(latest);
    await expect(window.getByTestId('save-status')).toHaveText(
      'Unsaved changes',
    );
    expect(await storedDrafts(page, entry.id)).toEqual(recovery);
    expect(writes).toEqual([]);
  } finally {
    await context.setOffline(false);
  }
  expect(await readContent(request, entry.id)).toBe(saved);
});

test('a database rejection freezes desktop requests while retaining the editor and a local draft download', async ({
  page,
  context,
  request,
  browserName,
  hasTouch,
}, testInfo) => {
  const entry = await createEntry(request, 'E2E Database changed.txt');
  await page.reload();
  const window = await openDocument(page, entry, hasTouch);
  const text = window.getByRole('textbox', { name: 'Document text' });
  const draft = 'Keep the original database draft\n';
  await text.fill(draft);
  await text.evaluate((element) => {
    element.dataset.mountProbe = 'original-editor';
  });
  const timeOrigin = await page.evaluate(() => performance.timeOrigin);
  await expect
    .poll(async () => (await storedDrafts(page, entry.id))[0]?.text)
    .toBe(draft);
  const recovery = await storedDrafts(page, entry.id);
  await expect
    .poll(async () => {
      const layout = (await persistedLayout(page, request)) as DesktopLayout;
      return layout?.windows.some((win) => win.params.entryId === entry.id);
    })
    .toBe(true);
  const originalGeometry = layoutGeometry(await persistedLayout(page, request));
  let rejected = 0;
  await page.route('**/api/entries', async (route) => {
    rejected++;
    expect(route.request().headers()['x-axv-database-id']).toBe(
      recovery[0]!.databaseId,
    );
    await route.fulfill({
      status: 409,
      json: {
        code: 'database_changed',
        error: 'The original database was replaced.',
      },
    });
  });
  await activate(page, filesWindow(page), hasTouch);
  await actionMenu(
    page,
    filesWindow(page),
    'More file actions',
    'Refresh Folder',
    hasTouch,
  );
  const overlay = page.locator('.ns-status-overlay');
  const banner = page.locator('.desktop-warnings .persistence-warning');
  await expect(banner).toContainText('The AXV desktop database changed.');
  await expect(banner).toContainText('Keep this page open');
  await expect(banner).toBeHidden();
  await expect(overlay).toHaveAttribute('inert', '');
  await page
    .getByRole('dialog', { name: 'Action failed', exact: true })
    .getByRole('button', { name: 'OK', exact: true })
    .click();
  await expect(banner).toBeVisible();
  await expect(overlay).not.toHaveAttribute('inert', '');
  const originalReservation = await overlay.evaluate(
    (element) => element.getBoundingClientRect().height,
  );
  await openDockOptions(page, 'notes', hasTouch);
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
    page.getByRole('button', { name: 'Show all windows', exact: true }),
    hasTouch,
  );
  await expect(page.locator('.ns-overview-backdrop')).toBeVisible();
  await expect(banner).toBeHidden();
  await expect(overlay).toHaveAttribute('inert', '');
  await page.keyboard.press('Escape');
  await expect(banner).toBeVisible();
  expect(rejected).toBe(1);
  const laterRequests: string[] = [];
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname;
    if (
      /^\/api\/(bootstrap|entries|layout|preferences|documents|projects|codex)(\/|$)/.test(
        path,
      )
    )
      laterRequests.push(`${request.method()} ${path}`);
  });
  await activate(page, window, hasTouch);
  await expect(
    window.getByRole('button', { name: 'Save', exact: true }),
  ).toBeDisabled();
  await expect(
    window.getByRole('button', { name: 'Retry', exact: true }),
  ).toHaveCount(0);
  await expect(window).toContainText('Saving is unavailable in this window.');
  await expect(
    window.getByRole('button', { name: 'Download Draft', exact: true }),
  ).toBeEnabled();
  await downloadedDraft(
    page,
    window,
    'More document actions',
    `${entry.name}.draft.txt`,
    draft,
    browserName,
    hasTouch,
  );
  await activate(page, filesWindow(page), hasTouch);
  await actionMenu(
    page,
    filesWindow(page),
    'More file actions',
    'Refresh Folder',
    hasTouch,
  );
  await page
    .getByRole('dialog', { name: 'Action failed', exact: true })
    .getByRole('button', { name: 'OK', exact: true })
    .click();
  await page.evaluate(() => {
    globalThis.dispatchEvent(new Event('focus'));
    document.dispatchEvent(new Event('visibilitychange'));
  });
  await activate(page, window, hasTouch);
  await expect(text).toHaveValue(draft);
  await expect(text).toHaveAttribute('data-mount-probe', 'original-editor');
  await expect(window).toHaveAttribute('aria-label', / \*$/);
  expect(await page.evaluate(() => performance.timeOrigin)).toBe(timeOrigin);
  expect(await storedDrafts(page, entry.id)).toEqual(
    recovery.map((record) => ({ ...record, updatedAt: expect.any(Number) })),
  );
  expect(layoutGeometry(await persistedLayout(page, request))).toEqual(
    originalGeometry,
  );
  expect(
    await page.evaluate(() => localStorage.getItem('nextstep-browser-id')),
  ).toBe(recovery[0]!.clientId);
  expect(laterRequests).toEqual([]);
  expect(rejected).toBe(1);
  expect(await readContent(request, entry.id)).toBe('Initial text');
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
        const editor = (await window.boundingBox())!;
        return editor.y + editor.height <= notice.y;
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
    await openDockOptions(page, 'notes', hasTouch);
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
    await expect(text).toHaveValue(draft);
  } finally {
    await current.close();
  }
});

test('a retryable status reserves space without rewriting queued geometry or remounting editors', async ({
  page,
  request,
  hasTouch,
}) => {
  const entry = await createEntry(request, 'E2E Status reservation.txt');
  await page.reload();
  const window = await openDocument(page, entry, hasTouch);
  const text = window.getByRole('textbox', {
    name: 'Document text',
    includeHidden: true,
  });
  await text.fill('Keep this local text while retrying layout');
  await text.evaluate((element) => {
    element.dataset.mountProbe = 'status-editor';
  });
  await expect
    .poll(async () => {
      const layout = (await persistedLayout(page, request)) as DesktopLayout;
      return layout?.windows.find((win) => win.params.entryId === entry.id)
        ?.title;
    })
    .toMatch(/ \*$/);
  const originalGeometry = layoutGeometry(await persistedLayout(page, request));
  const clientId = await page.evaluate(() =>
    localStorage.getItem('nextstep-browser-id'),
  );
  const writes: unknown[] = [];
  let fail = true;
  await page.route('**/api/layout*', async (route) => {
    if (route.request().method() !== 'PUT') return route.continue();
    writes.push(route.request().postDataJSON());
    if (fail)
      return route.fulfill({
        status: 503,
        json: { error: 'The layout store is temporarily unavailable.' },
      });
    return route.continue();
  });
  await expect(page.locator('.ns-status-overlay')).toHaveCount(0);
  await activate(page, filesWindow(page), hasTouch);
  const banner = page.locator('.desktop-warnings .persistence-warning');
  await expect(banner).toContainText('temporarily unavailable');
  expect(writes).toHaveLength(1);
  await expect
    .poll(async () => {
      const notice = (await banner.boundingBox())!;
      const files = (await filesWindow(page).boundingBox())!;
      return files.y + files.height <= notice.y;
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
  await expect(text).toHaveAttribute('data-mount-probe', 'status-editor');
  await expect(text).toHaveValue('Keep this local text while retrying layout');
});

test('a VM editor downloads its current offline text and retains its original recovery binding', async ({
  page,
  context,
  browserName,
  hasTouch,
}) => {
  const project: VMProject = {
    id: 'download-project',
    hostId: 'download-host',
    kind: 'vm',
    name: 'Download VM project',
    volumeId: 'download-volume',
    root: 'repo',
    rootIdentity: 'original-root',
    revision: 1,
    createdAt: '2026-10-28T00:00:00Z',
  };
  const binding: VMProjectBinding = {
    hostId: project.hostId,
    projectId: project.id,
    projectRevision: 1,
    volumeId: project.volumeId,
    volumeRevision: 1,
    machineId: 'download-machine',
    bootId: 'original-boot',
    instanceId: 'original-guest',
  };
  const saved = 'Original VM file\n';
  const writes: unknown[] = [];
  let disconnected = false;
  await page.route('**/api/compute/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith('/write')) writes.push(route.request().postDataJSON());
    if (disconnected) return route.abort('internetdisconnected');
    if (path === '/api/compute/info')
      return route.fulfill({
        json: {
          configured: true,
          hostId: project.hostId,
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
    if (path.endsWith('/binding'))
      return route.fulfill({ json: { project, binding } });
    if (path.endsWith('/list'))
      return route.fulfill({
        json: {
          project,
          binding,
          entries: [
            {
              name: 'main.ts',
              path: 'main.ts',
              kind: 'file',
              size: saved.length,
              modifiedAt: project.createdAt,
            },
          ],
        },
      });
    if (path.endsWith('/read'))
      return route.fulfill({
        json: {
          project,
          binding,
          path: 'main.ts',
          content: saved,
          sha256: 'original-sha256',
          size: saved.length,
          modifiedAt: project.createdAt,
        },
      });
    return route.fulfill({
      status: 404,
      json: { error: 'Unexpected fixture operation.' },
    });
  });
  await launch(page, 'project-files');
  const launcher = page.locator('[data-window-id="project-files:projects"]');
  await choose(
    launcher.getByRole('tab', { name: 'VM', exact: true }),
    hasTouch,
  );
  await choose(
    launcher.getByRole('button', { name: /^Download VM project ·/ }),
    hasTouch,
  );
  const window = page.locator(
    `[data-window-id="project-files:vm:${project.hostId}:${project.id}"]`,
  );
  await choose(
    window.getByRole('button', { name: 'main.ts', exact: true }),
    hasTouch,
  );
  await expectEditorText(codeEditor(window), saved);
  await choose(
    window.getByRole('button', { name: 'Edit', exact: true }),
    hasTouch,
  );
  const text = codeEditor(window);
  await expect(text).toBeEditable();
  await text.fill('First VM draft');
  const latest = 'const draft = "最新 🐱";\n';
  await text.fill(latest);
  await expectEditorText(text, latest);
  const recoveryRecords = () =>
    page.evaluate(() =>
      Object.entries(sessionStorage).filter(([key]) =>
        key.includes('axv-vm-project-draft'),
      ),
    );
  await expect.poll(async () => (await recoveryRecords()).length).toBe(1);
  const recovery = await recoveryRecords();
  expect(JSON.parse(recovery[0]![1])).toMatchObject({
    binding,
    editor: { text: latest, saved, sha256: 'original-sha256' },
  });
  disconnected = true;
  await context.setOffline(true);
  try {
    await downloadedDraft(
      page,
      window,
      'More VM project actions',
      'main.ts.draft.txt',
      latest,
      browserName,
      hasTouch,
    );
    await expectEditorText(text, latest);
    expect(await recoveryRecords()).toEqual(recovery);
    expect(writes).toEqual([]);
    await expect(
      window.getByRole('status').filter({ hasText: ' · Unsaved changes' }),
    ).toBeVisible();
    await expect(
      window.getByRole('button', { name: 'Save', exact: true }),
    ).toBeEnabled();
  } finally {
    disconnected = false;
    await context.setOffline(false);
  }
});

test('short desktop notices can be reached and scrolled with the keyboard without changing saved work', async ({
  page,
  context,
}) => {
  const clientId = await page.evaluate(() =>
    localStorage.getItem('nextstep-browser-id'),
  );
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

test('expired authorization has a recoverable notice and retains editor focus and identity', async ({
  page,
  request,
  hasTouch,
}) => {
  const entry = await createEntry(request, 'E2E Authorization draft.txt');
  await page.reload();
  const window = await openDocument(page, entry, hasTouch);
  const text = window.getByRole('textbox', { name: 'Document text' });
  const draft = 'Keep typing while authorization is restored';
  await text.fill(draft);
  await text.evaluate((element) => {
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
  await page.route('**/api/entries', (route) =>
    route.fulfill({
      status: 401,
      json: {
        code: 'cloudflare_unauthorized',
        error: 'Authorization required',
      },
    }),
  );
  await page.evaluate(() => globalThis.dispatchEvent(new Event('focus')));
  const notices = page.getByRole('region', { name: 'Desktop notices' });
  await expect(notices).toContainText('Authorization is required.');
  await expect(text).toBeFocused();
  await expect(text).toHaveValue(draft);
  await expect(
    window.getByRole('button', { name: 'Save', exact: true }),
  ).toBeEnabled();
  await page.unroute('**/api/entries');
  await choose(
    notices.getByRole('button', { name: 'Retry', exact: true }),
    hasTouch,
  );
  await expect(notices).toHaveCount(0);
  await expect(text).toHaveAttribute(
    'data-mount-probe',
    'authorization-editor',
  );
  await expect(text).toBeFocused();
  await expect(text).toHaveValue(draft);
  expect(
    await page.evaluate(() => localStorage.getItem('nextstep-browser-id')),
  ).toBe(clientId);
  expect(await page.evaluate(() => performance.timeOrigin)).toBe(timeOrigin);
  expect(bootstraps).toBe(0);
  expect(await readContent(request, entry.id)).toBe('Initial text');
});

test('removing one focused notice restores the editor without reclaiming deliberately moved focus', async ({
  page,
  context,
  request,
  hasTouch,
}) => {
  const entry = await createEntry(request, 'E2E Stacked notice focus.txt');
  await page.reload();
  const window = await openDocument(page, entry, hasTouch);
  const text = window.getByRole('textbox', { name: 'Document text' });
  const draft = 'Keep this editor and its focus through notice recovery.';
  await text.fill(draft);
  const element = await text.elementHandle();
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
  const notices = page.getByRole('region', { name: 'Desktop notices' });
  const authentication = notices
    .locator('.persistence-warning')
    .filter({ hasText: 'Authorization is required.' });
  let denied = true;
  await page.route('**/api/entries', (route) =>
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
  const refresh = () =>
    page.evaluate(() => globalThis.dispatchEvent(new Event('focus')));
  await text.focus();
  await refresh();
  await expect(authentication).toBeVisible();
  await expect(notices.locator('.persistence-warning')).toHaveCount(2);
  denied = false;
  await choose(
    authentication.getByRole('button', { name: 'Retry', exact: true }),
    hasTouch,
  );
  await expect(authentication).toHaveCount(0);
  await expect(notices.locator('.persistence-warning')).toHaveCount(1);
  await expect(text).toBeFocused();

  denied = true;
  await refresh();
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
  await refresh();
  await expect(authentication).toHaveCount(0);
  await expect(workspaceMenu).toBeFocused();

  denied = true;
  await refresh();
  await expect(authentication).toBeVisible();
  await authentication
    .getByRole('button', { name: 'Retry', exact: true })
    .focus();
  await choose(page.locator('.ns-connection'), hasTouch);
  expect(
    await page.evaluate(() => document.activeElement === document.body),
  ).toBe(true);
  denied = false;
  await refresh();
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
  await expect(text).toHaveValue(draft);
  await expect(window.getByTestId('save-status')).toHaveText('Unsaved changes');
  expect(await readContent(request, entry.id)).toBe('Initial text');
});

test('protected draft close defaults to keeping the editor and can download without closing', async ({
  page,
  request,
  hasTouch,
  browserName,
}) => {
  const entry = await createEntry(request, 'E2E Protected close.txt');
  await page.reload();
  const window = await openDocument(page, entry, hasTouch);
  const text = window.getByRole('textbox', { name: 'Document text' });
  const draft = 'Preserve this draft through the close dialog';
  await text.fill(draft);
  await page.route(`**/api/entries/${entry.id}`, (route) =>
    route.fulfill({
      status: 409,
      json: {
        code: 'database_changed',
        error: 'The original database changed.',
      },
    }),
  );
  await choose(
    window.getByRole('button', { name: 'Save', exact: true }),
    hasTouch,
  );
  await expect(
    window.getByRole('button', { name: 'Save', exact: true }),
  ).toBeDisabled();
  await expect(
    window.getByRole('button', { name: 'Retry', exact: true }),
  ).toHaveCount(0);
  await choose(window.getByRole('button', { name: /^Close / }), hasTouch);
  const dialog = page.getByRole('dialog', {
    name: 'Keep your draft',
    exact: true,
  });
  await expect(
    dialog.getByRole('button', { name: 'Cancel', exact: true }),
  ).toBeFocused();
  await expect(
    dialog.getByRole('button', { name: 'Save', exact: true }),
  ).toHaveCount(0);
  const downloading = page.waitForEvent('download');
  await choose(
    dialog.getByRole('button', { name: 'Download Draft', exact: true }),
    hasTouch,
  );
  const download = await downloading;
  expect(download.suggestedFilename()).toBe(
    expectedDownloadFilename(`${entry.name}.draft.txt`, browserName),
  );
  expect(await readFile((await download.path())!, 'utf8')).toBe(draft);
  await expect(dialog).toHaveCount(0);
  await expect(window).toBeVisible();
  await expect(text).toHaveValue(draft);
  await expect(window).toHaveAttribute('aria-label', / \*$/);
  expect(await readContent(request, entry.id)).toBe('Initial text');
});

test('document composition events block Save and Download Draft and retain the final input across a menu', async ({
  page,
  request,
  hasTouch,
  browserName,
}) => {
  const entry = await createEntry(request, 'E2E Composition draft.txt');
  await page.reload();
  const window = await openDocument(page, entry, hasTouch);
  const text = window.getByRole('textbox', { name: 'Document text' });
  await text.fill('Draft before composition');
  const writes: string[] = [];
  page.on('request', (request) => {
    if (
      request.method() === 'PATCH' &&
      new URL(request.url()).pathname === `/api/entries/${entry.id}`
    )
      writes.push(request.url());
  });
  await text.evaluate((element) => {
    element.setAttribute('data-composition-owner', 'original-editor');
    element.dispatchEvent(
      new CompositionEvent('compositionstart', { bubbles: true, data: '' }),
    );
    Object.getOwnPropertyDescriptor(
      HTMLTextAreaElement.prototype,
      'value',
    )!.set!.call(element, 'Draft 中');
    element.dispatchEvent(
      new InputEvent('input', {
        bubbles: true,
        inputType: 'insertCompositionText',
        data: '中',
        isComposing: true,
      }),
    );
  });
  await expect(
    window.getByRole('button', { name: 'Save', exact: true }),
  ).toBeDisabled();
  expect(
    await text.evaluate((element) =>
      element.dispatchEvent(
        new KeyboardEvent('keydown', {
          bubbles: true,
          cancelable: true,
          key: 's',
          ctrlKey: true,
          isComposing: true,
        }),
      ),
    ),
  ).toBe(false);
  await choose(
    window.getByRole('button', { name: 'More document actions', exact: true }),
    hasTouch,
  );
  await expect(
    page.getByRole('menuitem', { name: /^Download Draft(?:\s|$)/ }),
  ).toBeDisabled();
  await text.evaluate((element) => {
    element.dispatchEvent(
      new CompositionEvent('compositionend', { bubbles: true, data: '中文' }),
    );
    Object.getOwnPropertyDescriptor(
      HTMLTextAreaElement.prototype,
      'value',
    )!.set!.call(element, 'Draft 中文');
    element.dispatchEvent(
      new InputEvent('input', {
        bubbles: true,
        inputType: 'insertText',
        data: '中文',
        isComposing: false,
      }),
    );
  });
  await expect(text).toHaveValue('Draft 中文');
  await expect(text).not.toBeFocused();
  await page.keyboard.press('Escape');
  await expect(
    window.getByRole('button', { name: 'Save', exact: true }),
  ).toBeEnabled();
  expect(writes).toEqual([]);
  await downloadedDraft(
    page,
    window,
    'More document actions',
    `${entry.name}.draft.txt`,
    'Draft 中文',
    browserName,
    hasTouch,
  );
  await expect(text).toHaveValue('Draft 中文');
  await expect(text).toHaveAttribute(
    'data-composition-owner',
    'original-editor',
  );
  expect(writes).toEqual([]);
});

test('a completed document composition can be saved before the next animation frame', async ({
  page,
  request,
  hasTouch,
}) => {
  const entry = await createEntry(request, 'E2E Completed composition.txt');
  await page.reload();
  const window = await openDocument(page, entry, hasTouch);
  const text = window.getByRole('textbox', { name: 'Document text' });
  await expect(text).toHaveValue('Initial text');
  const result = await text.evaluate((element) => {
    const textarea = element as HTMLTextAreaElement;
    textarea.dispatchEvent(
      new CompositionEvent('compositionstart', { bubbles: true, data: '' }),
    );
    Object.getOwnPropertyDescriptor(
      HTMLTextAreaElement.prototype,
      'value',
    )!.set!.call(textarea, 'Immediately saved 中文');
    textarea.dispatchEvent(
      new CompositionEvent('compositionend', {
        bubbles: true,
        data: 'Immediately saved 中文',
      }),
    );
    textarea.dispatchEvent(
      new InputEvent('input', {
        bubbles: true,
        inputType: 'insertCompositionText',
        data: 'Immediately saved 中文',
        isComposing: false,
      }),
    );
    return textarea.dispatchEvent(
      new KeyboardEvent('keydown', {
        bubbles: true,
        cancelable: true,
        key: 's',
        ctrlKey: true,
        isComposing: false,
      }),
    );
  });
  expect(result).toBe(false);
  await expect
    .poll(() => readContent(request, entry.id))
    .toBe('Immediately saved 中文');
  await expect(window.getByTestId('save-status')).toHaveText('Saved');
});

test('native document composition preserves the committed text when a menu opens at compositionend', async ({
  page,
  request,
  hasTouch,
  browserName,
}) => {
  test.skip(
    browserName !== 'chromium',
    'Native IME input requires Chromium CDP.',
  );
  const entry = await createEntry(request, 'E2E Native composition.txt');
  await page.reload();
  const window = await openDocument(page, entry, hasTouch);
  const text = window.getByRole('textbox', { name: 'Document text' });
  await text.fill('Draft ');
  await text.press('End');
  const writes: string[] = [];
  page.on('request', (request) => {
    if (
      request.method() === 'PATCH' &&
      new URL(request.url()).pathname === `/api/entries/${entry.id}`
    )
      writes.push(request.url());
  });
  const protocol = await page.context().newCDPSession(page);
  try {
    await protocol.send('Input.imeSetComposition', {
      text: '中',
      selectionStart: 1,
      selectionEnd: 1,
    });
    await expect(text).toHaveValue('Draft 中');
    await expect(
      window.getByRole('button', { name: 'Save', exact: true }),
    ).toBeDisabled();
    await text.evaluate((element) => {
      element.setAttribute('data-composition-owner', 'native-editor');
      element.addEventListener(
        'compositionend',
        () => {
          const menu = document.querySelector<HTMLButtonElement>(
            'button[aria-label="Workspace menu"]',
          );
          if (!menu) throw new Error('Expected the desktop workspace menu.');
          menu.click();
        },
        { once: true },
      );
    });
    await protocol.send('Input.insertText', { text: '中文' });
    await expect(
      page.getByRole('menuitem', { name: 'Applications', exact: true }),
    ).toBeVisible();
    await expect(text).toHaveValue('Draft 中文');
    await expect(text).not.toBeEditable();
    await text.evaluate((element) => (element as HTMLElement).focus());
    await expect(text).not.toBeFocused();
    await page.keyboard.insertText('blocked');
    await expect(text).toHaveValue('Draft 中文');
    await page.keyboard.press('Escape');
    await expect(
      window.getByRole('button', { name: 'Save', exact: true }),
    ).toBeEnabled();
    await downloadedDraft(
      page,
      window,
      'More document actions',
      `${entry.name}.draft.txt`,
      'Draft 中文',
      browserName,
      hasTouch,
    );
    await expect(text).toHaveAttribute(
      'data-composition-owner',
      'native-editor',
    );
    expect(writes).toEqual([]);
    await choose(
      window.getByRole('button', { name: 'Save', exact: true }),
      hasTouch,
    );
    await expect(window.getByTestId('save-status')).toHaveText('Saved');
    expect(await readContent(request, entry.id)).toBe('Draft 中文');
  } finally {
    await protocol.detach();
  }
});
