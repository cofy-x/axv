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
import { output } from './terminal-helpers';

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
    manager.getByRole('button', { name: 'Start', exact: true }),
  ).toBeDisabled();
  await expect(
    manager.getByText(/active VM or project-volume operation/),
  ).toBeVisible();
  await manager.getByRole('button', { name: 'Inspect', exact: true }).click();
  const inspector = page.locator('[data-app-id="machine-inspector"]');
  await expect(
    inspector.getByText(/active VM or project-volume operation/),
  ).toBeVisible();
  await inspector.getByRole('button', { name: 'More VM actions' }).click();
  await expect(menuItem(page, 'Delete')).toBeDisabled();
  await page.keyboard.press('Escape');
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
    manager.getByRole('button', { name: 'New Terminal', exact: true }),
  ).toBeDisabled();
  await manager.getByRole('button', { name: 'Inspect', exact: true }).click();
  const inspector = page.locator('[data-app-id="machine-inspector"]');
  await expect(
    inspector.getByText('Guest not installed', { exact: false }),
  ).toBeVisible();
  await expect(
    inspector.getByRole('button', { name: 'New Terminal', exact: true }),
  ).toBeDisabled();
  await expect(
    inspector.getByRole('button', { name: 'Stop', exact: true }),
  ).toBeEnabled();
  expect(state.terminalReads).toBe(0);
  await capture(page, info, 'basic-vm-inspector');
});

test('a ready guest without terminal capability gives a concrete disabled reason', async ({
  page,
}) => {
  const state = fixture();
  state.machine.status = state.machine.desired = 'running';
  state.machine.ready = true;
  state.machine.guestState = 'ready';
  state.machine.guestCapabilities = ['exec'];
  const manager = await host(page, state);
  const terminal = manager.getByRole('button', {
    name: 'New Terminal',
    exact: true,
  });
  await expect(terminal).toBeDisabled();
  await expect(terminal).toHaveAttribute(
    'title',
    'This image does not provide guest terminals.',
  );
  await manager.getByRole('button', { name: 'Inspect', exact: true }).click();
  const inspector = page.locator('[data-app-id="machine-inspector"]');
  await inspector.getByText('Guest terminals', { exact: true }).click();
  await expect(
    inspector.getByText('This image does not provide guest terminals.', {
      exact: true,
    }),
  ).toBeVisible();
  expect(state.terminalReads).toBe(0);
});

test('narrow guest terminal menus keep clear and end bound to their original shell', async ({
  page,
}, info) => {
  await page.setViewportSize({ width: 390, height: 844 });
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
      };
      terminals.push(terminal);
      await route.fulfill({ json: terminal });
      return;
    }
    if (path === prefix) {
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
    await route.fulfill({ json: terminal });
  });
  await page.routeWebSocket(
    (url) =>
      url.pathname.startsWith(`${prefix}/`) && url.pathname.endsWith('/attach'),
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
  await manager
    .getByRole('button', { name: 'New Terminal', exact: true })
    .click();
  const first = page.locator('[data-app-id="guest-terminal"]').first();
  await expect(first.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  await expect(output(first)).toContainText('GUEST guest-shell-1');
  const firstWindowId = await first.getAttribute('data-window-id');
  const more = first.getByRole('button', {
    name: 'More guest terminal actions',
    exact: true,
  });
  for (const label of [
    'Copy',
    'Paste',
    'Interrupt',
    'More guest terminal actions',
  ])
    await expect(
      first.getByRole('button', { name: label, exact: true }),
    ).toBeInViewport();
  await more.focus();
  await more.press('Enter');
  await expect(menuItem(page, 'End Session')).toBeInViewport();
  await expect(first.getByLabel('Guest terminal input')).not.toBeFocused();
  await capture(page, info, 'guest-terminal-more-390');
  await page.keyboard.press('Escape');
  await expect(more).toBeFocused();
  await more.press('Enter');
  await page
    .getByRole('button', { name: 'Pin Session menu', exact: true })
    .click();
  const pinned = page.locator('.ns-pinned-menu').filter({
    has: page.getByRole('menuitem', { name: 'End Session', exact: true }),
  });
  await expect(pinned).toHaveAttribute('data-owner-window-id', firstWindowId!);
  const title = (await pinned.locator('.ns-menu-title').boundingBox())!;
  await page.mouse.move(title.x + title.width / 2, title.y + title.height / 2);
  await page.mouse.down();
  await page.mouse.move(110, 520, { steps: 4 });
  await page.mouse.up();
  await activate(page, manager);
  await manager
    .getByRole('button', { name: 'New Terminal', exact: true })
    .click();
  const second = page.locator('[data-app-id="guest-terminal"]').last();
  await expect(second.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  await expect(output(second)).toContainText('GUEST guest-shell-2');
  await pinned.getByRole('menuitem', { name: 'Clear', exact: true }).click();
  await expect(output(first)).not.toContainText('GUEST guest-shell-1');
  await expect(output(second)).toContainText('GUEST guest-shell-2');
  expect(input).toHaveLength(0);
  await pinned
    .getByRole('menuitem', { name: 'End Session', exact: true })
    .click();
  const confirmation = page.getByRole('dialog', {
    name: 'End guest terminal?',
    exact: true,
  });
  await expect(confirmation).toContainText(
    `End “${state.machine.spec.name} — Terminal” and its jobs? Terminal: guest-shell-1. Closing the window only detaches it.`,
  );
  await expect(confirmation).not.toContainText('guest-shell-2');
  await expect(
    confirmation.getByRole('button', { name: 'Cancel', exact: true }),
  ).toBeFocused();
  await capture(page, info, 'guest-terminal-end-confirmation-390');
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
  await pinned
    .getByRole('menuitem', { name: 'End Session', exact: true })
    .click();
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
});

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
    manager.getByRole('combobox', {
      name: 'VM without a project volume',
      exact: true,
    }),
  ).toBeDisabled();
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
  const select = create.getByRole('combobox', { name: 'Volume', exact: true });
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
