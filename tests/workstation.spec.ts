import type { Locator } from '@playwright/test';
import {
  actionMenu,
  activate,
  bounds,
  createEntry,
  editorWindow,
  entries,
  filesWindow,
  launch,
  menuItem,
  openDocument,
  openDockOptions,
  openWorkspaceControls,
  readContent,
  persistedLayout,
  switchWorkspace,
} from './helpers';
import { applicationMenu } from './desktop-application-helpers';
import { expect, openCodexManager, test } from './codex-helpers';

test('compact Workspace commands remain reachable with selected desktop icons', async ({
  page,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const choose = async (target: Locator) =>
    touch ? target.tap() : target.click();
  await page.setViewportSize({ width: 390, height: 844 });
  const filesMenu = await openDockOptions(page, 'files', touch);
  await choose(menuItem(filesMenu, 'Show on Desktop'));
  await switchWorkspace(page, 2, touch);
  const workspaceMenu = page.getByRole('button', {
    name: 'Workspace menu',
    exact: true,
  });
  for (const id of ['application:files', 'system:documents']) {
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
});

test('mobile shell keeps long workspace names and connection states within 320 pixels', async ({
  page,
  request,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const choose = async (control: Locator) =>
    touch ? control.tap() : control.click();
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
  const workspaceButton = page.getByRole('button', { name: /^Workspace 1:/ });
  await expect(workspaceButton).toHaveAccessibleName(
    `Workspace 1: ${workspaceName}`,
  );
  await expect
    .poll(async () => (await persistedLayout(page, request)).workspaces[0].name)
    .toBe(workspaceName);
  const header = page.locator('.ns-desktop-status');
  const status = header.getByRole('status');
  const clip = page.getByRole('complementary', { name: 'Workspace Clip' });
  const menus = page.getByRole('navigation', { name: 'Application menus' });
  const expectShellFits = async (label: string) => {
    await expect(status).toHaveText(label);
    const headerBox = await bounds(header);
    expect(headerBox).toEqual({ x: 0, y: 0, width: 320, height: 26 });
    let previousRight = 0;
    for (const control of [
      page.getByRole('button', { name: 'Workspace menu', exact: true }),
      page.getByRole('button', { name: 'Show all windows', exact: true }),
      status,
    ]) {
      const box = await bounds(control);
      expect(box.x).toBeGreaterThanOrEqual(previousRight);
      expect(box.x + box.width).toBeLessThanOrEqual(320);
      expect(box.y).toBeGreaterThanOrEqual(0);
      expect(box.y + box.height).toBeLessThanOrEqual(26);
      previousRight = box.x + box.width;
    }
    const clipBox = await bounds(clip);
    const menuBox = await bounds(menus);
    expect(clipBox.x + clipBox.width).toBeLessThanOrEqual(menuBox.x);
    expect(menuBox.x + menuBox.width).toBeLessThanOrEqual(320);
    const frame = await bounds(filesWindow(page));
    for (const box of [clipBox, menuBox])
      expect(box.y + box.height).toBeLessThanOrEqual(frame.y);
    await expect(menus.locator('.ns-menu-title')).toHaveText('Files');
    for (const group of ['File', 'Edit', 'View', 'Window', 'Help'])
      await expect(
        menus.getByRole('button', { name: group, exact: true }),
      ).toBeInViewport();
  };
  await expectShellFits('Connected');
  await page.route('**/api/entries', async (route) => {
    if (route.request().method() === 'GET') await route.abort('failed');
    else await route.continue();
  });
  try {
    await expectShellFits('Offline');
    await expect(workspaceButton).toHaveAccessibleName(
      `Workspace 1: ${workspaceName}`,
    );
  } finally {
    await page.unroute('**/api/entries');
  }
  await expectShellFits('Connected');
});

test('compact application menus keep keyboard focus and pinned commands with their owner', async ({
  page,
  request,
}, info) => {
  const touch = info.project.use.hasTouch === true;
  const choose = async (control: Locator) =>
    touch ? control.tap() : control.click();
  await page.setViewportSize({ width: 320, height: 844 });
  const firstEntry = await createEntry(request, 'E2E Compact Owner A.txt');
  const secondEntry = await createEntry(request, 'E2E Compact Owner B.txt');
  await page.reload();
  const first = await openDocument(page, firstEntry, touch);
  const firstText = first.getByRole('textbox', { name: 'Document text' });
  await firstText.fill('Only the first document is saved by its pinned menu.');
  const second = await openDocument(page, secondEntry, touch);
  const secondText = second.getByRole('textbox', { name: 'Document text' });
  await secondText.fill('The second document stays an unsaved draft.');
  await activate(page, first, touch);
  const appButton = page
    .getByRole('navigation', { name: 'Application menus' })
    .getByRole('button', { name: 'File', exact: true });
  await expect(appButton).toBeInViewport();
  await appButton.focus();
  await appButton.press('Enter');
  const fileMenu = page.getByRole('menu', { name: 'File', exact: true });
  await expect(fileMenu).toBeFocused();
  await expect(menuItem(fileMenu, 'Save')).toBeEnabled();
  await fileMenu.press('Escape');
  await expect(appButton).toBeFocused();
  await expect(page.locator('.ns-menu-layer')).toHaveCount(0);
  await appButton.press('Enter');
  await fileMenu.press('Tab');
  await expect(appButton).toBeFocused();
  await expect(page.locator('.ns-menu-layer')).toHaveCount(0);

  await applicationMenu(page, 'File', touch);
  const windowCount = await page.locator('[data-window-id]').count();
  const pin = page.getByRole('button', { name: 'Pin File menu', exact: true });
  await pin.focus();
  await pin.press('Enter');
  const pinned = page.locator(
    `.ns-pinned-menu[data-owner-window-id="notes:${firstEntry.id}"]`,
  );
  await expect(pinned).toBeVisible();
  await expect(pinned.locator('.ns-menu-owner')).toHaveText(
    `${firstEntry.name} *`,
  );
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.locator('[data-window-id]')).toHaveCount(windowCount);
  await expect(appButton).toBeFocused();
  await activate(page, second, touch);
  await expect(second).toHaveClass(/is-active/);
  await choose(menuItem(pinned, 'Save'));
  await expect(first.getByTestId('save-status')).toHaveText('Saved');
  await expect(second.getByTestId('save-status')).toHaveText('Unsaved changes');
  expect(await readContent(request, firstEntry.id)).toBe(
    'Only the first document is saved by its pinned menu.',
  );
  expect(await readContent(request, secondEntry.id)).toBe('Initial text');
  await choose(menuItem(pinned, 'Close Window'));
  await expect(first).toHaveCount(0);
  await expect(pinned).toHaveCount(0);
  await expect(secondText).toHaveValue(
    'The second document stays an unsaved draft.',
  );
  await activate(page, filesWindow(page), touch);
  const menus = page.getByRole('navigation', { name: 'Application menus' });
  await expect(menus.locator('.ns-menu-title')).toHaveText('Files');
  await applicationMenu(page, 'View', touch);
  await expect(
    menuItem(
      page.getByRole('menu', { name: 'View', exact: true }),
      'Arrange Icons',
    ),
  ).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(
    menus.getByRole('button', { name: 'View', exact: true }),
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
  const entry = await createEntry(request, 'E2E Short Desktop.txt');
  await page.reload();
  const note = await openDocument(page, entry, touch);
  const editor = note.getByRole('textbox', { name: 'Document text' });
  await editor.fill(
    'This draft survives a short viewport and workspace changes.',
  );
  const normal = await bounds(note);
  const savedGeometry = async () => {
    const layout = await persistedLayout(page, request);
    const win = layout?.windows.find(
      (item: { id: string }) => item.id === `notes:${entry.id}`,
    );
    return win && { x: win.x, y: win.y, width: win.width, height: win.height };
  };
  await expect.poll(savedGeometry).toEqual(normal);
  const savedShellPositions = async () => {
    const layout = await persistedLayout(page, request);
    return {
      clip: layout.clipPosition,
      wide: layout.icons,
      compact: layout.compactIcons,
    };
  };
  const shellPositions = await savedShellPositions();
  await page.setViewportSize({ width: 568, height: 320 });
  const desktop = page.locator('.ns-desktop');
  await expect(desktop).toHaveClass(/is-short/);
  await expect(desktop).toHaveClass(/has-side-dock/);
  await expect
    .poll(() => bounds(note))
    .toEqual({
      x: 4,
      y: 64,
      width: 496,
      height: 248,
    });
  const dock = page.getByRole('complementary', { name: 'Application Dock' });
  const clip = page.getByRole('complementary', { name: 'Workspace Clip' });
  const menus = page.getByRole('navigation', { name: 'Application menus' });
  const frame = await bounds(note);
  const dockBox = await bounds(dock);
  expect(dockBox.x).toBeGreaterThanOrEqual(frame.x + frame.width);
  expect(dockBox.y + dockBox.height).toBeLessThanOrEqual(320);
  for (const control of [clip, menus]) {
    const box = await bounds(control);
    expect(box.y + box.height).toBeLessThanOrEqual(frame.y);
  }
  for (const control of [
    clip.getByRole('button', { name: /^Workspace 1:/ }),
    menus.getByRole('button', { name: 'Window', exact: true }),
  ])
    expect((await bounds(control)).height).toBeGreaterThanOrEqual(32);
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
  await applicationMenu(page, 'Window', touch);
  await expect(menuItem(page, 'Miniaturize')).toBeInViewport();
  await page.keyboard.press('Escape');
  await switchWorkspace(page, 2, touch);
  await expect(note).toBeHidden();
  await switchWorkspace(page, 1, touch);
  await expect(editor).toHaveValue(
    'This draft survives a short viewport and workspace changes.',
  );
  await openWorkspaceControls(page, touch);
  await choose(menuItem(page, 'Add Active Window'));
  await openWorkspaceControls(page, touch);
  await choose(menuItem(page, 'Shortcuts'));
  const title = (await note.getAttribute('aria-label'))!;
  await choose(menuItem(page, title));
  await choose(menuItem(page, 'Open'));
  await expect(editorWindow(page, entry.id)).toHaveClass(/is-active/);
  await page.keyboard.press('F10');
  await expect(menuItem(page, 'Applications')).toBeInViewport();
  await page.keyboard.press('Escape');
  const landscape = info.outputPath('568x320-short-desktop.png');
  await page.screenshot({ path: landscape, fullPage: true });
  await info.attach('short-landscape-desktop', {
    path: landscape,
    contentType: 'image/png',
  });
  await choose(note.getByRole('button', { name: /^Miniaturize / }));
  await activate(page, filesWindow(page), touch);
  await expect
    .poll(() => bounds(filesWindow(page)))
    .toEqual({ x: 4, y: 64, width: 496, height: 172 });
  const miniature = page.getByRole('button', {
    name: `Restore ${title}`,
    exact: true,
  });
  await expect(miniature).toBeInViewport();
  await choose(miniature);
  await expect
    .poll(() => bounds(note))
    .toEqual({ x: 4, y: 64, width: 496, height: 248 });
  for (const viewport of [
    { width: 390, height: 320 },
    { width: 320, height: 260 },
  ]) {
    await page.setViewportSize(viewport);
    await expect(desktop).not.toHaveClass(/has-side-dock/);
    await expect
      .poll(() => bounds(note))
      .toEqual({
        x: 4,
        y: 64,
        width: viewport.width - 8,
        height: viewport.height - 128,
      });
    await expect(
      note.getByRole('button', { name: /^Close / }),
    ).toBeInViewport();
    await applicationMenu(page, 'Window', touch);
    await expect(menuItem(page, 'Miniaturize')).toBeInViewport();
    await page.keyboard.press('Escape');
  }
  await page.setViewportSize({ width: 1440, height: 900 });
  await expect(desktop).not.toHaveClass(/is-short/);
  await expect.poll(() => bounds(note)).toEqual(normal);
  await expect.poll(savedGeometry).toEqual(normal);
  await expect.poll(savedShellPositions).toEqual(shellPositions);
  await expect(editor).toHaveValue(
    'This draft survives a short viewport and workspace changes.',
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
      welcome.getByRole('button', { name: /^Codex / }),
    ).toBeVisible();
    const machinesLink = welcome.getByRole('button', {
      name: /^Virtual Machines /,
    });
    if (info.project.use.hasTouch) await machinesLink.tap();
    else await machinesLink.click();
    const machines = page.locator('[data-window-id="machines:main"]');
    await expect(machines).toBeVisible();
    await expect(
      machines.getByRole('heading', { name: 'Enable virtual machines' }),
    ).toBeVisible();
    await expect(
      machines.getByRole('button', { name: 'Review', exact: true }),
    ).toBeVisible();
    await expect(
      machines.getByRole('button', { name: 'New VM', exact: true }),
    ).toHaveCount(0);
    await expect(
      machines.getByRole('button', { name: 'Refresh', exact: true }),
    ).toBeInViewport();
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

test('Recycler menus preserve their selection and confirm permanent deletion', async ({
  page,
  request,
  isMobile,
}, info) => {
  const choose = (control: Locator) =>
    isMobile ? control.tap() : control.click();
  const first = await createEntry(request, 'E2E Recycler original.txt');
  const second = await createEntry(request, 'E2E Recycler later.txt');
  for (const entry of [first, second])
    expect(
      (await request.post(`/api/entries/${entry.id}/trash`)).ok(),
    ).toBeTruthy();
  await page.reload();
  await launch(page, 'trash');
  const recycler = page.locator('[data-app-id="trash"]');
  const original = recycler.getByRole('option', {
    name: new RegExp(first.name),
  });
  const later = recycler.getByRole('option', { name: new RegExp(second.name) });
  const more = recycler.getByRole('button', {
    name: 'More recycler actions',
    exact: true,
  });
  await choose(original);
  await more.focus();
  await more.press('Enter');
  await expect(menuItem(page, 'Delete')).toBeEnabled();
  await expect(menuItem(page, 'Empty Recycler')).toBeInViewport();
  const path = info.outputPath('recycler-more-menu.png');
  await page.screenshot({ path, fullPage: true });
  await info.attach('recycler-more-menu', { path, contentType: 'image/png' });
  await page.keyboard.press('Escape');
  await expect(more).toBeFocused();
  await actionMenu(page, recycler, 'More recycler actions', 'Delete', isMobile);
  const confirmation = page.getByRole('dialog', {
    name: 'Delete permanently?',
    exact: true,
  });
  await choose(
    confirmation.getByRole('button', { name: 'Cancel', exact: true }),
  );
  await expect(original).toBeVisible();
  await expect(later).toBeVisible();

  await choose(more);
  // A background selection change must not retarget an already open menu.
  await later.evaluate((element) => (element as HTMLButtonElement).click());
  await expect(later).toHaveAttribute('aria-selected', 'true');
  await choose(menuItem(page, 'Delete'));
  await choose(
    confirmation.getByRole('button', { name: 'Delete', exact: true }),
  );
  await expect(original).toHaveCount(0);
  await expect(later).toBeVisible();
  await expect
    .poll(async () =>
      (await entries(request)).some((entry) => entry.id === first.id),
    )
    .toBe(false);
  await expect
    .poll(async () =>
      (await entries(request)).some((entry) => entry.id === second.id),
    )
    .toBe(true);

  await actionMenu(
    page,
    recycler,
    'More recycler actions',
    'Empty Recycler',
    isMobile,
  );
  const empty = page.getByRole('dialog', {
    name: 'Empty the Recycler?',
    exact: true,
  });
  await choose(empty.getByRole('button', { name: 'Cancel', exact: true }));
  await expect(later).toBeVisible();
  await actionMenu(
    page,
    recycler,
    'More recycler actions',
    'Empty Recycler',
    isMobile,
  );
  await choose(empty.getByRole('button', { name: 'Delete', exact: true }));
  await expect(later).toHaveCount(0);
});
