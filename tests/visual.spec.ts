import { encoded, openTerminal, output, run } from './terminal-helpers';

import { expect, launch, menuItem, test, windowMenu } from './helpers';

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 1024, height: 768 },
  { width: 390, height: 844 },
]) {
  test(`Welcome, Project Files, window menu and dialog at ${viewport.width}×${viewport.height}`, async ({
    page,
  }, info) => {
    const capture = async (name: string) => {
      const path = info.outputPath(
        `${viewport.width}x${viewport.height}-${name}.png`,
      );
      await page.screenshot({ path, fullPage: true });
      await info.attach(name, { path, contentType: 'image/png' });
    };
    await page.setViewportSize(viewport);
    const welcome = page.locator('[data-app-id="welcome"]');
    const entries = welcome.getByRole('group', { name: 'Work applications' });
    for (const app of [
      'Codex',
      'Virtual Machines',
      'Project Files',
      'Terminal',
    ])
      await expect(entries.getByRole('button', { name: app })).toBeInViewport();
    await expect(
      page.getByRole('button', { name: 'Workspace menu', exact: true }),
    ).toBeInViewport();
    await capture('welcome');
    await entries.getByRole('button', { name: 'Project Files' }).click();
    const project = page.locator('[data-app-id="project-files"]');
    await expect(
      project.getByRole('button', { name: /^Close / }),
    ).toBeInViewport();
    await expect(
      project.getByRole('textbox', { name: 'Host project directory' }),
    ).toBeInViewport();
    await capture('project-files');
    await windowMenu(page, project);
    await expect(
      page.getByRole('menu', { name: 'Window', exact: true }),
    ).toBeVisible();
    await capture('window-menu');
    await page.keyboard.press('Escape');
    await page
      .getByRole('button', { name: 'Workspace menu', exact: true })
      .click();
    await menuItem(page, 'Clock').click();
    const clock = page.getByRole('dialog', { name: 'Clock', exact: true });
    await expect(
      clock.getByRole('button', { name: 'OK', exact: true }),
    ).toBeInViewport();
    await capture('clock-dialog');
    await clock.getByRole('button', { name: 'OK', exact: true }).click();
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
    const capture = async (name: string) => {
      const path = info.outputPath(
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
