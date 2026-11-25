import { preparationCheckFixture } from './machines-preparation-fixture';
import { creationDefaultsFixture } from './machines-creation-fixture';
import type { Locator, Page, TestInfo, WebSocketRoute } from '@playwright/test';
import type {
  GuestTerminal,
  Machine,
  Operation,
  Volume,
} from '../apps/dashboard/src/features/machines/api';
import {
  activate,
  actionMenu,
  expect,
  launch,
  menuItem,
  test,
} from './helpers';
import { expectShellToolbar, output } from './terminal-helpers';
import { applicationMenu } from './desktop-application-helpers';

async function capture(page: Page, info: TestInfo, name: string) {
  const path = info.outputPath(`${name}.png`);
  await page.screenshot({ path });
  await info.attach(name, { path, contentType: 'image/png' });
}

function fixture() {
  const machine: Machine = {
    id: 'vm-browser-review',
    hostId: 'host-review',
    revision: 1,
    spec: {
      name: 'Review VM',
      provider: 'lima',
      imageId: 'linux',
      kind: 'development',
      resources: { cpus: 2, memoryMiB: 1024, diskGiB: 8 },
      projectGiB: 1,
      retainVolume: true,
      volumeId: 'vol-review',
    },
    desired: 'stopped',
    status: 'stopped',
    ready: false,
    guestState: 'unavailable',
    guestCapabilities: [],
    reserved: false,
    createdAt: '2026-10-16T00:00:00Z',
    updatedAt: '2026-10-16T00:00:00Z',
  };
  const volume: Volume = {
    id: 'vol-review',
    name: 'Review project',
    provider: 'lima',
    revision: 1,
    sizeGiB: 1,
    retain: true,
    status: 'ready',
    machineId: machine.id,
  };
  return {
    machine,
    volume,
    additionalVolumes: [] as Volume[],
    operations: [] as Operation[],
    terminalReads: 0,
  };
}

async function host(page: Page, state: ReturnType<typeof fixture>) {
  await page.route('**/api/compute/**', async (route) => {
    const path = new URL(route.request().url()).pathname.replace(
      '/api/compute',
      '',
    );
    const method = route.request().method();
    let value: unknown;
    if (method !== 'GET') {
      await route.fulfill({
        status: 409,
        json: {
          code: 'revision_conflict',
          error: 'Provider inspection failed. Your changes were preserved.',
        },
      });
      return;
    }
    if (path === '/preparation/check') value = preparationCheckFixture();
    else if (path === '/environment')
      value = {
        platform: 'darwin',
        configured: true,
        configPath: '/test/axv/config.json',
        profileHome: '/test/axv',
        executablePath: '/test/bin/axv',
        requiredLimaVersion: '2.0.3',
        automaticSetupSupported: true,
      };
    else if (path === '/info')
      value = {
        configured: true,
        defaults: creationDefaultsFixture('linux'),
        hostId: 'host-review',
        hostArch: 'arm64',
        capacity: {
          cpus: 4,
          memoryMiB: 4096,
          diskGiB: 30,
          machines: 4,
          workers: 1,
        },
        providers: [
          {
            provider: 'lima',
            available: true,
            volumes: true,
            images: true,
            configuration: true,
            forceStop: true,
          },
        ],
        images: [
          {
            id: 'linux',
            name: 'Linux',
            provider: 'lima',
            arch: 'arm64',
            status: 'configured',
          },
        ],
      };
    else if (path === '/machines') value = { machines: [state.machine] };
    else if (path === '/volumes')
      value = { volumes: [state.volume, ...state.additionalVolumes] };
    else if (path === '/operations') value = { operations: state.operations };
    else if (path === '/images')
      value = {
        images: [
          {
            id: 'linux',
            name: 'Linux',
            provider: 'lima',
            arch: 'arm64',
            status: 'configured',
          },
        ],
      };
    else if (path.endsWith('/guest/terminals')) {
      state.terminalReads++;
      value = { terminals: [] };
    } else throw new Error(`Unexpected compute request ${method} ${path}`);
    await route.fulfill({ json: value });
  });
  await launch(page, 'machines');
  const manager = page.locator('[data-window-id="machines:main"]');
  await manager
    .getByRole('button', { name: 'Review VM lima', exact: true })
    .click();
  return manager;
}

test('application menus follow the active VM view and retain its selection', async ({
  page,
  hasTouch,
}, info) => {
  const state = fixture();
  const manager = await host(page, state);
  const menus = page.getByRole('navigation', { name: 'Application menus' });
  await expect(
    menus.getByRole('button', { name: 'Machines', exact: true }),
  ).toBeVisible();
  await expect(
    menus.getByRole('button', { name: 'File', exact: true }),
  ).toHaveCount(0);
  await applicationMenu(page, 'Selected VM', hasTouch);
  await expect(menuItem(page, 'Start')).toBeEnabled();
  await expect(menuItem(page, 'Stop')).toBeDisabled();
  await capture(page, info, 'selected-vm-application-menu');
  await menus.screenshot({ path: info.outputPath('vm-application-panel.png') });
  const selectedVM = page.getByRole('menu', {
    name: 'Selected VM',
    exact: true,
  });
  await menuItem(selectedVM, 'Inspect').click();
  const inspector = page.locator('[data-app-id="machine-inspector"]');
  await expect(inspector).toBeVisible();
  await expect(selectedVM).toHaveCount(0);
  await expect(
    menus.getByRole('button', { name: 'Selected VM', exact: true }),
  ).toHaveCount(0);
  await activate(page, manager, hasTouch);
  await applicationMenu(page, 'Selected VM', hasTouch);
  await expect(menuItem(selectedVM, 'Start')).toBeEnabled();
  await page.keyboard.press('Escape');
  await applicationMenu(page, 'View', hasTouch);
  await menuItem(page, 'VM Host').click();
  await expect(manager.locator('.machines-environment')).toBeVisible();
  await applicationMenu(page, 'View', hasTouch);
  await expect(menuItem(page, 'VM Host')).toHaveAttribute(
    'aria-checked',
    'true',
  );
  await menuItem(page, 'VMs').click();
  await expect(
    manager.getByRole('button', { name: 'Review VM lima', exact: true }),
  ).toBeVisible();
  await expect(
    manager.getByRole('button', { name: 'Inspect', exact: true }),
  ).toBeEnabled();
});

test('VM and volume controls respect cross-resource operation ownership', async ({
  page,
}) => {
  const state = fixture();
  state.operations = [
    {
      id: 'op-copy',
      machineId: '',
      volumeId: 'copy-destination',
      locks: [state.machine.id, state.volume.id],
      action: 'copy-volume',
      status: 'running',
      createdAt: '2026-10-16T00:00:00Z',
    },
  ];
  const manager = await host(page, state);
  await expect(
    manager.getByRole('button', { name: 'Start VM', exact: true }),
  ).toHaveCount(0);
  await expect(
    manager
      .locator('.machines-selection')
      .getByText('Updating VM storage…', { exact: true }),
  ).toBeVisible();
  await manager.getByRole('button', { name: 'Inspect', exact: true }).click();
  const inspector = page.locator('[data-app-id="machine-inspector"]');
  await expect(
    inspector.getByText('Updating VM storage…', { exact: true }),
  ).toBeVisible();
  await expect(
    inspector.getByRole('button', { name: 'More VM actions' }),
  ).toHaveCount(0);
  await activate(page, manager);
  await manager.getByRole('button', { name: 'Volumes', exact: true }).click();
  await manager
    .getByRole('button', { name: 'Review project', exact: true })
    .click();
  await manager.getByRole('button', { name: 'More volume actions' }).click();
  await expect(menuItem(page, 'Grow')).toBeDisabled();
  await page.keyboard.press('Escape');
});

test('guest polling preserves lifecycle failures and configuration drafts', async ({
  page,
}, info) => {
  const state = fixture();
  state.machine.status = state.machine.desired = 'running';
  state.machine.ready = true;
  state.machine.guestState = 'ready';
  state.machine.guestCapabilities = ['terminals'];
  const manager = await host(page, state);
  await manager.getByRole('button', { name: 'Inspect', exact: true }).click();
  const inspector = page.locator('[data-app-id="machine-inspector"]');
  await expect(
    inspector.getByText('Choose New Terminal to begin.', { exact: true }),
  ).toHaveCount(0);
  await inspector.getByRole('button', { name: 'Stop', exact: true }).click();
  await page
    .getByRole('dialog', { name: 'Stop VM?' })
    .getByRole('button', { name: 'Stop VM', exact: true })
    .click();
  await expect(inspector.getByRole('alert')).toContainText(
    'Provider inspection failed',
  );
  await expect
    .poll(() => state.terminalReads, { timeout: 12000 })
    .toBeGreaterThanOrEqual(2);
  await expect(inspector.getByRole('alert')).toContainText(
    'Provider inspection failed',
  );
  await inspector
    .locator('summary')
    .filter({ hasText: /^Configuration$/ })
    .click();
  await inspector
    .getByLabel('VM name', { exact: true })
    .fill('Unsaved VM name');
  state.machine = {
    ...state.machine,
    revision: 2,
    spec: { ...state.machine.spec, name: 'Changed elsewhere' },
  };
  await inspector.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(
    inspector.getByText('The VM changed since this draft was loaded.', {
      exact: false,
    }),
  ).toBeVisible();
  await expect(inspector.getByLabel('VM name', { exact: true })).toHaveValue(
    'Unsaved VM name',
  );
  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 1024, height: 768 },
    { width: 390, height: 844 },
  ]) {
    await page.setViewportSize(viewport);
    await inspector
      .getByLabel('VM name', { exact: true })
      .scrollIntoViewIfNeeded();
    await expect(
      inspector.getByLabel('VM name', { exact: true }),
    ).toBeVisible();
    await capture(page, info, `vm-settings-${viewport.width}`);
  }
});

test('uncertain project attachments cannot be deleted from the volume view', async ({
  page,
}) => {
  const state = fixture();
  state.machine.spec.volumeId = '';
  state.volume.machineId = '';
  state.volume.status = 'failed';
  state.volume.pendingAction = 'attach-volume';
  state.volume.pendingMachineId = state.machine.id;
  const manager = await host(page, state);
  await manager.getByRole('button', { name: 'Volumes', exact: true }).click();
  await manager
    .getByRole('button', { name: 'Review project', exact: true })
    .click();
  await manager.getByRole('button', { name: 'More volume actions' }).click();
  await expect(menuItem(page, 'Delete')).toBeDisabled();
  await page.keyboard.press('Escape');
  await expect(
    manager.getByText(
      'Retry the pending attachment before deleting this volume or VM.',
    ),
  ).toBeVisible();
  await manager
    .getByRole('button', { name: 'Attach Review project', exact: true })
    .click();
  await expect(
    manager.getByRole('button', { name: 'Attach', exact: true }),
  ).toBeEnabled();
});

test('volume and image forms remain usable with pointer and touch at review sizes', async ({
  page,
  hasTouch,
}, info) => {
  const manager = await host(page, fixture());
  const press = async (control: Locator) => {
    if (hasTouch) await control.tap();
    else await control.click();
  };
  await press(manager.getByRole('button', { name: 'Volumes', exact: true }));
  await press(manager.getByRole('button', { name: 'New Volume', exact: true }));
  await manager
    .getByLabel('Volume name', { exact: true })
    .fill('Draft project');
  const allocationHelp =
    'Disk space shares the host budget. Volumes cannot shrink.';
  await expect(
    manager.getByText(allocationHelp, { exact: true }),
  ).toBeVisible();
  for (const width of [1440, 1024, 390]) {
    await page.setViewportSize({
      width,
      height: width === 390 ? 844 : width === 1024 ? 768 : 900,
    });
    await manager
      .getByLabel('Volume name', { exact: true })
      .scrollIntoViewIfNeeded();
    await expect(
      manager.getByLabel('Volume name', { exact: true }),
    ).toHaveValue('Draft project');
    await capture(page, info, `volume-form-${width}`);
  }
  await press(manager.getByRole('button', { name: 'Cancel', exact: true }));
  await expect(manager.getByLabel('Volume name', { exact: true })).toHaveCount(
    0,
  );
  await press(
    manager.getByRole('button', { name: 'Review project', exact: true }),
  );
  await press(manager.getByRole('button', { name: 'Copy', exact: true }));
  await expect(manager.getByLabel('Volume name', { exact: true })).toHaveValue(
    'Review project copy',
  );
  await expect(
    manager.getByText(allocationHelp, { exact: true }),
  ).toBeVisible();
  await expect(
    manager.getByRole('button', { name: 'Create Copy', exact: true }),
  ).toBeEnabled();
  await capture(page, info, 'volume-copy-help');
  await press(manager.getByRole('button', { name: 'Cancel', exact: true }));
  await actionMenu(page, manager, 'More volume actions', 'Grow', hasTouch);
  await expect(
    manager.getByLabel('New size (GiB)', { exact: true }),
  ).toHaveValue('1');
  await expect(
    manager.getByText(
      'Disk space shares the host budget. Disks cannot shrink; an attached VM must be stopped before growth. Its filesystem grows on the next boot.',
      { exact: true },
    ),
  ).toBeVisible();
  await capture(page, info, 'volume-growth-help');
  await press(manager.getByRole('button', { name: 'Cancel', exact: true }));
  await press(manager.getByRole('button', { name: 'Images', exact: true }));
  await press(manager.getByRole('button', { name: 'Add Image', exact: true }));
  await manager.getByLabel('Image name', { exact: true }).fill('Draft image');
  for (const width of [1440, 1024, 390]) {
    await page.setViewportSize({
      width,
      height: width === 390 ? 844 : width === 1024 ? 768 : 900,
    });
    await manager
      .getByLabel('Image name', { exact: true })
      .scrollIntoViewIfNeeded();
    await expect(manager.getByLabel('Image name', { exact: true })).toHaveValue(
      'Draft image',
    );
    await expect(
      manager.getByLabel('Expected SHA-256', { exact: true }),
    ).toBeVisible();
    await capture(page, info, `image-form-${width}`);
  }
  await press(manager.getByRole('button', { name: 'Cancel', exact: true }));
  await expect(manager.getByLabel('Image name', { exact: true })).toHaveCount(
    0,
  );
});

test('a basic running VM keeps lifecycle controls while guest terminals remain unavailable', async ({
  page,
}, info) => {
  const state = fixture();
  state.machine.status = state.machine.desired = 'running';
  state.machine.guestState = 'not-installed';
  state.machine.guestReason =
    'This image provides basic VM management without AXV guest enhancements.';
  const manager = await host(page, state);
  await expect(
    manager.getByRole('button', { name: 'Review VM lima' }),
  ).toContainText('Running');
  await expect(
    manager.getByRole('button', { name: 'Stop', exact: true }),
  ).toBeEnabled();
  await manager.getByRole('button', { name: 'More VM actions' }).click();
  await expect(menuItem(page, 'Restart')).toBeEnabled();
  await page.keyboard.press('Escape');
  await expect(
    manager.getByRole('button', { name: 'Open Terminal', exact: true }),
  ).toHaveCount(0);
  await manager.getByRole('button', { name: 'Inspect', exact: true }).click();
  const inspector = page.locator('[data-app-id="machine-inspector"]');
  await expect(
    inspector.getByText(
      'Basic VM management only. This image has no guest enhancements.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    inspector.getByRole('button', { name: /^(Open|New) Terminal$/ }),
  ).toHaveCount(0);
  await expect(
    inspector.getByRole('button', { name: 'Stop', exact: true }),
  ).toBeEnabled();
  expect(state.terminalReads).toBe(0);
  await capture(page, info, 'basic-vm-inspector');
});

test('a ready guest without terminal capability explains unavailable terminals', async ({
  page,
}) => {
  const state = fixture();
  state.machine.status = state.machine.desired = 'running';
  state.machine.ready = true;
  state.machine.guestState = 'ready';
  state.machine.guestCapabilities = ['exec'];
  const manager = await host(page, state);
  await expect(
    manager.getByRole('button', { name: 'Open Terminal', exact: true }),
  ).toHaveCount(0);
  await manager.getByRole('button', { name: 'Inspect', exact: true }).click();
  const inspector = page.locator('[data-app-id="machine-inspector"]');
  await expect(
    inspector.getByText('Guest terminals are unavailable in this image.', {
      exact: true,
    }),
  ).toBeVisible();
  expect(state.terminalReads).toBe(0);
});

test(
  'narrow guest terminal menus clear and end only the selected shell',
  { tag: '@vm-ui' },
  async ({ page, hasTouch }, info) => {
    await page.setViewportSize({ width: 320, height: 568 });
    const state = fixture();
    state.machine.status = state.machine.desired = 'running';
    state.machine.ready = true;
    state.machine.guestState = 'ready';
    state.machine.guestCapabilities = ['terminals'];
    const manager = await host(page, state);
    const terminals: GuestTerminal[] = [];
    const sockets = new Map<string, WebSocketRoute>();
    const ended: string[] = [];
    const input: Buffer[] = [];
    let rejectRoster = true;
    let rosterGate: Promise<void> | undefined;
    let rejectAttachment = true;
    const prefix = `/api/compute/machines/${state.machine.id}/guest/terminals`;
    await page.route(`**${prefix}**`, async (route) => {
      const request = route.request();
      expect(request.headers()['x-axv-host-id']).toBe(state.machine.hostId);
      const path = new URL(request.url()).pathname;
      if (request.method() === 'POST') {
        expect(path).toBe(prefix);
        const terminal = {
          id: `guest-shell-${terminals.length + 1}`,
          bootId: 'review-boot',
          status: 'running',
          sequence: terminals.length + 1,
        };
        terminals.push(terminal);
        await route.fulfill({ json: terminal });
        return;
      }
      if (path === prefix) {
        if (rejectRoster) {
          await route.fulfill({
            status: 503,
            json: { error: 'Could not list current terminals.' },
          });
          return;
        }
        await rosterGate;
        await route.fulfill({ json: { terminals } });
        return;
      }
      const id = path.slice(prefix.length + 1);
      const terminal = terminals.find((item) => item.id === id);
      expect(terminal).toBeTruthy();
      if (request.method() === 'DELETE') {
        ended.push(id);
        terminal!.status = 'ended';
      }
      if (request.method() === 'GET' && rejectAttachment) {
        rejectAttachment = false;
        await route.fulfill({
          status: 409,
          json: { error: 'Guest attachment is temporarily unavailable.' },
        });
        return;
      }
      await route.fulfill({ json: terminal });
    });
    await page.routeWebSocket(
      (url) =>
        url.pathname.startsWith(`${prefix}/`) &&
        url.pathname.endsWith('/attach'),
      (socket) => {
        const url = new URL(socket.url());
        expect(url.searchParams.get('hostId')).toBe(state.machine.hostId);
        const id = url.pathname.slice(prefix.length + 1).split('/')[0]!;
        sockets.set(id, socket);
        socket.onMessage((message) => {
          if (Buffer.isBuffer(message)) input.push(message);
        });
        socket.send(JSON.stringify({ ready: true, bootId: 'review-boot' }));
        socket.send(Buffer.from(`GUEST ${id}\r\nguest-test> `));
      },
    );
    const openTerminal = manager.getByRole('button', {
      name: 'Open Terminal',
      exact: true,
    });
    await openTerminal.click();
    const first = page.locator('[data-app-id="guest-terminal"]').first();
    await expect(manager.getByRole('alert')).toContainText(
      'Could not list current terminals.',
    );
    await expect(first).toHaveCount(0);
    expect(terminals).toHaveLength(0);
    rejectRoster = false;

    let releaseRoster!: () => void;
    rosterGate = new Promise<void>((resolve) => {
      releaseRoster = resolve;
    });
    try {
      await openTerminal.click();
      await expect(
        manager.getByRole('button', { name: 'Opening Terminal…' }),
      ).toBeDisabled();
      await manager
        .getByRole('button', { name: 'Volumes', exact: true })
        .click();
    } finally {
      releaseRoster();
      rosterGate = undefined;
    }
    await expect(manager.getByRole('button', { name: 'New VM' })).toBeEnabled();
    await expect(first).toHaveCount(0);
    expect(terminals).toHaveLength(0);
    await manager.getByRole('button', { name: 'VMs', exact: true }).click();
    await openTerminal.click();
    await expect(first).toHaveAttribute(
      'aria-label',
      `${state.machine.spec.name} — Terminal 1`,
    );
    await expect(first.getByTestId('terminal-status')).toHaveAttribute(
      'data-state',
      'error',
    );
    await expectShellToolbar(first, 'Guest shell', [
      'Copy',
      'Reconnect',
      'More guest terminal actions',
    ]);
    await expect(first.getByRole('alert')).toContainText(
      'Guest attachment is temporarily unavailable.',
    );
    await capture(page, info, 'guest-terminal-recovery-320');
    const reconnect = first.getByRole('button', {
      name: 'Reconnect',
      exact: true,
    });
    if (hasTouch) await reconnect.tap();
    else await reconnect.click();
    await expect(first.getByTestId('terminal-status')).toHaveAttribute(
      'data-state',
      'connected',
    );
    await expect(output(first)).toContainText('GUEST guest-shell-1');
    expect(terminals.map(({ id }) => id)).toEqual(['guest-shell-1']);
    const more = first.getByRole('button', {
      name: 'More guest terminal actions',
      exact: true,
    });
    await expectShellToolbar(first, 'Guest shell', [
      'Copy',
      'Paste',
      'Interrupt',
      'More guest terminal actions',
    ]);
    await capture(page, info, 'guest-terminal-320');
    await more.focus();
    await more.press('Enter');
    const sessionMenu = page.getByRole('menu', {
      name: 'Session',
      exact: true,
    });
    for (const name of ['Clear', 'Details', 'Paste text', 'End Session'])
      await expect(menuItem(sessionMenu, name)).toBeInViewport();
    for (const name of ['Interrupt', 'Reconnect'])
      await expect(menuItem(sessionMenu, name)).toHaveCount(0);
    await expect(first.getByLabel('Guest terminal input')).not.toBeFocused();
    await capture(page, info, 'guest-terminal-more-320');
    await page.keyboard.press('Escape');
    await expect(more).toBeFocused();
    const originalWindow = await first.getAttribute('data-window-id');
    await first.getByRole('button', { name: /^Close / }).click();
    await expect(first).toHaveCount(0);
    await activate(page, manager);
    await applicationMenu(page, 'Selected VM', hasTouch);
    const selectedVM = page.getByRole('menu', { name: 'Selected VM' });
    await menuItem(selectedVM, 'Open Terminal').click();
    await expect(first).toHaveAttribute('data-window-id', originalWindow!);
    await expect(output(first)).toContainText('GUEST guest-shell-1');
    expect(terminals.map(({ id }) => id)).toEqual(['guest-shell-1']);
    await first.getByRole('button', { name: /^Close / }).click();
    await expect(first).toHaveCount(0);
    await activate(page, manager);
    await manager.getByRole('button', { name: 'Inspect', exact: true }).click();
    const inspector = page.locator('[data-app-id="machine-inspector"]');
    await inspector
      .getByRole('button', { name: 'Resume Terminal', exact: true })
      .click();
    await expect(first).toHaveAttribute('data-window-id', originalWindow!);
    await expect(first.getByTestId('terminal-status')).toHaveAttribute(
      'data-state',
      'connected',
    );
    expect(terminals.map(({ id }) => id)).toEqual(['guest-shell-1']);
    await activate(page, inspector);
    await inspector
      .getByRole('button', { name: 'New Terminal', exact: true })
      .click();
    const second = page.locator('[data-app-id="guest-terminal"]').last();
    await expect(second.getByTestId('terminal-status')).toHaveAttribute(
      'data-state',
      'connected',
    );
    await expect(second).toHaveAttribute(
      'aria-label',
      `${state.machine.spec.name} — Terminal 2`,
    );
    await expect(output(second)).toContainText('GUEST guest-shell-2');
    await activate(page, manager);
    await openTerminal.click();
    await expect(inspector).toHaveClass(/is-active/);
    const sessions = inspector.locator('details[open]').filter({
      has: page.getByText('Guest terminals', { exact: true }),
    });
    await expect(sessions.locator('summary')).toBeFocused();
    await expect(
      sessions.getByRole('button', { name: 'Resume Terminal 1', exact: true }),
    ).toBeVisible();
    await capture(page, info, 'guest-terminal-picker-320');
    await sessions
      .getByRole('button', { name: 'Resume Terminal 2', exact: true })
      .click();
    await expect(second).toHaveClass(/is-active/);
    await expect(output(second)).toContainText('GUEST guest-shell-2');
    expect(terminals.map(({ id }) => id)).toEqual([
      'guest-shell-1',
      'guest-shell-2',
    ]);
    await page
      .getByRole('button', { name: 'Window Overview', exact: true })
      .click();
    const firstTitle = await first.getAttribute('aria-label');
    await page
      .getByRole('button', { name: `Switch to ${firstTitle}`, exact: true })
      .click();
    await expect(first).toHaveClass(/is-active/);
    await actionMenu(
      page,
      first,
      'More guest terminal actions',
      'Details',
      hasTouch,
    );
    const details = page.getByRole('dialog', {
      name: 'Guest Terminal Details',
      exact: true,
    });
    for (const binding of [
      state.machine.id,
      state.machine.hostId,
      'review-boot',
      'guest-shell-1',
    ])
      await expect(details.getByText(binding, { exact: true })).toBeVisible();
    await expect(
      details.getByText('guest-shell-2', { exact: true }),
    ).toHaveCount(0);
    await capture(page, info, 'guest-terminal-details-320');
    await details.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(more).toBeFocused();
    await actionMenu(page, first, 'More guest terminal actions', 'Clear');
    await expect(output(first)).not.toContainText('GUEST guest-shell-1');
    await expect(output(second)).toContainText('GUEST guest-shell-2');
    expect(input).toHaveLength(0);
    await actionMenu(page, first, 'More guest terminal actions', 'End Session');
    const confirmation = page.getByRole('dialog', {
      name: 'End guest terminal?',
      exact: true,
    });
    await expect(confirmation).toContainText(
      `End “${state.machine.spec.name} — Terminal 1” and its jobs? Closing the window only detaches it.`,
    );
    await expect(confirmation).not.toContainText('Terminal 2');
    await expect(
      confirmation.getByRole('button', { name: 'Cancel', exact: true }),
    ).toBeFocused();
    await capture(page, info, 'guest-terminal-end-confirmation-320');
    await confirmation
      .getByRole('button', { name: 'Cancel', exact: true })
      .click();
    expect(ended).toEqual([]);
    for (const target of [first, second])
      await expect(target.getByTestId('terminal-status')).toHaveAttribute(
        'data-state',
        'connected',
      );
    expect(terminals.map((terminal) => terminal.status)).toEqual([
      'running',
      'running',
    ]);
    await actionMenu(page, first, 'More guest terminal actions', 'End Session');
    await confirmation
      .getByRole('button', { name: 'End Session', exact: true })
      .click();
    await expect(first.getByTestId('terminal-status')).toHaveAttribute(
      'data-state',
      'ended',
    );
    await expect(second.getByTestId('terminal-status')).toHaveAttribute(
      'data-state',
      'connected',
    );
    expect(ended).toEqual(['guest-shell-1']);
    expect(terminals.map((terminal) => terminal.status)).toEqual([
      'ended',
      'running',
    ]);
    expect([...sockets.keys()]).toEqual(['guest-shell-1', 'guest-shell-2']);
  },
);

function backupProvenance(): NonNullable<Volume['backup']> {
  return {
    hostId: 'host-review',
    sourceVolumeId: 'source-volume',
    sourceVolumeRevision: 1,
    sourceMachineId: 'source-vm',
    sourceMachineRevision: 1,
    projects: [],
    createdAt: '2026-10-22T00:00:00Z',
  };
}

test('immutable project backups expose deletion but cannot attach or change', async ({
  page,
}) => {
  const state = fixture();
  state.machine.spec.volumeId = '';
  state.volume.machineId = '';
  state.volume.backup = backupProvenance();
  const manager = await host(page, state);
  await manager.getByRole('button', { name: 'Volumes', exact: true }).click();
  await manager
    .getByRole('button', { name: 'Review project', exact: true })
    .click();
  await expect(
    manager.getByText('Immutable backup', { exact: true }),
  ).toBeVisible();
  await expect(
    manager.getByRole('button', { name: 'Copy', exact: true }),
  ).toBeDisabled();
  await expect(
    manager.getByLabel('Keep this volume after VM deletion', { exact: true }),
  ).toBeDisabled();
  await expect(
    manager.getByRole('button', { name: 'Attach Review project', exact: true }),
  ).toHaveCount(0);
  await manager.getByRole('button', { name: 'More volume actions' }).click();
  for (const label of ['Rename', 'Grow', 'Detach'])
    await expect(menuItem(page, label)).toBeDisabled();
  await expect(menuItem(page, 'Delete')).toBeEnabled();
  await menuItem(page, 'Delete').click();
  const dialog = page.getByRole('dialog', { name: 'Delete project volume?' });
  await expect(dialog).toContainText('Permanently delete');
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
});

test('restored working volumes remain attachable and editable without generic copying', async ({
  page,
}) => {
  const state = fixture();
  state.machine.spec.volumeId = '';
  state.volume.machineId = '';
  state.volume.backup = backupProvenance();
  state.volume.restoredFrom = 'backup-original';
  const manager = await host(page, state);
  await manager.getByRole('button', { name: 'Volumes', exact: true }).click();
  await manager
    .getByRole('button', { name: 'Review project', exact: true })
    .click();
  await expect(
    manager.getByText('Restored working volume', { exact: true }),
  ).toBeVisible();
  await expect(
    manager.getByRole('button', { name: 'Copy', exact: true }),
  ).toBeDisabled();
  await expect(
    manager.getByLabel('Keep this volume after VM deletion', { exact: true }),
  ).toBeEnabled();
  await manager
    .getByRole('button', { name: 'Attach Review project', exact: true })
    .click();
  await manager
    .getByRole('combobox', { name: 'VM without a project volume', exact: true })
    .selectOption(state.machine.id);
  await expect(
    manager.getByRole('button', { name: 'Attach', exact: true }),
  ).toBeEnabled();
  await manager.getByRole('button', { name: 'More volume actions' }).click();
  await expect(menuItem(page, 'Rename')).toBeEnabled();
  await expect(menuItem(page, 'Grow')).toBeEnabled();
  await page.keyboard.press('Escape');
});

test('VM creation excludes immutable backups and offers restored working volumes', async ({
  page,
}) => {
  const state = fixture();
  state.volume.machineId = '';
  state.volume.backup = backupProvenance();
  state.additionalVolumes.push({
    ...state.volume,
    id: 'restored-working',
    name: 'Restored project',
    restoredFrom: state.volume.id,
  });
  const manager = await host(page, state);
  await manager.getByRole('button', { name: 'New VM', exact: true }).click();
  const create = page.locator('[data-app-id="machine-create"]');
  await create.getByText('Advanced', { exact: true }).click();
  const select = create.getByRole('combobox', {
    name: 'Project volume',
    exact: true,
  });
  await expect(
    select.locator('optgroup[label="Existing volumes"]'),
  ).toHaveCount(1);
  await expect(
    select.locator(`option[value="${state.volume.id}"]`),
  ).toHaveCount(0);
  await expect(select.locator('option[value="restored-working"]')).toHaveCount(
    1,
  );
  await select.selectOption('restored-working');
  await expect(
    create.getByLabel('Project disk (GiB)', { exact: true }),
  ).toHaveValue('1');
});
