import { windowAction } from './helpers';
import type { Page } from '@playwright/test';
import {
  choose,
  expect,
  openCodexFolderPicker,
  projectPath,
  selectCodexProject,
  test,
} from './codex-helpers';

async function recentProjects(page: Page, paths: string[]) {
  await page.route('**/api/host/codex/projects', (route) =>
    route.fulfill({
      json: {
        projects: paths.map((path) => ({
          path,
          lastUsedAt: new Date().toISOString(),
        })),
      },
    }),
  );
}

test('recent projects open by keyboard and close when their owner stops being interactive', async ({
  page,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  await recentProjects(page, [projectPath]);
  try {
    for (const action of ['Shade Codex', 'Miniaturize Codex', 'Close Codex']) {
      const manager = await selectCodexProject(page, touch);
      const frame = page
        .locator('.ns-window[data-app-id="codex"]')
        .filter({ has: manager });
      const recent = manager.getByRole('button', {
        name: 'Recent Projects',
        exact: true,
      });
      await recent.focus();
      await recent.press('ArrowDown');
      const menu = page.getByRole('menu', {
        name: 'Recent Projects',
        exact: true,
      });
      await expect(menu).toBeFocused();
      // Invoke the actual window command as an asynchronous application action
      // can do, without clicking through the menu backdrop.
      await frame
        .getByRole('button', { name: action, exact: true, includeHidden: true })
        .evaluate((button: HTMLButtonElement) => button.click());
      await expect(menu).toHaveCount(0);
      if (action === 'Shade Codex') {
        await windowAction(page, frame, 'Unshade', touch);
        await expect(
          manager.getByLabel('Project directory', { exact: true }),
        ).toHaveValue(projectPath);
      }
    }
    const manager = await selectCodexProject(page, touch);
    await choose(
      manager.getByRole('button', { name: 'Recent Projects', exact: true }),
      touch,
    );
    await page
      .locator('.ns-window[data-app-id="files"]')
      .dispatchEvent('pointerdown', {
        pointerType: 'mouse',
        pointerId: 1,
        button: 0,
        buttons: 1,
      });
    await expect(
      page.getByRole('menu', { name: 'Recent Projects', exact: true }),
    ).toHaveCount(0);
    await expect(page.locator('.ns-window[data-app-id="files"]')).toHaveClass(
      /is-active/,
    );
  } finally {
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('project names and parent paths preserve distinguishing spaces and literal backslashes', async ({
  page,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  await recentProjects(page, [
    '/customers/team A/workspace',
    '/customers/team  A/workspace',
    '/customers/team/work space',
    '/customers/team/work  space',
    '/customers/team/north\\workspace',
    '/customers/team/south\\workspace',
  ]);
  try {
    const manager = await openCodexFolderPicker(page, touch);
    await choose(
      manager.getByRole('button', { name: 'Recent Projects', exact: true }),
      touch,
    );
    const menu = page.getByRole('menu', {
      name: 'Recent Projects',
      exact: true,
    });
    const dimensions = await menu.getByRole('menuitem').evaluateAll((rows) =>
      rows.map((row) => {
        const width = (selector: string) => {
          const element = row.querySelector(selector)!;
          const range = document.createRange();
          range.selectNodeContents(element);
          return range.getBoundingClientRect().width;
        };
        return {
          name: width('.ns-menu-name'),
          parent: width('.ns-menu-description'),
        };
      }),
    );
    expect(dimensions[1].parent).toBeGreaterThan(dimensions[0].parent);
    expect(dimensions[3].name).toBeGreaterThan(dimensions[2].name);
    await expect(menu.locator('.ns-menu-name').nth(4)).toHaveText(
      'north\\workspace',
    );
    await expect(menu.locator('.ns-menu-name').nth(5)).toHaveText(
      'south\\workspace',
    );
    await page.keyboard.press('Escape');
    await expect(
      manager.getByRole('button', { name: 'Recent Projects', exact: true }),
    ).toBeFocused();
  } finally {
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('recent menus fit a reduced visual viewport and recover when it grows', async ({
  page,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  await page.setViewportSize({ width: 390, height: 844 });
  await recentProjects(
    page,
    Array.from(
      { length: 15 },
      (_, index) => `/customer-${index}/${'long-parent-'.repeat(8)}/workspace`,
    ),
  );
  try {
    const manager = await openCodexFolderPicker(page, touch);
    const recent = manager.getByRole('button', {
      name: 'Recent Projects',
      exact: true,
    });
    await choose(recent, touch);
    const menu = page.getByRole('menu', {
      name: 'Recent Projects',
      exact: true,
    });
    await page.evaluate(() => {
      Object.defineProperty(window.visualViewport!, 'height', {
        configurable: true,
        value: 240,
      });
      window.visualViewport!.dispatchEvent(new Event('resize'));
    });
    await expect
      .poll(async () => {
        const bounds = (await menu.boundingBox())!;
        return bounds.y + bounds.height;
      })
      .toBeLessThanOrEqual(240);
    await page.keyboard.press('End');
    const last = menu.getByRole('menuitem').last();
    await expect(last).toHaveClass(/is-selected/);
    const label = (await last.locator('.ns-menu-label').boundingBox())!;
    expect(label.y).toBeGreaterThanOrEqual((await menu.boundingBox())!.y);
    expect(label.y + label.height).toBeLessThanOrEqual(240);
    await page.evaluate(() => {
      Reflect.deleteProperty(window.visualViewport!, 'height');
      window.visualViewport!.dispatchEvent(new Event('resize'));
    });
    await expect
      .poll(async () => (await menu.boundingBox())!.height)
      .toBeGreaterThan(240);
    await page.keyboard.press('Escape');
    await expect(recent).toBeFocused();
  } finally {
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('scrolling recent projects never selects a directory or starts a session', async ({
  page,
  browserName,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  await page.setViewportSize({ width: 390, height: 568 });
  await recentProjects(page, [
    ...Array.from(
      { length: 14 },
      (_, index) => `/customer-${index}/${'long-parent-'.repeat(8)}/workspace`,
    ),
    projectPath,
  ]);
  try {
    const manager = await selectCodexProject(page, touch);
    const requests: string[] = [];
    page.on('request', (request) => {
      if (
        request.url().includes('/api/host/directories') ||
        (request.method() === 'POST' &&
          request.url().endsWith('/api/host/codex/sessions'))
      )
        requests.push(request.url());
    });
    await choose(
      manager.getByRole('button', { name: 'Recent Projects', exact: true }),
      touch,
    );
    const menu = page.getByRole('menu', {
      name: 'Recent Projects',
      exact: true,
    });
    await expect(menu).toHaveCSS('touch-action', 'pan-y');
    await expect(menu).toHaveCSS('overscroll-behavior-y', 'contain');
    const bounds = (await menu.boundingBox())!;
    if (touch && browserName === 'chromium') {
      const protocol = await page.context().newCDPSession(page);
      try {
        const x = bounds.x + bounds.width / 2;
        const y = bounds.y + bounds.height - 60;
        await protocol.send('Input.dispatchTouchEvent', {
          type: 'touchStart',
          touchPoints: [{ x, y }],
        });
        for (let step = 1; step <= 6; step++)
          await protocol.send('Input.dispatchTouchEvent', {
            type: 'touchMove',
            touchPoints: [{ x, y: y - ((bounds.height - 130) * step) / 6 }],
          });
        await protocol.send('Input.dispatchTouchEvent', {
          type: 'touchEnd',
          touchPoints: [],
        });
      } finally {
        await protocol.detach();
      }
    } else if (!touch) {
      await page.mouse.move(
        bounds.x + bounds.width / 2,
        bounds.y + bounds.height / 2,
      );
      await page.mouse.wheel(0, 420);
    } else {
      // Mobile WebKit exposes taps, not native swipes. Check its pan policy,
      // scroll range and final touch selection without claiming native input.
      await menu.getByRole('menuitem').last().scrollIntoViewIfNeeded();
    }
    await expect
      .poll(() => menu.evaluate((element) => element.scrollTop))
      .toBeGreaterThan(0);
    await expect(
      manager.getByLabel('Project directory', { exact: true }),
    ).toHaveValue(projectPath);
    expect(requests).toEqual([]);
    await choose(menu.getByRole('menuitem').last(), touch);
    await expect(menu).toHaveCount(0);
    await expect(
      manager.getByRole('button', { name: 'Start Here', exact: true }),
    ).toBeEnabled();
    expect(
      requests.filter((url) => url.endsWith('/api/host/codex/sessions')),
    ).toEqual([]);
  } finally {
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('recent project choices expose full paths on demand without repeating them in the picker', async ({
  page,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  await page.setViewportSize({ width: 390, height: 844 });
  const basename = projectPath.split('/').at(-1)!;
  const other = `/another-${'long-parent-'.repeat(8)}root/with-a-different-parent/${basename}`;
  const url = '**/api/host/codex/projects';
  await page.route(url, (route) =>
    route.fulfill({
      json: {
        projects: [projectPath, other].map((path) => ({
          path,
          lastUsedAt: new Date().toISOString(),
        })),
      },
    }),
  );
  try {
    const manager = await openCodexFolderPicker(page, touch);
    const recent = manager.getByRole('button', {
      name: 'Recent Projects',
      exact: true,
    });
    await expect(recent).toBeInViewport();
    await expect(manager.locator('.codex-project-choice code')).toHaveCount(0);
    const controls = await manager
      .locator('.codex-directory-actions .ns-button')
      .evaluateAll((buttons) =>
        buttons.map((button) => button.getBoundingClientRect().top),
      );
    expect(Math.max(...controls) - Math.min(...controls)).toBeLessThanOrEqual(
      1,
    );
    const menu = page.getByRole('menu', {
      name: 'Recent Projects',
      exact: true,
    });
    for (const width of [1440, 390, 320]) {
      await page.setViewportSize({ width, height: 844 });
      await choose(recent, touch);
      await expect(menu).toBeVisible();
      const bounds = (await menu.boundingBox())!;
      expect(bounds.width).toBeGreaterThanOrEqual(
        Math.min(420, width - 16) - 1,
      );
      expect(bounds.x).toBeGreaterThanOrEqual(8);
      expect(bounds.x + bounds.width).toBeLessThanOrEqual(width - 8);
      expect(bounds.x).toBeLessThan((await recent.boundingBox())!.x);
      for (const path of [projectPath, other]) {
        const parent = path.slice(0, path.lastIndexOf('/'));
        const row = menu.getByRole('menuitem', {
          name: `${basename} ${parent}`,
          exact: true,
        });
        const label = row.locator('.ns-menu-label');
        await expect(row).toBeVisible();
        await expect(row.locator('.ns-menu-name')).toHaveText(basename);
        await expect(row.locator('.ns-menu-description')).toHaveText(parent);
        const nameBounds = (await row.locator('.ns-menu-name').boundingBox())!;
        expect(
          (await row.locator('.ns-menu-description').boundingBox())!.y,
        ).toBeGreaterThanOrEqual(nameBounds.y + nameBounds.height);
        expect(
          await label.evaluate(
            (element) =>
              element.scrollWidth <= element.clientWidth &&
              element.scrollHeight <= element.clientHeight,
          ),
        ).toBe(true);
      }
      await page.keyboard.press('Escape');
      await expect(menu).toHaveCount(0);
      await expect(recent).toBeFocused();
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth),
      ).toBeLessThanOrEqual(width);
    }
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(
      manager.getByRole('region', { name: 'New Codex session', exact: true }),
    ).toBeVisible();
    const starts: string[] = [];
    page.on('request', (request) => {
      if (
        request.url().endsWith('/api/host/codex/sessions') &&
        request.method() === 'POST'
      )
        starts.push(request.url());
    });
    await choose(recent, touch);
    await choose(
      menu.getByRole('menuitem', {
        name: `${basename} ${projectPath.slice(0, projectPath.lastIndexOf('/'))}`,
        exact: true,
      }),
      touch,
    );
    await expect(
      manager.getByLabel('Project directory', { exact: true }),
    ).toHaveValue(projectPath);
    await expect(
      manager.getByRole('button', { name: 'Start Here', exact: true }),
    ).toBeEnabled();
    await expect(manager.getByTestId('codex-directory-context')).toHaveCount(0);
    expect(starts).toEqual([]);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBeLessThanOrEqual(390);
  } finally {
    await page.unroute(url);
  }
});

test('recent project menus keep long names, root paths and keyboard selections reachable', async ({
  page,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  await page.setViewportSize({ width: 390, height: 844 });
  const paths = [
    '/',
    '/standalone',
    ...Array.from(
      { length: 12 },
      (_, index) =>
        `/customer-${index}/${'long-parent-'.repeat(8)}/${'long-project-'.repeat(8)}${index}`,
    ),
    projectPath,
  ];
  await page.route('**/api/host/codex/projects', (route) =>
    route.fulfill({
      json: {
        projects: paths.map((path) => ({
          path,
          lastUsedAt: new Date().toISOString(),
        })),
      },
    }),
  );
  try {
    const manager = await openCodexFolderPicker(page, touch);
    const recent = manager.getByRole('button', {
      name: 'Recent Projects',
      exact: true,
    });
    await choose(recent, touch);
    const menu = page.getByRole('menu', {
      name: 'Recent Projects',
      exact: true,
    });
    await expect(menu.getByRole('menuitem')).toHaveCount(paths.length);
    await expect(
      menu
        .getByRole('menuitem', { name: '/', exact: true })
        .locator('.ns-menu-description'),
    ).toHaveCount(0);
    await expect(
      menu
        .getByRole('menuitem', { name: 'standalone /', exact: true })
        .locator('.ns-menu-description'),
    ).toHaveText('/');
    expect(
      await menu.evaluate((element) => element.scrollHeight),
    ).toBeGreaterThan(await menu.evaluate((element) => element.clientHeight));
    await page.keyboard.press('End');
    const last = menu.getByRole('menuitem').last();
    await expect(last).toHaveClass(/is-selected/);
    await expect(menu).toHaveAttribute(
      'aria-activedescendant',
      (await last.getAttribute('id'))!,
    );
    await expect(last.locator('.ns-menu-label')).toBeInViewport({ ratio: 1 });
    const lastBounds = (await last.boundingBox())!;
    const menuBounds = (await menu.boundingBox())!;
    expect(lastBounds.y + lastBounds.height).toBeLessThanOrEqual(
      menuBounds.y + menuBounds.height,
    );
    expect(await menu.evaluate((element) => element.scrollTop)).toBeGreaterThan(
      0,
    );
    if (!touch) {
      const previous = menu.getByRole('menuitem').nth(paths.length - 2);
      await previous.hover();
      await expect(previous).toHaveClass(/is-selected/);
    }
    await page.keyboard.press('Home');
    await expect(menu.getByRole('menuitem').first()).toHaveClass(/is-selected/);
    await expect(
      menu.getByRole('menuitem').first().locator('.ns-menu-label'),
    ).toBeInViewport({
      ratio: 1,
    });
    await page.setViewportSize({ width: 320, height: 568 });
    await expect(menu).toBeFocused();
    await expect.poll(async () => (await menu.boundingBox())!.width).toBe(304);
    const bounds = (await menu.boundingBox())!;
    expect(bounds.x).toBeGreaterThanOrEqual(8);
    expect(bounds.x + bounds.width).toBeLessThanOrEqual(312);
    expect(bounds.y + bounds.height).toBeLessThanOrEqual(568);
    await page.keyboard.press('End');
    await expect(last.locator('.ns-menu-label')).toBeInViewport({ ratio: 1 });
    await page.keyboard.press('Enter');
    await expect(menu).toHaveCount(0);
    await expect(
      manager.getByLabel('Project directory', { exact: true }),
    ).toHaveValue(projectPath);
    await expect(
      manager.getByRole('button', { name: 'Start Here', exact: true }),
    ).toBeEnabled();
    await expect(recent).toBeFocused();
  } finally {
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('an unavailable recent directory keeps the original listing and recovers through explicit selection', async ({
  page,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  const missing = `${projectPath}/missing-recent-project`;
  const url = '**/api/host/codex/projects';
  await page.route(url, (route) =>
    route.fulfill({
      json: {
        projects: [projectPath, missing].map((path) => ({
          path,
          lastUsedAt: new Date().toISOString(),
        })),
      },
    }),
  );
  try {
    const manager = await selectCodexProject(page, touch);
    const select = async (path: string) => {
      await choose(
        manager.getByRole('button', { name: 'Recent Projects', exact: true }),
        touch,
      );
      await choose(
        page
          .getByRole('menu', { name: 'Recent Projects', exact: true })
          .getByRole('menuitem', {
            name: `${path.split('/').at(-1)} ${path.slice(0, path.lastIndexOf('/'))}`,
            exact: true,
          }),
        touch,
      );
    };
    await select(missing);
    await expect(manager.getByRole('alert')).toBeVisible();
    await expect(
      manager.getByLabel('Project directory', { exact: true }),
    ).toHaveValue(missing);
    await expect(
      manager.getByTestId('codex-directory-context').locator('code'),
    ).toHaveText(projectPath);
    for (const name of ['Up', 'Open folder nested', 'Start Here'])
      await expect(
        manager.getByRole('button', { name, exact: true }),
      ).toBeDisabled();
    await select(projectPath);
    await expect(
      manager.getByRole('button', { name: 'Start Here', exact: true }),
    ).toBeEnabled();
    await expect(manager.getByRole('alert')).toHaveCount(0);
    await expect(manager.getByTestId('codex-directory-context')).toHaveCount(0);
    await expect(
      manager.getByLabel('Project directory', { exact: true }),
    ).toHaveValue(projectPath);
  } finally {
    await page.unroute(url);
  }
});
