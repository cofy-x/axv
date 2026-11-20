import {
  activate,
  bounds,
  expectCurrentWorkspace,
  dockEntry,
  expect,
  launch,
  menuItem,
  openDockOptions,
  openWorkspaceControls,
  openWorkspaceShortcutMenu,
  persistedLayout,
  pinDockApplication,
  switchWorkspace,
  test,
  windowMenu,
  workspaceButton,
} from './helpers';
import { applicationMenu } from './desktop-application-helpers';

test.describe('fresh desktop defaults', () => {
  test('Welcome starts alone and preserves chosen windows and Dock pins after reload', async ({
    page,
    request,
  }) => {
    const welcome = page.locator('[data-app-id="welcome"]');
    const projects = page.locator('[data-window-id="project-files:projects"]');
    const coreApplications = ['codex', 'machines', 'project-files', 'terminal'];
    const dockOrder = () =>
      page
        .locator('.ns-dock [data-dock-app]')
        .evaluateAll((items) =>
          items.map((item) => item.getAttribute('data-dock-app')),
        );
    await expect(page.locator('[data-window-id]')).toHaveCount(1);
    await expect(projects).toHaveCount(0);
    await expect(welcome).toHaveClass(/is-active/);
    await expect(
      page.getByRole('navigation', { name: 'Application menus' }),
    ).toHaveCount(0);
    const launcher = page.getByRole('button', {
      name: 'Workspace menu',
      exact: true,
    });
    expect(await bounds(launcher)).toMatchObject({ width: 30 });
    await expect(launcher).toHaveText('');
    const icon = launcher.locator('img');
    await expect(icon).toHaveAttribute('src', '/favicon.svg');
    expect(await bounds(icon)).toMatchObject({ width: 17, height: 17 });
    await launcher.press('Enter');
    await expect(
      page.getByRole('menu', { name: 'Workspace', exact: true }),
    ).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(launcher).toBeFocused();
    for (const index of [1, 2, 3, 4]) {
      const button = workspaceButton(page, index);
      await expect(button).toHaveAccessibleName(`Workspace ${index}`);
      await expect(button).toHaveText(String(index));
      await expect(button).toHaveAttribute('aria-pressed', String(index === 1));
    }
    const workspaceMenu = await openWorkspaceControls(page);
    await expect(menuItem(workspaceMenu, 'Rename Workspace')).toBeVisible();
    await expect(menuItem(workspaceMenu, 'Switch Workspace')).toHaveCount(0);
    await page.keyboard.press('Escape');
    const workApps = welcome.getByRole('group', { name: 'Work applications' });
    const codex = workApps.getByRole('button', { name: 'Codex', exact: true });
    await expect(codex).toBeInViewport();
    await expect(codex).toHaveAccessibleDescription(
      'Start or resume coding sessions.',
    );
    await expect(
      workApps.getByRole('button', { name: 'Project Files', exact: true }),
    ).toBeInViewport();
    for (const label of ['Connect from another device', 'Install AXV']) {
      await expect(welcome.getByText(label, { exact: true })).toBeInViewport({
        ratio: 1,
      });
    }
    await expect(page.locator('[data-icon-id="system:terminal"]')).toHaveCount(
      0,
    );
    await expect(
      welcome.getByRole('button', { name: 'Documents', exact: true }),
    ).toHaveCount(0);
    for (const id of ['system:computer', 'system:documents', 'system:trash'])
      await expect(page.locator(`[data-icon-id="${id}"]`)).toHaveCount(0);
    await expect(dockEntry(page, 'codex')).toBeVisible();
    await expect(dockEntry(page, 'gallery')).toHaveCount(0);
    await expect.poll(dockOrder).toEqual([...coreApplications, 'welcome']);
    await expect
      .poll(async () => {
        const layout = await persistedLayout(page, request);
        return (
          layout && {
            windows: layout.windows.map(
              (window: { appId: string }) => window.appId,
            ),
            pinned: layout.dock
              .filter((entry: { pinned: boolean }) => entry.pinned)
              .map((entry: { appId: string }) => entry.appId),
          }
        );
      })
      .toEqual({ windows: ['welcome'], pinned: coreApplications });
    await page.reload();
    await expect(page.locator('[data-window-id]')).toHaveCount(1);
    await expect(welcome).toHaveClass(/is-active/);
    await expect.poll(dockOrder).toEqual([...coreApplications, 'welcome']);
    const options = await openDockOptions(page, 'codex');
    await menuItem(options, 'Keep in Dock').click();
    await expect(dockEntry(page, 'codex')).toHaveCount(0);
    await expect
      .poll(async () =>
        (await persistedLayout(page, request))?.dock.some(
          (entry: { appId: string; pinned: boolean }) =>
            entry.appId === 'codex' && entry.pinned,
        ),
      )
      .toBe(false);
    await workApps
      .getByRole('button', { name: 'Project Files', exact: true })
      .click();
    await expect(projects).toHaveClass(/is-active/);
    await expect
      .poll(async () => (await persistedLayout(page, request))?.activeWindowId)
      .toBe('project-files:projects');
    const chosen = await persistedLayout(page, request);
    const projectWindow = chosen.windows.find(
      (window: { id: string }) => window.id === 'project-files:projects',
    );
    await page.reload();
    await expect(page.locator('[data-window-id]')).toHaveCount(2);
    await expect(projects).toHaveClass(/is-active/);
    await expect(dockEntry(page, 'codex')).toHaveCount(0);
    await expect(page.locator('[data-icon-id="system:terminal"]')).toHaveCount(
      0,
    );
    await expect(dockEntry(page, 'gallery')).toHaveCount(0);
    await expect
      .poll(async () => {
        const layout = await persistedLayout(page, request);
        return {
          dock: layout.dock,
          project: layout.windows.find(
            (window: { id: string }) => window.id === 'project-files:projects',
          ),
        };
      })
      .toEqual({ dock: chosen.dock, project: projectWindow });
  });
});

test('windows drag, resize, shade, miniaturize and retain desktop geometry', async ({
  page,
  request,
}) => {
  await launch(page, 'project-files');
  const projects = page.locator('[data-window-id="project-files:projects"]');
  const initial = await bounds(projects);
  const title = await bounds(projects.locator('.ns-window-title'));
  await page.mouse.move(title.x + title.width / 2, title.y + title.height / 2);
  await page.mouse.down();
  await page.mouse.move(
    title.x + title.width / 2 + 72,
    title.y + title.height / 2 + 48,
    { steps: 8 },
  );
  await page.mouse.up();
  const moved = await bounds(projects);
  expect(moved.x).toBeCloseTo(initial.x + 72, 0);
  expect(moved.y).toBeCloseTo(initial.y + 48, 0);
  const corner = await bounds(projects.locator('[data-resize="se"]'));
  await page.mouse.move(
    corner.x + corner.width / 2,
    corner.y + corner.height / 2,
  );
  await page.mouse.down();
  await page.mouse.move(
    corner.x + corner.width / 2 + 60,
    corner.y + corner.height / 2 + 40,
    { steps: 8 },
  );
  await page.mouse.up();
  const resized = await bounds(projects);
  expect(resized.width).toBeCloseTo(moved.width + 60, 0);
  expect(resized.height).toBeCloseTo(moved.height + 40, 0);
  await projects.getByRole('button', { name: /^Shade / }).click();
  await expect(projects).toHaveClass(/is-shaded/);
  await expect(
    projects.getByRole('button', { name: 'Choose Folder', exact: true }),
  ).toBeHidden();
  await projects.getByRole('button', { name: /^Unshade / }).click();
  await expect.poll(() => bounds(projects)).toEqual(resized);
  await projects.getByRole('button', { name: /^Maximize / }).click();
  expect((await bounds(projects)).width).toBeGreaterThan(resized.width);
  await projects.getByRole('button', { name: /^Restore / }).click();
  await expect.poll(() => bounds(projects)).toEqual(resized);
  await projects.getByRole('button', { name: /^Miniaturize / }).click();
  await expect(projects).toBeHidden();
  await expect(page.locator('.ns-miniwindow-shelf')).toBeVisible();
  await activate(page, projects);
  await expect.poll(() => bounds(projects)).toEqual(resized);
  await expect
    .poll(
      async () =>
        (await persistedLayout(page, request))?.windows.find(
          (win: { id: string }) => win.id === 'project-files:projects',
        )?.width,
    )
    .toBe(resized.width);
  await page.reload();
  await expect.poll(() => bounds(projects)).toEqual(resized);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    projects.getByRole('button', { name: /^Close / }),
  ).toBeInViewport();
  await page.setViewportSize({ width: 1440, height: 900 });
  await expect.poll(() => bounds(projects)).toEqual(resized);
});

test('Dock pinning, order, clock and running window selection persist', async ({
  page,
  request,
}) => {
  const terminalEntry = dockEntry(page, 'terminal');
  const terminalDock = terminalEntry.locator('.ns-dock-tile');
  const running = terminalDock.locator('.ns-dock-running');
  await expect(terminalEntry.getByRole('button')).toHaveCount(1);
  await expect(running).toHaveCount(0);
  expect(await bounds(terminalDock)).toMatchObject({ width: 52, height: 52 });
  const clock = page.getByRole('button', {
    name: 'Clock and date',
    exact: true,
  });
  expect(await bounds(clock)).toMatchObject({ width: 52, height: 52 });
  const addApplication = page.getByRole('button', {
    name: 'Add application to Dock',
    exact: true,
  });
  expect(await bounds(addApplication)).toMatchObject({ width: 52, height: 23 });
  await expect(addApplication).toHaveText('+');
  await expect(
    page.getByRole('button', { name: 'Dock menu', exact: true }),
  ).toHaveCount(0);
  await launch(page, 'terminal');
  await applicationMenu(page, 'File');
  await menuItem(page, 'New Terminal Window').click();
  const first = page.locator('[data-app-id="terminal"][aria-label="Terminal"]');
  const second = page.locator(
    '[data-app-id="terminal"][aria-label="Terminal 2"]',
  );
  await expect(terminalDock).toHaveAttribute('aria-label', /2 running windows/);
  await expect(terminalDock.locator('.ns-dock-count')).toHaveText('2');
  const tileBox = await bounds(terminalDock);
  const glyphBox = await bounds(terminalDock.locator('.ns-pixel-icon'));
  const markerBox = await bounds(running);
  expect(glyphBox.x + glyphBox.width / 2).toBeCloseTo(
    tileBox.x + tileBox.width / 2,
    0,
  );
  expect(glyphBox.y + glyphBox.height / 2).toBeCloseTo(
    tileBox.y + tileBox.height / 2,
    0,
  );
  expect(markerBox).toMatchObject({ width: 5, height: 5 });
  expect(markerBox.x).toBeLessThan(glyphBox.x);
  expect(markerBox.y).toBeLessThan(glyphBox.y);
  await terminalDock.click();
  await menuItem(page, 'Terminal').click();
  await expect(first).toHaveClass(/is-active/);
  await expect(second).toHaveCount(1);
  const gallery = dockEntry(page, 'gallery');
  await expect(gallery).toHaveCount(0);
  await pinDockApplication(page, 'Component Gallery');
  await menuItem(
    await openDockOptions(page, 'gallery'),
    'Keep in Dock',
  ).click();
  await expect(gallery).toHaveCount(0);
  await pinDockApplication(page, 'Component Gallery');
  await expect(gallery).toBeVisible();
  const order = () =>
    page
      .locator('.ns-dock [data-dock-app]')
      .evaluateAll((items) =>
        items.map((item) => (item as HTMLElement).dataset.dockApp),
      );
  const before = await order();
  const galleryTile = gallery.locator('.ns-dock-tile');
  const galleryMenu = page.getByRole('menu', {
    name: 'Component Gallery',
    exact: true,
  });
  for (const key of ['Shift+F10', 'ContextMenu']) {
    await galleryTile.press(key);
    await expect(menuItem(galleryMenu, 'Keep in Dock')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(galleryTile).toBeFocused();
  }
  await menuItem(
    await openDockOptions(page, 'gallery'),
    'Move Earlier',
  ).click();
  let changed = await order();
  expect(changed.indexOf('gallery')).toBe(before.indexOf('gallery') - 1);
  const dragged = await bounds(gallery.locator('.ns-dock-tile'));
  const destination = await bounds(
    dockEntry(page, changed[0]!).locator('.ns-dock-tile'),
  );
  await page.mouse.move(
    dragged.x + dragged.width / 2,
    dragged.y + dragged.height / 2,
  );
  await page.mouse.down();
  await page.mouse.move(
    destination.x + destination.width / 2,
    destination.y + destination.height / 2,
    { steps: 12 },
  );
  await page.mouse.up();
  await expect(page.locator('[data-app-id="gallery"]')).toHaveCount(0);
  await expect.poll(async () => (await order())[0]).toBe('gallery');
  changed = await order();
  await expect
    .poll(async () =>
      (await persistedLayout(page, request))?.dock.map(
        (item: { appId: string }) => item.appId,
      ),
    )
    .toEqual(changed);
  await page.reload();
  await expect.poll(order).toEqual(changed);
  await clock.click();
  await expect(
    page.getByRole('dialog', { name: 'Clock', exact: true }),
  ).toContainText(String(new Date().getFullYear()));
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'OK', exact: true })
    .click();
});

test('Dock drag cancellation and missing clicks do not consume later activation', async ({
  page,
}) => {
  await pinDockApplication(page, 'Component Gallery');
  const gallery = dockEntry(page, 'gallery');
  const tile = gallery.locator('.ns-dock-tile');
  const window = page.locator('[data-app-id="gallery"]');
  const before = await page
    .locator('.ns-dock [data-dock-app]')
    .evaluateAll((entries) =>
      entries.map((entry) => entry.getAttribute('data-dock-app')),
    );
  const gesture = async (pointerType: string, cancelled: boolean) => {
    const box = await bounds(tile);
    const pointer = {
      pointerId: 41,
      pointerType,
      button: 0,
      clientX: box.x + 12,
      clientY: box.y + 12,
    };
    await tile.dispatchEvent('pointerdown', pointer);
    await page.locator('body').dispatchEvent('pointermove', {
      ...pointer,
      clientY: pointer.clientY + 20,
    });
    if (pointerType === 'mouse')
      await expect(gallery).toHaveClass(/is-dragging/);
    else await expect(gallery).not.toHaveClass(/is-dragging/);
    // Synthetic pointer completion deliberately produces no native click.
    await page
      .locator('body')
      .dispatchEvent(cancelled ? 'pointercancel' : 'pointerup', pointer);
    await expect(gallery).not.toHaveClass(/is-dragging/);
    expect(
      await page
        .locator('.ns-dock [data-dock-app]')
        .evaluateAll((entries) =>
          entries.map((entry) => entry.getAttribute('data-dock-app')),
        ),
    ).toEqual(before);
  };
  for (const pointerType of ['touch', 'pen']) await gesture(pointerType, false);
  await gesture('mouse', true);
  await tile.click();
  await expect(window).toBeVisible();
  await window.getByRole('button', { name: /^Close / }).click();
  await gesture('mouse', false);
  await tile.focus();
  await tile.press('Enter');
  await expect(window).toBeVisible();
  await window.getByRole('button', { name: /^Close / }).click();
  await gesture('mouse', false);
  await tile.click();
  await expect(window).toBeVisible();
});

test('workspaces retain mounted forms, focus, sticky windows and shortcuts across renaming', async ({
  page,
  request,
}) => {
  await launch(page, 'project-files');
  const projects = page.locator('[data-window-id="project-files:projects"]');
  const directory = projects.getByLabel('Host project directory', {
    exact: true,
  });
  const draft = '/project/directory-not-yet-opened';
  await directory.fill(draft);
  await directory.press('Home');
  await directory.press('Shift+End');
  await directory.evaluate((element) => {
    element.dataset.mountProbe = 'same-input';
  });
  const selection = () =>
    directory.evaluate((element: HTMLInputElement) => [
      element.selectionStart,
      element.selectionEnd,
    ]);
  await projects.getByRole('button', { name: /^Shade / }).click();
  await expect(directory).toBeHidden();
  await projects.getByRole('button', { name: /^Unshade / }).click();
  await expect.poll(selection).toEqual([0, draft.length]);
  await projects.getByRole('button', { name: /^Miniaturize / }).click();
  await expect(projects).toBeHidden();
  await activate(page, projects);
  await expect.poll(selection).toEqual([0, draft.length]);
  await windowMenu(page, projects);
  await menuItem(page, 'Add Workspace Shortcut').click();
  await menuItem(
    await openWorkspaceShortcutMenu(page, 'Project Files'),
    'Open',
  ).click();
  await expect(projects).toHaveClass(/is-active/);
  await switchWorkspace(page, 2);
  await expect(projects).toBeHidden();
  await expect(projects).toHaveCount(1);
  expect(
    await directory.evaluate((element) =>
      element.contains(document.activeElement),
    ),
  ).toBe(false);
  const secondControls = await openWorkspaceControls(page);
  await expect(menuItem(secondControls, 'Shortcuts')).toHaveCount(0);
  await page.keyboard.press('Escape');
  await switchWorkspace(page, 1);
  await expect(directory).toHaveAttribute('data-mount-probe', 'same-input');
  await expect(directory).toHaveValue(draft);
  await expect.poll(selection).toEqual([0, draft.length]);
  await windowMenu(page, projects);
  await menuItem(page, 'Move to Workspace').hover();
  await menuItem(page, 'Workspace 2').click();
  await expect(projects).toBeHidden();
  await switchWorkspace(page, 2);
  await expect(projects).toBeVisible();
  await windowMenu(page, projects);
  await menuItem(page, 'All Workspaces').click();
  await switchWorkspace(page, 3);
  await expect(projects).toBeVisible();
  const thirdControls = await openWorkspaceControls(page);
  await menuItem(thirdControls, 'Rename Workspace').click();
  const dialog = page.getByRole('dialog', {
    name: 'Rename Workspace',
    exact: true,
  });
  await dialog
    .getByRole('textbox', { name: 'Workspace name', exact: true })
    .fill('E2E Projects');
  await dialog.getByRole('button', { name: 'OK', exact: true }).click();
  const renamed = workspaceButton(page, 3);
  await expect(renamed).toBeVisible();
  await expect(renamed).toHaveText('3');
  await expect(renamed).toHaveAccessibleName('Workspace 3 · E2E Projects');
  await expect
    .poll(
      async () => (await persistedLayout(page, request))?.workspaces[2].name,
    )
    .toBe('E2E Projects');
  await page.reload();
  await expect(renamed).toHaveAccessibleName('Workspace 3 · E2E Projects');
  await menuItem(await openWorkspaceControls(page), 'Rename Workspace').click();
  const name = dialog.getByRole('textbox', {
    name: 'Workspace name',
    exact: true,
  });
  await expect(name).toBeFocused();
  await expect(name).toHaveValue('E2E Projects');
  await name.fill('');
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(
    page.getByRole('button', { name: 'Workspace menu', exact: true }),
  ).toBeFocused();
  await expect(renamed).toHaveAccessibleName('Workspace 3 · E2E Projects');
  await menuItem(await openWorkspaceControls(page), 'Rename Workspace').click();
  await name.fill('');
  await dialog.getByRole('button', { name: 'OK', exact: true }).click();
  await expect(renamed).toHaveAccessibleName('Workspace 3');
  await switchWorkspace(page, 1);
  await menuItem(
    await openWorkspaceShortcutMenu(page, 'Project Files'),
    'Remove Shortcut',
  ).click();
  await expect
    .poll(
      async () => (await persistedLayout(page, request))?.workspaces[0].clip,
    )
    .toEqual([]);
});

test('keyboard menus skip disabled items and modal dialogs restore focus', async ({
  page,
}) => {
  await launch(page, 'gallery');
  const gallery = page.locator('[data-app-id="gallery"]');
  const group = gallery.getByRole('group', {
    name: 'Grouped buttons',
    exact: true,
  });
  const first = group.getByRole('button', { name: 'First', exact: true });
  const second = group.getByRole('button', { name: 'Second', exact: true });
  await expect(first).toHaveAttribute('aria-pressed', 'true');
  await second.click();
  await expect(first).toHaveAttribute('aria-pressed', 'false');
  await expect(second).toHaveAttribute('aria-pressed', 'true');
  await gallery
    .getByRole('tab', { name: 'Dialogs & Menus', exact: true })
    .click();
  const opener = gallery.getByRole('button', {
    name: 'Show Input Dialog',
    exact: true,
  });
  await opener.click();
  const dialog = page.getByRole('dialog', {
    name: 'Name Your Document',
    exact: true,
  });
  const name = dialog.getByRole('textbox', { name: 'Name', exact: true });
  await expect(name).toBeFocused();
  await page.keyboard.press('Shift+Tab');
  await expect(
    dialog.getByRole('button', { name: 'Save', exact: true }),
  ).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(name).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(opener).toBeFocused();
  await opener.click();
  await name.fill('E2E Keyboard.txt');
  await page.keyboard.press('Enter');
  await expect(gallery).toContainText('Selected: E2E Keyboard.txt');
  const menu = gallery.getByRole('button', { name: 'Show Menu', exact: true });
  await menu.click();
  await expect(menuItem(page, 'Unavailable')).toHaveAttribute(
    'aria-disabled',
    'true',
  );
  await expect(menuItem(page, 'Selected option')).toHaveAttribute(
    'aria-checked',
    'true',
  );
  await page.keyboard.press('End');
  await page.keyboard.press('ArrowRight');
  await expect(page.getByRole('menu')).toHaveCount(2);
  await page.keyboard.press('Enter');
  await expect(gallery).toContainText('Nested command selected.');
  await expect(page.getByRole('menu')).toHaveCount(0);
  await expect(menu).toBeFocused();
  await gallery.getByRole('tab', { name: 'Controls', exact: true }).click();
  await expect(second).toHaveAttribute('aria-pressed', 'true');
  await first.focus();
  await first.press('Enter');
  await expect(first).toHaveAttribute('aria-pressed', 'true');
  await expect(second).toHaveAttribute('aria-pressed', 'false');
});

test('preferences persist and browser clients keep independent layouts', async ({
  page,
  request,
  browser,
}) => {
  await launch(page, 'appearance');
  const preferences = page.locator('[data-app-id="appearance"]');
  const slate = preferences.getByRole('button', {
    name: 'Slate',
    exact: true,
  });
  const dither = preferences.getByRole('button', {
    name: 'Dither',
    exact: true,
  });
  await expect(slate).toHaveAttribute('aria-pressed', 'true');
  await page.keyboard.press('Tab');
  await slate.focus();
  await expect(slate).toBeFocused();
  await expect(slate).toHaveCSS('outline-style', 'solid');
  await expect(slate).toHaveCSS('outline-color', 'rgb(32, 90, 138)');
  await slate.press('Tab');
  await expect(dither).toBeFocused();
  await expect(slate).toHaveAttribute('aria-pressed', 'true');
  await expect(dither).toHaveAttribute('aria-pressed', 'false');
  await dither.press('Enter');
  await expect(preferences).toContainText('Saved.');
  await expect(dither).toHaveAttribute('aria-pressed', 'true');
  await switchWorkspace(page, 4);
  await expect
    .poll(
      async () => (await persistedLayout(page, request))?.currentWorkspaceId,
    )
    .toBe('workspace-4');
  const independent = await browser.newContext({
    baseURL: test.info().project.use.baseURL,
  });
  try {
    const other = await independent.newPage();
    await other.goto('/');
    await expectCurrentWorkspace(other, 1);
    await expect(other.locator('.ns-desktop')).toHaveClass(/dither/);
  } finally {
    await independent.close();
  }
  await page.reload();
  await expectCurrentWorkspace(page, 4);
  await expect(page.locator('.ns-desktop')).toHaveClass(/dither/);
});

test('the application menu stays accessible above a maximized window and controls the selected owner', async ({
  page,
}) => {
  await launch(page, 'terminal');
  const first = page.locator('[data-app-id="terminal"][aria-label="Terminal"]');
  await first.getByRole('button', { name: /^Maximize / }).click();
  const menus = page.getByRole('navigation', { name: 'Application menus' });
  expect(await bounds(menus)).toMatchObject({ x: 4, y: 30, height: 32 });
  const file = menus.getByRole('button', { name: 'File', exact: true });
  const fileBounds = await bounds(file);
  expect(fileBounds.y + fileBounds.height).toBeLessThanOrEqual(
    (await bounds(first)).y,
  );
  await file.click();
  const fileMenu = page.getByRole('menu', { name: 'File', exact: true });
  expect((await bounds(fileMenu)).y).toBeGreaterThanOrEqual(
    fileBounds.y + fileBounds.height,
  );
  await expect(
    fileMenu.getByRole('button', { name: /^Pin .* menu$/ }),
  ).toHaveCount(0);
  await menuItem(fileMenu, 'New Terminal Window').click();
  const second = page.locator(
    '[data-app-id="terminal"][aria-label="Terminal 2"]',
  );
  await expect(second).toHaveClass(/is-active/);
  await expect(fileMenu).toHaveCount(0);
  await activate(page, first);
  await file.click();
  await menuItem(fileMenu, 'Close Window').click();
  await page
    .getByRole('dialog', { name: 'End host shell?', exact: true })
    .getByRole('button', { name: 'End Session', exact: true })
    .click();
  await expect(first).toHaveCount(0);
  await expect(fileMenu).toHaveCount(0);
  await expect(second).toHaveClass(/is-active/);
});

test('saved names and extra workspaces restore without obsolete floating menus', async ({
  page,
  request,
}) => {
  await expect
    .poll(async () => (await persistedLayout(page, request))?.version)
    .toBe(2);
  const saved = await persistedLayout(page, request);
  const clientId = await page.evaluate(() =>
    localStorage.getItem('nextstep-browser-id'),
  );
  saved.workspaces[1].name = 'Development';
  saved.workspaces.push({
    id: 'workspace-lab',
    name: 'QA Lab',
    order: [],
    activeWindowId: null,
    clip: [],
  });
  saved.pinnedMenus = [
    {
      id: 'old-workspace-panel',
      kind: 'desktop',
      title: 'Workspace',
      x: 10,
      y: 100,
    },
  ];
  const stored = await request.put('/api/layout', {
    data: { clientId, layout: saved },
  });
  expect(stored.ok()).toBeTruthy();
  await page.reload();

  await expect(workspaceButton(page, 2)).toHaveAccessibleName(
    'Workspace 2 · Development',
  );
  await expectCurrentWorkspace(page, 1);
  await expect(workspaceButton(page, 5)).toHaveCount(0);
  await expect(page.locator('.ns-pinned-menu')).toHaveCount(0);
  const workspaces = await openWorkspaceControls(page);
  await menuItem(workspaces, 'Switch Workspace').click();
  const extra = page.getByRole('menu', {
    name: 'Switch Workspace',
    exact: true,
  });
  await menuItem(extra, 'Workspace 5 · QA Lab').click();
  await expectCurrentWorkspace(page, 5);
  await expect(workspaceButton(page, 5)).toHaveText('5');
  const welcome = page.locator('[data-app-id="welcome"]');
  await expect(welcome).toBeHidden();
  await expect
    .poll(async () => {
      const layout = await persistedLayout(page, request);
      return {
        workspaceId: layout?.currentWorkspaceId,
        retiredPanels: layout?.pinnedMenus,
      };
    })
    .toEqual({
      workspaceId: 'workspace-lab',
      retiredPanels: undefined,
    });
  await switchWorkspace(page, 1);
  await expect(welcome).toBeVisible();
});

test('future layout recovery resets only desktop state after confirmation', async ({
  page,
  request,
}) => {
  await expect
    .poll(async () => (await persistedLayout(page, request))?.version)
    .toBe(2);
  const clientId = await page.evaluate(() =>
    localStorage.getItem('nextstep-browser-id'),
  );
  const otherClientId = `${clientId}-independent`;
  const otherLayout = {
    version: 999,
    windows: [],
    currentWorkspaceId: 'workspace-3',
  };
  const storedOther = await request.put('/api/layout', {
    data: { clientId: otherClientId, layout: otherLayout },
  });
  expect(storedOther.ok()).toBeTruthy();
  const response = await request.put('/api/layout', {
    data: { clientId, layout: { version: 999, windows: [] } },
  });
  expect(response.ok()).toBeTruthy();
  await page.reload();
  await expect(page.getByRole('alert')).toContainText('newer version');
  await page.getByRole('button', { name: 'Reset Layout', exact: true }).click();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  expect((await persistedLayout(page, request)).version).toBe(999);
  await page.getByRole('button', { name: 'Reset Layout', exact: true }).click();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Reset Layout', exact: true })
    .click();
  await expect(
    page.getByRole('button', { name: 'Reset Layout', exact: true }),
  ).toHaveCount(0);
  await expect
    .poll(async () => (await persistedLayout(page, request))?.version)
    .toBe(2);
  const independent = await request.get(
    `/api/bootstrap?clientId=${encodeURIComponent(otherClientId)}`,
  );
  expect(independent.ok()).toBeTruthy();
  expect((await independent.json()).layout).toEqual(otherLayout);
});
