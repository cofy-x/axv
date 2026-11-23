import type { Locator, Page } from '@playwright/test';
import {
  activate,
  bounds,
  expect,
  expectCurrentWorkspace,
  launch,
  menuItem,
  persistedLayout,
  switchWorkspace,
  test,
  windowAction,
  windowMenu,
} from './helpers';
import { captureWorkArea, expectRect, rect } from './work-area-helpers';

const fullArea = { x: 4, y: 64, width: 1372, height: 828 };
const reservedArea = { ...fullArea, height: 752 };

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
  await launch(page, 'project-files');
  const project = page.locator('[data-app-id="project-files"]');
  const welcome = page.locator('[data-app-id="welcome"]');
  await activate(page, project);
  const initial = await bounds(project);
  const title = await bounds(project.locator('.ns-window-title'));
  await dragBy(
    page,
    project,
    1438 - title.x - title.width / 2,
    898 - title.y - title.height / 2,
  );
  await expectRect(project, {
    ...initial,
    x: fullArea.x + fullArea.width - initial.width,
    y: 892 - initial.height,
  });
  const atEdge = await bounds(project);
  await dragBy(page, project, 60 - atEdge.x, 120 - atEdge.y);
  await expectRect(project, { ...initial, x: 60, y: 120 });
  const corner = await bounds(project.locator('[data-resize="se"]'));
  await page.mouse.move(
    corner.x + corner.width / 2,
    corner.y + corner.height / 2,
  );
  await page.mouse.down();
  await page.mouse.move(1438, 898, { steps: 8 });
  await page.mouse.up();
  // Resizing a lower edge must preserve the opposite top and left edges.
  const normal = { x: 60, y: 120, width: 1316, height: 772 };
  await expectRect(project, normal);
  const savedProject = async () =>
    (await persistedLayout(page, request))?.windows.find(
      (window: { id: string }) => window.id === 'project-files:projects',
    );
  await expect
    .poll(async () => {
      const saved = await savedProject();
      return saved && rect(saved);
    })
    .toEqual(normal);
  await project.getByRole('button', { name: /^Maximize / }).click();
  await expectRect(project, fullArea);
  await captureWorkArea(page, info, 'without-reserve');
  await activate(page, welcome);
  await welcome.getByRole('button', { name: /^Miniaturize / }).click();
  await expectRect(project, reservedArea);
  await expect(
    page.getByRole('button', { name: 'Restore Welcome', exact: true }),
  ).toBeVisible();
  await expect
    .poll(async () => (await savedProject())?.restoreBounds)
    .toEqual(normal);
  await captureWorkArea(page, info, 'with-miniwindow');
  await page
    .getByRole('button', { name: 'Restore Welcome', exact: true })
    .click();
  await expectRect(project, fullArea);
  await activate(page, project);
  await project.getByRole('button', { name: /^Restore / }).click();
  await expectRect(project, normal);

  // A temporary shelf clamp changes only the rendered rectangle, including
  // after the layout containing the minimized window has reached the server.
  await activate(page, welcome);
  await welcome.getByRole('button', { name: /^Miniaturize / }).click();
  const clamped = {
    ...normal,
    y: reservedArea.y,
    height: reservedArea.height,
  };
  await expectRect(project, clamped);
  await expect
    .poll(
      async () =>
        (await persistedLayout(page, request))?.windows.find(
          (window: { appId: string }) => window.appId === 'welcome',
        )?.minimized,
    )
    .toBe(true);
  expect(rect(await savedProject())).toEqual(normal);
  await page.reload();
  await expectRect(project, clamped);
  await page
    .getByRole('button', { name: 'Restore Welcome', exact: true })
    .click();
  await activate(page, project);
  await expectRect(project, normal);
  await page.setViewportSize({ width: 390, height: 844 });
  await expectRect(project, { x: 4, y: 30, width: 382, height: 754 });
  await expect(
    project.getByRole('button', { name: /^Close / }),
  ).toBeInViewport();
  await page.setViewportSize({ width: 1440, height: 900 });
  await expectRect(project, normal);
  expect(rect(await savedProject())).toEqual(normal);
});

test('work area reserves only current-workspace miniwindows, includes sticky ones and releases on close', async ({
  page,
}) => {
  await launch(page, 'project-files');
  const project = page.locator('[data-app-id="project-files"]');
  const welcome = page.locator('[data-app-id="welcome"]');
  await windowMenu(page, project);
  await menuItem(page, 'All Workspaces').click();
  await project.getByRole('button', { name: /^Maximize / }).click();
  await expectRect(project, fullArea);
  await activate(page, welcome);
  await windowAction(page, welcome, 'Miniaturize');
  await expectRect(project, reservedArea);
  await page.keyboard.press('Alt+2');
  await expectCurrentWorkspace(page, 2);
  await expectRect(project, fullArea);
  await expect(page.locator('.ns-miniwindow-shelf button')).toHaveCount(0);
  await switchWorkspace(page, 1);
  await expectRect(project, reservedArea);
  const restore = page.getByRole('button', {
    name: 'Restore Welcome',
    exact: true,
  });
  await restore.click();
  await windowAction(page, welcome, 'All Workspaces');
  await windowAction(page, welcome, 'Miniaturize');
  await switchWorkspace(page, 2);
  await expectRect(project, reservedArea);
  await expect(restore).toBeVisible();
  await restore.click();
  await windowAction(page, welcome, 'Close Window');
  await expect(welcome).toHaveCount(0);
  await expect(page.locator('.ns-miniwindow-shelf button')).toHaveCount(0);
  await expectRect(project, fullArea);
});
