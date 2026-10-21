import type { Locator, Page } from '@playwright/test';
import {
  bounds,
  createEntry,
  editorWindow,
  expect,
  filesWindow,
  menuItem,
  persistedLayout,
  test,
} from './helpers';
import { captureWorkArea } from './work-area-helpers';
import { preparationCheckFixture } from './machines-preparation-fixture';
import type { Machine } from '../apps/dashboard/src/features/machines/api';

async function showDesktop(page: Page) {
  await filesWindow(page)
    .getByRole('button', { name: /^Close / })
    .click();
  await page
    .locator('[data-app-id="welcome"]')
    .getByRole('button', { name: /^Close / })
    .click();
  await expect(page.locator('[data-window-id]')).toHaveCount(0);
  await expect(page.locator('[data-icon-id="system:codex"]')).toBeVisible();
}

async function desktopVMHost(page: Page) {
  const machine: Machine = {
    id: 'desktop-vm-one',
    hostId: 'desktop-host-one',
    revision: 1,
    spec: {
      name: 'Desktop VM',
      provider: 'lima',
      imageId: 'linux',
      kind: 'development',
      resources: { cpus: 2, memoryMiB: 1024, diskGiB: 8 },
      projectGiB: 1,
      retainVolume: true,
    },
    desired: 'stopped',
    status: 'stopped',
    ready: false,
    guestState: 'unavailable',
    reserved: false,
    createdAt: '2026-10-16T00:00:00Z',
    updatedAt: '2026-10-16T00:00:00Z',
  };
  const state = {
    hostId: machine.hostId,
    generation: 1,
    machines: [machine],
    offline: false,
    mutations: [] as string[],
    infoReads: 0,
    preparationReads: 0,
  };
  await page.route('**/api/compute/**', async (route) => {
    const path = new URL(route.request().url()).pathname.replace(
      '/api/compute',
      '',
    );
    if (route.request().method() !== 'GET') {
      state.mutations.push(path);
      await route.fulfill({
        status: 409,
        json: {
          code: 'unexpected_mutation',
          error: 'Desktop icons must only inspect a VM.',
        },
      });
      return;
    }
    if (state.offline && path !== '/environment') {
      await route.fulfill({
        status: 503,
        json: { code: 'host_unavailable', error: 'Desktop host is offline.' },
      });
      return;
    }
    let json: unknown = {};
    if (path === '/environment')
      json = {
        platform: 'darwin',
        configured: true,
        configPath: '/test/axv/config.json',
        profileHome: '/test/axv',
        executablePath: '/test/bin/axv',
        requiredLimaVersion: '2.2.1',
        automaticSetupSupported: true,
        runtime: {
          mode: 'managed',
          state: 'ready',
          hostId: state.hostId,
          generation: state.generation,
          readOnly: false,
        },
      };
    else if (path === '/info') {
      state.infoReads++;
      json = {
        configured: true,
        hostId: state.hostId,
        providers: [],
        images: [],
      };
    } else if (path === '/machines') json = { machines: state.machines };
    else if (path === '/volumes') json = { volumes: [] };
    else if (path === '/operations') json = { operations: [] };
    else if (path === '/images') json = { images: [] };
    else if (path === '/preparation/check') {
      state.preparationReads++;
      json = preparationCheckFixture();
    } else if (path.endsWith('/terminals')) json = { terminals: [] };
    await route.fulfill({ json });
  });
  await page.reload();
  await showDesktop(page);
  return state;
}

async function cleanUp(page: Page) {
  await page
    .getByRole('navigation', { name: 'Application menus' })
    .getByRole('button', { name: 'View', exact: true })
    .click();
  await menuItem(page, 'Arrange Icons').click();
}

async function dragBy(page: Page, icon: Locator, dx: number, dy: number) {
  const box = await bounds(icon);
  const x = box.x + box.width / 2;
  const y = box.y + 20;
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x + dx, y + dy, { steps: 8 });
  await page.mouse.up();
}

async function expectIconsInArea(page: Page) {
  await expect
    .poll(async () => {
      const box = await bounds(
        page.getByRole('group', { name: 'Desktop icons' }),
      );
      return box.x + box.width;
    })
    .toBe(
      page.viewportSize()!.width - (page.viewportSize()!.width < 600 ? 8 : 80),
    );
  const area = await bounds(page.getByRole('group', { name: 'Desktop icons' }));
  const icons = await page.locator('[data-icon-id]').evaluateAll((elements) =>
    elements.map((element) => {
      const box = element.getBoundingClientRect();
      return { x: box.x, y: box.y, width: box.width, height: box.height };
    }),
  );
  for (const [index, icon] of icons.entries()) {
    expect(icon.x).toBeGreaterThanOrEqual(area.x);
    expect(icon.y).toBeGreaterThanOrEqual(area.y);
    expect(icon.x + icon.width).toBeLessThanOrEqual(area.x + area.width);
    expect(icon.y + icon.height).toBeLessThanOrEqual(area.y + area.height);
    for (const other of icons.slice(index + 1))
      expect(
        icon.x < other.x + other.width &&
          icon.x + icon.width > other.x &&
          icon.y < other.y + other.height &&
          icon.y + icon.height > other.y,
      ).toBe(false);
  }
  expect(Math.max(...icons.map((icon) => icon.x + icon.width))).toBe(
    area.x + area.width,
  );
  return area;
}

test('VM desktop icons retain identity, expose status and stay outside document deletion', async ({
  page,
  request,
}, info) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const state = await desktopVMHost(page);
  const canvas = page.getByRole('group', { name: 'Desktop icons' });
  const launcher = canvas.getByRole('button', {
    name: 'Virtual Machines',
    exact: true,
  });
  const icon = canvas.getByRole('button', {
    name: 'Desktop VM — Stopped',
    exact: true,
  });
  await expect(launcher).toBeVisible();
  await expect(icon).toBeVisible();
  expect(state.preparationReads).toBe(0);
  await icon.click({ button: 'right' });
  await expect(menuItem(page, 'Open')).toBeVisible();
  await expect(menuItem(page, 'Inspect')).toBeVisible();
  await expect(menuItem(page, 'Rename')).toHaveCount(0);
  await expect(menuItem(page, 'Recycle')).toHaveCount(0);
  await page.keyboard.press('Escape');
  await icon.click();
  await page.keyboard.press('Delete');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(icon).toBeVisible();
  const start = await bounds(icon);
  const recycler = canvas.getByRole('button', {
    name: 'Recycler',
    exact: true,
  });
  const target = await bounds(recycler);
  await page.mouse.move(start.x + start.width / 2, start.y + 16);
  await page.mouse.down();
  await page.mouse.move(target.x + target.width / 2, target.y + 16, {
    steps: 8,
  });
  await expect(recycler).not.toHaveClass(/is-trash-target/);
  await page.mouse.move(start.x + start.width / 2, start.y + 16, { steps: 8 });
  await page.mouse.up();
  const id = (await icon.getAttribute('data-icon-id'))!;
  await dragBy(page, icon, -100, 0);
  const position = await bounds(icon);
  await expect
    .poll(async () => (await persistedLayout(page, request)).icons[id])
    .toEqual({ x: position.x, y: position.y });
  state.machines = [
    {
      ...state.machines[0],
      spec: { ...state.machines[0].spec, name: 'Renamed desktop VM' },
      status: 'running',
      ready: true,
    },
  ];
  const renamed = canvas.getByRole('button', {
    name: 'Renamed desktop VM — Ready',
    exact: true,
  });
  await expect(renamed).toBeVisible();
  await expect(renamed).toHaveAttribute('data-icon-id', id);
  expect((await bounds(renamed)).x).toBe(position.x);
  expect(state.infoReads).toBe(1);
  await captureWorkArea(page, info, 'vm-desktop-wide');
  await page.setViewportSize({ width: 390, height: 844 });
  await renamed.scrollIntoViewIfNeeded();
  await expect(renamed).toBeInViewport();
  const status = renamed.locator('.ns-desktop-icon-status');
  expect(
    (await bounds(status)).y + (await bounds(status)).height,
  ).toBeLessThanOrEqual(
    (await bounds(renamed)).y + (await bounds(renamed)).height,
  );
  await captureWorkArea(page, info, 'vm-desktop-narrow');
  state.offline = true;
  await expect(
    canvas.getByRole('button', {
      name: 'Renamed desktop VM — Unavailable',
      exact: true,
    }),
  ).toBeVisible();
  state.offline = false;
  state.machines = [{ ...state.machines[0], status: 'deleted' }];
  await expect(canvas.locator('[data-icon-id^="machine:"]')).toHaveCount(0);
  expect(state.mutations).toEqual([]);
  expect(state.preparationReads).toBe(0);
});

test('VM desktop activation opens only the exact host Inspector across host changes', async ({
  page,
}) => {
  const state = await desktopVMHost(page);
  const canvas = page.getByRole('group', { name: 'Desktop icons' });
  const original = canvas.getByRole('button', {
    name: 'Desktop VM — Stopped',
    exact: true,
  });
  await original.dblclick();
  const inspectors = page.locator('[data-app-id="machine-inspector"]');
  await expect(inspectors).toHaveCount(1);
  await expect(inspectors).toHaveAttribute(
    'data-window-id',
    'machine-inspector:["desktop-host-one","desktop-vm-one"]',
  );
  await expect(
    inspectors.getByRole('heading', { name: 'Desktop VM', exact: true }),
  ).toBeVisible();
  const firstId = (await inspectors.getAttribute('data-window-id'))!;
  state.hostId = 'desktop-host-two';
  state.generation++;
  state.machines = [
    {
      ...state.machines[0],
      hostId: state.hostId,
      spec: { ...state.machines[0].spec, name: 'Replacement host VM' },
    },
  ];
  await expect(
    inspectors.getByText(
      'Original host unavailable. This window is read-only until its host reconnects.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    inspectors.getByRole('heading', { name: 'Desktop VM', exact: true }),
  ).toBeVisible();
  await inspectors.getByRole('button', { name: /^Miniaturize / }).click();
  const replacement = canvas.getByRole('button', {
    name: 'Replacement host VM — Stopped',
    exact: true,
  });
  await replacement.dblclick();
  await expect(inspectors).toHaveCount(2);
  const ids = await inspectors.evaluateAll((items) =>
    items.map((item) => item.getAttribute('data-window-id')),
  );
  expect(ids).toContain(firstId);
  expect(ids).toContain(
    'machine-inspector:["desktop-host-two","desktop-vm-one"]',
  );
  expect(state.mutations).toEqual([]);
});

test('desktop icons use the available right edge and avoid menus, Dock and miniwindows', async ({
  page,
}, info) => {
  await showDesktop(page);
  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 1024, height: 768 },
    { width: 500, height: 652 },
    { width: 390, height: 844 },
  ]) {
    await page.setViewportSize(viewport);
    const area = await expectIconsInArea(page);
    const dock = await bounds(page.locator('.ns-dock'));
    if (viewport.width < 600) {
      const menu = await bounds(
        page.getByRole('navigation', { name: 'Application menus' }),
      );
      expect(area.y).toBeGreaterThanOrEqual(menu.y + menu.height);
      expect(area.x + area.width).toBe(viewport.width - 8);
      expect(area.y + area.height).toBeLessThan(dock.y);
    } else expect(area.x + area.width).toBeLessThan(dock.x);
    await captureWorkArea(page, info, 'desktop-icons');
  }
  await page.locator('[data-icon-id="system:documents"]').dblclick();
  await filesWindow(page)
    .getByRole('button', { name: /^Miniaturize / })
    .click();
  const area = await expectIconsInArea(page);
  const shelf = await bounds(page.getByLabel('Miniaturized windows'));
  expect(area.y + area.height).toBeLessThanOrEqual(shelf.y);
  await captureWorkArea(page, info, 'desktop-icons-miniwindow');
});

test('compact dragging and cleanup persist independently of wide desktop positions', async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await showDesktop(page);
  const icon = page.locator('[data-icon-id="system:computer"]');
  await cleanUp(page);
  await dragBy(page, icon, -160, 16);
  const wide = await bounds(icon);
  await expect
    .poll(
      async () =>
        (await persistedLayout(page, request))?.icons['system:computer'],
    )
    .toEqual({ x: wide.x, y: wide.y });
  const savedWide = (await persistedLayout(page, request)).icons;

  await page.setViewportSize({ width: 390, height: 844 });
  const area = await expectIconsInArea(page);
  await cleanUp(page);
  const start = await bounds(icon);
  await dragBy(page, icon, 389 - start.x - start.width / 2, 843 - start.y - 20);
  const compact = await bounds(icon);
  expect(compact.x + compact.width).toBe(area.x + area.width);
  expect(compact.y + compact.height).toBe(area.y + area.height);
  await expect
    .poll(
      async () =>
        (await persistedLayout(page, request))?.compactIcons['system:computer'],
    )
    .toEqual({ x: compact.x, y: compact.y });
  expect((await persistedLayout(page, request)).icons).toEqual(savedWide);
  await page.reload();
  await expect.poll(() => bounds(icon)).toEqual(compact);
  await page.setViewportSize({ width: 1440, height: 900 });
  await expect.poll(() => bounds(icon)).toEqual(wide);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => bounds(icon)).toEqual(compact);
});

test('overflow icons stay scrollable and open through pointer or touch', async ({
  page,
  request,
  isMobile,
  browserName,
}, info) => {
  let document;
  for (let index = 0; index < 20; index++)
    document = await createEntry(
      request,
      `E2E Desktop ${String(index).padStart(2, '0')}.txt`,
      'file',
      'desktop',
    );
  await page.setViewportSize({ width: 390, height: 420 });
  await page.reload();
  await showDesktop(page);
  const canvas = page.getByRole('group', { name: 'Desktop icons' });
  expect(
    await canvas.evaluate(
      (element) => element.scrollHeight > element.clientHeight,
    ),
  ).toBe(true);
  const visibleArea = await bounds(canvas);
  // Start over an icon: a full grid must remain scrollable without aiming
  // for the narrow gaps between its cells.
  const x = visibleArea.x + visibleArea.width - 44;
  if (isMobile && browserName === 'chromium') {
    const protocol = await page.context().newCDPSession(page);
    try {
      const start = visibleArea.y + visibleArea.height - 20;
      await protocol.send('Input.dispatchTouchEvent', {
        type: 'touchStart',
        touchPoints: [{ x, y: start }],
      });
      for (let step = 1; step <= 5; step++)
        await protocol.send('Input.dispatchTouchEvent', {
          type: 'touchMove',
          touchPoints: [
            { x, y: start - ((visibleArea.height - 40) * step) / 5 },
          ],
        });
      await protocol.send('Input.dispatchTouchEvent', {
        type: 'touchEnd',
        touchPoints: [],
      });
    } finally {
      await protocol.detach();
    }
  } else if (!isMobile) {
    await page.mouse.move(x, visibleArea.y + 40);
    await page.mouse.wheel(0, 400);
  }
  const icon = page.locator(`[data-icon-id="${document!.id}"]`);
  // Playwright does not expose swipe or wheel input in mobile WebKit.
  // Verify its native pan policy, scroll range and touch activation instead.
  if (isMobile && browserName === 'webkit') {
    await expect(icon).toHaveCSS('touch-action', 'pan-y');
    await icon.scrollIntoViewIfNeeded();
  }
  await expect
    .poll(() => canvas.evaluate((element) => element.scrollTop))
    .toBeGreaterThan(0);
  expect((await persistedLayout(page, request)).compactIcons).toEqual({});
  await icon.scrollIntoViewIfNeeded();
  await expect(icon).toBeInViewport();
  const box = await bounds(icon);
  const area = await bounds(canvas);
  expect(box.y).toBeGreaterThanOrEqual(area.y);
  expect(box.y + box.height).toBeLessThanOrEqual(area.y + area.height);
  await captureWorkArea(page, info, 'desktop-icons-overflow');
  if (isMobile) {
    await icon.tap();
    await icon.tap();
  } else await icon.dblclick();
  await expect(editorWindow(page, document!.id)).toBeVisible();
});
