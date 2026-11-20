import { expect, test as base } from '@playwright/test';
import type { APIRequestContext, Locator, Page } from '@playwright/test';
import { monitorRuntimeErrors } from './runtime-errors';

export const dockEntry = (page: Page, id: string) =>
  page.locator(`.ns-dock [data-dock-app="${id}"]`);

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

export async function launch(page: Page, appId: string) {
  const tile = dockEntry(page, appId).locator('.ns-dock-tile');
  if (await tile.count()) {
    await tile.click();
    return;
  }
  const titles: Record<string, string> = {
    welcome: 'Welcome',
    'project-files': 'Project Files',
    terminal: 'Terminal',
    machines: 'Virtual Machines',
    codex: 'Codex',
    appearance: 'Preferences',
    gallery: 'Component Gallery',
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

export async function pinDockApplication(
  page: Page,
  title: string,
  touch = false,
) {
  const choose = (control: Locator) =>
    touch ? control.tap() : control.click();
  const plus = page.getByRole('button', {
    name: 'Add application to Dock',
    exact: true,
  });
  const options = page.getByRole('button', { name: 'Dock menu', exact: true });
  await choose(plus.or(options));
  const direct = page.getByRole('menu', { name: 'Add to Dock', exact: true });
  const dock = page.getByRole('menu', { name: 'Dock', exact: true });
  await expect(direct.or(dock)).toBeVisible();
  if (await dock.isVisible()) {
    await choose(menuItem(dock, 'Add application'));
    await choose(
      menuItem(
        page.getByRole('menu', { name: 'Add application', exact: true }),
        title,
      ),
    );
  } else {
    await choose(menuItem(direct, title));
  }
}

export async function openDockOptions(
  page: Page,
  appId: string,
  touch = false,
) {
  const entry = dockEntry(page, appId);
  const title = await entry.getAttribute('data-app-title');
  expect(title, `Dock title for ${appId}`).toBeTruthy();
  if (touch) {
    await page.getByRole('button', { name: 'Dock menu', exact: true }).tap();
    const dock = page.getByRole('menu', { name: 'Dock', exact: true });
    await menuItem(dock, title!).tap();
  } else {
    await entry.locator('.ns-dock-tile').click({ button: 'right' });
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
  await choose(
    page.getByRole('button', { name: 'Workspace menu', exact: true }),
  );
  const workspace = page.getByRole('menu', { name: 'Workspace', exact: true });
  await choose(menuItem(workspace, 'Workspaces'));
  const menu = page.getByRole('menu', { name: 'Workspaces', exact: true });
  await expect(menu).toBeVisible();
  return menu;
}

export async function openWorkspaceShortcutMenu(
  page: Page,
  title: string,
  touch = false,
) {
  const choose = (control: Locator) =>
    touch ? control.tap() : control.click();
  const workspaces = await openWorkspaceControls(page, touch);
  await choose(menuItem(workspaces, 'Shortcuts'));
  const shortcuts = page.getByRole('menu', { name: 'Shortcuts', exact: true });
  await choose(menuItem(shortcuts, title));
  const menu = page.getByRole('menu', { name: title, exact: true });
  await expect(menu).toBeVisible();
  return menu;
}

export function workspaceButton(page: Page, index: number) {
  return page.locator('.ns-desktop-status').getByRole('button', {
    name: new RegExp(`^Workspace ${index}(?: · |$)`),
  });
}

export async function expectCurrentWorkspace(page: Page, index: number) {
  await expect(workspaceButton(page, index)).toHaveAttribute(
    'aria-pressed',
    'true',
  );
}

export async function switchWorkspace(
  page: Page,
  index: number,
  touch = false,
) {
  const button = workspaceButton(page, index);
  if (touch) await button.tap();
  else await button.click();
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
}>({
  request: async ({ playwright, baseURL }, use) => {
    const discovery = await playwright.request.newContext({ baseURL });
    let databaseId: string;
    try {
      const response = await discovery.get(
        '/api/bootstrap?clientId=e2e-fixture',
      );
      expect(response.ok()).toBeTruthy();
      databaseId = (await response.json()).databaseId;
    } finally {
      await discovery.dispose();
    }
    // Only the test's API client gets this header. Browser requests must obtain
    // and carry their own binding; injecting browser headers would hide regressions.
    const request = await playwright.request.newContext({
      baseURL,
      extraHTTPHeaders: { 'X-AXV-Database-ID': databaseId },
    });
    try {
      await use(request);
    } finally {
      await request.dispose();
    }
  },
  desktopReady: [
    async ({ page, request }, use, testInfo) => {
      // WebKit also maps native navigation-loader diagnostics to pageerror:
      // https://github.com/microsoft/playwright/issues/43120
      // Native ErrorEvent/unhandledrejection remain the uncaught-JS evidence.
      const runtime = await monitorRuntimeErrors(page);
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
      await expect(page.locator('[data-app-id="welcome"]')).toHaveClass(
        /is-active/,
      );
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
