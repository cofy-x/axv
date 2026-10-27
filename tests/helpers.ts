import { expect, test as base } from '@playwright/test';
import type { APIRequestContext, Locator, Page } from '@playwright/test';
import { monitorRuntimeErrors } from './runtime-errors';

export interface Entry {
  id: string;
  name: string;
  parentId: string;
  kind: 'folder' | 'file';
  revision: number;
  size: number;
  editable: boolean;
  trashedAt?: string;
}

export const filesWindow = (page: Page) =>
  page.locator('[data-window-id="files:documents"]');

export const editorWindow = (page: Page, id: string) =>
  page.locator(`[data-window-id="notes:${id}"]`);

export const dockEntry = (page: Page, id: string) =>
  page.locator(`.ns-dock [data-dock-app="${id}"]`);

export async function entries(request: APIRequestContext): Promise<Entry[]> {
  const response = await request.get('/api/entries');
  expect(response.ok()).toBeTruthy();
  return (await response.json()).entries;
}

export async function createEntry(
  request: APIRequestContext,
  name: string,
  kind: 'folder' | 'file' = 'file',
  parentId = 'documents',
  content = 'Initial text',
): Promise<Entry> {
  const response = await request.post('/api/entries', {
    data: {
      parentId,
      name,
      kind,
      content: kind === 'file' ? content : undefined,
    },
  });
  expect(response.status()).toBe(201);
  return (await response.json()).entry;
}

export async function readContent(request: APIRequestContext, id: string) {
  const response = await request.get(`/api/entries/${id}`);
  expect(response.ok()).toBeTruthy();
  return (await response.json()).content as string;
}

export function expectedDownloadFilename(name: string, browserName: string) {
  // Browsers compute the final filename; Linux WebKit replaces spaces.
  // https://playwright.dev/docs/api/class-download#download-suggested-filename
  return process.platform === 'linux' && browserName === 'webkit'
    ? name.replaceAll(' ', '_')
    : name;
}

export function menuItem(scope: Page | Locator, name: string | RegExp) {
  return scope
    .getByRole('menuitem', { name, exact: typeof name === 'string' })
    .or(
      scope.getByRole('menuitemcheckbox', {
        name,
        exact: typeof name === 'string',
      }),
    );
}

export async function actionMenu(
  page: Page,
  window: Locator,
  buttonLabel: string,
  itemName: string,
  touch = false,
) {
  const button = window.getByRole('button', {
    name: buttonLabel,
    exact: true,
  });
  if (touch) await button.tap();
  else await button.click();
  const item = page
    .locator('.ns-menu-layer')
    .getByRole('menuitem', { name: itemName, exact: true });
  if (touch) await item.tap();
  else await item.click();
}

export async function newFileItem(
  page: Page,
  files: Locator,
  name: string,
  touch = false,
) {
  await actionMenu(page, files, 'New', name, touch);
}

export async function launch(page: Page, appId: string) {
  const tile = dockEntry(page, appId).locator('.ns-dock-tile');
  if (await tile.count()) {
    await tile.click();
    return;
  }
  const titles: Record<string, string> = {
    welcome: 'Welcome',
    files: 'Files',
    notes: 'Text Editor',
    'project-files': 'Project Files',
    terminal: 'Terminal',
    machines: 'Virtual Machines',
    codex: 'Codex',
    appearance: 'Preferences',
    gallery: 'Component Gallery',
    inspector: 'Inspector',
    trash: 'Recycler',
  };
  const title = titles[appId];
  expect(title, `Known application title for ${appId}`).toBeTruthy();
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await page
    .getByRole('menu', { name: 'Applications', exact: true })
    .getByRole('menuitem', { name: title!, exact: true })
    .click();
}

export async function openDockOptions(
  page: Page,
  appId: string,
  touch = false,
) {
  const entry = dockEntry(page, appId);
  const title = await entry.getAttribute('data-app-title');
  expect(title, `Dock title for ${appId}`).toBeTruthy();
  const choose = async (control: Locator) =>
    touch ? control.tap() : control.click();
  const options = entry.locator('.ns-dock-options');
  if (await options.isVisible()) await choose(options);
  else {
    await choose(page.getByRole('button', { name: 'Dock menu', exact: true }));
    await choose(
      menuItem(page.getByRole('menu', { name: 'Dock', exact: true }), title!),
    );
  }
  const menu = page.getByRole('menu', { name: title!, exact: true });
  await expect(menu).toBeVisible();
  return menu;
}

export async function activate(page: Page, win: Locator, touch = false) {
  const appId = await win.getAttribute('data-app-id');
  const title = await win.getAttribute('aria-label');
  expect(appId).toBeTruthy();
  expect(title).toBeTruthy();
  const menu = await openDockOptions(page, appId!, touch);
  const item = menuItem(menu, title!);
  if (touch) await item.tap();
  else await item.click();
  await expect(win).toBeVisible();
  await expect(win).toHaveClass(/is-active/);
}

export async function openDocument(page: Page, entry: Entry, touch = false) {
  const choose = async (control: Locator) =>
    touch ? control.tap() : control.click();
  await activate(page, filesWindow(page), touch);
  await choose(
    filesWindow(page).getByRole('option', { name: entry.name, exact: true }),
  );
  await choose(
    filesWindow(page).getByRole('button', { name: 'Open', exact: true }),
  );
  const editor = editorWindow(page, entry.id);
  await expect(
    editor.getByRole('textbox', { name: 'Document text' }),
  ).toBeEnabled();
  return editor;
}

export async function nameDialog(page: Page, name: string) {
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await dialog.getByRole('textbox', { name: 'Name', exact: true }).fill(name);
  await dialog.getByRole('button', { name: 'OK', exact: true }).click();
  await expect(dialog).toBeHidden();
}

export async function windowMenu(page: Page, win: Locator) {
  await win.getByRole('button', { name: /^Window menu for / }).click();
}

export async function windowAction(
  page: Page,
  win: Locator,
  action: string,
  touch = false,
) {
  const button = win.getByRole('button', { name: /^Window menu for / });
  if (touch) await button.tap();
  else await button.click();
  const item = menuItem(
    page.getByRole('menu', { name: 'Window', exact: true }),
    action,
  );
  if (touch) await item.tap();
  else await item.click();
}

export async function openWorkspaceControls(page: Page, touch = false) {
  const choose = async (control: Locator) =>
    touch ? control.tap() : control.click();
  await choose(page.getByRole('button', { name: /^Workspace \d+: / }));
  const menu = page.getByRole('menu', { name: 'Workspaces', exact: true });
  await expect(menu).toBeVisible();
  return menu;
}

export async function expectCurrentWorkspace(page: Page, index: number) {
  await expect(
    page.getByRole('button', { name: /^Workspace \d+: / }),
  ).toHaveAccessibleName(new RegExp(`^Workspace ${index}: `));
}

export async function switchWorkspace(
  page: Page,
  index: number,
  touch = false,
) {
  const menu = await openWorkspaceControls(page, touch);
  const item = menuItem(menu, new RegExp(`^${index}\\s+`));
  if (touch) await item.tap();
  else await item.click();
  await expectCurrentWorkspace(page, index);
}

export async function bounds(locator: Locator) {
  const result = await locator.boundingBox();
  expect(result).not.toBeNull();
  return result!;
}

export async function persistedLayout(page: Page, request: APIRequestContext) {
  const clientId = await page.evaluate(() =>
    localStorage.getItem('nextstep-browser-id'),
  );
  expect(clientId).toBeTruthy();
  const response = await request.get(
    `/api/bootstrap?clientId=${encodeURIComponent(clientId!)}`,
  );
  expect(response.ok()).toBeTruthy();
  return (await response.json()).layout;
}

export const test = base.extend<{
  desktopReady: void;
  initialApp: 'files' | 'welcome';
}>({
  initialApp: ['files', { option: true }],
  desktopReady: [
    async ({ page, request, initialApp }, use, testInfo) => {
      // WebKit also maps native navigation-loader diagnostics to pageerror:
      // https://github.com/microsoft/playwright/issues/43120
      // Native ErrorEvent/unhandledrejection remain the uncaught-JS evidence.
      const runtime = await monitorRuntimeErrors(page);
      // The runner owns this isolated database. Remove only data created by tests;
      // each browser context has its own client layout and IndexedDB namespace.
      for (const entry of await entries(request)) {
        if (!entry.name.startsWith('E2E ')) continue;
        const trashed = await request.post(`/api/entries/${entry.id}/trash`);
        if (trashed.status() === 404) continue;
        expect(trashed.ok()).toBeTruthy();
        const removed = await request.delete(`/api/entries/${entry.id}`);
        expect([200, 404]).toContain(removed.status());
      }
      expect(
        (
          await request.put('/api/preferences', {
            data: { wallpaper: 'slate', iconSnap: true, clock24Hour: true },
          })
        ).ok(),
      ).toBeTruthy();
      await page.goto('/');
      await expect(
        page.getByRole('complementary', { name: 'Application Dock' }),
      ).toBeVisible();
      if (initialApp === 'files') {
        await page
          .locator('[data-app-id="welcome"]')
          .getByRole('button', { name: 'Documents', exact: true })
          .click();
        await expect(filesWindow(page)).toBeVisible();
        await expect(filesWindow(page)).toHaveClass(/is-active/);
        await expect
          .poll(
            async () => (await persistedLayout(page, request))?.activeWindowId,
          )
          .toBe('files:documents');
      } else {
        await expect(page.locator('[data-app-id="welcome"]')).toHaveClass(
          /is-active/,
        );
      }
      await use();
      try {
        expect(
          runtime.documents.size,
          'The native runtime error monitor must be installed.',
        ).toBeGreaterThan(0);
        if (runtime.pageErrors.length)
          await testInfo.attach('browser-page-errors', {
            body: JSON.stringify(runtime.pageErrors, null, 2),
            contentType: 'application/json',
          });
        expect(
          runtime.errors,
          'The desktop must not throw browser runtime errors.',
        ).toEqual([]);
      } finally {
        runtime.dispose();
      }
    },
    { auto: true },
  ],
});

export { expect };
