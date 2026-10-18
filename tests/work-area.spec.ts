import type { Locator, Page } from '@playwright/test';
import {
  activate,
  bounds,
  expect,
  filesWindow,
  menuItem,
  persistedLayout,
  test,
  windowMenu,
} from './helpers';
import { captureWorkArea, expectRect, rect } from './work-area-helpers';

const fullArea = { x: 4, y: 28, width: 1360, height: 864 };
const reservedArea = { ...fullArea, height: 788 };

async function dragBy(page: Page, window: Locator, dx: number, dy: number) {
  const title = await bounds(window.locator('.ns-window-title'));
  const x = title.x + title.width / 2;
  const y = title.y + title.height / 2;
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x + dx, y + dy, { steps: 8 });
  await page.mouse.up();
}

test('work area keeps drag, resize and maximize edges consistent without changing saved normal bounds', async ({
  page,
  request,
}, info) => {
  const files = filesWindow(page);
  const welcome = page.locator('[data-app-id="welcome"]');
  await activate(page, files);
  const initial = await bounds(files);
  const title = await bounds(files.locator('.ns-window-title'));
  await dragBy(
    page,
    files,
    1438 - title.x - title.width / 2,
    898 - title.y - title.height / 2,
  );
  await expectRect(files, {
    ...initial,
    x: 1364 - initial.width,
    y: 892 - initial.height,
  });
  const atEdge = await bounds(files);
  await dragBy(page, files, 60 - atEdge.x, 120 - atEdge.y);
  await expectRect(files, { ...initial, x: 60, y: 120 });
  const corner = await bounds(files.locator('[data-resize="se"]'));
  await page.mouse.move(
    corner.x + corner.width / 2,
    corner.y + corner.height / 2,
  );
  await page.mouse.down();
  await page.mouse.move(1438, 898, { steps: 8 });
  await page.mouse.up();
  // Resizing a lower edge must preserve the opposite top and left edges.
  const normal = { x: 60, y: 120, width: 1304, height: 772 };
  await expectRect(files, normal);
  const savedFiles = async () =>
    (await persistedLayout(page, request))?.windows.find(
      (window: { id: string }) => window.id === 'files:documents',
    );
  await expect
    .poll(async () => {
      const saved = await savedFiles();
      return saved && rect(saved);
    })
    .toEqual(normal);
  await files.getByRole('button', { name: /^Maximize / }).click();
  await expectRect(files, fullArea);
  await captureWorkArea(page, info, 'without-reserve');
  await activate(page, welcome);
  await welcome.getByRole('button', { name: /^Miniaturize / }).click();
  await expectRect(files, reservedArea);
  await expect(
    page.getByRole('button', { name: 'Restore Welcome', exact: true }),
  ).toBeVisible();
  await expect
    .poll(async () => (await savedFiles())?.restoreBounds)
    .toEqual(normal);
  await captureWorkArea(page, info, 'with-miniwindow');
  await page
    .getByRole('button', { name: 'Restore Welcome', exact: true })
    .click();
  await expectRect(files, fullArea);
  await activate(page, files);
  await files.getByRole('button', { name: /^Restore / }).click();
  await expectRect(files, normal);

  // A temporary shelf clamp changes only the rendered rectangle, including
  // after the layout containing the minimized window has reached the server.
  await activate(page, welcome);
  await welcome.getByRole('button', { name: /^Miniaturize / }).click();
  await expectRect(files, { ...normal, y: 44 });
  await expect
    .poll(
      async () =>
        (await persistedLayout(page, request))?.windows.find(
          (window: { appId: string }) => window.appId === 'welcome',
        )?.minimized,
    )
    .toBe(true);
  expect(rect(await savedFiles())).toEqual(normal);
  await page.reload();
  await expectRect(filesWindow(page), { ...normal, y: 44 });
  await page
    .getByRole('button', { name: 'Restore Welcome', exact: true })
    .click();
  await activate(page, files);
  await expectRect(files, normal);
  await page.setViewportSize({ width: 390, height: 844 });
  await expectRect(files, { x: 4, y: 90, width: 382, height: 682 });
  await expect(files.getByRole('button', { name: /^Close / })).toBeInViewport();
  await page.setViewportSize({ width: 1440, height: 900 });
  await expectRect(files, normal);
  expect(rect(await savedFiles())).toEqual(normal);
});

test('work area reserves only current-workspace miniwindows, includes sticky ones and releases on close', async ({
  page,
}) => {
  const files = filesWindow(page);
  const welcome = page.locator('[data-app-id="welcome"]');
  const switchWorkspace = async (index: number) => {
    // A maximized window covers the desktop Clip; its keyboard equivalent stays available.
    await page.keyboard.press(`Alt+${index}`);
    await expect(
      page.getByRole('button', { name: new RegExp(`^Workspace ${index}: `) }),
    ).toBeVisible();
  };
  await windowMenu(page, files);
  await menuItem(page, 'All Workspaces').click();
  await files.getByRole('button', { name: /^Maximize / }).click();
  await expectRect(files, fullArea);
  await activate(page, welcome);
  const welcomeId = await welcome.getAttribute('data-window-id');
  await windowMenu(page, welcome);
  await page
    .getByRole('button', { name: 'Pin Window menu', exact: true })
    .click();
  const pinned = page.locator(
    `.ns-pinned-menu[data-owner-window-id="${welcomeId}"]`,
  );
  await pinned
    .getByRole('menuitem', { name: 'Miniaturize', exact: true })
    .click();
  await expectRect(files, reservedArea);
  await switchWorkspace(2);
  await expectRect(files, fullArea);
  await expect(page.locator('.ns-miniwindow-shelf button')).toHaveCount(0);
  await switchWorkspace(1);
  await expectRect(files, reservedArea);
  // The fixed menu stays bound to its minimized owner while another window is active.
  await pinned
    .getByRole('menuitemcheckbox', { name: 'All Workspaces', exact: true })
    .click();
  await switchWorkspace(2);
  await expectRect(files, reservedArea);
  await expect(
    page.getByRole('button', { name: 'Restore Welcome', exact: true }),
  ).toBeVisible();
  await pinned
    .getByRole('menuitem', { name: 'Close Window', exact: true })
    .click();
  await expect(welcome).toHaveCount(0);
  await expect(pinned).toHaveCount(0);
  await expect(page.locator('.ns-miniwindow-shelf button')).toHaveCount(0);
  await expectRect(files, fullArea);
});
