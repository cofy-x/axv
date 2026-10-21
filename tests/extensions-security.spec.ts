import { readFileSync } from 'node:fs';
import type { APIRequestContext, Locator, Page } from '@playwright/test';
import type {
  ExtensionCatalog,
  ExtensionRecord,
} from '../apps/dashboard/src/features/extensions/types';
import { expect, menuItem, persistedLayout, test } from './helpers';

const sdk = readFileSync(
  new URL('../examples/extensions/sdk.js', import.meta.url),
  'utf8',
);
const probeHTML = `<!doctype html><html lang="en"><title>Probe</title><body><pre id="result">Starting</pre><script>${sdk}</script><script>
const checks = {};
try { localStorage.getItem('nextstep-browser-id'); checks.storage = false; } catch { checks.storage = true; }
try { parent.document.title; checks.parent = false; } catch { checks.parent = true; }
document.getElementById('result').textContent = JSON.stringify(checks);
axv.ready.then(async (initial) => {
  checks.credentials = !('sessionId' in initial) && !('token' in initial);
  checks.ownData = (await axv.request('storage.get')).value === null;
  try { await axv.request('host.exec'); checks.exec = false; } catch { checks.exec = true; }
  try { await axv.request('storage.get', { appId: 'local.other' }); checks.crossApp = false; } catch { checks.crossApp = true; }
  try { await fetch('/api/entries'); checks.api = false; } catch { checks.api = true; }
  const image = new Image();
  image.src = '/api/health?extension-probe=1';
  await new Promise((resolve) => { image.onload = resolve; image.onerror = resolve; });
  checks.done = true;
  document.getElementById('result').textContent = JSON.stringify(checks);
}).catch((error) => { document.getElementById('result').textContent = error.message; });
</script></body></html>`;

async function installProbe(
  request: APIRequestContext,
  baseURL: string,
  id: string,
  html = probeHTML,
) {
  const listed = await request.get('/api/extensions');
  expect(listed.ok()).toBeTruthy();
  const catalog = (await listed.json()) as ExtensionCatalog;
  const old = catalog.apps.find((app) => app.id === id);
  const response = await request.post('/api/extensions/install', {
    headers: {
      Origin: new URL(baseURL).origin,
      'X-AXV-Extension-Store': catalog.storeId,
    },
    data: {
      expectedGeneration: old?.generation,
      package: {
        manifest: {
          schemaVersion: 1,
          id,
          title: id,
          version: '1.0.0',
          apiVersion: 1,
          capabilities: ['storage'],
        },
        html,
      },
    },
  });
  expect(response.ok()).toBeTruthy();
  const { app } = (await response.json()) as { app: ExtensionRecord };
  return { ...app, storeId: catalog.storeId };
}

async function openManager(page: Page) {
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await menuItem(page, 'Application Manager').click();
  return page.locator('[data-app-id="applications"]');
}

test('an extension cannot access desktop credentials, another app, host commands or raw APIs', async ({
  page,
  request,
  baseURL,
}, info) => {
  const id = `local.security-${info.project.name}`;
  await installProbe(request, baseURL!, id);
  const manager = await openManager(page);
  await manager.getByRole('button', { name: 'Refresh', exact: true }).click();
  await manager
    .locator(`[aria-label="${id} actions"]`)
    .getByRole('button', { name: 'Open', exact: true })
    .click();
  const win = page.locator(`[data-app-id="extension.${id}"]`);
  const result = win.frameLocator('iframe').locator('#result');
  await expect(result).toContainText('"done":true');
  expect(JSON.parse((await result.textContent())!)).toEqual({
    storage: true,
    parent: true,
    credentials: true,
    ownData: true,
    exec: true,
    crossApp: true,
    api: true,
    done: true,
  });
  expect(
    (
      await request.get('/api/health', {
        headers: { 'Sec-Fetch-Site': 'cross-site' },
      })
    ).status(),
  ).toBe(403);
  await expect(win.locator('iframe')).toHaveAttribute(
    'sandbox',
    'allow-scripts',
  );
  expect(await win.locator('iframe').getAttribute('src')).not.toContain(
    'token',
  );
});

test('extension documents retain their sandbox when opened directly', async ({
  page,
  context,
  request,
  baseURL,
}, info) => {
  const app = await installProbe(
    request,
    baseURL!,
    `local.direct-${info.project.name}`,
  );
  const url = `/api/extensions/${app.id}/content/${app.storeId}/${app.digest}`;
  const response = await request.get(url, {
    headers: { 'X-AXV-Extension-Store': app.storeId },
  });
  expect(response.ok()).toBeTruthy();
  expect(response.headers()['content-security-policy']).toContain(
    'sandbox allow-scripts;',
  );
  expect(response.headers()['content-security-policy']).not.toContain(
    'allow-same-origin',
  );
  expect(response.headers()['x-frame-options']).toBe('SAMEORIGIN');
  const direct = await context.newPage();
  try {
    await direct.goto(new URL(url, baseURL).href);
    await expect(direct.locator('#result')).toContainText('"storage":true');
    expect(await direct.evaluate(() => globalThis.origin)).toBe('null');
  } finally {
    await direct.close();
  }
  await expect(page.locator('.ns-desktop')).toBeVisible();
});

test('an oversized draft rejection stays unsaved and cannot silently close its application', async ({
  page,
  request,
  baseURL,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const choose = async (target: Locator) => {
    if (touch) await target.tap();
    else await target.click();
  };
  const id = `local.oversized-${info.project.name}`;
  const html = `<!doctype html><html lang="en"><title>Draft Rejection Probe</title><body>
<button id="reject" disabled>Create oversized draft</button><p id="result" role="status">Connecting</p>
<script>${sdk}</script><script>
axv.ready.then(async () => {
  const saved = await axv.request('storage.get');
  const button = document.getElementById('reject');
  button.disabled = false;
  button.onclick = async () => {
    try {
      await axv.request('draft.set', { value: { text: 'x'.repeat(129 * 1024) }, expectedRevision: saved.revision });
      document.getElementById('result').textContent = 'Unexpectedly accepted';
    } catch {
      document.getElementById('result').textContent = 'Oversized draft rejected';
    }
  };
});
</script></body></html>`;
  const app = await installProbe(request, baseURL!, id, html);
  const manager = await openManager(page);
  await choose(manager.getByRole('button', { name: 'Refresh', exact: true }));
  await choose(
    manager
      .locator(`[aria-label="${id} actions"]`)
      .getByRole('button', { name: 'Open', exact: true }),
  );
  const win = page.locator(`[data-app-id="extension.${id}"]`);
  const frame = win.frameLocator('iframe');
  await choose(
    frame.getByRole('button', { name: 'Create oversized draft', exact: true }),
  );
  await expect(frame.getByRole('status')).toHaveText(
    'Oversized draft rejected',
  );
  const status = win.locator('.toolbar-caption');
  await expect(status).not.toHaveText('Saved');
  const close = win.getByRole('button', { name: `Close ${id}`, exact: true });
  await choose(close);
  const guard = page.getByRole('dialog', { name: 'Unsaved application data' });
  await expect(guard).toBeVisible();
  await choose(guard.getByRole('button', { name: 'Cancel', exact: true }));
  await expect(guard).toBeHidden();
  await expect(win).toHaveCount(1);
  await expect(status).not.toHaveText('Saved');
  await choose(close);
  await choose(guard.getByRole('button', { name: 'Save', exact: true }));
  const failure = page.getByRole('dialog', { name: 'Application not saved' });
  await expect(failure).toBeVisible();
  await expect(win).toHaveCount(1);
  await choose(failure.getByRole('button', { name: 'OK', exact: true }));
  await expect(status).not.toHaveText('Saved');
  const data = await request.get(`/api/extensions/${id}/data`, {
    headers: { 'X-AXV-Extension-Store': app.storeId },
  });
  expect(data.ok()).toBeTruthy();
  expect((await data.json()).value).toBeNull();
  await choose(close);
  await choose(guard.getByRole('button', { name: 'Discard', exact: true }));
  await expect(win).toHaveCount(0);
});

test('temporarily missing application definitions preserve windows, Dock and Clip across reload', async ({
  page,
  request,
}) => {
  await expect
    .poll(async () => !!(await persistedLayout(page, request)))
    .toBeTruthy();
  const layout = await persistedLayout(page, request);
  const original = layout.windows[0];
  const id = 'extension.local.missing:original';
  layout.windows.push({
    ...original,
    id,
    appId: 'extension.local.missing',
    title: 'Retained application',
    params: { resource: 'original' },
    viewState: { selected: 'keep-me' },
    minimized: false,
  });
  layout.dock.push({ appId: 'extension.local.missing', pinned: true });
  layout.desktopApplications = { 'extension.local.missing': true };
  layout.workspaces[0].clip.push({
    id: 'missing-shortcut',
    appId: 'extension.local.missing',
    title: 'Retained shortcut',
    params: { resource: 'original' },
  });
  const clientId = await page.evaluate(() =>
    localStorage.getItem('nextstep-browser-id'),
  );
  expect(
    (await request.put('/api/layout', { data: { clientId, layout } })).ok(),
  ).toBeTruthy();
  await page.reload();
  const missing = page.locator(`[data-window-id="${id}"]`);
  await page
    .locator('[data-dock-app="extension.local.missing"] .ns-dock-tile')
    .click();
  await expect(
    missing.getByText('Application unavailable', { exact: true }),
  ).toBeVisible();
  await expect(
    page.locator('[data-dock-app="extension.local.missing"]'),
  ).toBeVisible();
  await expect
    .poll(async () => {
      const saved = await persistedLayout(page, request);
      return saved.windows.find((win: { id: string }) => win.id === id)
        ?.viewState;
    })
    .toEqual({ selected: 'keep-me' });
  const retained = await persistedLayout(page, request);
  expect(retained.desktopApplications['extension.local.missing']).toBe(true);
  await expect(
    page.locator('[data-icon-id="application:extension.local.missing"]'),
  ).toBeVisible();
  expect(
    retained.workspaces[0].clip.some(
      (item: { id: string }) => item.id === 'missing-shortcut',
    ),
  ).toBeTruthy();
});
