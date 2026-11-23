import type { Locator } from '@playwright/test';
import {
  actionMenu,
  activate,
  bounds,
  dockEntry,
  expectCurrentWorkspace,
  launch,
  menuItem,
  openDockOptions,
  openWorkspaceControls,
  openWorkspaceShortcutMenu,
  persistedLayout,
  switchWorkspace,
  workspaceButton,
} from './helpers';
import { applicationMenu } from './desktop-application-helpers';
import { expect, openCodexManager, test } from './codex-helpers';
import { expectRect } from './work-area-helpers';

test('compact windows fit the visible application menu row as window focus changes', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const choose = (target: Locator) => (touch ? target.tap() : target.click());
  await page.setViewportSize({ width: 390, height: 844 });

  await choose(page.getByRole('button', { name: 'Workspace menu' }));
  await choose(menuItem(page, 'Applications'));
  await choose(
    page
      .getByRole('menu', { name: 'Applications', exact: true })
      .getByRole('menuitem', { name: 'GitHub Review', exact: true }),
  );
  const review = page.locator('[data-app-id="github-review"]');
  const pullRequests = review.getByRole('tab', {
    name: 'Pull requests',
    exact: true,
  });
  await choose(pullRequests);
  const menus = page.getByRole('navigation', { name: 'Application menus' });
  const withoutMenu = { x: 4, y: 30, width: 382, height: 754 };
  const withMenu = { ...withoutMenu, y: 64, height: 720 };
  await expect(menus).toHaveCount(0);
  await expectRect(review, withoutMenu);

  await launch(page, 'machines');
  const machines = page.locator('[data-app-id="machines"]');
  const viewMenu = menus.getByRole('button', { name: 'View', exact: true });
  await expect(viewMenu).toBeVisible();
  await expectRect(machines, withMenu);
  await expect(review).toBeHidden();
  await expect(review).toHaveCSS('height', `${withoutMenu.height}px`);
  expect((await bounds(menus)).height).toBe(32);
  await choose(viewMenu);
  await expect(menuItem(page, 'VM Host')).toBeVisible();
  await page.keyboard.press('Escape');

  await choose(
    machines.getByRole('button', { name: 'Shade Virtual Machines' }),
  );
  await expect(menus).toHaveCount(0);
  await expect.poll(async () => (await bounds(machines)).y).toBe(withMenu.y);
  await choose(
    machines.getByRole('button', { name: 'Unshade Virtual Machines' }),
  );
  await expectRect(machines, withMenu);
  await expect(viewMenu).toBeVisible();

  await activate(page, review, touch);
  await expect(menus).toHaveCount(0);
  await expectRect(review, withoutMenu);
  await expect(machines).toBeHidden();
  await expect(machines).toHaveCSS('height', `${withMenu.height}px`);
  await expect(pullRequests).toHaveAttribute('aria-selected', 'true');
});

test('compact Workspace commands remain reachable with selected desktop icons', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const choose = async (target: Locator) =>
    touch ? target.tap() : target.click();
  await page.setViewportSize({ width: 390, height: 844 });
  for (const appId of ['project-files', 'terminal']) {
    const options = await openDockOptions(page, appId, touch);
    await choose(menuItem(options, 'Show on Desktop'));
  }
  await switchWorkspace(page, 2, touch);
  const workspaceMenu = page.getByRole('button', {
    name: 'Workspace menu',
    exact: true,
  });
  for (const id of ['system:project-files', 'system:terminal']) {
    const icon = page.locator(`[data-icon-id="${id}"]`);
    await choose(icon);
    await expect(icon).toHaveAttribute('aria-pressed', 'true');
    await choose(workspaceMenu);
    const menu = page.getByRole('menu', { name: 'Workspace', exact: true });
    await expect(menuItem(menu, 'Applications')).toBeVisible();
    await choose(menuItem(menu, 'Clock'));
    const dialog = page.getByRole('dialog', { name: 'Clock', exact: true });
    await expect(dialog).toBeVisible();
    await choose(dialog.getByRole('button', { name: 'OK', exact: true }));
    await expect(workspaceMenu).toBeFocused();
    await expect(icon).toHaveAttribute('aria-pressed', 'true');
  }
  await page.setViewportSize({ width: 320, height: 720 });
  await choose(workspaceMenu);
  await choose(menuItem(page, 'Welcome'));
  const welcome = page.locator('[data-app-id="welcome"]');
  await expect(welcome).toHaveClass(/is-active/);
  await expect(
    welcome.getByRole('button', { name: 'Remote access', exact: true }),
  ).toBeInViewport({ ratio: 1 });
  await expect(
    page.getByRole('navigation', { name: 'Application menus' }),
  ).toHaveCount(0);
});

for (const viewport of [
  { width: 701, height: 852 },
  { width: 320, height: 568 },
]) {
  test(`Welcome keeps launchers visible and opens setup on demand at ${viewport.width}×${viewport.height}`, async ({
    page,
  }, info) => {
    const touch = info.project.use.hasTouch === true;
    const choose = (control: Locator) =>
      touch ? control.tap() : control.click();
    await page.setViewportSize(viewport);
    const welcome = page.locator('[data-app-id="welcome"]');
    const workApps = welcome.getByRole('group', { name: 'Work applications' });
    const remoteAccess = welcome.getByRole('button', {
      name: 'Remote access',
      exact: true,
    });
    const more = welcome.getByRole('button', {
      name: 'More welcome actions',
      exact: true,
    });
    const showDesktop = welcome.getByRole('button', {
      name: 'Show Desktop',
      exact: true,
    });
    await expect(workApps.getByRole('button')).toHaveCount(4);
    for (const name of [
      'Codex',
      'Virtual Machines',
      'Project Files',
      'Terminal',
    ])
      await expect(
        workApps.getByRole('button', { name, exact: true }),
      ).toBeInViewport({ ratio: 1 });
    for (const control of [remoteAccess, more, showDesktop])
      await expect(control).toBeInViewport({ ratio: 1 });
    await expect(
      welcome.getByText('Notifications', { exact: true }),
    ).toHaveCount(0);
    await expect(
      welcome.getByText('Application Manager', { exact: true }),
    ).toHaveCount(0);
    await expect(welcome.locator('details')).toHaveCount(0);

    await choose(more);
    await expect(menuItem(page, 'Application Manager')).toBeVisible();
    await choose(menuItem(page, 'Browser installation guide'));
    const guide = page.getByRole('dialog', {
      name: 'Browser installation guide',
      exact: true,
    });
    await expect(guide).toBeVisible();
    for (const copy of [
      'stable AXV address',
      'Chrome or Edge',
      'Share → Add to Home Screen',
      'Keep AXV running and reachable.',
    ])
      await expect(guide).toContainText(copy);
    await choose(guide.getByRole('button', { name: 'OK', exact: true }));
    await expect(guide).toBeHidden();
    await expect(more).toBeFocused();

    const preferences = page.locator('[data-app-id="appearance"]');
    const remoteSection = preferences.getByRole('group', {
      name: 'Remote access',
      exact: true,
    });
    const openRemoteAccess = async () => {
      await choose(remoteAccess);
      await expect(preferences).toHaveClass(/is-active/);
      await expect(remoteSection).toBeFocused();
      for (const name of ['Open Tailscale', 'Open Cloudflare'])
        await expect(
          remoteSection.getByRole('button', { name, exact: true }),
        ).toBeInViewport({ ratio: 1 });
    };
    await openRemoteAccess();
    await expect(preferences).toHaveAttribute(
      'data-window-id',
      'appearance:main',
    );
    await remoteSection.evaluate((section) => {
      section.setAttribute('data-mount-probe', 'remote-access');
    });
    await activate(page, welcome, touch);
    await openRemoteAccess();
    await expect(preferences).toHaveCount(1);
    await expect(remoteSection).toHaveAttribute(
      'data-mount-probe',
      'remote-access',
    );
  });
}

test('mobile shell keeps long workspace names and connection states within 320 pixels', async ({
  page,
  request,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const choose = async (control: Locator) =>
    touch ? control.tap() : control.click();
  await launch(page, 'terminal');
  const terminal = page.locator('[data-app-id="terminal"]');
  await page.setViewportSize({ width: 320, height: 568 });
  await openWorkspaceControls(page, touch);
  await choose(menuItem(page, 'Rename Workspace'));
  const rename = page.getByRole('dialog', {
    name: 'Rename Workspace',
    exact: true,
  });
  const workspaceName = 'E2E Mobile workspace with a very long name'.slice(
    0,
    40,
  );
  await rename
    .getByRole('textbox', { name: 'Workspace name', exact: true })
    .fill(workspaceName);
  await choose(rename.getByRole('button', { name: 'OK', exact: true }));
  await expect(rename).toBeHidden();
  const header = page.locator('.ns-desktop-status');
  const firstWorkspace = workspaceButton(page, 1);
  await expect(firstWorkspace).toHaveAccessibleName(
    `Workspace 1 · ${workspaceName}`,
  );
  await expect(firstWorkspace).toHaveText('1');
  await expect
    .poll(
      async () => (await persistedLayout(page, request))?.workspaces[0].name,
    )
    .toBe(workspaceName);
  const status = header.getByRole('status');
  const menus = page.getByRole('navigation', { name: 'Application menus' });
  const expectShellFits = async (label: string) => {
    await expect(status).toHaveText(label);
    const headerBox = await bounds(header);
    expect(headerBox).toEqual({ x: 0, y: 0, width: 320, height: 26 });
    let previousRight = 0;
    for (const control of [
      header.getByRole('button', { name: 'Workspace menu', exact: true }),
      ...[1, 2, 3, 4].map((index) => workspaceButton(page, index)),
      header.getByRole('button', { name: 'Window Overview', exact: true }),
      status,
    ]) {
      const box = await bounds(control);
      expect(box.x).toBeGreaterThanOrEqual(previousRight);
      expect(box.x + box.width).toBeLessThanOrEqual(320);
      expect(box.y).toBeGreaterThanOrEqual(0);
      expect(box.y + box.height).toBeLessThanOrEqual(26);
      previousRight = box.x + box.width;
    }
    const menuBox = await bounds(menus);
    expect(menuBox.y).toBeGreaterThanOrEqual(headerBox.y + headerBox.height);
    expect(menuBox.x + menuBox.width).toBeLessThanOrEqual(320);
    const frame = await bounds(terminal);
    expect(menuBox.y + menuBox.height).toBeLessThanOrEqual(frame.y);
    await expect(menus.locator('.ns-menu-title')).toHaveText('Terminal');
    for (const group of ['File', 'Edit', 'Session'])
      await expect(
        menus.getByRole('button', { name: group, exact: true }),
      ).toBeInViewport();
    for (const group of ['Window', 'Help'])
      await expect(
        menus.getByRole('button', { name: group, exact: true }),
      ).toHaveCount(0);
  };
  await expectShellFits('Connected');
  await page.route('**/api/health', async (route) => {
    if (route.request().method() === 'GET') await route.abort('failed');
    else await route.continue();
  });
  try {
    await expectShellFits('Offline');
    await expect(firstWorkspace).toHaveAccessibleName(
      `Workspace 1 · ${workspaceName}`,
    );
  } finally {
    await page.unroute('**/api/health');
  }
  await expectShellFits('Connected');
});

test('Window Overview restores a lone minimized window across workspaces and keeps its place when empty', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const choose = (control: Locator) =>
    touch ? control.tap() : control.click();
  await page.setViewportSize({ width: 604, height: 852 });
  await expect(page.locator('.ns-desktop')).toHaveClass(/has-side-dock/);
  const overview = page.getByRole('button', {
    name: 'Window Overview',
    exact: true,
  });
  const welcome = page.locator('[data-app-id="welcome"]');
  await expect(overview).toBeEnabled();
  await expect(overview).toHaveText('');
  await expect(overview).toHaveAttribute('aria-pressed', 'false');
  await expect(overview).toHaveAttribute(
    'title',
    'Window Overview · 1 open window',
  );
  const buttonBounds = await bounds(overview);
  await choose(welcome.getByRole('button', { name: 'Miniaturize Welcome' }));
  await switchWorkspace(page, 2, touch);
  await expect(welcome).toBeHidden();
  await expect(overview).toBeEnabled();
  await expect(overview).toHaveAttribute(
    'title',
    'Window Overview · 1 open window',
  );
  await choose(overview);
  await expect(overview).toHaveAttribute('aria-pressed', 'true');
  await expect(welcome.locator('.ns-window-titlebar')).toHaveAttribute(
    'inert',
    '',
  );
  const contents = welcome.locator('.ns-window-content');
  await expect(contents).toHaveAttribute('inert', '');
  await expect(contents).toHaveAttribute('aria-hidden', 'true');
  const thumbnail = await bounds(welcome);
  const dock = await bounds(page.locator('.ns-dock'));
  expect(thumbnail.x + thumbnail.width).toBeLessThanOrEqual(dock.x);
  const pick = welcome.getByRole('button', { name: 'Switch to Welcome' });
  await pick.focus();
  await expect(pick).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(overview).toHaveAttribute('aria-pressed', 'false');
  await expect(overview).toBeFocused();
  await choose(overview);
  await choose(pick);
  await expectCurrentWorkspace(page, 1);
  await expect(welcome).toHaveClass(/is-active/);
  await expect(welcome).not.toHaveAttribute('data-minimized', 'true');
  await expect(contents).not.toHaveAttribute('inert', '');
  await expect(overview).toHaveAttribute('aria-pressed', 'false');
  await choose(welcome.getByRole('button', { name: 'Close Welcome' }));
  await expect(overview).toBeDisabled();
  await expect(overview).toHaveAttribute(
    'title',
    'Window Overview · 0 open windows',
  );
  expect(await bounds(overview)).toEqual(buttonBounds);
});

test('context-only Dock entries do not offer unusable standalone launchers', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const choose = (control: Locator) =>
    touch ? control.tap() : control.click();
  await choose(
    page.getByRole('button', { name: 'Workspace menu', exact: true }),
  );
  await choose(menuItem(page, 'About This Desktop'));
  const about = page.locator('[data-app-id="about"]');
  await expect(about).toBeVisible();
  const options = await openDockOptions(page, 'about', touch);
  await expect(menuItem(options, 'Activate Application')).toBeVisible();
  for (const label of [
    'Keep in Dock',
    'Show on Desktop',
    'Add Workspace Shortcut',
  ])
    await expect(menuItem(options, label)).toHaveCount(0);
  await page.keyboard.press('Escape');
  await choose(about.getByRole('button', { name: 'Close About AXV' }));
  await expect(dockEntry(page, 'about')).toHaveCount(0);
});

test('compact application menus return keyboard focus and run commands on the selected window', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const choose = async (control: Locator) =>
    touch ? control.tap() : control.click();
  await page.setViewportSize({ width: 320, height: 844 });
  await launch(page, 'terminal');
  const first = page.locator('[data-app-id="terminal"][aria-label="Terminal"]');
  const appButton = page
    .getByRole('navigation', { name: 'Application menus' })
    .getByRole('button', { name: 'File', exact: true });
  await expect(appButton).toBeInViewport();
  await appButton.focus();
  await appButton.press('Enter');
  const fileMenu = page.getByRole('menu', { name: 'File', exact: true });
  await expect(fileMenu).toBeFocused();
  await expect(menuItem(fileMenu, 'New Terminal Window')).toBeEnabled();
  await fileMenu.press('Escape');
  await expect(appButton).toBeFocused();
  await expect(page.locator('.ns-menu-layer')).toHaveCount(0);
  await appButton.press('Enter');
  await fileMenu.press('Tab');
  await expect(appButton).toBeFocused();
  await expect(page.locator('.ns-menu-layer')).toHaveCount(0);

  await applicationMenu(page, 'File', touch);
  await choose(menuItem(fileMenu, 'New Terminal Window'));
  const second = page.locator(
    '[data-app-id="terminal"][aria-label="Terminal 2"]',
  );
  await expect(second).toHaveClass(/is-active/);
  await expect(fileMenu).toHaveCount(0);
  await activate(page, first, touch);
  await applicationMenu(page, 'File', touch);
  await choose(menuItem(fileMenu, 'Close Window'));
  await choose(
    page
      .getByRole('dialog', { name: 'End host shell?', exact: true })
      .getByRole('button', { name: 'End Session', exact: true }),
  );
  await expect(first).toHaveCount(0);
  await expect(fileMenu).toHaveCount(0);
  await expect(second).toHaveClass(/is-active/);
  const menus = page.getByRole('navigation', { name: 'Application menus' });
  await expect(menus.locator('.ns-menu-title')).toHaveText('Terminal');
  await applicationMenu(page, 'Edit', touch);
  await expect(
    menuItem(
      page.getByRole('menu', { name: 'Edit', exact: true }),
      'Paste text',
    ),
  ).toBeVisible();
  await expect(
    menuItem(
      page.getByRole('menu', { name: 'Edit', exact: true }),
      'Arrange Icons',
    ),
  ).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect(
    menus.getByRole('button', { name: 'Edit', exact: true }),
  ).toBeFocused();
});

test('short desktops preserve application space, shell commands and saved geometry', async ({
  page,
  request,
  browserName,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const choose = async (target: Locator) =>
    touch ? target.tap() : target.click();
  await page.setViewportSize({ width: 1440, height: 900 });
  const welcome = page.locator('[data-app-id="welcome"]');
  await launch(page, 'gallery');
  await launch(page, 'appearance');
  await launch(page, 'terminal');
  const terminal = page.locator('[data-app-id="terminal"]');
  const screen = terminal.getByTestId('terminal-screen');
  await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  await screen.evaluate((element) => {
    element.dataset.mountProbe = 'original-terminal';
  });
  const terminalId = await terminal.getAttribute('data-window-id');
  const normal = await bounds(terminal);
  const savedGeometry = async () => {
    const layout = await persistedLayout(page, request);
    const win = layout?.windows.find(
      (item: { id: string }) => item.id === terminalId,
    );
    return win && { x: win.x, y: win.y, width: win.width, height: win.height };
  };
  await expect.poll(savedGeometry).toEqual(normal);
  const savedIconPositions = async () => {
    const layout = await persistedLayout(page, request);
    return {
      wide: layout.icons,
      compact: layout.compactIcons,
    };
  };
  const iconPositions = await savedIconPositions();
  await page.setViewportSize({ width: 568, height: 320 });
  const desktop = page.locator('.ns-desktop');
  await expect(desktop).toHaveClass(/is-short/);
  await expect(desktop).toHaveClass(/has-side-dock/);
  await expect
    .poll(() => bounds(terminal))
    .toEqual({
      x: 4,
      y: 64,
      width: 500,
      height: 248,
    });
  const dock = page.getByRole('complementary', { name: 'Application Dock' });
  const header = page.locator('.ns-desktop-status');
  const menus = page.getByRole('navigation', { name: 'Application menus' });
  const frame = await bounds(terminal);
  const dockBox = await bounds(dock);
  expect(dockBox.x).toBeGreaterThanOrEqual(frame.x + frame.width);
  expect(dockBox.y + dockBox.height).toBeLessThanOrEqual(320);
  for (const control of [header, menus]) {
    const box = await bounds(control);
    expect(box.y + box.height).toBeLessThanOrEqual(frame.y);
  }
  await expect(workspaceButton(page, 1)).toBeInViewport();
  expect(
    (await bounds(menus.getByRole('button', { name: 'File', exact: true })))
      .height,
  ).toBeGreaterThanOrEqual(32);
  const windowCount = await page.locator('[data-window-id]').count();
  const scroller = dock.locator('.ns-dock-scroll');
  await expect(
    dock.getByRole('button', { name: 'Clock and date' }),
  ).toHaveCount(0);
  await expect(dock.locator('.ns-dock-tile').first()).toHaveCSS(
    'touch-action',
    'pan-y',
  );
  await expect(dock.locator('.ns-dock-entry').first()).toHaveCSS(
    'flex-shrink',
    '0',
  );
  if (touch && browserName === 'chromium') {
    const protocol = await page.context().newCDPSession(page);
    try {
      const start = await dock
        .locator('.ns-dock-entry')
        .evaluateAll((entries) => {
          const area = entries[0]!.parentElement!.getBoundingClientRect();
          const box = entries
            .map((element) => element.getBoundingClientRect())
            .filter((box) => box.top >= area.top && box.bottom <= area.bottom)
            .at(-1)!;
          return { x: box.left + 24, y: box.bottom - 8 };
        });
      await protocol.send('Input.dispatchTouchEvent', {
        type: 'touchStart',
        touchPoints: [start],
      });
      for (let step = 1; step <= 8; step++)
        await protocol.send('Input.dispatchTouchEvent', {
          type: 'touchMove',
          touchPoints: [{ x: start.x, y: start.y - step * 18 }],
        });
      await protocol.send('Input.dispatchTouchEvent', {
        type: 'touchEnd',
        touchPoints: [],
      });
      await expect
        .poll(() => scroller.evaluate((element) => element.scrollTop))
        .toBeGreaterThan(0);
    } finally {
      await protocol.detach();
    }
  }
  const add = dock.getByRole('button', {
    name: 'Dock menu',
    exact: true,
  });
  await add.focus();
  await expect
    .poll(async () => {
      const button = await bounds(add);
      return (
        button.y >= dockBox.y &&
        button.y + button.height <= dockBox.y + dockBox.height
      );
    })
    .toBe(true);
  await add.press('Enter');
  await expect(
    page.getByRole('menu', { name: 'Dock', exact: true }),
  ).toBeVisible();
  await expect(menuItem(page, 'Add application')).toBeInViewport();
  await page.keyboard.press('Escape');
  await choose(
    page.getByRole('button', { name: 'Workspace menu', exact: true }),
  );
  await choose(menuItem(page, 'Clock'));
  const clockDialog = page.getByRole('dialog', { name: 'Clock', exact: true });
  await expect(clockDialog).toBeVisible();
  await choose(clockDialog.getByRole('button', { name: 'OK', exact: true }));
  expect(await page.locator('[data-window-id]').count()).toBe(windowCount);
  await choose(terminal.getByRole('button', { name: /^Window menu for / }));
  await expect(menuItem(page, 'Miniaturize')).toBeInViewport();
  await page.keyboard.press('Escape');
  await switchWorkspace(page, 2, touch);
  await expect(terminal).toBeHidden();
  await switchWorkspace(page, 1, touch);
  await expect(screen).toHaveAttribute('data-mount-probe', 'original-terminal');
  await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  await openWorkspaceControls(page, touch);
  await choose(menuItem(page, 'Add Active Window'));
  const title = (await terminal.getAttribute('aria-label'))!;
  const shortcut = await openWorkspaceShortcutMenu(page, title, touch);
  await choose(menuItem(shortcut, 'Open'));
  await expect(terminal).toHaveClass(/is-active/);
  await page.keyboard.press('F10');
  await expect(menuItem(page, 'Applications')).toBeInViewport();
  await page.keyboard.press('Escape');
  const landscape = info.outputPath('568x320-short-desktop.png');
  await page.screenshot({ path: landscape, fullPage: true });
  await info.attach('short-landscape-desktop', {
    path: landscape,
    contentType: 'image/png',
  });
  await choose(terminal.getByRole('button', { name: /^Miniaturize / }));
  await activate(page, welcome, touch);
  await expect
    .poll(() => bounds(welcome))
    .toEqual({ x: 4, y: 30, width: 500, height: 206 });
  const miniature = page.getByRole('button', {
    name: `Restore ${title}`,
    exact: true,
  });
  await expect(miniature).toBeInViewport();
  await choose(miniature);
  await expect
    .poll(() => bounds(terminal))
    .toEqual({ x: 4, y: 64, width: 500, height: 248 });
  for (const viewport of [
    { width: 390, height: 320 },
    { width: 320, height: 260 },
  ]) {
    await page.setViewportSize(viewport);
    await expect(desktop).not.toHaveClass(/has-side-dock/);
    await expect
      .poll(() => bounds(terminal))
      .toEqual({
        x: 4,
        y: 64,
        width: viewport.width - 8,
        height: viewport.height - 124,
      });
    await expect(
      terminal.getByRole('button', { name: /^Close / }),
    ).toBeInViewport();
    await choose(terminal.getByRole('button', { name: /^Window menu for / }));
    await expect(menuItem(page, 'Miniaturize')).toBeInViewport();
    await page.keyboard.press('Escape');
  }
  await page.setViewportSize({ width: 1440, height: 900 });
  await expect(desktop).not.toHaveClass(/is-short/);
  await expect.poll(() => bounds(terminal)).toEqual(normal);
  await expect.poll(savedGeometry).toEqual(normal);
  await expect.poll(savedIconPositions).toEqual(iconPositions);
  await expect(screen).toHaveAttribute('data-mount-probe', 'original-terminal');
  await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
});

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 1024, height: 768 },
  { width: 390, height: 844 },
]) {
  test(`Codex and Virtual Machines coexist at ${viewport.width}×${viewport.height}`, async ({
    page,
    request,
  }, info) => {
    await page.setViewportSize(viewport);
    test.skip(
      info.project.use.hasTouch === true && viewport.width !== 390,
      'Touch projects use the narrow viewport.',
    );
    const welcome = page.locator('[data-window-id="welcome:main"]');
    await activate(page, welcome);
    await expect(
      welcome.getByRole('button', { name: 'Codex', exact: true }),
    ).toBeVisible();
    const machinesLink = welcome.getByRole('button', {
      name: 'Virtual Machines',
      exact: true,
    });
    if (info.project.use.hasTouch) await machinesLink.tap();
    else await machinesLink.click();
    const machines = page.locator('[data-window-id="machines:main"]');
    await expect(machines).toBeVisible();
    const check = await request.get('/api/compute/preparation/check');
    const { platform, localService, lockedReason } = await check.json();
    const defaultServiceReady =
      localService?.status === 'ready' &&
      localService.socket === '/run/axv-host/control.sock' &&
      !lockedReason;
    const vmHost = machines.getByRole('region', {
      name: 'VM host',
      exact: true,
    });
    await expect(vmHost.getByRole('heading', { level: 2 })).toBeInViewport();
    await expect(
      vmHost.getByRole('button', {
        name:
          platform !== 'linux'
            ? 'Review'
            : defaultServiceReady
              ? 'Connect'
              : 'Check connection',
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      machines.getByRole('button', { name: 'New VM', exact: true }),
    ).toHaveCount(0);
    await expect(
      machines.getByRole('button', { name: 'Refresh', exact: true }),
    ).toHaveCount(0);
    await expect(machines.locator('.machines-toolbar')).toHaveCount(0);
    const compute = await request.get('/api/compute/info');
    expect((await compute.json()).configured).toBe(false);
    const capture = async (name: string) => {
      const path = info.outputPath(
        `${viewport.width}x${viewport.height}-${name}.png`,
      );
      await page.screenshot({ path, fullPage: true });
      await info.attach(name, { path, contentType: 'image/png' });
    };
    await capture('machines-integrated');
    const codex = await openCodexManager(page);
    await expect(
      codex.getByRole('button', { name: 'New Session', exact: true }),
    ).toBeEnabled();
    await capture('codex-integrated');
    await activate(page, welcome);
    await capture('welcome-integrated');
    const more = welcome.getByRole('button', {
      name: 'More welcome actions',
      exact: true,
    });
    await more.focus();
    await more.press('Enter');
    await expect(menuItem(page, 'Preferences')).toBeInViewport();
    await expect(menuItem(page, 'About AXV')).toBeInViewport();
    await capture('welcome-more-menu');
    await page.keyboard.press('Escape');
    await expect(more).toBeFocused();
    await actionMenu(
      page,
      welcome,
      'More welcome actions',
      'Preferences',
      info.project.use.hasTouch === true,
    );
    const preferences = page.locator('[data-app-id="appearance"]');
    await expect(preferences).toBeVisible();
    await preferences.getByRole('button', { name: /^Close / }).click();
    await activate(page, welcome);
    await actionMenu(
      page,
      welcome,
      'More welcome actions',
      'About AXV',
      info.project.use.hasTouch === true,
    );
    const about = page.locator('[data-window-id="about:main"]');
    await expect(about).toBeVisible();
    await capture('about-integrated');
    await about.getByRole('button', { name: /^Close / }).click();
    // Layout restoration must retain both registered application identities.
    await expect
      .poll(async () => {
        const clientId = await page.evaluate(() =>
          localStorage.getItem('nextstep-browser-id'),
        );
        const response = await request.get(
          `/api/bootstrap?clientId=${encodeURIComponent(clientId!)}`,
        );
        const layout = (await response.json()).layout;
        return ['machines', 'codex'].every((appId) =>
          layout?.windows.some(
            (window: { appId: string }) => window.appId === appId,
          ),
        );
      })
      .toBe(true);
    await page.reload();
    await expect(machines).toBeAttached();
    await expect(codex).toBeAttached();
  });
}
