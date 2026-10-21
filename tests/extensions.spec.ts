import { readFile } from 'node:fs/promises';
import type {
  APIRequestContext,
  Locator,
  Page,
  TestInfo,
} from '@playwright/test';
import type {
  ExtensionCatalog,
  ExtensionData,
  ExtensionManifest,
  ExtensionPackage,
} from '../apps/dashboard/src/features/extensions/types';
import {
  actionMenu,
  activate,
  expect,
  menuItem,
  persistedLayout,
  test,
} from './helpers';

const id = 'local.checklist';
const extensionHeaders = (storeId: string) => ({
  Origin: new URL(process.env.E2E_BASE_URL!).origin,
  'X-AXV-Extension-Store': storeId,
});
const managerWindow = (page: Page) =>
  page.locator('[data-app-id="applications"]');
const checklistWindow = (page: Page) =>
  page.locator('[data-app-id="extension.local.checklist"]');
const checklistFrame = (page: Page) =>
  checklistWindow(page).frameLocator('iframe');

async function choose(target: Locator, touch: boolean) {
  if (touch) await target.tap();
  else await target.click();
}

async function packageFixture(version = '1.0.0'): Promise<ExtensionPackage> {
  const manifest = JSON.parse(
    await readFile(
      new URL(
        '../examples/extensions/checklist/manifest.json',
        import.meta.url,
      ),
      'utf8',
    ),
  ) as ExtensionManifest;
  const sdk = await readFile(
    new URL('../examples/extensions/sdk.js', import.meta.url),
    'utf8',
  );
  const template = await readFile(
    new URL('../examples/extensions/checklist/index.html', import.meta.url),
    'utf8',
  );
  return {
    manifest: { ...manifest, version },
    html: template.replace('/* AXV_EXTENSION_SDK */', () => sdk),
  };
}

async function catalog(request: APIRequestContext) {
  const response = await request.get('/api/extensions');
  expect(response.ok()).toBeTruthy();
  return (await response.json()) as ExtensionCatalog;
}

async function savedData(request: APIRequestContext, expectedStoreId?: string) {
  const storeId = expectedStoreId ?? (await catalog(request)).storeId;
  const response = await request.get(`/api/extensions/${id}/data`, {
    headers: extensionHeaders(storeId),
  });
  expect(response.ok()).toBeTruthy();
  return (await response.json()) as ExtensionData;
}

async function installFixture(request: APIRequestContext) {
  const current = await catalog(request);
  const previous = current.apps.find((app) => app.id === id);
  const response = await request.post('/api/extensions/install', {
    headers: extensionHeaders(current.storeId),
    data: {
      package: await packageFixture(),
      expectedGeneration: previous?.generation,
    },
  });
  expect(response.ok()).toBeTruthy();
}

async function openManager(page: Page, touch: boolean) {
  const manager = managerWindow(page);
  if (await manager.count()) {
    await activate(page, manager);
  } else {
    await choose(
      page.getByRole('button', { name: 'Workspace menu', exact: true }),
      touch,
    );
    await choose(menuItem(page, 'Applications'), touch);
    await choose(menuItem(page, 'Application Manager'), touch);
  }
  await expect(manager).toBeVisible();
  return manager;
}

function applicationRow(page: Page) {
  return managerWindow(page)
    .locator('.applications-row')
    .filter({
      has: page.getByRole('heading', { name: 'Checklist', exact: true }),
    });
}

async function expandAddPackage(page: Page, touch: boolean) {
  const add = managerWindow(page).getByRole('button', {
    name: 'Add package',
    exact: true,
  });
  if ((await add.getAttribute('aria-expanded')) !== 'true')
    await choose(add, touch);
  await expect(
    managerWindow(page).getByLabel('Extension package'),
  ).toBeVisible();
}

async function applicationAction(page: Page, label: string, touch: boolean) {
  await actionMenu(
    page,
    applicationRow(page),
    `More actions for Checklist, ${id}`,
    label,
    touch,
  );
}

async function stageExample(page: Page, touch: boolean, info?: TestInfo) {
  const manager = managerWindow(page);
  await expandAddPackage(page, touch);
  await choose(
    manager.getByRole('button', { name: 'Install Checklist', exact: true }),
    touch,
  );
  await expect(
    manager.getByRole('heading', { name: 'Review Checklist', exact: true }),
  ).toBeVisible();
  await expect(manager.locator('.applications-review')).toContainText(
    'Private application storage',
  );
  if (info) {
    await manager.locator('.applications-review').scrollIntoViewIfNeeded();
    const screenshot = info.outputPath('application-manager-review.png');
    await manager.screenshot({ path: screenshot });
    await info.attach('application-manager-review', {
      path: screenshot,
      contentType: 'image/png',
    });
  }
  await choose(
    manager.getByRole('button', { name: 'Install', exact: true }),
    touch,
  );
  await expect(applicationRow(page)).toContainText('Enabled');
}

async function openChecklist(page: Page, touch: boolean) {
  await choose(
    applicationRow(page).getByRole('button', { name: 'Open', exact: true }),
    touch,
  );
  await expect(checklistWindow(page)).toBeVisible();
  await expect(checklistFrame(page).getByLabel('New item')).toBeEnabled();
}

async function addItem(page: Page, text: string, touch: boolean) {
  const frame = checklistFrame(page);
  await frame.getByLabel('New item').fill(text);
  await choose(frame.getByRole('button', { name: 'Add', exact: true }), touch);
  await expect(frame.getByRole('textbox', { name: 'Item text' })).toHaveValue(
    text,
  );
  await expect(checklistWindow(page).locator('.toolbar-caption')).toHaveText(
    'Unsaved changes',
  );
}

async function reloadChecklist(page: Page, touch: boolean) {
  await activate(page, checklistWindow(page));
  await choose(
    checklistWindow(page).getByRole('button', { name: 'Reload', exact: true }),
    touch,
  );
  await expect(checklistFrame(page).getByLabel('New item')).toBeEnabled();
}

test.beforeEach(async ({ page, request }) => {
  const current = await catalog(request);
  const app = current.apps.find((item) => item.id === id);
  if (!app) return;
  if (app.installed) {
    const response = await request.delete(
      `/api/extensions/${id}?generation=${app.generation}`,
      { headers: extensionHeaders(current.storeId) },
    );
    expect(response.ok()).toBeTruthy();
  }
  const data = await savedData(request, current.storeId);
  const response = await request.delete(
    `/api/extensions/${id}/data?revision=${data.revision}`,
    { headers: extensionHeaders(current.storeId) },
  );
  expect(response.ok()).toBeTruthy();
  // The desktop fixture loads before this cleanup; start from the cleaned catalog.
  await page.reload();
});

test('Application Manager keeps the directory first, preserves hidden package reviews and exposes secondary actions', async ({
  page,
  request,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  await page.route('**/api/extensions', async (route) => {
    const response = await route.fetch();
    const current = (await response.json()) as ExtensionCatalog;
    await route.fulfill({
      response,
      json: { ...current, apps: current.apps.filter((app) => app.id === id) },
    });
  });
  await page.reload();
  const manager = await openManager(page, touch);
  const add = manager.getByRole('button', { name: 'Add package', exact: true });
  const packageInput = manager.getByLabel('Extension package');
  await expect(add).toHaveAttribute('aria-expanded', 'true');
  await expect(packageInput).toBeVisible();
  await choose(add, touch);
  await expect(packageInput).toBeHidden();
  const refresh = page.waitForResponse('**/api/extensions');
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await (await refresh).finished();
  await expect(add).toHaveAttribute('aria-expanded', 'false');
  await expandAddPackage(page, touch);
  await packageInput.setInputFiles({
    name: 'checklist-review.json',
    mimeType: 'application/json',
    buffer: Buffer.from(JSON.stringify(await packageFixture('2.0.0'))),
  });
  const review = manager.locator('.applications-review');
  await expect(review).toContainText('2.0.0');
  await choose(add, touch);
  await expect(review).toBeHidden();
  await choose(add, touch);
  await expect(review).toBeVisible();
  await expect(review).toContainText('2.0.0');
  await choose(
    review.getByRole('button', { name: 'Cancel', exact: true }),
    touch,
  );
  await expect(review).toHaveCount(0);
  let releaseInstall = () => {};
  const installGate = new Promise<void>((resolve) => {
    releaseInstall = resolve;
  });
  let installObserved = false;
  await page.route('**/api/extensions/install', async (route) => {
    const response = await route.fetch();
    installObserved = true;
    await installGate;
    await route.fulfill({ response });
  });
  const installing = stageExample(page, touch);
  const workspaceMenu = page.getByRole('button', {
    name: 'Workspace menu',
    exact: true,
  });
  try {
    await expect.poll(() => installObserved).toBe(true);
    await expect(add).toBeFocused();
    await workspaceMenu.focus();
  } finally {
    releaseInstall();
    await installing;
    await page.unroute('**/api/extensions/install');
  }
  await expect(workspaceMenu).toBeFocused();
  await expect(packageInput).toBeHidden();
  await expect(add).toHaveAttribute('aria-expanded', 'false');
  const row = applicationRow(page);
  await expect(
    row.getByRole('button', { name: 'Open', exact: true }),
  ).toBeVisible();
  await expect(
    row.getByRole('button', { name: 'Disable', exact: true }),
  ).toHaveCount(0);
  await expect(
    row.getByRole('button', { name: 'Uninstall', exact: true }),
  ).toHaveCount(0);
  await expect(
    row.getByRole('checkbox', { name: 'Show Checklist on desktop' }),
  ).toBeVisible();
  const screenshot = info.outputPath('application-manager-directory.png');
  await manager.screenshot({ path: screenshot });
  await info.attach('application-manager-directory', {
    path: screenshot,
    contentType: 'image/png',
  });
  const more = row.getByRole('button', {
    name: `More actions for Checklist, ${id}`,
    exact: true,
  });
  await more.focus();
  await more.press('Enter');
  await expect(menuItem(page, 'Disable')).toBeVisible();
  await expect(menuItem(page, 'Export saved data')).toBeVisible();
  await expect(menuItem(page, 'Uninstall')).toBeVisible();
  const menuScreenshot = info.outputPath('application-manager-more.png');
  await page.screenshot({ path: menuScreenshot });
  await info.attach('application-manager-more', {
    path: menuScreenshot,
    contentType: 'image/png',
  });
  await page.keyboard.press('Escape');
  await expect(menuItem(page, 'Disable')).toBeHidden();
  const downloadReady = page.waitForEvent('download');
  await applicationAction(page, 'Export saved data', touch);
  const exported = JSON.parse(
    await readFile((await (await downloadReady).path())!, 'utf8'),
  ) as ExtensionData;
  expect(exported.value).toEqual((await savedData(request)).value);
});

test('an open application menu rejects a replaced store without changing its original installation', async ({
  page,
  request,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  await installFixture(request);
  await page.reload();
  const manager = await openManager(page, touch);
  const original = await catalog(request);
  const originalApp = original.apps.find((app) => app.id === id)!;
  const originalData = await savedData(request);
  await choose(
    applicationRow(page).getByRole('button', {
      name: `More actions for Checklist, ${id}`,
      exact: true,
    }),
    touch,
  );
  await expect(menuItem(page, 'Disable')).toBeVisible();
  await page.route('**/api/extensions', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      json: {
        storeId: 'b3a1a9cd-08b2-4bb2-90a3-a06cb3c8ca17',
        apps: [{ ...originalApp, title: 'Replacement Checklist' }],
      },
    });
  });
  let mutations = 0;
  page.on('request', (request) => {
    if (
      request.url().endsWith(`/api/extensions/${id}`) &&
      request.method() === 'PUT'
    )
      mutations++;
  });
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await expect(
    manager.getByRole('heading', {
      name: 'Replacement Checklist',
      exact: true,
    }),
  ).toBeVisible();
  await choose(menuItem(page, 'Disable'), touch);
  await expect(manager.locator('.app-error')).toContainText(
    'The application store changed',
  );
  expect(mutations).toBe(0);
  expect((await catalog(request)).apps.find((app) => app.id === id)).toEqual(
    originalApp,
  );
  expect(await savedData(request)).toEqual(originalData);
});

test('a stale application menu rejects a changed installation before prompting to discard its draft', async ({
  page,
  request,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  await installFixture(request);
  await page.reload();
  await openManager(page, touch);
  await openChecklist(page, touch);
  const text = 'Keep the draft when an old application menu becomes stale';
  await addItem(page, text, touch);
  const manager = await openManager(page, touch);
  const original = await catalog(request);
  const originalApp = original.apps.find((app) => app.id === id)!;
  const originalData = await savedData(request);
  await choose(
    applicationRow(page).getByRole('button', {
      name: `More actions for Checklist, ${id}`,
      exact: true,
    }),
    touch,
  );
  await expect(menuItem(page, 'Disable')).toBeVisible();
  const changed = await request.put(`/api/extensions/${id}`, {
    headers: extensionHeaders(original.storeId),
    data: { enabled: false, expectedGeneration: originalApp.generation },
  });
  expect(changed.ok()).toBeTruthy();
  const changedApp = (await catalog(request)).apps.find(
    (app) => app.id === id,
  )!;
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await expect(
    applicationRow(page).getByRole('button', { name: 'Enable', exact: true }),
  ).toBeVisible();
  await choose(menuItem(page, 'Disable'), touch);
  await expect(manager.locator('.app-error')).toContainText(
    /application.*changed/i,
  );
  await expect(
    page.getByRole('dialog', { name: 'Unsaved application data' }),
  ).toHaveCount(0);
  expect((await catalog(request)).apps.find((app) => app.id === id)).toEqual(
    changedApp,
  );
  expect(await savedData(request)).toEqual(originalData);
  await expect(checklistWindow(page).locator('.toolbar-caption')).toHaveText(
    'Unsaved changes',
  );
  await activate(page, checklistWindow(page));
  const downloadReady = page.waitForEvent('download');
  await choose(
    checklistWindow(page).getByRole('button', {
      name: 'Export draft',
      exact: true,
    }),
    touch,
  );
  const exported = JSON.parse(
    await readFile((await (await downloadReady).path())!, 'utf8'),
  ) as { value: { items: { text: string }[] } };
  expect(exported.value.items[0]?.text).toBe(text);
});

test('Applications installs, saves, upgrades and removes an extension while preserving its data and window', async ({
  page,
  request,
}, info) => {
  test.setTimeout(90_000);
  const touch = info.project.use.hasTouch === true;
  const manager = await openManager(page, touch);
  await expect
    .poll(
      async () =>
        (await persistedLayout(page, request))?.dock.find(
          (entry: { appId: string; pinned: boolean }) =>
            entry.appId === 'applications',
        )?.pinned,
    )
    .toBe(false);
  await stageExample(page, touch, info);
  await openChecklist(page, touch);
  const retainedWindow =
    await checklistWindow(page).getAttribute('data-window-id');
  const frame = checklistFrame(page);
  await addItem(page, 'Review the application boundary', touch);
  await choose(frame.getByRole('button', { name: 'Save', exact: true }), touch);
  await expect(frame.getByRole('status')).toHaveText('Saved');
  const savedScreenshot = info.outputPath('checklist-saved.png');
  await checklistWindow(page).screenshot({ path: savedScreenshot });
  await info.attach('checklist-saved', {
    path: savedScreenshot,
    contentType: 'image/png',
  });
  await reloadChecklist(page, touch);
  await expect(frame.getByRole('textbox', { name: 'Item text' })).toHaveValue(
    'Review the application boundary',
  );
  await frame
    .getByRole('textbox', { name: 'Item text' })
    .fill('Keep this draft before disabling');
  await expect(checklistWindow(page).locator('.toolbar-caption')).toHaveText(
    'Unsaved changes',
  );
  await openManager(page, touch);
  await applicationAction(page, 'Disable', touch);
  const guard = page.getByRole('dialog', { name: 'Unsaved application data' });
  await expect(guard).toBeVisible();
  await choose(
    guard.getByRole('button', { name: 'Cancel', exact: true }),
    touch,
  );
  await expect(applicationRow(page)).toContainText('Enabled');
  await applicationAction(page, 'Disable', touch);
  await choose(guard.getByRole('button', { name: 'Save', exact: true }), touch);
  await expect(applicationRow(page)).toContainText('Disabled');
  await activate(page, checklistWindow(page));
  await expect(checklistWindow(page)).toContainText(
    'This application is disabled',
  );
  await expect(checklistWindow(page).locator('iframe')).toHaveCount(0);
  await expect(checklistWindow(page)).toHaveAttribute(
    'data-window-id',
    retainedWindow!,
  );
  await openManager(page, touch);
  await choose(
    applicationRow(page).getByRole('button', { name: 'Enable', exact: true }),
    touch,
  );
  await reloadChecklist(page, touch);
  await expect(frame.getByRole('textbox', { name: 'Item text' })).toHaveValue(
    'Keep this draft before disabling',
  );
  await openManager(page, touch);
  await expandAddPackage(page, touch);
  await manager.getByLabel('Extension package').setInputFiles({
    name: 'checklist-2.json',
    mimeType: 'application/json',
    buffer: Buffer.from(JSON.stringify(await packageFixture('2.0.0'))),
  });
  await expect(manager.locator('.applications-review')).toContainText(
    '1.0.0 → 2.0.0',
  );
  await choose(
    manager.getByRole('button', { name: 'Replace package', exact: true }),
    touch,
  );
  await expect(applicationRow(page)).toContainText('2.0.0');
  await reloadChecklist(page, touch);
  await expect(frame.getByRole('textbox', { name: 'Item text' })).toHaveValue(
    'Keep this draft before disabling',
  );
  await openManager(page, touch);
  await applicationAction(page, 'Uninstall', touch);
  await choose(
    page
      .getByRole('dialog')
      .getByRole('button', { name: 'Uninstall', exact: true }),
    touch,
  );
  await expect(applicationRow(page)).toContainText('Uninstalled');
  const downloadReady = page.waitForEvent('download');
  await choose(
    applicationRow(page).getByRole('button', {
      name: 'Export saved data',
      exact: true,
    }),
    touch,
  );
  const download = await downloadReady;
  const exported = JSON.parse(
    await readFile((await download.path())!, 'utf8'),
  ) as {
    value: { items: { text: string }[] };
  };
  expect(exported.value.items[0]?.text).toBe(
    'Keep this draft before disabling',
  );
  await stageExample(page, touch);
  await reloadChecklist(page, touch);
  await expect(frame.getByRole('textbox', { name: 'Item text' })).toHaveValue(
    'Keep this draft before disabling',
  );
  await expect(checklistWindow(page)).toHaveAttribute(
    'data-window-id',
    retainedWindow!,
  );
  await openManager(page, touch);
  await applicationAction(page, 'Uninstall', touch);
  await choose(
    page
      .getByRole('dialog')
      .getByRole('button', { name: 'Uninstall', exact: true }),
    touch,
  );
  await expect(applicationRow(page)).toContainText('Uninstalled');
  await applicationAction(page, 'Delete data', touch);
  const deletion = page.getByRole('dialog', { name: 'Delete Checklist data?' });
  await expect(deletion).toContainText('This cannot be undone');
  await choose(
    deletion.getByRole('button', { name: 'Cancel', exact: true }),
    touch,
  );
  expect((await savedData(request)).value).not.toBeNull();
  await applicationAction(page, 'Delete data', touch);
  await choose(
    deletion.getByRole('button', { name: 'Delete data', exact: true }),
    touch,
  );
  await expect.poll(async () => (await savedData(request)).value).toBeNull();
});

test('application icons follow explicit desktop choices across reload, hiding and reinstall', async ({
  page,
  request,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const managerIcon = page.locator('[data-icon-id="application:applications"]');
  const checklistIcon = page.locator(
    '[data-icon-id="application:extension.local.checklist"]',
  );
  for (const appId of ['files', 'welcome'])
    await choose(
      page
        .locator(`[data-app-id="${appId}"]`)
        .getByRole('button', { name: /^Close / }),
      touch,
    );
  await expect(managerIcon).toBeVisible();
  await expect(checklistIcon).toHaveCount(0);
  const openIcon = async (icon: Locator) => {
    await icon.scrollIntoViewIfNeeded();
    if (touch) {
      await icon.tap();
      await icon.tap();
    } else await icon.dblclick();
  };
  await openIcon(managerIcon);
  const manager = managerWindow(page);
  await expect(manager).toBeVisible();
  await stageExample(page, touch);
  const installed = (await catalog(request)).apps.find((app) => app.id === id)!;
  expect(installed.icon).toBe('checklist');
  const originalData = await savedData(request);
  const checkbox = applicationRow(page).getByRole('checkbox', {
    name: 'Show Checklist on desktop',
  });
  await expect(checkbox).not.toBeChecked();
  await choose(checkbox, touch);
  await expect(checkbox).toBeChecked();
  await expect(checklistIcon).toBeVisible();
  await expect
    .poll(
      async () =>
        (await persistedLayout(page, request))?.desktopApplications?.[
          'extension.local.checklist'
        ],
    )
    .toBe(true);
  const managerScreenshot = info.outputPath('application-manager-icons.png');
  await manager.screenshot({ path: managerScreenshot });
  await info.attach('application-manager-icons', {
    path: managerScreenshot,
    contentType: 'image/png',
  });
  await choose(
    manager.getByRole('button', {
      name: 'Close Application Manager',
      exact: true,
    }),
    touch,
  );
  await choose(
    page
      .getByRole('navigation', { name: 'Application menus' })
      .getByRole('button', { name: 'View', exact: true }),
    touch,
  );
  await choose(menuItem(page, 'Arrange Icons'), touch);
  const positions = page.viewportSize()!.width < 600 ? 'compactIcons' : 'icons';
  await expect
    .poll(
      async () =>
        (await persistedLayout(page, request))?.[positions]?.[
          'application:extension.local.checklist'
        ],
    )
    .toBeTruthy();
  const recordedPosition = (await persistedLayout(page, request))[positions][
    'application:extension.local.checklist'
  ];
  await openIcon(checklistIcon);
  await expect(checklistFrame(page).getByLabel('New item')).toBeEnabled();
  await choose(
    checklistWindow(page).getByRole('button', {
      name: 'Close Checklist',
      exact: true,
    }),
    touch,
  );
  await checklistIcon.scrollIntoViewIfNeeded();
  const desktopScreenshot = info.outputPath('application-icons-desktop.png');
  await page.screenshot({ path: desktopScreenshot });
  await info.attach('application-icons-desktop', {
    path: desktopScreenshot,
    contentType: 'image/png',
  });
  await openIcon(managerIcon);
  await choose(checkbox, touch);
  await expect(checklistIcon).toHaveCount(0);
  await choose(manager.locator('.applications-builtins summary'), touch);
  const managerCheckbox = manager.getByRole('checkbox', {
    name: 'Show Application Manager on desktop',
  });
  await expect(managerCheckbox).toBeChecked();
  await choose(managerCheckbox, touch);
  await expect(managerIcon).toHaveCount(0);
  await expect(manager).toBeVisible();
  await expect
    .poll(
      async () => (await persistedLayout(page, request))?.desktopApplications,
    )
    .toMatchObject({ applications: false, 'extension.local.checklist': false });
  await page.reload();
  await expect(manager).toBeVisible();
  await expect(managerIcon).toHaveCount(0);
  await expect(checkbox).not.toBeChecked();
  await choose(checkbox, touch);
  await expect(checklistIcon).toBeVisible();
  await expect
    .poll(
      async () =>
        (await persistedLayout(page, request))?.[positions]?.[
          'application:extension.local.checklist'
        ],
    )
    .toEqual(recordedPosition);
  expect((await catalog(request)).apps.find((app) => app.id === id)).toEqual(
    installed,
  );
  expect(await savedData(request)).toEqual(originalData);
  await applicationAction(page, 'Uninstall', touch);
  await choose(
    page
      .getByRole('dialog', { name: 'Uninstall Checklist?' })
      .getByRole('button', { name: 'Uninstall', exact: true }),
    touch,
  );
  await expect(
    manager.getByRole('heading', {
      name: 'Uninstalled applications',
      exact: true,
    }),
  ).toBeVisible();
  await expect(checklistIcon).toBeVisible();
  await expect(
    manager.getByRole('checkbox', { name: 'Show Checklist on desktop' }),
  ).toBeChecked();
  await stageExample(page, touch);
  await expect(checkbox).toBeChecked();
  await expect(checklistIcon).toBeVisible();
  expect(await savedData(request)).toEqual(originalData);
});

test('a browser reload offers explicit draft recovery before restoring the original unsaved list', async ({
  page,
  request,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  await installFixture(request);
  await page.reload();
  await openManager(page, touch);
  await openChecklist(page, touch);
  await addItem(page, 'Recover this unsaved item', touch);
  const windowId = await checklistWindow(page).getAttribute('data-window-id');
  await expect
    .poll(async () =>
      (await persistedLayout(page, request))?.windows.some(
        (window: { id: string }) => window.id === windowId,
      ),
    )
    .toBe(true);
  page.once('dialog', (dialog) => void dialog.accept());
  await page.reload();
  const checklist = checklistWindow(page);
  await expect(
    checklist.getByText('A draft was recovered', { exact: false }),
  ).toBeVisible();
  await expect(checklist.locator('iframe')).toHaveCount(0);
  await choose(
    checklist.getByRole('button', { name: 'Restore draft', exact: true }),
    touch,
  );
  const frame = checklistFrame(page);
  await expect(frame.getByRole('textbox', { name: 'Item text' })).toHaveValue(
    'Recover this unsaved item',
  );
  expect((await savedData(request)).value).toBeNull();
  await choose(frame.getByRole('button', { name: 'Save', exact: true }), touch);
  await expect(frame.getByRole('status')).toHaveText('Saved');
  await expect(checklist.locator('.toolbar-caption')).toHaveText('Saved');
});

test('Save preserves later typing and rejects a conflicting write until the user reloads saved data', async ({
  page,
  request,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  await installFixture(request);
  await page.reload();
  await openManager(page, touch);
  await openChecklist(page, touch);
  await addItem(page, 'Captured save snapshot', touch);
  let releaseSave = () => {};
  let saveObserved = false;
  const saveGate = new Promise<void>((resolve) => {
    releaseSave = resolve;
  });
  await page.route(`**/api/extensions/${id}/sessions/*/rpc`, async (route) => {
    const body = route.request().postDataJSON() as { method: string };
    if (body.method !== 'storage.set') return route.continue();
    const response = await route.fetch();
    saveObserved = true;
    await saveGate;
    await route.fulfill({ response });
  });
  const frame = checklistFrame(page);
  try {
    await choose(
      frame.getByRole('button', { name: 'Save', exact: true }),
      touch,
    );
    await expect.poll(() => saveObserved).toBe(true);
    await frame
      .getByRole('textbox', { name: 'Item text' })
      .fill('Typing after the saved snapshot');
    await expect
      .poll(() =>
        page.evaluate(() =>
          Object.keys(localStorage).some(
            (key) =>
              key.startsWith('axv-extension-draft:') &&
              localStorage
                .getItem(key)
                ?.includes('Typing after the saved snapshot'),
          ),
        ),
      )
      .toBe(true);
  } finally {
    releaseSave();
  }
  await expect(frame.getByRole('status')).toHaveText('Unsaved changes');
  await expect(frame.getByRole('textbox', { name: 'Item text' })).toHaveValue(
    'Typing after the saved snapshot',
  );
  const firstSave = await savedData(request);
  expect(
    (firstSave.value as { items: { text: string }[] }).items[0]?.text,
  ).toBe('Captured save snapshot');
  const current = await catalog(request);
  const app = current.apps.find((item) => item.id === id)!;
  const created = await request.post(`/api/extensions/${id}/sessions`, {
    headers: extensionHeaders(current.storeId),
    data: {
      digest: app.digest,
      generation: app.generation,
      clientId: 'E2E-other-browser',
      windowId: 'E2E-other-window',
    },
  });
  expect(created.ok()).toBeTruthy();
  const { sessionId } = (await created.json()) as { sessionId: string };
  try {
    const written = await request.post(
      `/api/extensions/${id}/sessions/${sessionId}/rpc`,
      {
        headers: extensionHeaders(current.storeId),
        data: {
          method: 'storage.set',
          params: {
            expectedRevision: firstSave.revision,
            value: {
              schemaVersion: 1,
              items: [
                {
                  id: 'other-window',
                  text: 'Another window saved this',
                  done: false,
                },
              ],
              entry: '',
            },
          },
        },
      },
    );
    expect(written.ok()).toBeTruthy();
    await choose(
      frame.getByRole('button', { name: 'Save', exact: true }),
      touch,
    );
    await expect(
      frame.getByRole('alert').filter({ hasText: 'Save failed' }),
    ).toBeVisible();
    await expect(frame.getByRole('textbox', { name: 'Item text' })).toHaveValue(
      'Typing after the saved snapshot',
    );
    await expect(frame.getByRole('status')).toHaveText('Unsaved changes');
    await choose(
      frame.getByRole('button', { name: 'Reload saved', exact: true }),
      touch,
    );
    await choose(
      frame.getByRole('button', { name: 'Keep editing', exact: true }),
      touch,
    );
    await expect(frame.getByRole('textbox', { name: 'Item text' })).toHaveValue(
      'Typing after the saved snapshot',
    );
    await choose(
      frame.getByRole('button', { name: 'Reload saved', exact: true }),
      touch,
    );
    await choose(
      frame.getByRole('button', { name: 'Discard and reload', exact: true }),
      touch,
    );
    await expect(frame.getByRole('textbox', { name: 'Item text' })).toHaveValue(
      'Another window saved this',
    );
    await expect(frame.getByRole('status')).toHaveText('Saved');
  } finally {
    await request.delete(`/api/extensions/${id}/sessions/${sessionId}`, {
      headers: extensionHeaders(current.storeId),
    });
  }
});

test('Checklist rejects oversized edits while preserving accepted text, recovery and saved data', async ({
  page,
  request,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  await installFixture(request);
  await page.reload();
  await openManager(page, touch);
  await openChecklist(page, touch);
  await addItem(page, 'Previously saved item', touch);
  const frame = checklistFrame(page);
  await choose(frame.getByRole('button', { name: 'Save', exact: true }), touch);
  await expect(frame.getByRole('status')).toHaveText('Saved');
  const originalSaved = await savedData(request);
  const acceptedItem = 'Keep this accepted unsaved item';
  const acceptedEntry = 'Keep this accepted pending entry';
  const itemText = frame.getByRole('textbox', { name: 'Item text' });
  const entry = frame.getByLabel('New item');
  await itemText.fill(acceptedItem);
  await entry.fill(acceptedEntry);
  const acceptedRecovery = () =>
    page.evaluate(
      ({ item, pendingEntry }) => {
        for (const [key, raw] of Object.entries(localStorage)) {
          if (!key.startsWith('axv-extension-draft:') || !raw || raw === 'null')
            continue;
          const draft = JSON.parse(raw) as {
            value?: { items?: { text: string }[]; entry?: string };
          };
          if (
            draft.value?.items?.[0]?.text === item &&
            draft.value.entry === pendingEntry
          )
            return raw;
        }
        return '';
      },
      { item: acceptedItem, pendingEntry: acceptedEntry },
    );
  await expect.poll(acceptedRecovery).not.toBe('');
  const recovery = await acceptedRecovery();
  const acceptedValue = (
    JSON.parse(recovery) as {
      value: {
        schemaVersion: number;
        items: { id: string; text: string; done: boolean }[];
        entry: string;
      };
    }
  ).value;
  const targetBytes = 129 * 1024;
  const emptyEntry = { ...acceptedValue, entry: '' };
  const oversizedEntry = 'x'.repeat(
    targetBytes - Buffer.byteLength(JSON.stringify(emptyEntry)),
  );
  expect(
    Buffer.byteLength(JSON.stringify({ ...emptyEntry, entry: oversizedEntry })),
  ).toBe(targetBytes);
  await entry.fill(oversizedEntry);
  await expect(entry).toHaveValue(acceptedEntry);
  await expect(itemText).toHaveValue(acceptedItem);
  const error = frame.getByRole('alert').filter({ hasText: '128 KiB' });
  await expect(error).toBeVisible();
  await expect(error).toContainText('was not applied');
  expect(await acceptedRecovery()).toBe(recovery);
  expect(await savedData(request)).toEqual(originalSaved);
  const emptyItem = {
    ...acceptedValue,
    items: acceptedValue.items.map((item) => ({ ...item, text: '' })),
  };
  const oversizedItem = 'x'.repeat(
    targetBytes - Buffer.byteLength(JSON.stringify(emptyItem)),
  );
  expect(
    Buffer.byteLength(
      JSON.stringify({
        ...emptyItem,
        items: emptyItem.items.map((item) => ({
          ...item,
          text: oversizedItem,
        })),
      }),
    ),
  ).toBe(targetBytes);
  await itemText.fill(oversizedItem);
  await expect(itemText).toHaveValue(acceptedItem);
  await expect(entry).toHaveValue(acceptedEntry);
  await expect(error).toBeVisible();
  expect(await acceptedRecovery()).toBe(recovery);
  expect(await savedData(request)).toEqual(originalSaved);
  await expect(checklistWindow(page).locator('.toolbar-caption')).toHaveText(
    'Unsaved changes',
  );
  await entry.fill('Continue editing after the rejected change');
  await expect(error).toBeHidden();
  await itemText.fill('Normal edit after the rejected change');
  await choose(frame.getByRole('button', { name: 'Save', exact: true }), touch);
  await expect(frame.getByRole('status')).toHaveText('Saved');
  await expect(checklistWindow(page).locator('.toolbar-caption')).toHaveText(
    'Saved',
  );
  const saved = await savedData(request);
  expect(saved.revision).toBe(originalSaved.revision + 1);
  expect(saved.value).toMatchObject({
    items: [{ text: 'Normal edit after the rejected change' }],
    entry: 'Continue editing after the rejected change',
  });
});

test('a different application store retains the original window and its exportable unsaved draft', async ({
  page,
  request,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const text = 'Retain this draft from the original application store';
  await installFixture(request);
  await page.reload();
  await openManager(page, touch);
  await openChecklist(page, touch);
  await addItem(page, text, touch);
  const win = checklistWindow(page);
  const retainedId = await win.getAttribute('data-window-id');
  const original = await catalog(request);
  const differentStoreId = 'b3a1a9cd-08b2-4bb2-90a3-a06cb3c8ca17';
  expect(original.storeId).not.toBe(differentStoreId);
  let replacementObserved = false;
  await page.route('**/api/extensions', async (route) => {
    if (route.request().method() !== 'GET') return route.continue();
    replacementObserved = true;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ storeId: differentStoreId, apps: [] }),
    });
  });
  await expect
    .poll(async () => {
      if (!replacementObserved)
        await page.evaluate(() => window.dispatchEvent(new Event('focus')));
      return replacementObserved;
    })
    .toBe(true);
  await expect(win).toHaveAttribute('data-window-id', retainedId!);
  await expect(win).toContainText('The application store changed');
  await expect(win.locator('iframe')).toHaveCount(0);
  await expect(win.locator('.toolbar-caption')).toHaveText('Unsaved changes');
  const downloadReady = page.waitForEvent('download');
  await choose(
    win.getByRole('button', { name: 'Export draft', exact: true }),
    touch,
  );
  const download = await downloadReady;
  const exported = JSON.parse(
    await readFile((await download.path())!, 'utf8'),
  ) as { value: { items: { text: string }[] } };
  expect(exported.value.items[0]?.text).toBe(text);
  await choose(
    win.getByRole('button', { name: 'Close Checklist', exact: true }),
    touch,
  );
  const guard = page.getByRole('dialog', { name: 'Unsaved application data' });
  await expect(guard).toBeVisible();
  await choose(
    guard.getByRole('button', { name: 'Cancel', exact: true }),
    touch,
  );
  await expect(guard).toBeHidden();
  await expect(win).toHaveAttribute('data-window-id', retainedId!);
  await expect(
    win.getByRole('button', { name: 'Export draft', exact: true }),
  ).toBeVisible();
});

test('a pending recovery claim cannot be rebound to a replacement application store', async ({
  page,
  request,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  await installFixture(request);
  const original = await catalog(request);
  await page.addInitScript(
    ({ storeId }) => {
      if (window.parent !== window) return;
      const control = window as Window & {
        __axvExtensionClaimPaused?: boolean;
        __axvReleaseExtensionClaim?: () => void;
        __axvExtensionLockEvidence?: {
          stableManager: boolean;
          targetClaims: number;
        };
      };
      const manager = navigator.locks;
      const prototype = Object.getPrototypeOf(manager) as LockManager;
      const nativeRequest = prototype.request;
      control.__axvExtensionLockEvidence = {
        stableManager: manager === navigator.locks,
        targetClaims: 0,
      };
      let release!: () => void;
      const gate = new Promise<void>((resolve) => {
        release = resolve;
      });
      control.__axvReleaseExtensionClaim = release;
      // Interpose the shared prototype so the later asynchronous claim uses this hook,
      // preserving the native receiver.
      Object.defineProperty(prototype, 'request', {
        ...Object.getOwnPropertyDescriptor(prototype, 'request'),
        value(
          this: LockManager,
          name: string,
          options: LockOptions | LockGrantedCallback<unknown>,
          callback?: LockGrantedCallback<unknown>,
        ) {
          const acquire = () =>
            Reflect.apply(
              nativeRequest,
              this,
              typeof options === 'function'
                ? [name, options]
                : [name, options, callback],
            ) as Promise<unknown>;
          let scope: unknown;
          try {
            scope = JSON.parse(name) as unknown;
          } catch {
            return acquire();
          }
          if (
            Array.isArray(scope) &&
            scope[0] === 'nextstep-draft' &&
            scope[1] === `extensions:${storeId}`
          ) {
            control.__axvExtensionClaimPaused = true;
            control.__axvExtensionLockEvidence!.targetClaims += 1;
            return gate.then(acquire);
          }
          return acquire();
        },
      });
    },
    { storeId: original.storeId },
  );
  const sessionRequests: string[] = [];
  page.on('request', (outgoing) => {
    if (
      outgoing.method() === 'POST' &&
      new URL(outgoing.url()).pathname === `/api/extensions/${id}/sessions`
    )
      sessionRequests.push(outgoing.headers()['x-axv-extension-store'] || '');
  });
  await page.reload();
  await openManager(page, touch);
  await choose(
    applicationRow(page).getByRole('button', { name: 'Open', exact: true }),
    touch,
  );
  const win = checklistWindow(page);
  const retainedId = await win.getAttribute('data-window-id');
  await expect(win).toContainText('Preparing application recovery');
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as Window & { __axvExtensionClaimPaused?: boolean })
            .__axvExtensionClaimPaused === true,
      ),
    )
    .toBe(true);
  await info.attach('extension-recovery-lock-interposer', {
    contentType: 'application/json',
    body: JSON.stringify(
      await page.evaluate(
        () =>
          (
            window as Window & {
              __axvExtensionLockEvidence?: {
                stableManager: boolean;
                targetClaims: number;
              };
            }
          ).__axvExtensionLockEvidence,
      ),
    ),
  });
  const differentStoreId = 'eb8de9e4-10a5-49cd-aee4-4c998a8fe985';
  expect(original.storeId).not.toBe(differentStoreId);
  let replacementObserved = false;
  await page.route('**/api/extensions', async (route) => {
    if (route.request().method() !== 'GET') return route.continue();
    replacementObserved = true;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...original, storeId: differentStoreId }),
    });
  });
  const replacement = page.waitForResponse(async (response) => {
    if (
      new URL(response.url()).pathname !== '/api/extensions' ||
      response.request().method() !== 'GET' ||
      !response.ok()
    )
      return false;
    const value = (await response.json()) as ExtensionCatalog;
    return value.storeId === differentStoreId;
  });
  await expect
    .poll(async () => {
      if (!replacementObserved)
        await page.evaluate(() => window.dispatchEvent(new Event('focus')));
      return replacementObserved;
    })
    .toBe(true);
  await (await replacement).finished();
  // Let the catalog update commit while the original recovery claim is still held.
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      ),
  );
  await expect(win).toContainText('Preparing application recovery');
  await page.evaluate(() => {
    const release = (
      window as Window & { __axvReleaseExtensionClaim?: () => void }
    ).__axvReleaseExtensionClaim;
    if (!release)
      throw new Error('The application recovery claim was not paused.');
    release();
  });
  await expect(win).toHaveAttribute('data-window-id', retainedId!);
  await expect(win).toContainText('The application store changed');
  await expect(win.locator('iframe')).toHaveCount(0);
  expect(sessionRequests).toEqual([]);
});

test('two real tabs isolate drafts for one retained window and recover a stale draft without rebasing it', async ({
  page,
  request,
  context,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const firstText = 'Unsaved Checklist A belongs to the first tab';
  const secondText = 'Unsaved Checklist B belongs to the second tab';
  await installFixture(request);
  await page.reload();
  await openManager(page, touch);
  await openChecklist(page, touch);
  await addItem(page, firstText, touch);
  const windowId = await checklistWindow(page).getAttribute('data-window-id');
  const originalRevision = (await savedData(request)).revision;
  await expect
    .poll(async () =>
      (await persistedLayout(page, request))?.windows.some(
        (window: { id: string }) => window.id === windowId,
      ),
    )
    .toBe(true);
  const storedDrafts = () =>
    page.evaluate(() =>
      Object.entries(localStorage)
        .filter(
          ([key, raw]) =>
            key.startsWith('axv-extension-draft:') && raw && raw !== 'null',
        )
        .map(([key, raw]) => ({
          key,
          draft: JSON.parse(raw) as {
            expectedRevision: number;
            value: { items: { text: string }[] };
          },
        })),
    );
  await expect
    .poll(async () =>
      (await storedDrafts()).some(
        ({ draft }) => draft.value.items[0]?.text === firstText,
      ),
    )
    .toBe(true);
  const firstDraft = (await storedDrafts()).find(
    ({ draft }) => draft.value.items[0]?.text === firstText,
  )!;
  expect(firstDraft.draft.expectedRevision).toBe(originalRevision);
  const clone = await page.evaluate(() => ({
    supported: !!navigator.locks,
    entries: Object.entries(sessionStorage),
    clientId: localStorage.getItem('nextstep-browser-id'),
  }));
  const other = await context.newPage();
  const { monitorRuntimeErrors } = await import('./runtime-errors');
  const otherRuntime = await monitorRuntimeErrors(other);
  try {
    if (clone.supported)
      await other.addInitScript((entries: [string, string][]) => {
        if (window.parent !== window) return;
        const seeded = 'axv-e2e-extension-session-cloned';
        if (sessionStorage.getItem(seeded)) return;
        // A duplicated tab begins with copied sessionStorage. A reload then
        // retains the second tab's independently claimed identity.
        for (const [key, value] of entries) sessionStorage.setItem(key, value);
        sessionStorage.setItem(seeded, 'true');
      }, clone.entries);
    await other.goto('/');
    await expect(checklistWindow(other)).toHaveCount(1);
    await expect(checklistWindow(other)).toHaveAttribute(
      'data-window-id',
      windowId!,
    );
    expect(
      await other.evaluate(() => localStorage.getItem('nextstep-browser-id')),
    ).toBe(clone.clientId);
    await activate(other, checklistWindow(other));
    await expect(checklistFrame(other).getByLabel('New item')).toBeEnabled();
    await addItem(other, secondText, touch);
    await expect
      .poll(async () =>
        (await storedDrafts()).some(
          ({ draft }) => draft.value.items[0]?.text === secondText,
        ),
      )
      .toBe(true);
    const secondDraft = (await storedDrafts()).find(
      ({ draft }) => draft.value.items[0]?.text === secondText,
    )!;
    expect(secondDraft.key).not.toBe(firstDraft.key);
    expect(secondDraft.draft.expectedRevision).toBe(originalRevision);
    await expect(
      checklistFrame(page).getByRole('textbox', { name: 'Item text' }),
    ).toHaveValue(firstText);
    await page.bringToFront();
    await activate(page, checklistWindow(page));
    await choose(
      checklistFrame(page).getByRole('button', { name: 'Save', exact: true }),
      touch,
    );
    await expect(checklistFrame(page).getByRole('status')).toHaveText('Saved');
    const saved = await savedData(request);
    expect((saved.value as { items: { text: string }[] }).items[0]?.text).toBe(
      firstText,
    );
    expect(saved.revision).toBeGreaterThan(originalRevision);
    expect(
      (await storedDrafts()).find(({ key }) => key === secondDraft.key),
    ).toEqual(secondDraft);
    await expect(
      checklistFrame(other).getByRole('textbox', { name: 'Item text' }),
    ).toHaveValue(secondText);
    await other.bringToFront();
    other.once('dialog', (dialog) => void dialog.accept());
    await other.reload();
    const recovered = checklistWindow(other);
    await expect(
      recovered.getByText('A draft was recovered', { exact: false }),
    ).toBeVisible();
    await expect(recovered.locator('iframe')).toHaveCount(0);
    await choose(
      recovered.getByRole('button', { name: 'Restore draft', exact: true }),
      touch,
    );
    const secondFrame = checklistFrame(other);
    await expect(
      secondFrame.getByRole('textbox', { name: 'Item text' }),
    ).toHaveValue(secondText);
    await expect(recovered.locator('.toolbar-caption')).toHaveText(
      'Unsaved changes',
    );
    const restoredDraft = (await storedDrafts()).find(
      ({ draft }) => draft.value.items[0]?.text === secondText,
    )!;
    expect(restoredDraft.draft.expectedRevision).toBe(originalRevision);
    const conflict = other.waitForResponse(
      (response) =>
        response.url().includes(`/api/extensions/${id}/sessions/`) &&
        response.url().endsWith('/rpc') &&
        response.request().postData()?.includes('"storage.set"') === true &&
        response.status() === 409,
    );
    await choose(
      secondFrame.getByRole('button', { name: 'Save', exact: true }),
      touch,
    );
    expect((await conflict).status()).toBe(409);
    await expect(
      secondFrame.getByRole('alert').filter({ hasText: 'Save failed' }),
    ).toBeVisible();
    await expect(
      secondFrame.getByRole('textbox', { name: 'Item text' }),
    ).toHaveValue(secondText);
    await expect(secondFrame.getByRole('status')).toHaveText('Unsaved changes');
    expect((await savedData(request)).value).toEqual(saved.value);
    expect(
      (await storedDrafts()).find(
        ({ draft }) => draft.value.items[0]?.text === secondText,
      )?.draft.expectedRevision,
    ).toBe(originalRevision);
    await choose(
      recovered.getByRole('button', { name: 'Reload', exact: true }),
      touch,
    );
    const reconnectDialog = other.getByRole('dialog', {
      name: 'Reload application?',
      exact: true,
    });
    await expect(reconnectDialog).toBeVisible();
    const reconnected = other.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/extensions/${id}/sessions`) &&
        response.request().method() === 'POST' &&
        response.ok(),
    );
    await choose(
      reconnectDialog.getByRole('button', { name: 'Keep draft', exact: true }),
      touch,
    );
    await reconnected;
    await expect(secondFrame.getByLabel('New item')).toBeEnabled();
    await expect(
      secondFrame.getByRole('textbox', { name: 'Item text' }),
    ).toHaveValue(secondText);
    await expect(recovered.locator('.toolbar-caption')).toHaveText(
      'Unsaved changes',
    );
    expect(
      (await storedDrafts()).find(
        ({ draft }) => draft.value.items[0]?.text === secondText,
      )?.draft.expectedRevision,
    ).toBe(originalRevision);
    const keptConflict = other.waitForResponse(
      (response) =>
        response.url().includes(`/api/extensions/${id}/sessions/`) &&
        response.url().endsWith('/rpc') &&
        response.request().postData()?.includes('"storage.set"') === true &&
        response.status() === 409,
    );
    await choose(
      secondFrame.getByRole('button', { name: 'Save', exact: true }),
      touch,
    );
    const rejected = await keptConflict;
    expect(rejected.status()).toBe(409);
    expect(rejected.url()).not.toBe((await conflict).url());
    await expect(
      secondFrame.getByRole('alert').filter({ hasText: 'Save failed' }),
    ).toBeVisible();
    await expect(
      secondFrame.getByRole('textbox', { name: 'Item text' }),
    ).toHaveValue(secondText);
    await expect(secondFrame.getByRole('status')).toHaveText('Unsaved changes');
    expect(await savedData(request)).toEqual(saved);
    expect(
      (await storedDrafts()).find(
        ({ draft }) => draft.value.items[0]?.text === secondText,
      )?.draft.expectedRevision,
    ).toBe(originalRevision);
    expect(otherRuntime.errors).toEqual([]);
  } finally {
    otherRuntime.dispose();
    await other.close({ runBeforeUnload: false });
  }
  await page.bringToFront();
  await expect(checklistWindow(page)).toBeVisible();
  await expect(
    checklistFrame(page).getByRole('textbox', { name: 'Item text' }),
  ).toHaveValue(firstText);
  await expect(checklistFrame(page).getByRole('status')).toHaveText('Saved');
});
