import { encoded, openTerminal, output, run } from './terminal-helpers';
import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';

import {
  activate,
  createEntry,
  expect,
  filesWindow,
  launch,
  menuItem,
  newFileItem,
  test,
  windowMenu,
} from './helpers';

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 1024, height: 768 },
  { width: 390, height: 844 },
]) {
  test(`desktop, columns, Inspector, pinned menu and dialog at ${viewport.width}×${viewport.height}`, async ({
    page,
    request,
  }, info) => {
    const screenshotDir = resolve('screenshots', `final-${info.project.name}`);
    await mkdir(screenshotDir, { recursive: true });
    const capture = async (name: string) => {
      const path = resolve(
        screenshotDir,
        `${viewport.width}x${viewport.height}-${name}.png`,
      );
      await page.screenshot({ path, fullPage: true });
      await info.attach(name, { path, contentType: 'image/png' });
    };
    await page.setViewportSize(viewport);
    await createEntry(
      request,
      'E2E Visual document.txt',
      'file',
      'documents',
      'A readable text preview.\nWindow Maker-inspired controls at every scale.',
    );
    await page.reload();
    const files = filesWindow(page);
    await files
      .getByRole('option', { name: 'E2E Visual document.txt', exact: true })
      .click();
    await expect(
      files.getByRole('button', { name: /^Close / }),
    ).toBeInViewport();
    await expect(
      page.getByRole('button', { name: /^Workspace 1:/ }),
    ).toBeInViewport();
    await capture('desktop-columns');
    await files
      .getByRole('button', { name: 'More file actions', exact: true })
      .click();
    await expect(
      page
        .locator('.ns-menu-layer')
        .getByRole('menuitem', { name: 'Rename', exact: true }),
    ).toBeInViewport();
    await capture('files-more-menu');
    await page.keyboard.press('Escape');
    await files.getByRole('button', { name: 'Inspect', exact: true }).click();
    const inspector = page.locator('[data-app-id="inspector"]');
    await expect(inspector.getByTestId('inspector-name')).toHaveText(
      'E2E Visual document.txt',
    );
    await expect(
      inspector.getByRole('button', { name: /^Close / }),
    ).toBeInViewport();
    await expect(
      inspector.getByRole('button', { name: 'Open', exact: true }),
    ).toBeInViewport();
    await expect(inspector.getByTestId('inspector-revision')).toBeHidden();
    await capture('inspector');
    const details = inspector
      .locator('summary')
      .filter({ hasText: /^Details$/ });
    await details.click();
    await expect(inspector.getByTestId('inspector-revision')).toBeVisible();
    await capture('inspector-details');
    await details.click();
    await inspector
      .getByRole('button', { name: 'More inspector actions', exact: true })
      .click();
    await expect(
      page
        .locator('.ns-menu-layer')
        .getByRole('menuitem', { name: 'Move', exact: true }),
    ).toBeInViewport();
    await capture('inspector-more-menu');
    await page.keyboard.press('Escape');
    await windowMenu(page, inspector);
    await page
      .getByRole('button', { name: 'Pin Window menu', exact: true })
      .click();
    await expect(page.locator('.ns-pinned-menu')).toBeVisible();
    await capture('pinned-window-menu');
    await page
      .getByRole('button', { name: 'Close Window menu', exact: true })
      .click();
    await activate(page, files);
    await newFileItem(page, files, 'New Folder');
    await expect(page.getByRole('dialog')).toBeVisible();
    await expect(
      page.getByRole('dialog').getByRole('button', { name: 'OK', exact: true }),
    ).toBeInViewport();
    await capture('name-dialog');
    await page.keyboard.press('Escape');
    await launch(page, 'gallery');
    await expect(page.locator('[data-app-id="gallery"]')).toBeVisible();
    await capture('component-gallery');
    await page
      .getByRole('button', { name: 'Workspace menu', exact: true })
      .click();
    await menuItem(page, 'Applications').click();
    if (viewport.width < 600) {
      await expect(page.getByRole('menu')).toHaveCount(1);
      const back = page.getByRole('button', {
        name: 'Back to Workspace menu',
        exact: true,
      });
      await expect(back).toBeVisible();
      await back.click();
      await menuItem(page, 'Applications').click();
      await expect(page.getByRole('menu')).toHaveCount(1);
      await expect(back).toBeVisible();
    }
    for (const menu of await page.getByRole('menu').all()) {
      const box = await menu.boundingBox();
      expect(box).not.toBeNull();
      expect(box!.x).toBeGreaterThanOrEqual(0);
      expect(box!.x + box!.width).toBeLessThanOrEqual(viewport.width + 1);
    }
    await capture('workspace-submenu');
  });
}

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 1024, height: 768 },
  { width: 390, height: 844 },
]) {
  test(`Terminal and end-session dialog at ${viewport.width}×${viewport.height}`, async ({
    page,
  }, info) => {
    test.skip(
      process.platform === 'win32',
      'The visual sample uses POSIX printf.',
    );
    await page.setViewportSize(viewport);
    const terminal = await openTerminal(page);
    await run(
      terminal,
      `printf '\\033[2J\\033[H\\033[32m${encoded('NEXTSTEP host terminal')}\\033[0m\\n${encoded('Unicode: 你好 ✓')}\\n${encoded('One window. One real shell.')}\\n'`,
    );
    await expect(output(terminal)).toContainText('One window. One real shell.');
    await expect(
      terminal.getByRole('button', { name: /^Close / }),
    ).toBeInViewport();
    await expect(
      terminal.getByRole('button', { name: 'Interrupt', exact: true }),
    ).toBeInViewport();
    for (const label of ['Copy', 'Paste', 'More terminal actions'])
      await expect(
        terminal.getByRole('button', { name: label, exact: true }),
      ).toBeInViewport();
    const screenshotDir = resolve('screenshots', `final-${info.project.name}`);
    await mkdir(screenshotDir, { recursive: true });
    const capture = async (name: string) => {
      const path = resolve(
        screenshotDir,
        `${viewport.width}x${viewport.height}-${name}.png`,
      );
      await page.screenshot({ path, fullPage: true });
      await info.attach(name, { path, contentType: 'image/png' });
    };
    await capture('terminal');
    await terminal.getByRole('button', { name: /^Close / }).click();
    const dialog = page.getByRole('dialog', { name: 'End host shell?' });
    await expect(
      dialog.getByRole('button', { name: 'Cancel', exact: true }),
    ).toBeFocused();
    await expect(
      dialog.getByRole('button', { name: 'End Session', exact: true }),
    ).toBeInViewport();
    await capture('terminal-close-dialog');
    await dialog
      .getByRole('button', { name: 'End Session', exact: true })
      .click();
  });
}
