import { captureWorkArea, expectRect } from './work-area-helpers';
import {
  encoded,
  marker,
  openTerminal,
  output,
  run,
  terminalAction,
} from './terminal-helpers';
import type { Point } from '@cofy-x/axv-nextstep';

import {
  bounds,
  dockEntry,
  expect,
  menuItem,
  openDockOptions,
  openWorkspaceShortcutMenu,
  pinDockApplication,
  switchWorkspace,
  persistedLayout,
  test,
} from './helpers';

test('overflowing Dock keeps native touch scrolling separate from launch and ordering', async ({
  page,
  request,
  isMobile,
  browserName,
}) => {
  test.skip(!isMobile, 'Runs in the explicit touch projects.');
  await page.setViewportSize({ width: 320, height: 844 });
  await pinDockApplication(page, 'Tailscale', true);
  const dock = page.getByRole('complementary', { name: 'Application Dock' });
  const scroller = dock.locator('.ns-dock-scroll');
  const dockMenu = dock.getByRole('button', { name: 'Dock menu', exact: true });
  const menuBounds = await bounds(dockMenu);
  expect(menuBounds).toMatchObject({ width: 52, height: 52 });
  await expect(
    dock.getByRole('button', { name: 'Add application to Dock', exact: true }),
  ).toHaveCount(0);
  await expect(
    dock.getByRole('button', { name: 'Clock and date' }),
  ).toHaveCount(0);
  const order = () =>
    dock
      .locator('[data-dock-app]')
      .evaluateAll((entries) =>
        entries.map((entry) => entry.getAttribute('data-dock-app')!),
      );
  const before = await order();
  const firstEntry = dockEntry(page, before[0]!);
  await expect(firstEntry.getByRole('button')).toHaveCount(1);
  const launcher = await bounds(firstEntry.locator('.ns-dock-tile'));
  expect(launcher).toMatchObject({ width: 52, height: 52 });
  expect(menuBounds.y).toBe(launcher.y);
  const windows = await page.locator('[data-window-id]').count();
  const scrollLeft = () => scroller.evaluate((element) => element.scrollLeft);
  const scrollEnd = await scroller.evaluate(
    (element) => element.scrollWidth - element.clientWidth,
  );
  expect(scrollEnd).toBeGreaterThan(0);
  await expect(dockEntry(page, before[0]!).locator('.ns-dock-tile')).toHaveCSS(
    'touch-action',
    'pan-x',
  );
  await expect
    .poll(async () =>
      (await persistedLayout(page, request))?.dock?.map(
        (entry: { appId: string }) => entry.appId,
      ),
    )
    .toEqual(before);
  const savedDock = (await persistedLayout(page, request)).dock;

  if (browserName === 'chromium') {
    const protocol = await page.context().newCDPSession(page);
    const area = await bounds(scroller);
    const reachable = new Set<string>();
    const recordReachable = async () => {
      const visible = await dock.locator('[data-dock-app]').evaluateAll(
        (entries, area) =>
          entries
            .filter((entry) => {
              const box = entry.getBoundingClientRect();
              return box.left >= area.x && box.right <= area.x + area.width;
            })
            .map((entry) => entry.getAttribute('data-dock-app')!),
        area,
      );
      visible.forEach((id) => reachable.add(id));
    };
    const swipe = async (start: Point, endX: number) => {
      await protocol.send('Input.dispatchTouchEvent', {
        type: 'touchStart',
        touchPoints: [start],
      });
      for (let step = 1; step <= 8; step++)
        await protocol.send('Input.dispatchTouchEvent', {
          type: 'touchMove',
          touchPoints: [
            { x: start.x + ((endX - start.x) * step) / 8, y: start.y },
          ],
        });
      await protocol.send('Input.dispatchTouchEvent', {
        type: 'touchEnd',
        touchPoints: [],
      });
    };
    const swipeOnApp = async (later: boolean) => {
      const start = await dock.locator('[data-dock-app]').evaluateAll(
        (entries, { area, later }) => {
          const boxes = entries
            .map((entry) => entry.getBoundingClientRect())
            .filter(
              (box) => box.left >= area.x && box.right <= area.x + area.width,
            );
          const box = later ? boxes[boxes.length - 1]! : boxes[0]!;
          return {
            x: later ? box.right - 8 : box.left + 8,
            y: box.top + 24,
          };
        },
        { area, later },
      );
      await swipe(start, later ? area.x + 6 : area.x + area.width - 6);
    };
    try {
      await recordReachable();
      await swipeOnApp(true);
      await expect.poll(scrollLeft).toBeGreaterThan(0);
      for (let step = 0; step < before.length; step++) {
        await recordReachable();
        const prior = await scrollLeft();
        if (prior >= scrollEnd - 1) break;
        await swipeOnApp(true);
        await expect.poll(scrollLeft).toBeGreaterThan(prior);
      }
      await recordReachable();
      await expect.poll(scrollLeft).toBeGreaterThanOrEqual(scrollEnd - 1);
      await expect(dockMenu).toBeInViewport({ ratio: 1 });
      expect(await bounds(dockMenu)).toEqual(menuBounds);
      expect([...reachable].sort()).toEqual([...before].sort());
      for (let step = 0; step < before.length; step++) {
        const prior = await scrollLeft();
        if (prior <= 1) break;
        await swipeOnApp(false);
        await expect.poll(scrollLeft).toBeLessThan(prior);
      }
      await expect.poll(scrollLeft).toBeLessThanOrEqual(1);
      await expect(firstEntry).toBeInViewport({ ratio: 1 });
    } finally {
      await protocol.detach();
    }
  } else {
    // Playwright WebKit exposes taps, not native swipes. This checks pan policy
    // and reachability only; real iPhone Safari scrolling needs device acceptance.
    for (const id of before) {
      const entry = dockEntry(page, id);
      await entry.scrollIntoViewIfNeeded();
      await expect(entry).toBeInViewport({ ratio: 1 });
      await expect(dockMenu).toBeInViewport({ ratio: 1 });
      expect(await bounds(dockMenu)).toEqual(menuBounds);
    }
    await expect(dockMenu).toBeInViewport({ ratio: 1 });
    expect(await bounds(dockMenu)).toEqual(menuBounds);
  }
  await expect(page.locator('[data-window-id]')).toHaveCount(windows);
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.locator('.ns-menu-layer')).toHaveCount(0);
  await expect(dock.locator('.is-dragging')).toHaveCount(0);
  expect(await order()).toEqual(before);
  expect((await persistedLayout(page, request)).dock).toEqual(savedDock);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
  ).toBe(false);

  const reorderedId = before[1]!;
  const reorderedEntry = dockEntry(page, reorderedId);
  await reorderedEntry.locator('.ns-dock-tile').tap();
  await expect(
    page.locator(`[data-app-id="${reorderedId}"]`).first(),
  ).toBeVisible();
  await openDockOptions(page, reorderedId, true);
  await menuItem(page, 'Move Earlier').tap();
  const reordered = [...before];
  const index = reordered.indexOf(reorderedId);
  reordered.splice(index, 1);
  reordered.splice(index - 1, 0, reorderedId);
  await expect.poll(order).toEqual(reordered);
  await expect
    .poll(async () =>
      (await persistedLayout(page, request)).dock.map(
        (entry: { appId: string }) => entry.appId,
      ),
    )
    .toEqual(reordered);
  await page.reload();
  await expect.poll(order).toEqual(reordered);
  // The Dock menu can pin the application without launching it.
  const beforePin = await page.locator('[data-window-id]').count();
  await pinDockApplication(page, 'Preferences', true);
  await expect(dockEntry(page, 'appearance')).toHaveCount(1);
  await expect(page.locator('[data-window-id]')).toHaveCount(beforePin);
  const preferencesMenu = await openDockOptions(page, 'appearance', true);
  await menuItem(preferencesMenu, 'Keep in Dock').tap();
  await expect(dockEntry(page, 'appearance')).toHaveCount(0);
  await expect
    .poll(
      async () =>
        (await persistedLayout(page, request)).dock.find(
          (entry: { appId: string }) => entry.appId === 'appearance',
        )?.pinned,
    )
    .toBe(false);
});

test('touch preserves the project folder input through window and workspace changes', async ({
  page,
  isMobile,
}, info) => {
  test.skip(!isMobile, 'Runs in the explicit touch projects.');
  await dockEntry(page, 'project-files').locator('.ns-dock-tile').tap();
  const project = page.locator('[data-app-id="project-files"]');
  const path = project.getByRole('textbox', {
    name: 'Host project directory',
    exact: true,
  });
  const pending = '/example/unfinished-project';
  await path.fill(pending);
  await switchWorkspace(page, 2, true);
  await expect(project).toBeHidden();
  await switchWorkspace(page, 1, true);
  await expect(path).toHaveValue(pending);
  const windowMenu = project.getByRole('button', { name: /^Window menu for / });
  await windowMenu.tap();
  await menuItem(page, 'Shade').tap();
  await expect(path).toBeHidden();
  await windowMenu.tap();
  await menuItem(page, 'Unshade').tap();
  await windowMenu.tap();
  await menuItem(page, 'Add Workspace Shortcut').tap();
  await menuItem(
    await openWorkspaceShortcutMenu(page, 'Project Files', true),
    'Open',
  ).tap();
  await expect(project).toBeVisible();
  await expect(path).toHaveValue(pending);
  await project.getByRole('button', { name: /^Miniaturize / }).tap();
  await expect(project).toBeHidden();
  await page
    .getByRole('button', { name: 'Restore Project Files', exact: true })
    .tap();
  await expect(path).toHaveValue(pending);
  await expect(
    project.getByRole('button', { name: /^Close / }),
  ).toBeInViewport();
  await captureWorkArea(page, info, 'touch-project-files');
});

test('touch Terminal supports input, explicit controls and workspace restoration', async ({
  page,
  isMobile,
}, info) => {
  test.skip(!isMobile, 'Runs in the explicit touch projects.');
  test.skip(
    process.platform === 'win32',
    'These commands use POSIX shell syntax.',
  );
  const terminal = await openTerminal(page, true);
  const input = terminal.getByLabel('Host terminal input');
  await terminal.getByTestId('terminal-screen').tap();
  await expect(input).toBeFocused();
  await marker(terminal, 'touch-shell-ready');
  await run(terminal, `printf '${encoded('touch-cat')}\\n'; cat`);
  await expect(output(terminal)).toContainText('touch-cat');
  await terminal.getByRole('button', { name: 'Interrupt', exact: true }).tap();
  await marker(terminal, 'touch-interrupt-ok');
  await terminalAction(page, terminal, 'Clear', true);
  await expect(output(terminal)).not.toContainText('touch-cat');
  await switchWorkspace(page, 2, true);
  await expect(terminal).toBeHidden();
  await switchWorkspace(page, 1, true);
  await expect(terminal).toBeVisible();
  await marker(terminal, 'touch-shell-retained');
  await run(terminal, 'exit');
  await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'ended',
  );
  await terminal
    .getByRole('button', { name: 'New Session', exact: true })
    .tap();
  await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  await marker(terminal, 'touch-fresh-session');
  await expect(
    terminal.getByRole('button', { name: /^Close / }),
  ).toBeInViewport();
  const path = info.outputPath('390x844-touch-terminal.png');
  await page.screenshot({ path, fullPage: true });
  await info.attach('touch-terminal', { path, contentType: 'image/png' });
  await terminal.getByRole('button', { name: /^Close / }).tap();
  const dialog = page.getByRole('dialog', { name: 'End host shell?' });
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).tap();
  await marker(terminal, 'touch-cancel-keeps-shell');
  await terminal.getByRole('button', { name: /^Close / }).tap();
  await dialog.getByRole('button', { name: 'End Session', exact: true }).tap();
  await expect(terminal).toHaveCount(0);
});

test('touch work area releases the miniwindow row while keeping the Dock and window controls usable', async ({
  page,
  isMobile,
}, info) => {
  test.skip(!isMobile, 'Runs in the explicit touch projects.');
  await dockEntry(page, 'project-files').locator('.ns-dock-tile').tap();
  const project = page.locator('[data-app-id="project-files"]');
  const welcome = page.locator('[data-app-id="welcome"]');
  const fullArea = { x: 4, y: 64, width: 382, height: 720 };
  const reservedArea = { ...fullArea, height: 644 };
  await expectRect(project, fullArea);
  for (const action of ['Miniaturize', 'Shade', 'Maximize', 'Close'])
    await expect(
      project.getByRole('button', { name: new RegExp(`^${action} `) }),
    ).toBeInViewport();
  await expect(
    project.getByRole('button', { name: /^Window menu for / }),
  ).toBeInViewport();
  await expect(
    page
      .locator('.ns-desktop-status')
      .getByRole('button', { name: 'Workspace 1', exact: true }),
  ).toBeInViewport();
  const dockGap = async () => {
    const frame = await bounds(project);
    const dock = await bounds(page.locator('.ns-dock'));
    return dock.y - frame.y - frame.height;
  };
  await expect.poll(dockGap).toBe(4);
  await project.getByRole('button', { name: /^Window menu for / }).tap();
  await menuItem(page, 'All Workspaces').tap();
  await captureWorkArea(page, info, 'without-reserve');
  const welcomeMenu = await openDockOptions(page, 'welcome', true);
  await menuItem(welcomeMenu, 'Welcome').tap();
  await welcome.getByRole('button', { name: /^Miniaturize / }).tap();
  await expectRect(project, reservedArea);
  await expect.poll(dockGap).toBe(80);
  const miniature = page.getByRole('button', {
    name: 'Restore Welcome',
    exact: true,
  });
  await expect(miniature).toBeInViewport();
  await expect(
    project.getByRole('button', { name: /^Close / }),
  ).toBeInViewport();
  await captureWorkArea(page, info, 'with-miniwindow');
  await switchWorkspace(page, 2, true);
  await expectRect(project, fullArea);
  await expect(miniature).toHaveCount(0);
  await expect.poll(dockGap).toBe(4);
  await switchWorkspace(page, 1, true);
  await expectRect(project, reservedArea);
  await miniature.tap();
  await expectRect(welcome, fullArea);
  await welcome.getByRole('button', { name: /^Close / }).tap();
  await expectRect(project, fullArea);
  await openDockOptions(page, 'project-files', true);
  await expect(menuItem(page, 'Project Files')).toBeInViewport();
  await menuItem(page, 'Project Files').tap();
  await expectRect(project, fullArea);
  await expect(
    project.getByRole('button', { name: /^Close / }),
  ).toBeInViewport();
});
