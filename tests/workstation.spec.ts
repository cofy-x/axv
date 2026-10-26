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
  persistedLayout,
  switchWorkspace,
} from './helpers';
import { expect, openCodexManager, test } from './codex-helpers';

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
  const note = await openDocument(page, entry);
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
  const clock = dock.getByRole('button', {
    name: 'Clock and date',
    exact: true,
  });
  await expect(clock).toHaveCSS('touch-action', 'pan-y');
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
        .poll(() => dock.evaluate((element) => element.scrollTop))
        .toBeGreaterThan(0);
    } finally {
      await protocol.detach();
    }
  }
  const add = dock.getByRole('button', {
    name: 'Add application to Dock',
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
  await expect(page.getByRole('menu')).toBeVisible();
  await page.keyboard.press('Escape');
  expect(await page.locator('[data-window-id]').count()).toBe(windowCount);
  await choose(menus.getByRole('button', { name: 'Window', exact: true }));
  await expect(menuItem(page, 'Miniaturize')).toBeInViewport();
  await page.keyboard.press('Escape');
  await switchWorkspace(page, 2);
  await expect(note).toBeHidden();
  await switchWorkspace(page, 1);
  await expect(editor).toHaveValue(
    'This draft survives a short viewport and workspace changes.',
  );
  const workspaceButton = clip.getByRole('button', { name: /^Workspace 1:/ });
  await choose(workspaceButton);
  await choose(menuItem(page, 'Add Active Window'));
  await choose(workspaceButton);
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
  await activate(page, filesWindow(page));
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
    await choose(menus.getByRole('button', { name: 'Window', exact: true }));
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
