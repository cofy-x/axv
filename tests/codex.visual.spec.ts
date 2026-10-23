import {
  codexOutput,
  codexSessionAction,
  defaultProjectPath,
  expect,
  openCodexManager,
  openCodexEnvironment,
  openCodexFolderPicker,
  requestEndCodex,
  runCodex,
  selectCodexProject,
  session,
  startCodex,
  test,
} from './codex-helpers';

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 1024, height: 768 },
  { width: 390, height: 844 },
]) {
  test(`Codex environment, session organization and project terminals at ${viewport.width}×${viewport.height}`, async ({
    page,
    request,
  }, info) => {
    await page.setViewportSize(viewport);
    await expect(
      page
        .locator('[data-app-id="welcome"]')
        .getByRole('button', { name: 'Refresh', exact: true }),
    ).toHaveCount(0);
    const capture = async (name: string) => {
      const path = info.outputPath(
        `${viewport.width}x${viewport.height}-${name}.png`,
      );
      await page.screenshot({ path, fullPage: true });
      await info.attach(name, { path, contentType: 'image/png' });
    };
    const manager = await openCodexManager(page);
    await expect(manager.getByTestId('codex-default-project')).toHaveText(
      defaultProjectPath,
    );
    await expect(manager.getByTestId('codex-default-project')).toBeHidden();
    await expect(manager.getByTestId('codex-default-project-name')).toHaveText(
      defaultProjectPath.split('/').filter(Boolean).at(-1)!,
    );
    await expect(
      manager.getByRole('button', { name: 'New Session', exact: true }),
    ).toBeInViewport();
    await capture('codex-manager');
    const environment = await openCodexEnvironment(page);
    await expect(
      environment.getByRole('button', { name: 'Refresh', exact: true }),
    ).toBeInViewport();
    expect(
      await environment.evaluate((element) =>
        [...element.querySelectorAll('.codex-environment-content')].every(
          (content) => content.scrollWidth <= content.clientWidth,
        ),
      ),
    ).toBe(true);
    await capture('codex-environment');
    await environment.getByRole('button', { name: /^Close / }).click();
    await selectCodexProject(page);
    await expect(
      manager.getByRole('button', {
        name: 'Start Here',
        exact: true,
      }),
    ).toBeInViewport();
    await capture('codex-project-picker');
    await manager.getByRole('button', { name: 'Cancel', exact: true }).click();
    const { window, id, runId } = await startCodex(page);
    await codexSessionAction(page, window, 'Rename');
    const rename = page.getByRole('dialog', { name: 'Rename Codex Session' });
    const name = 'Review project updates and next steps ✓';
    await rename.getByLabel('Session name', { exact: true }).fill(name);
    await expect(
      rename.getByRole('button', { name: 'Rename', exact: true }),
    ).toBeInViewport();
    await capture('codex-rename-dialog');
    await rename.getByRole('button', { name: 'Rename', exact: true }).click();
    await expect(window).toHaveAttribute('aria-label', `Codex · ${name}`);
    await runCodex(window, 'inspect visual');
    await expect(codexOutput(window)).toContainText('STATE visual');
    await expect(
      window.getByRole('button', { name: /^Close / }),
    ).toBeInViewport();
    await expect(
      window.getByRole('button', { name: 'More Codex actions', exact: true }),
    ).toBeInViewport();
    for (const label of ['Attachments', 'Page Up', 'Page Down'])
      await expect(
        window.getByRole('button', { name: label, exact: true }),
      ).toBeInViewport();
    await expect
      .poll(async () => {
        const host = (await window.getByTestId('codex-screen').boundingBox())!;
        const cells = (await window.locator('.xterm-screen').boundingBox())!;
        return Math.max(cells.width - host.width, cells.height - host.height);
      })
      .toBeLessThanOrEqual(1);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBeLessThanOrEqual(viewport.width);
    await capture('codex-terminal');
    const more = window.getByRole('button', {
      name: 'More Codex actions',
      exact: true,
    });
    await more.click();
    for (const label of ['Project Files', 'Rename', 'End Session'])
      await expect(
        page
          .locator('.ns-menu-layer')
          .getByRole('menuitem', { name: label, exact: true }),
      ).toBeInViewport();
    await capture('codex-more-menu');
    await page.keyboard.press('Escape');
    await expect(more).toBeFocused();
    await codexSessionAction(page, window, 'Details');
    const details = page.getByRole('dialog', { name: 'Codex Session Details' });
    await expect(
      details.getByRole('button', { name: 'Copy Command', exact: true }),
    ).toBeInViewport();
    await expect(
      details.getByRole('button', {
        name: 'Host Terminal',
        exact: true,
      }),
    ).toBeInViewport();
    await capture('codex-session-details');
    await details
      .getByRole('button', { name: 'Host Terminal', exact: true })
      .click();
    const terminal = page.locator('.ns-window[data-app-id="terminal"]');
    await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
      'data-state',
      'connected',
    );
    await expect(terminal.getByTestId('terminal-project-name')).toHaveText(
      defaultProjectPath.split('/').filter(Boolean).at(-1)!,
    );
    await expect(
      terminal.getByTestId('terminal-project-name'),
    ).toBeInViewport();
    await expect(
      terminal.getByRole('button', { name: /^Close / }),
    ).toBeInViewport();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBeLessThanOrEqual(viewport.width);
    await capture('codex-project-terminal');
    await terminal
      .getByRole('button', { name: 'Details', exact: true })
      .click();
    const hostDetails = page.getByRole('dialog', {
      name: 'Host Terminal Details',
      exact: true,
    });
    await expect(hostDetails.getByTestId('terminal-project-path')).toHaveText(
      defaultProjectPath,
    );
    await expect(
      hostDetails.getByTestId('terminal-project-path'),
    ).toBeInViewport();
    await capture('codex-project-terminal-details');
    await hostDetails
      .getByRole('button', { name: 'Close', exact: true })
      .click();
    await terminal.getByRole('button', { name: /^Close / }).click();
    await page
      .getByRole('dialog', { name: 'End host shell?' })
      .getByRole('button', { name: 'End Session', exact: true })
      .click();
    await expect(terminal).toHaveCount(0);
    await openCodexManager(page);
    await openCodexFolderPicker(page);
    const recent = manager.getByRole('button', {
      name: 'Recent Projects',
      exact: true,
    });
    if (await recent.count()) {
      await recent.click();
      const menu = page.getByRole('menu', {
        name: 'Recent Projects',
        exact: true,
      });
      await expect(
        menu.getByRole('menuitem', {
          name: `${defaultProjectPath.split('/').at(-1)} ${defaultProjectPath.slice(0, defaultProjectPath.lastIndexOf('/'))}`,
          exact: true,
        }),
      ).toHaveCount(0);
      await capture('codex-recent-projects-menu');
      await page.keyboard.press('Escape');
      await expect(recent).toBeFocused();
    } else
      await expect(
        manager.getByRole('region', { name: 'Recent projects', exact: true }),
      ).toHaveCount(0);
    await capture('codex-folder-picker-default-excluded');
    await manager.getByRole('button', { name: 'Cancel', exact: true }).click();
    const row = manager.locator(`[data-session-id="${id}"]`);
    const rowAction = async (label: string, screenshot?: string) => {
      await row
        .getByRole('button', {
          name: `More session actions for ${name}, run ${runId.slice(0, 8)}`,
          exact: true,
        })
        .click();
      const menu = page.getByRole('menu', {
        name: `Run ${runId.slice(0, 8)} · ${name}`,
        exact: true,
      });
      await expect(menu).toBeVisible();
      if (screenshot) await capture(screenshot);
      await menu.getByRole('menuitem', { name: label, exact: true }).click();
    };
    await rowAction('Archive', 'codex-session-actions-menu');
    await expect(row).toHaveCount(0);
    await manager
      .getByRole('button', { name: 'Archived', exact: true })
      .click();
    await expect(row).toContainText(name);
    await row.scrollIntoViewIfNeeded();
    await capture('codex-archived-sessions');
    await rowAction('Unarchive');
    await expect(row).toHaveCount(0);
    await manager
      .getByRole('button', { name: 'Unarchived', exact: true })
      .click();
    await row.getByRole('button', { name: 'Open', exact: true }).click();
    const dialog = await requestEndCodex(page, window);
    await expect(
      dialog.getByRole('button', { name: 'Cancel', exact: true }),
    ).toBeFocused();
    await expect(
      dialog.getByRole('button', { name: 'End Session', exact: true }),
    ).toBeInViewport();
    await capture('codex-end-dialog');
    await dialog
      .getByRole('button', { name: 'End Session', exact: true })
      .click();
    await expect(window.getByTestId('codex-status')).toHaveAttribute(
      'data-state',
      'ended',
    );
    const continueButton = window.getByRole('button', {
      name: 'Continue',
      exact: true,
    });
    await expect(continueButton).toBeEnabled();
    await expect(continueButton).toBeInViewport();
    await expect(
      window.getByRole('button', { name: 'Reconnect', exact: true }),
    ).toHaveCount(0);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBeLessThanOrEqual(viewport.width);
    await capture('codex-ended-run');
    await continueButton.click();
    const continuation = page.getByRole('dialog', {
      name: 'Continue Conversation?',
    });
    await expect(continuation).toContainText(defaultProjectPath);
    await expect(
      continuation.getByRole('button', {
        name: 'Continue',
        exact: true,
      }),
    ).toBeInViewport();
    await expect(
      continuation.getByRole('button', { name: 'Cancel', exact: true }),
    ).toBeInViewport();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBeLessThanOrEqual(viewport.width);
    await capture('codex-continue-dialog');
    await continuation
      .getByRole('button', { name: 'Cancel', exact: true })
      .click();
    expect((await session(request, id)).runId).toBe(runId);
    expect((await session(request, id, runId)).status).toBe('ended');
  });
}
