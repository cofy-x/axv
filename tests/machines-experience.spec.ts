import { preparationCheckFixture } from './machines-preparation-fixture';
import { creationDefaultsFixture } from './machines-creation-fixture';
import { refreshMachines } from './machines-helpers';
import type { Locator, Page, TestInfo } from '@playwright/test';
import type {
  ComputeEnvironment,
  HostInfo,
  ImageInfo,
  Machine,
  Operation,
  Volume,
} from '../apps/dashboard/src/features/machines/api';
import {
  activate,
  actionMenu,
  bounds,
  expect,
  launch,
  menuItem,
  persistedLayout,
  test,
} from './helpers';

interface HostFixture {
  environment: ComputeEnvironment;
  check?: Parameters<typeof preparationCheckFixture>[0];
  info: HostInfo;
  machines: Machine[];
  volumes: Volume[];
  images: ImageInfo[];
  operations: Operation[];
  offline: boolean;
  writes: string[];
  guestReads: string[];
  gate?: Promise<void>;
  environmentGate?: Promise<void>;
}

function fixture(): HostFixture {
  const image: ImageInfo = {
    id: 'configured-linux',
    name: 'Ubuntu LTS',
    provider: 'lima',
    arch: 'arm64',
    status: 'configured',
  };
  const machines: Machine[] = ['Build VM', 'Task VM'].map((name, index) => ({
    id: `vm-experience-${index}`,
    hostId: 'host-experience',
    revision: 1,
    spec: {
      name,
      provider: 'lima',
      imageId: image.id,
      kind: 'development',
      resources: { cpus: 2, memoryMiB: 1024, diskGiB: 8 },
      projectGiB: 4,
      retainVolume: true,
      volumeId: `volume-experience-${index}`,
    },
    desired: index ? 'running' : 'stopped',
    status: index ? 'running' : 'stopped',
    ready: !!index,
    guestState: index ? 'ready' : 'unavailable',
    guestCapabilities: index ? ['exec', 'terminals', 'ssh', 'network'] : [],
    reserved: !!index,
    createdAt: '2026-10-16T00:00:00Z',
    updatedAt: '2026-10-16T00:00:00Z',
  }));
  return {
    environment: {
      platform: 'darwin',
      configured: true,
      configPath: '/test/Team Profile/config.json',
      profileHome: '/test/Team Profile',
      executablePath: '/test/AXV Build/axv',
      requiredLimaVersion: '2.2.1',
      automaticSetupSupported: true,
    },
    info: {
      configured: true,
      defaults: creationDefaultsFixture(image.id),
      hostId: 'host-experience',
      hostArch: 'arm64',
      capacity: {
        cpus: 8,
        memoryMiB: 8192,
        diskGiB: 100,
        machines: 8,
        workers: 2,
      },
      providers: [
        {
          provider: 'lima',
          available: true,
          persistentVolumes: true,
          snapshots: false,
          guestExecution: true,
          volumes: true,
          images: true,
          configuration: true,
          forceStop: true,
        },
      ],
      images: [image],
    },
    machines,
    volumes: machines.map((machine) => ({
      id: machine.spec.volumeId!,
      name: `${machine.spec.name} project`,
      revision: 1,
      provider: 'lima',
      sizeGiB: 4,
      retain: true,
      machineId: machine.id,
      status: 'ready',
    })),
    images: [image],
    operations: [],
    offline: false,
    writes: [],
    guestReads: [],
  };
}

async function mockHost(page: Page, state: HostFixture) {
  await page.route('**/api/compute/**', async (route) => {
    const path = new URL(route.request().url()).pathname.replace(
      '/api/compute',
      '',
    );
    const method = route.request().method();
    if (method !== 'GET') {
      state.writes.push(`${method} ${path}`);
      await route.fulfill({
        status: 409,
        json: { code: 'unexpected_mutation', error: 'Read-only fixture.' },
      });
      return;
    }
    if (['/environment', '/info', '/preparation/check'].includes(path))
      await state.gate;
    if (path === '/environment') await state.environmentGate;
    if (state.offline && path !== '/environment') {
      await route.fulfill({
        status: 503,
        json: { code: 'host_unavailable', error: 'Fixture host is offline.' },
      });
      return;
    }
    const responses: Record<string, unknown> = {
      '/preparation/check': preparationCheckFixture({
        platform: state.environment.platform,
        configured: state.environment.configured,
        automaticSetupSupported: state.environment.automaticSetupSupported,
        ...state.check,
      }),
      '/environment': state.environment,
      '/info': state.info,
      '/machines': { machines: state.machines },
      '/volumes': { volumes: state.volumes },
      '/images': { images: state.images },
      '/operations': { operations: state.operations },
      '/projects': { projects: [] },
    };
    if (path.endsWith('/guest/terminals')) {
      state.guestReads.push(route.request().headers()['x-axv-host-id']);
      await route.fulfill({ json: { terminals: [] } });
      return;
    }
    if (!(path in responses))
      throw new Error(`Unexpected compute request ${method} ${path}`);
    await route.fulfill({ json: responses[path] });
  });
}

async function host(page: Page, state: HostFixture) {
  await mockHost(page, state);
  await launch(page, 'machines');
  return page.locator('[data-window-id="machines:main"]');
}

async function captureSizes(
  page: Page,
  info: TestInfo,
  focus: Locator,
  name: string,
) {
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
    await focus.scrollIntoViewIfNeeded();
    await expect(focus).toBeVisible();
    const path = info.outputPath(`${name}-${width}.png`);
    await page.screenshot({ path });
    await info.attach(`${name}-${width}`, { path, contentType: 'image/png' });
  }
}

test('delayed discovery leads to platform-specific setup without showing premature setup', async ({
  page,
}, info) => {
  const state = fixture();
  state.environment.configured = false;
  state.info = { configured: false };
  let release = () => {};
  state.gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const manager = await host(page, state);
  try {
    await expect(
      manager.getByRole('heading', { name: 'Checking VM availability' }),
    ).toBeVisible();
    await expect(
      manager.getByRole('heading', { name: 'Enable virtual machines' }),
    ).toHaveCount(0);
    await expect(
      manager.getByRole('button', { name: 'Copy setup command' }),
    ).toHaveCount(0);
    await expect(
      manager.getByRole('button', { name: 'New VM', exact: true }),
    ).toHaveCount(0);
  } finally {
    release();
  }
  const heading = manager.getByRole('heading', {
    name: 'Enable virtual machines',
  });
  await expect(heading).toBeVisible();
  await expect(
    manager.getByLabel('VM storage directory', { exact: true }),
  ).toHaveValue('/test/axv/compute');
  await expect(
    manager.getByRole('button', { name: 'Review', exact: true }),
  ).toBeEnabled();
  await captureSizes(page, info, heading, 'vm-setup');

  await expect(manager.getByRole('button', { name: 'This Mac' })).toHaveCount(
    0,
  );
  await expect(
    manager.getByRole('button', { name: 'Existing host' }),
  ).toHaveCount(0);
  await expect(
    manager.getByLabel('Host socket path', { exact: true }),
  ).toHaveCount(0);

  state.environment = {
    ...state.environment,
    platform: 'linux',
    automaticSetupSupported: false,
    executablePath: '/temporary/build/server',
  };
  await refreshMachines(page);
  const linuxHeading = manager.getByRole('heading', {
    name: 'Connect VM service',
    exact: true,
  });
  await expect(linuxHeading).toBeVisible();
  await expect(manager.getByRole('button', { name: 'This Mac' })).toHaveCount(
    0,
  );
  await expect(
    manager.getByRole('button', { name: 'Existing host' }),
  ).toHaveCount(0);
  await expect(
    manager.getByLabel('Host socket path', { exact: true }),
  ).toHaveCount(0);
  await expect(
    manager.getByText('/run/axv-host/control.sock', { exact: true }),
  ).toBeVisible();
  await expect(
    manager.getByRole('button', { name: 'Edit connection', exact: true }),
  ).toBeVisible();
  await expect(
    manager.getByRole('button', { name: 'Check connection', exact: true }),
  ).toBeEnabled();

  await captureSizes(page, info, linuxHeading, 'vm-linux-setup');

  state.environment = {
    ...state.environment,
    platform: 'windows',
  };
  await refreshMachines(page);
  await expect(
    manager.getByText('VM hosting requires macOS or Linux.', {
      exact: false,
    }),
  ).toBeVisible();
  await expect(
    manager.getByRole('button', { name: 'Copy setup command' }),
  ).toHaveCount(0);
  expect(state.writes).toEqual([]);
});

test(
  'empty hosts distinguish unavailable providers, missing images and configured images',
  { tag: '@vm-ui' },
  async ({ page }, info) => {
    const state = fixture();
    const image = state.images[0]!;
    state.machines = [];
    state.volumes = [];
    state.images = [];
    state.info.images = [];
    state.info.providers![0]!.available = false;
    state.info.providers![0]!.reason = 'Install the required Lima release.';
    const manager = await host(page, state);
    const newVM = manager.getByRole('button', { name: 'New VM', exact: true });
    await expect(
      manager.getByRole('heading', { name: 'VM provider unavailable' }),
    ).toBeVisible();
    await expect(newVM).toBeDisabled();
    await expect(newVM).not.toHaveClass(/\bns-default\b/);
    await manager.getByRole('button', { name: 'View environment' }).click();
    await expect(
      manager.getByText('Install the required Lima release.', {
        exact: false,
      }),
    ).toBeVisible();
    await manager.getByRole('button', { name: 'VMs', exact: true }).click();

    state.info.providers![0]!.available = true;
    state.info.providers![0]!.reason = undefined;
    await refreshMachines(page);
    await expect(newVM).toBeDisabled();
    await manager.getByRole('button', { name: 'Open Images' }).click();
    await expect(
      manager.getByText(
        'No registered images. Add a verified Linux cloud image.',
      ),
    ).toBeVisible();
    await expect(
      manager.getByRole('button', { name: 'Add Image' }),
    ).toBeEnabled();
    await manager
      .getByRole('button', { name: 'Activity', exact: true })
      .click();
    await expect(
      manager.getByRole('cell', { name: 'No activity yet.', exact: true }),
    ).toBeVisible();

    state.images = [image];
    state.info.images = [image];
    await refreshMachines(page);
    await manager.getByRole('button', { name: 'VMs', exact: true }).click();
    const empty = manager.getByRole('heading', { name: 'No VMs yet' });
    await expect(empty).toBeVisible();
    await expect(manager.getByText(/Choose New VM to select/)).toHaveCount(0);
    await expect(newVM).toHaveCount(1);
    await expect(newVM).toBeEnabled();
    await expect(newVM).toHaveClass(/\bns-default\b/);
    expect((await bounds(empty)).x).toBeCloseTo((await bounds(newVM)).x);
    await captureSizes(page, info, empty, 'vm-empty-state');

    await manager.getByRole('button', { name: 'Volumes', exact: true }).click();
    const emptyVolumes = manager.getByRole('heading', {
      name: 'No project volumes yet',
    });
    const newVolume = manager.getByRole('button', {
      name: 'New Volume',
      exact: true,
    });
    await expect(emptyVolumes).toBeVisible();
    await expect(newVolume).toBeEnabled();
    await expect(newVM).not.toHaveClass(/\bns-default\b/);
    await expect(
      manager.getByText('Create a VM to add a project volume.'),
    ).toHaveCount(0);
    expect((await bounds(emptyVolumes)).x).toBeCloseTo(
      (await bounds(newVolume)).x,
    );
    await captureSizes(page, info, emptyVolumes, 'volume-empty-state');
    await newVolume.click();
    await expect(manager.getByLabel('Volume name')).toBeVisible();
    await manager.getByRole('button', { name: 'Cancel', exact: true }).click();
    await manager.getByRole('button', { name: 'VMs', exact: true }).click();
    await newVM.click();
    const create = page.locator('[data-window-id="machine-create:main"]');
    await expect(create.getByText(image.name, { exact: true })).toBeVisible();
    await expect(
      create.getByRole('button', { name: 'Create & Start', exact: true }),
    ).toBeEnabled();
    expect(state.writes).toEqual([]);
  },
);

test('Firecracker without a guest image directs users to host registration', async ({
  page,
}, info) => {
  const state = fixture();
  state.environment.platform = 'linux';
  state.environment.automaticSetupSupported = false;
  state.info.providers = [
    { ...state.info.providers![0]!, provider: 'firecracker', images: false },
  ];
  state.info.defaults = creationDefaultsFixture('', 'firecracker');
  state.info.images = [];
  state.images = [];
  state.machines = [];
  state.volumes = [];
  const manager = await host(page, state);
  const heading = manager.getByRole('heading', {
    name: 'Firecracker guest image required',
    exact: true,
  });
  const guide = manager.getByRole('link', {
    name: 'Linux VM setup guide',
    exact: true,
  });
  await expect(heading).toBeVisible();
  await expect(
    manager.getByText(
      'Ask the deployment host administrator to register a compatible Firecracker guest image.',
    ),
  ).toBeVisible();
  await expect(guide).toBeVisible();
  await expect(
    manager.getByRole('button', { name: 'View environment', exact: true }),
  ).toBeEnabled();
  await expect(
    manager.getByRole('button', { name: 'Open Images', exact: true }),
  ).toHaveCount(0);
  await expect(
    manager.getByRole('button', { name: 'New VM', exact: true }),
  ).toBeDisabled();
  await captureSizes(page, info, heading, 'vm-firecracker-image-required');

  await manager.getByRole('button', { name: 'Images', exact: true }).click();
  await expect(
    manager.getByText(
      'The deployment host administrator registers Firecracker guest images in the VM host configuration.',
    ),
  ).toBeVisible();
  await expect(
    manager.getByText('No Firecracker guest images are registered.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(guide).toBeVisible();
  await expect(
    manager.getByRole('button', { name: 'Add Image', exact: true }),
  ).toHaveCount(0);
  expect(state.writes).toEqual([]);
});

test(
  'Firecracker inventories show backup sources and expose eligible actions',
  { tag: '@vm-ui' },
  async ({ page, hasTouch }, info) => {
    const state = fixture();
    Object.assign(state.environment, {
      platform: 'linux',
      automaticSetupSupported: false,
    });
    state.info.hostArch = 'amd64';
    state.info.providers = [
      {
        ...state.info.providers![0]!,
        provider: 'firecracker',
        volumes: false,
        images: false,
      },
    ];
    const base = state.images[0]!;
    Object.assign(base, { provider: 'firecracker', arch: 'amd64' });
    state.images.push({
      ...base,
      id: 'workshop-image-id',
      name: 'Workshop Guest',
      sha256: 'b'.repeat(64),
    });
    state.info.images = state.images;
    state.info.defaults = creationDefaultsFixture(base.id, 'firecracker');
    for (const vm of state.machines) vm.spec.provider = 'firecracker';
    for (const volume of state.volumes) volume.provider = 'firecracker';
    const provenance: NonNullable<Volume['backup']> = {
      hostId: state.info.hostId!,
      sourceVolumeId: 'detached-workshop-volume-id',
      sourceVolumeRevision: 1,
      sourceMachineId: 'original-workshop-vm',
      sourceMachineRevision: 1,
      projects: [],
      createdAt: '2026-10-16T00:00:00Z',
    };
    state.volumes.push({
      ...state.volumes[0]!,
      id: 'detached-workshop-volume-id',
      name: 'Workshop storage',
      machineId: undefined,
      sizeGiB: 24,
    });
    const stored = state.volumes.at(-1)!;
    state.volumes.push(
      {
        ...stored,
        id: 'workshop-backup',
        name: 'Workshop backup',
        backup: provenance,
      },
      {
        ...stored,
        id: 'recovered-workshop',
        name: 'Recovered workshop',
        restoredFrom: 'workshop-backup',
        backup: provenance,
      },
    );
    state.operations = ['workshop-backup', 'recovered-workshop'].map(
      (volumeId) => ({
        id: `activity-${volumeId}`,
        action: 'copy-volume',
        machineId: '',
        volumeId,
        status: 'succeeded',
        phase: 'complete',
        createdAt: '2026-10-16T00:01:00Z',
      }),
    );
    state.operations.push(
      {
        id: 'activity-start-failed',
        action: 'start',
        machineId: state.machines[0]!.id,
        status: 'failed',
        phase: 'start',
        error: 'The guest failed to boot.',
        createdAt: '2026-10-16T00:02:00Z',
      },
      {
        id: 'activity-start-running',
        action: 'start',
        machineId: state.machines[1]!.id,
        status: 'running',
        phase: 'booting-vm',
        createdAt: '2026-10-16T00:03:00Z',
      },
    );
    const manager = await host(page, state);
    await manager.getByRole('button', { name: 'Images', exact: true }).click();
    await expect(
      manager.getByRole('button', { name: 'Add Image' }),
    ).toHaveCount(0);
    await expect(
      manager.getByRole('button', { name: 'More image actions' }),
    ).toHaveCount(0);
    const images = page.getByRole('dialog', { name: 'Image Details' });
    await manager
      .getByRole('button', { name: 'Image details for Ubuntu LTS' })
      .click();
    await expect(images).toContainText('Used by Build VM, Task VM.');
    await expect(
      images.locator('.ns-dialog-main > .ns-pixel-icon'),
    ).toHaveCount(0);
    await expect(images.getByRole('button', { name: 'Remove…' })).toHaveCount(
      0,
    );
    await images.getByRole('button', { name: 'Close', exact: true }).click();
    const workshopImage = manager.getByRole('button', {
      name: 'Image details for Workshop Guest',
    });
    await workshopImage.click();
    await expect(
      images.getByText('workshop-image-id', { exact: true }),
    ).toBeVisible();
    await images.getByRole('button', { name: 'Remove…' }).click();
    const removal = page.getByRole('dialog', { name: 'Remove image?' });
    await expect(removal).toContainText(
      'Remove “Workshop Guest” from the image catalog?',
    );
    await expect(
      removal.locator('.ns-dialog-main > .ns-pixel-icon'),
    ).toBeVisible();
    await removal.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(workshopImage).toBeFocused();

    await manager.getByRole('button', { name: 'Volumes', exact: true }).click();
    await expect(
      manager.getByRole('button', { name: 'Copy', exact: true }),
    ).toHaveCount(0);
    await expect(
      manager.getByRole('button', { name: 'More volume actions' }),
    ).toHaveCount(0);
    const workshopVolume = manager.getByRole('button', {
      name: 'Volume actions for Workshop storage',
      exact: true,
    });
    await expect(
      manager.getByRole('row', {
        name: /Workshop storage.*Detached/,
      }),
    ).toContainText('24 GiB');
    await expect(manager.getByText('Backup of Workshop storage')).toBeVisible();
    await expect(
      manager.getByText('Restored from Workshop backup'),
    ).toBeVisible();
    const backupRow = manager.getByRole('row', {
      name: /Workshop backup.*Immutable backup/,
    });
    await expect(backupRow).toContainText('Restore in Project Files');
    await expect(
      manager.getByText('Restore in Project Files', { exact: true }),
    ).toHaveCount(1);
    await expect(
      manager.getByText(/Reuse compatible detached volumes/),
    ).toHaveCount(0);
    await captureSizes(page, info, backupRow, 'volume-action-context');
    await expect(manager.getByText('detached-workshop-volume-id')).toHaveCount(
      0,
    );
    await expect(
      manager.getByRole('button', {
        name: 'Volume actions for Build VM project',
      }),
    ).toHaveCount(0);
    await actionMenu(
      page,
      manager,
      'Volume actions for Workshop storage',
      'Delete…',
      hasTouch,
    );
    const deletion = page.getByRole('dialog', {
      name: 'Delete project volume?',
    });
    await expect(deletion).toContainText(
      'Permanently delete “Workshop storage”',
    );
    await deletion.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(workshopVolume).toBeFocused();

    await manager
      .getByRole('button', { name: 'Activity', exact: true })
      .click();
    const history = manager.getByRole('table');
    await expect(
      history.getByRole('row', {
        name: /Back up volume.*Workshop backup.*Succeeded/,
      }),
    ).toBeVisible();
    await expect(
      history.getByRole('row', {
        name: /Restore volume.*Recovered workshop.*Succeeded/,
      }),
    ).toBeVisible();
    await expect(history.locator('summary')).toHaveCount(0);
    await expect(history.getByText('Complete', { exact: true })).toHaveCount(0);
    const failed = history.getByRole('row', {
      name: /Start.*Build VM.*Failed/,
    });
    await expect(failed.getByText('Start', { exact: true })).toHaveCount(1);
    await expect(failed).toContainText('The guest failed to boot.');
    await expect(
      history.getByText('Booting VM', { exact: true }),
    ).toBeVisible();
    await captureSizes(page, info, failed, 'activity-distinct-phases');
    expect(state.writes).toEqual([]);
  },
);

test('offline hosts keep inventory and image drafts while blocking changes', async ({
  page,
}, info) => {
  const state = fixture();
  const manager = await host(page, state);
  const row = manager.getByRole('button', {
    name: 'Build VM lima',
    exact: true,
  });
  await row.click();
  const start = manager.getByRole('button', { name: 'Start VM', exact: true });
  await expect(start).toBeEnabled();
  await captureSizes(page, info, row, 'vm-selected-inventory');
  await manager.getByRole('button', { name: 'Images', exact: true }).click();
  await manager.getByRole('button', { name: 'Add Image' }).click();
  const draft = manager.getByLabel('Image name', { exact: true });
  await draft.fill('Offline image draft');
  await manager.getByRole('button', { name: 'VMs', exact: true }).click();

  state.offline = true;
  await refreshMachines(page);
  await expect(manager.getByRole('alert')).toContainText(
    'Fixture host is offline.',
  );
  await expect(manager.getByRole('alert')).toContainText(
    'Showing the last known state',
  );
  await expect(row).toBeVisible();
  await expect(row).toHaveAttribute('aria-pressed', 'true');
  await expect(start).toBeDisabled();
  await manager.getByRole('button', { name: 'More VM actions' }).click();
  await expect(menuItem(page, 'Delete')).toBeDisabled();
  await page.keyboard.press('Escape');
  await expect(
    manager.getByRole('button', { name: 'New VM', exact: true }),
  ).toHaveCount(0);
  await expect(
    manager.locator('.machines-app > .machines-toolbar'),
  ).toHaveCount(0);
  await expect(
    manager.getByRole('button', { name: 'VM Host', exact: true }),
  ).toBeEnabled();
  await expect(
    manager.getByRole('button', { name: 'Inspect', exact: true }),
  ).toBeEnabled();
  await expect(
    manager.getByRole('heading', { name: 'No VMs yet' }),
  ).toHaveCount(0);

  await manager.getByRole('button', { name: 'Images', exact: true }).click();
  await expect(draft).toHaveValue('Offline image draft');
  await expect(
    manager.getByRole('button', { name: 'Download and Verify' }),
  ).toBeDisabled();
  await expect(
    manager.getByRole('button', { name: 'Add Image' }),
  ).toBeDisabled();
  await manager
    .getByRole('button', { name: 'Ubuntu LTS', exact: true })
    .click();
  await manager.getByRole('button', { name: 'More image actions' }).click();
  await expect(menuItem(page, 'Remove')).toBeDisabled();
  await page.keyboard.press('Escape');

  state.offline = false;
  await manager
    .getByRole('button', { name: 'Retry connection', exact: true })
    .click();
  await expect(manager.getByRole('alert')).toHaveCount(0);
  await expect(draft).toHaveValue('Offline image draft');
  await manager.getByRole('button', { name: 'VMs', exact: true }).click();
  await expect(start).toBeEnabled();
  expect(state.writes).toEqual([]);
});

test('volume and image menus preserve reviewed targets and cancel without changes', async ({
  page,
  hasTouch,
}) => {
  const state = fixture();
  state.volumes.push({
    id: 'volume-menu-review',
    name: 'Menu project',
    provider: 'lima',
    revision: 1,
    sizeGiB: 4,
    retain: true,
    status: 'ready',
  });
  state.images.push({
    id: 'image-menu-review',
    name: 'Menu image',
    provider: 'lima',
    arch: 'arm64',
    status: 'ready',
    managed: true,
  });
  const manager = await host(page, state);
  await manager.getByRole('button', { name: 'Volumes', exact: true }).click();
  await manager
    .getByRole('button', { name: 'Menu project', exact: true })
    .click();
  await actionMenu(page, manager, 'More volume actions', 'Rename', hasTouch);
  await expect(manager.getByLabel('Volume name', { exact: true })).toHaveValue(
    'Menu project',
  );
  await manager.getByRole('button', { name: 'Cancel', exact: true }).click();
  await actionMenu(page, manager, 'More volume actions', 'Delete', hasTouch);
  const deletion = page.getByRole('dialog', { name: 'Delete project volume?' });
  await expect(deletion).toContainText(
    'Permanently delete “Menu project” and all of its files?',
  );
  await deletion.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(
    manager.getByRole('button', { name: 'Menu project', exact: true }),
  ).toHaveAttribute('aria-pressed', 'true');

  await manager.getByRole('button', { name: 'Images', exact: true }).click();
  await manager
    .getByRole('button', { name: 'Menu image', exact: true })
    .click();
  await actionMenu(
    page,
    manager,
    'More image actions',
    'Add Version',
    hasTouch,
  );
  await expect(manager.getByLabel('Image name', { exact: true })).toHaveValue(
    'Menu image update',
  );
  await manager.getByRole('button', { name: 'Cancel', exact: true }).click();
  await actionMenu(page, manager, 'More image actions', 'Remove', hasTouch);
  const removal = page.getByRole('dialog', { name: 'Remove image?' });
  await expect(removal).toContainText(
    'Remove “Menu image” and its managed files?',
  );
  await removal.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(
    manager.getByRole('button', { name: 'Menu image', exact: true }),
  ).toHaveAttribute('aria-pressed', 'true');
  expect(state.writes).toEqual([]);
});

for (const resource of ['volume', 'image'] as const) {
  test(`an open ${resource} menu rechecks availability after inventory updates`, async ({
    page,
  }) => {
    const state = fixture();
    const targetId = `${resource}-menu-current`;
    if (resource === 'volume')
      state.volumes.push({
        id: targetId,
        name: 'Current menu project',
        provider: 'lima',
        revision: 1,
        sizeGiB: 4,
        retain: true,
        status: 'ready',
      });
    else
      state.images.push({
        id: targetId,
        name: 'Current menu image',
        provider: 'lima',
        arch: 'arm64',
        status: 'ready',
        managed: true,
      });
    const manager = await host(page, state);
    await manager
      .getByRole('button', {
        name: resource === 'volume' ? 'Volumes' : 'Images',
        exact: true,
      })
      .click();
    const target = manager.getByRole('button', {
      name:
        resource === 'volume' ? 'Current menu project' : 'Current menu image',
      exact: true,
    });
    await target.click();
    await manager
      .getByRole('button', { name: `More ${resource} actions`, exact: true })
      .click();
    const action = menuItem(page, resource === 'volume' ? 'Delete' : 'Remove');
    await expect(action).toBeEnabled();

    if (resource === 'volume') {
      state.volumes.find((volume) => volume.id === targetId)!.status =
        'deleting';
      await expect(
        manager.locator('tr').filter({ hasText: 'Current menu project' }),
      ).toContainText('Deleting');
    } else {
      const original = state.machines[0];
      state.machines.push({
        ...original,
        id: 'vm-menu-image-user',
        spec: {
          ...original.spec,
          name: 'Image User',
          imageId: targetId,
          volumeId: '',
        },
      });
      await expect(
        manager.getByText('Used by Image User.', { exact: false }),
      ).toBeVisible();
    }
    await action.click();
    await expect(page.getByRole('dialog')).toHaveCount(0);
    expect(state.writes).toEqual([]);
  });
}

test(
  'VM Host shows connection and slot usage for a launcher-managed service',
  { tag: '@vm-ui' },
  async ({ page }, info) => {
    const state = fixture();
    const profileSocket = '/run/profile-vm/control.sock';
    const lockedReason = 'AXV_COMPUTE_SOCKET supplies this connection.';
    state.environment = {
      ...state.environment,
      platform: 'linux',
      configurationSource: 'environment',
      runtime: {
        state: 'ready',
        mode: 'external',
        readOnly: true,
        hostId: state.info.hostId,
        generation: 1,
      },
    };
    state.check = {
      configured: false,
      configurationSource: 'environment',
      lockedReason,
    };
    let releaseEnvironment!: () => void;
    state.environmentGate = new Promise<void>((resolve) => {
      releaseEnvironment = resolve;
    });
    const manager = page.getByRole('region', {
      name: 'Virtual Machines',
      exact: true,
    });
    const setup = manager.getByRole('region', { name: 'VM host', exact: true });
    try {
      await mockHost(page, state);
      await page.reload();
      await launch(page, 'machines');
      await expect(
        setup.getByRole('heading', { name: 'Connecting VM service' }),
      ).toBeVisible();
      await expect(
        setup.getByText('/run/axv-host/control.sock', { exact: true }),
      ).toHaveCount(0);
      for (const name of ['Connect', 'Check connection', 'Edit connection'])
        await expect(
          setup.getByRole('button', { name, exact: true }),
        ).toHaveCount(0);
    } finally {
      releaseEnvironment();
    }
    await expect(
      manager.getByRole('button', { name: 'New VM', exact: true }),
    ).toBeEnabled();
    await expect(
      manager.getByText('Capacity limits', { exact: true }),
    ).toBeHidden();
    await manager.getByRole('button', { name: 'VM Host', exact: true }).click();
    const connected = setup.getByRole('heading', {
      name: 'VM service connected',
    });
    await expect(connected).toBeVisible();
    await expect(setup.getByText('8 GiB', { exact: true })).toBeVisible();
    await expect(setup.getByText('100 GiB', { exact: true })).toBeVisible();
    await expect(setup.getByText('2 of 8 used', { exact: true })).toBeVisible();
    await expect(
      setup.getByText('/run/axv-host/control.sock', { exact: true }),
    ).toHaveCount(0);
    for (const name of ['Connect', 'Check connection', 'Edit connection'])
      await expect(
        setup.getByRole('button', { name, exact: true }),
      ).toHaveCount(0);
    await captureSizes(page, info, connected, 'vm-launcher-connection');

    state.check = {
      ...state.check,
      configured: true,
      defaults: { socket: profileSocket },
    };
    await refreshMachines(page);
    const diagnostics = setup.getByRole('button', { name: 'Diagnostics' });
    await diagnostics.click();
    const dialog = page.getByRole('dialog', { name: 'VM Diagnostics' });
    await expect(dialog.getByText(lockedReason, { exact: true })).toBeVisible();
    await expect(dialog.getByText(profileSocket, { exact: true })).toHaveCount(
      0,
    );
    await dialog.getByText('Environment details', { exact: true }).click();
    await expect(
      dialog.locator('dt:text-is("VMs reserving CPU and memory") + dd'),
    ).toHaveText('1');
    await dialog.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(diagnostics).toBeFocused();
    expect(state.writes).toEqual([]);
  },
);

test(
  'Inspector shows one power state and the action for current guest readiness',
  { tag: '@vm-ui' },
  async ({ page }, info) => {
    const state = fixture();
    const manager = await host(page, state);
    const tile = manager.getByRole('button', {
      name: 'Build VM lima',
      exact: true,
    });
    await expect(tile).toBeVisible();
    const actions = manager.getByRole('group', { name: 'Selected VM actions' });
    await expect(actions).toHaveCount(0);
    await tile.click();
    await expect(
      actions.getByRole('button', { name: 'Start VM', exact: true }),
    ).toBeEnabled();
    await actions.getByRole('button', { name: 'Inspect', exact: true }).click();
    const inspector = page.locator('[data-app-id="machine-inspector"]');
    await expect(inspector).toHaveAttribute(
      'aria-label',
      'Build VM — Inspector',
    );
    await expect(
      inspector.getByRole('heading', { name: 'Stopped', exact: true }),
    ).toBeVisible();
    await expect(inspector.getByText('Build VM', { exact: true })).toHaveCount(
      0,
    );
    await expect(
      inspector.getByText('Stopped', { exact: true }).filter({ visible: true }),
    ).toHaveCount(1);
    await expect(
      inspector.getByText('Configuration', { exact: true }),
    ).toHaveCount(1);
    await expect(inspector.getByText('Provider', { exact: true })).toBeHidden();
    await captureSizes(
      page,
      info,
      inspector.getByRole('button', { name: 'Start VM', exact: true }),
      'vm-inspector-stopped',
    );

    const id = await inspector.getAttribute('data-window-id');
    const vm = state.machines[0]!;
    vm.spec.name = 'Renamed VM';
    vm.status = vm.desired = 'running';
    const refresh = inspector.getByRole('button', {
      name: 'Refresh',
      exact: true,
    });
    await refresh.click();
    await expect(inspector).toHaveAttribute(
      'aria-label',
      'Renamed VM — Inspector',
    );
    await expect(inspector).toHaveAttribute('data-window-id', id!);
    await expect(
      inspector.getByText('Waiting for the guest service.', { exact: true }),
    ).toBeVisible();
    await expect(
      inspector.getByRole('button', { name: 'Open Terminal', exact: true }),
    ).toHaveCount(0);
    vm.ready = true;
    vm.guestState = 'ready';
    vm.guestCapabilities = ['terminals'];
    await refresh.click();
    const terminal = inspector.getByRole('button', {
      name: 'Open Terminal',
      exact: true,
    });
    await expect(terminal).toBeEnabled();
    await expect(
      inspector.getByText('Running', { exact: true }).filter({ visible: true }),
    ).toHaveCount(1);
    await expect(
      inspector.getByText('Guest ready', { exact: false }),
    ).toHaveCount(0);
    await captureSizes(page, info, terminal, 'vm-inspector-ready');
    await inspector.getByRole('button', { name: 'Stop', exact: true }).click();
    const stop = page.getByRole('dialog', { name: 'Stop VM?' });
    await expect(stop).toContainText(
      'Guest sessions end. Project data remains on disk.',
    );
    await stop.getByRole('button', { name: 'Cancel', exact: true }).click();
    expect(state.writes).toEqual([]);
  },
);

test('current setup checks override retained inventory after host configuration is removed', async ({
  page,
}) => {
  const state = fixture();
  const manager = await host(page, state);
  const row = manager.getByRole('button', {
    name: 'Build VM lima',
    exact: true,
  });
  await expect(row).toBeVisible();

  state.environment.configured = false;
  state.info = { configured: false, reason: 'No host configured' };
  await refreshMachines(page);
  await expect(row).toBeVisible();
  await expect(manager.getByRole('alert')).toContainText('No host configured');
  await manager.getByRole('button', { name: 'VM Host', exact: true }).click();
  await expect(
    manager.getByRole('heading', { name: 'Enable virtual machines' }),
  ).toBeVisible();
  await expect(
    manager.getByLabel('VM storage directory', { exact: true }),
  ).toHaveValue('/test/axv/compute');
  await expect(
    manager.getByRole('button', { name: 'Review', exact: true }),
  ).toBeEnabled();
  await expect(
    manager.getByText(
      'The existing configuration and storage will be preserved.',
      {
        exact: false,
      },
    ),
  ).toHaveCount(0);
  expect(state.writes).toEqual([]);
});

test('filtering a selected VM disables its actions without choosing another VM', async ({
  page,
}) => {
  const state = fixture();
  const manager = await host(page, state);
  const row = manager.getByRole('button', {
    name: 'Build VM lima',
    exact: true,
  });
  await row.click();
  await expect(
    manager.getByText('Start to use this VM; Inspect to configure it.', {
      exact: true,
    }),
  ).toHaveCount(0);
  await expect(
    manager.getByRole('button', { name: 'Start VM', exact: true }),
  ).toBeEnabled();
  const search = manager.getByLabel('Search VMs', { exact: true });
  await search.fill('Task VM');
  await expect(row).toHaveCount(0);
  await expect(
    manager.getByRole('button', { name: 'Task VM lima', exact: true }),
  ).toHaveAttribute('aria-pressed', 'false');
  for (const name of [
    'Start VM',
    'Inspect',
    'Open Terminal',
    'More VM actions',
  ]) {
    await expect(
      manager.getByRole('button', { name, exact: true }),
    ).toHaveCount(0);
  }
  await search.fill('no matching VM');
  await expect(
    manager.getByRole('heading', { name: 'No VMs match your filters' }),
  ).toBeVisible();
  await expect(
    manager.getByRole('heading', { name: 'No VMs yet' }),
  ).toHaveCount(0);
  await manager.getByRole('button', { name: 'Clear filters' }).click();
  await expect(row).toHaveAttribute('aria-pressed', 'true');
  await manager
    .getByRole('combobox', { name: 'State', exact: true })
    .selectOption('running');
  await expect(row).toHaveCount(0);
  await expect(
    manager.getByRole('button', { name: 'Inspect', exact: true }),
  ).toHaveCount(0);
  await manager
    .getByRole('combobox', { name: 'State', exact: true })
    .selectOption('all');
  await expect(row).toHaveAttribute('aria-pressed', 'true');
  expect(state.writes).toEqual([]);
});

test('Inspector opens project files for its original VM volume', async ({
  page,
}) => {
  const state = fixture();
  const machine = state.machines[1]!;
  machine.guestCapabilities?.push('project-files');
  const manager = await host(page, state);
  await manager
    .getByRole('button', { name: 'Task VM lima', exact: true })
    .click();
  await manager.getByRole('button', { name: 'Inspect', exact: true }).click();
  const inspector = page.getByRole('region', {
    name: 'Task VM — Inspector',
    exact: true,
  });
  await inspector
    .getByRole('button', { name: 'Project Files', exact: true })
    .click();
  const files = page.getByRole('region', {
    name: 'Task VM — Project Files',
    exact: true,
  });
  const open = files.getByRole('button', { name: 'Open', exact: true });
  await expect(
    files.getByText('Task VM project', { exact: true }),
  ).toBeVisible();
  await expect(open).toBeEnabled();
  await expect(files.getByLabel('Project action')).toHaveCount(0);

  const volume = state.volumes.find(
    (item) => item.id === machine.spec.volumeId,
  )!;
  volume.machineId = state.machines[0]!.id;
  await files
    .getByRole('button', { name: 'Refresh VM projects', exact: true })
    .click();
  await expect(
    files.getByText('The original VM project volume is unavailable.'),
  ).toBeVisible();
  await expect(open).toBeDisabled();
  expect(state.writes).toEqual([]);
});

test('VM Host and tabs preserve mounted volume and image drafts', async ({
  page,
  hasTouch,
}) => {
  const state = fixture();
  const manager = await host(page, state);
  const press = async (control: Locator) => {
    if (hasTouch) await control.tap();
    else await control.click();
  };
  const views = manager.getByRole('group', { name: 'VM views', exact: true });
  const imageView = views.getByRole('button', { name: 'Images', exact: true });
  const volumeView = views.getByRole('button', {
    name: 'Volumes',
    exact: true,
  });
  const environment = manager.getByRole('button', {
    name: 'VM Host',
    exact: true,
  });
  const hostStatus = manager.getByRole('heading', {
    name: 'VM service connected',
    exact: true,
  });
  await press(manager.getByRole('button', { name: 'Volumes', exact: true }));
  await expect(volumeView).toHaveAttribute('aria-pressed', 'true');
  await press(manager.getByRole('button', { name: 'New Volume' }));
  const volume = manager.getByLabel('Volume name', { exact: true });
  await volume.fill('Retained volume draft');
  await volume.evaluate((input) => {
    input.dataset.mountProbe = 'volume';
  });
  await press(manager.getByRole('button', { name: 'Images', exact: true }));
  await expect(imageView).toHaveAttribute('aria-pressed', 'true');
  await expect(volumeView).toHaveAttribute('aria-pressed', 'false');
  await expect(volume).toBeHidden();
  await expect(volume).toHaveCount(1);
  await press(manager.getByRole('button', { name: 'Add Image' }));
  const image = manager.getByLabel('Image name', { exact: true });
  await image.fill('Retained image draft');
  await image.evaluate((input) => {
    input.dataset.mountProbe = 'image';
  });
  await manager
    .getByRole('combobox', { name: 'Source', exact: true })
    .selectOption('file');
  const file = manager.getByLabel('Linux cloud image', { exact: true });
  await file.setInputFiles({
    name: 'draft.qcow2',
    mimeType: 'application/octet-stream',
    buffer: Buffer.from('Not submitted; tests retained file selection only.'),
  });
  await press(environment);
  await expect(hostStatus).toBeVisible();
  await expect(image).toBeHidden();
  await expect(image).not.toBeFocused();
  await expect(image).toHaveCount(1);
  expect(await image.evaluate((input) => !!input.closest('[inert]'))).toBe(
    true,
  );
  await expect(environment).toHaveAttribute('aria-pressed', 'true');
  await expect(views.getByRole('button', { pressed: true })).toHaveCount(0);
  await press(environment);
  await expect(image).toBeVisible();
  await expect(imageView).toHaveAttribute('aria-pressed', 'true');
  await expect(environment).toHaveAttribute('aria-pressed', 'false');
  await press(environment);
  await press(manager.getByRole('button', { name: 'Images', exact: true }));
  await expect(hostStatus).toHaveCount(0);
  await expect(
    manager.getByRole('button', { name: 'VM Host', exact: true }),
  ).toHaveAttribute('aria-pressed', 'false');
  await expect(image).toHaveValue('Retained image draft');
  await expect(image).toHaveAttribute('data-mount-probe', 'image');
  await expect(file).toHaveValue(/draft\.qcow2$/);
  await press(manager.getByRole('button', { name: 'Volumes', exact: true }));
  await expect(volume).toHaveValue('Retained volume draft');
  await expect(volume).toHaveAttribute('data-mount-probe', 'volume');
  await expect(image).toBeHidden();
  expect(state.writes).toEqual([]);
});

test('an uncertain start can be resolved from the Inspector after inventory advances', async ({
  page,
}, info) => {
  const state = fixture();
  const original = structuredClone(state.machines[0]!);
  const operation: Operation = {
    id: 'operation-uncertain-start',
    machineId: original.id,
    action: 'start',
    status: 'succeeded',
    createdAt: '2026-10-16T00:01:00Z',
  };
  const attempts: { requestId: string; revision: number; action: string }[] =
    [];
  const manager = await host(page, state);
  await page.route(
    `**/api/compute/machines/${original.id}/actions`,
    async (route) => {
      expect(route.request().method()).toBe('POST');
      attempts.push(
        route.request().postDataJSON() as (typeof attempts)[number],
      );
      if (attempts.length === 1) {
        state.machines[0] = {
          ...original,
          revision: original.revision + 1,
          desired: 'running',
          status: 'running',
          ready: false,
          guestState: 'unavailable',
          guestCapabilities: [],
          reserved: true,
        };
        state.operations = [operation];
        await route.fulfill({
          status: 503,
          json: {
            code: 'host_unavailable',
            error: 'The start response was interrupted.',
          },
        });
        return;
      }
      await route.fulfill({
        status: 202,
        json: { machine: state.machines[0], operation },
      });
    },
  );
  const row = manager.getByRole('button', {
    name: 'Build VM lima',
    exact: true,
  });
  await row.click();
  await manager.getByRole('button', { name: 'Start VM', exact: true }).click();
  await expect(
    manager.getByRole('button', { name: 'Retry Original Request' }),
  ).toBeVisible();
  await refreshMachines(page);
  await expect(
    manager.getByRole('button', { name: 'Build VM lima', exact: true }),
  ).toContainText('Running');
  await expect(
    manager.getByRole('button', { name: 'Start VM', exact: true }),
  ).toHaveCount(0);
  await expect(
    manager.getByRole('button', { name: 'Stop', exact: true }),
  ).toBeDisabled();

  await manager.getByRole('button', { name: 'Inspect', exact: true }).click();
  const inspector = page.locator('[data-app-id="machine-inspector"]');
  await expect(
    inspector.getByRole('button', { name: 'Stop', exact: true }),
  ).toBeDisabled();
  await inspector
    .locator('summary')
    .filter({ hasText: /^Configuration$/ })
    .click();
  const name = inspector.getByLabel('VM name', { exact: true });
  await name.fill('Draft while start is unresolved');
  await expect(inspector.getByRole('button', { name: 'Apply' })).toBeDisabled();
  await inspector
    .getByRole('button', { name: 'Retry Original Request' })
    .click();
  await expect(
    inspector.getByRole('button', { name: 'Retry Original Request' }),
  ).toHaveCount(0);
  await expect(
    inspector.getByRole('button', { name: 'Stop', exact: true }),
  ).toBeEnabled();
  await expect(name).toHaveValue('Draft while start is unresolved');
  expect(attempts).toHaveLength(2);
  expect(attempts[0]).toEqual({
    action: 'start',
    revision: original.revision,
    requestId: expect.any(String),
  });
  expect(attempts[1]).toEqual(attempts[0]);
  await activate(page, manager);
  await expect(
    manager.getByRole('button', { name: 'Retry Original Request' }),
  ).toHaveCount(0);
  await expect(
    manager.getByRole('button', { name: 'Stop', exact: true }),
  ).toBeEnabled();
  expect(state.writes).toEqual([]);

  const stopOperation: Operation = {
    id: 'operation-activity-stop',
    machineId: original.id,
    action: 'stop',
    status: 'queued',
    phase: 'waiting-for-shutdown',
    createdAt: '2026-10-16T00:02:00Z',
  };
  await page.route(
    `**/api/compute/machines/${original.id}/actions`,
    async (route) => {
      expect(route.request().postDataJSON()).toMatchObject({ action: 'stop' });
      state.operations = [stopOperation, operation];
      await route.fulfill({
        status: 202,
        json: { machine: state.machines[0], operation: stopOperation },
      });
    },
  );
  await manager.getByRole('button', { name: 'Stop', exact: true }).click();
  await page
    .getByRole('dialog', { name: 'Stop VM?' })
    .getByRole('button', { name: 'Stop VM', exact: true })
    .click();
  const feedback = manager.locator('.machines-operation-feedback').filter({
    has: page.getByRole('button', { name: 'Dismiss', exact: true }),
  });
  const viewActivity = feedback.getByRole('button', {
    name: 'View Activity',
    exact: true,
  });
  await expect(feedback).toContainText('Stop · Queued · Waiting for shutdown');
  await expect(
    manager.getByText('Stopping…', { exact: true }).first(),
  ).toBeVisible();
  await expect(
    manager.getByRole('button', { name: 'Stop', exact: true }),
  ).toHaveCount(0);
  await viewActivity.click();
  await expect(
    manager.getByRole('button', { name: 'Activity', exact: true }),
  ).toBeFocused();
  const selectedOperation = manager.locator('tr[data-selected="true"]');
  await expect(selectedOperation).toHaveAttribute(
    'data-operation-id',
    stopOperation.id,
  );
  await expect(selectedOperation).toContainText(original.spec.name);
  await expect(selectedOperation).toContainText('Queued');
  await expect(viewActivity).toHaveCount(0);

  state.operations[0] = {
    ...stopOperation,
    status: 'failed',
    phase: 'shutdown',
    error: 'Guest shutdown failed; the VM is still running.',
  };
  await refreshMachines(page);
  await expect(selectedOperation).toContainText('Failed');
  await expect(selectedOperation).toContainText(
    'Guest shutdown failed; the VM is still running.',
  );
  await expect(feedback).toContainText('Stop · Failed · Shutdown');
  await expect(
    feedback.getByRole('button', { name: 'Dismiss', exact: true }),
  ).toBeVisible();
  await captureSizes(page, info, feedback, 'activity-selected-operation');
  await manager.getByRole('button', { name: 'VMs', exact: true }).click();
  await expect(viewActivity).toBeVisible();
  await expect(feedback).toContainText(
    'Guest shutdown failed; the VM is still running.',
  );
  await viewActivity.click();
  await expect(
    manager.getByRole('button', { name: 'Activity', exact: true }),
  ).toBeFocused();
  await expect(selectedOperation).toHaveAttribute(
    'data-operation-id',
    stopOperation.id,
  );
  await expect(viewActivity).toHaveCount(0);
  await manager.getByRole('button', { name: 'VM Host', exact: true }).click();
  await expect(viewActivity).toBeVisible();
  await viewActivity.click();
  await expect(
    manager.getByRole('button', { name: 'VM Host', exact: true }),
  ).toHaveAttribute('aria-pressed', 'false');
  await expect(
    manager.getByRole('button', { name: 'Activity', exact: true }),
  ).toBeFocused();
  await expect(selectedOperation).toBeVisible();
  await expect(selectedOperation).toHaveAttribute(
    'data-operation-id',
    stopOperation.id,
  );
  await expect(viewActivity).toHaveCount(0);
  await feedback.getByRole('button', { name: 'Dismiss', exact: true }).click();
  await expect(feedback).toHaveCount(0);
  await expect(selectedOperation).toContainText(
    'Guest shutdown failed; the VM is still running.',
  );
});

test(
  'View Activity reveals the exact failure in a long history',
  { tag: '@vm-ui' },
  async ({ page, hasTouch }, info) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const state = fixture();
    const failed: Operation = {
      id: 'older-start-failure',
      action: 'start',
      machineId: state.machines[0]!.id,
      status: 'failed',
      error: 'The previous boot did not complete.',
      createdAt: '2026-10-16T00:00:00Z',
    };
    state.operations = [
      ...Array.from({ length: 36 }, (_, index): Operation => ({
        id: `recent-stop-${index}`,
        action: 'stop',
        machineId: state.machines[1]!.id,
        status: 'succeeded',
        createdAt: '2026-10-16T00:01:00Z',
      })),
      failed,
    ];
    const manager = await host(page, state);
    await manager.getByRole('button', { name: 'Build VM lima' }).click();
    await manager.getByRole('button', { name: 'View Activity' }).click();

    const history = manager.getByRole('table');
    const row = history.getByRole('row', { name: /Start.*Build VM.*Failed/ });
    const activity = manager.getByRole('button', {
      name: 'Activity',
      exact: true,
    });
    await expect(row).toHaveAttribute('aria-current', 'true');
    await expect(row).toBeInViewport({ ratio: 0.9 });
    await expect(activity).toBeFocused();
    const path = info.outputPath('activity-failure-390.png');
    await page.screenshot({ path });
    await info.attach('activity-failure-390', {
      path,
      contentType: 'image/png',
    });

    const scrollport = manager.locator('.machines-scroll');
    await scrollport.evaluate((element) => (element.scrollTop = 0));
    state.operations[0]!.phase = 'observed-on-refresh';
    await refreshMachines(page, hasTouch);
    await expect(history.getByText('Observed on refresh')).toBeVisible();
    await expect(row).not.toBeInViewport();

    await manager.getByRole('button', { name: 'VMs', exact: true }).click();
    await manager.getByRole('button', { name: 'Inspect', exact: true }).click();
    const inspector = page.getByRole('region', {
      name: 'Build VM — Inspector',
    });
    await inspector.getByRole('button', { name: 'View Activity' }).click();
    await expect(row).toHaveAttribute('aria-current', 'true');
    await expect(row).toBeInViewport({ ratio: 0.9 });
    await expect(activity).toBeFocused();
    expect(state.writes).toEqual([]);
  },
);

test(
  'VM actions and task expiry stay visible when selecting at the bottom of a long inventory',
  { tag: '@vm-ui' },
  async ({ page, hasTouch }, info) => {
    await page.setViewportSize({ width: 320, height: 568 });
    const state = fixture();
    const regular = state.machines[0]!;
    const expiry = new Date(Date.now() + 15 * 60 * 1000).toISOString();
    const stopped: Machine = {
      ...regular,
      id: 'vm-last-stopped',
      spec: {
        ...regular.spec,
        name: 'Final stopped VM',
        volumeId: 'volume-last-stopped',
      },
    };
    state.volumes.push({
      ...state.volumes[0]!,
      id: stopped.spec.volumeId!,
      name: 'Final stopped project',
      machineId: stopped.id,
    });
    const task: Machine = {
      ...regular,
      id: 'vm-last-task',
      spec: { ...regular.spec, name: 'Final task', kind: 'task' },
      desired: 'running',
      status: 'running',
      ready: true,
      guestState: 'ready',
      guestCapabilities: ['terminals'],
      expiresAt: expiry,
    };
    state.machines.push(
      ...Array.from({ length: 10 }, (_, index) => ({
        ...regular,
        id: `vm-list-${index}`,
        spec: { ...regular.spec, name: `Review VM ${index + 1}` },
      })),
      stopped,
      task,
    );
    const manager = await host(page, state);
    const actions = manager.getByRole('group', { name: 'Selected VM actions' });
    const scrollport = manager.locator('.machines-scroll');
    const stoppedTile = manager.getByRole('button', {
      name: 'Final stopped VM lima',
    });
    await stoppedTile.click();
    await expect(stoppedTile).toHaveAttribute('aria-pressed', 'true');
    await expect
      .poll(() => scrollport.evaluate((element) => element.scrollTop))
      .toBeGreaterThan(100);
    const start = actions.getByRole('button', {
      name: 'Start VM',
      exact: true,
    });
    await expect(start).toBeEnabled();
    await expect(start).toBeInViewport();
    await expect(actions.locator('time')).toHaveCount(0);

    await manager.getByRole('button', { name: 'Final task lima' }).click();
    await expect(
      actions.getByRole('button', { name: 'Open Terminal' }),
    ).toBeInViewport();
    const deadline = actions.locator('time');
    await expect(deadline).toHaveAttribute('datetime', expiry);
    await expect(deadline).toBeInViewport();
    const path = info.outputPath('vm-selection-and-task-320.png');
    await page.screenshot({ path });
    await info.attach('vm-selection-and-task-320', {
      path,
      contentType: 'image/png',
    });

    const longName =
      'Final task for a shared project with a long workspace name';
    task.spec.name = longName;
    await refreshMachines(page, hasTouch);
    await expect(actions.getByText(longName, { exact: true })).toHaveAttribute(
      'title',
      longName,
    );
    const layout = await actions
      .locator('.machines-selection-identity')
      .evaluate((row) => {
        const name = row.querySelector('strong')!;
        const nameBounds = name.getBoundingClientRect();
        const timeBounds = row.querySelector('time')!.getBoundingClientRect();
        return {
          sameLine:
            nameBounds.top < timeBounds.bottom &&
            timeBounds.top < nameBounds.bottom,
          nameTruncates: name.scrollWidth > name.clientWidth,
          timeFits: timeBounds.right <= row.getBoundingClientRect().right + 1,
        };
      });
    expect(layout).toEqual({
      sameLine: true,
      nameTruncates: true,
      timeFits: true,
    });
    await expect(deadline).toBeInViewport();

    await actions.getByRole('button', { name: 'Inspect', exact: true }).click();
    await expect(
      page
        .getByRole('region', { name: `${longName} — Inspector` })
        .locator('time'),
    ).toHaveAttribute('datetime', expiry);
    expect(state.writes).toEqual([]);
  },
);

test('inspectors keep their original host when another host reuses the VM ID', async ({
  page,
}) => {
  const state = fixture();
  const manager = await host(page, state);
  const original = state.machines[1];
  await manager
    .getByRole('button', { name: 'Task VM lima', exact: true })
    .click();
  await manager.getByRole('button', { name: 'Inspect', exact: true }).click();
  const oldInspector = page
    .locator('[data-app-id="machine-inspector"]')
    .first();
  await expect.poll(() => state.guestReads.length).toBeGreaterThan(0);
  await oldInspector
    .locator('summary')
    .filter({ hasText: /^Configuration$/ })
    .click();
  const originalDraft = oldInspector.getByLabel('VM name', { exact: true });
  await originalDraft.fill('Original host draft');
  let mutations = 0;
  await page.route(
    `**/api/compute/machines/${original.id}/actions`,
    async (route) => {
      mutations++;
      expect(route.request().headers()['x-axv-host-id']).toBe(original.hostId);
      await route.fulfill({
        status: 503,
        json: { error: 'Response interrupted', code: 'host_unavailable' },
      });
    },
  );
  await oldInspector.getByRole('button', { name: 'Stop', exact: true }).click();
  await page
    .getByRole('dialog', { name: 'Stop VM?' })
    .getByRole('button', { name: 'Stop VM', exact: true })
    .click();
  await expect(
    oldInspector.getByRole('button', { name: 'Retry Original Request' }),
  ).toBeEnabled();
  state.info.hostId = 'replacement-host';
  state.machines = state.machines.map((machine) => ({
    ...machine,
    hostId: 'replacement-host',
    spec: { ...machine.spec, name: `Replacement ${machine.spec.name}` },
  }));
  await oldInspector
    .getByRole('button', { name: 'Refresh', exact: true })
    .click();
  await expect(
    oldInspector.getByText('Original host unavailable.', { exact: false }),
  ).toBeVisible();
  await expect(
    oldInspector.getByRole('button', { name: 'Retry Original Request' }),
  ).toBeDisabled();
  await expect(
    oldInspector.getByRole('button', { name: 'Stop', exact: true }),
  ).toBeDisabled();
  await expect(originalDraft).toHaveValue('Original host draft');
  expect(state.guestReads.every((hostId) => hostId === original.hostId)).toBe(
    true,
  );
  expect(mutations).toBe(1);
  await activate(page, manager);
  await manager
    .getByRole('button', { name: 'Replacement Task VM lima', exact: true })
    .click();
  await manager.getByRole('button', { name: 'Inspect', exact: true }).click();
  const inspectors = page.locator('[data-app-id="machine-inspector"]');
  await expect(inspectors).toHaveCount(2);
  const replacement = page.getByRole('region', {
    name: 'Replacement Task VM — Inspector',
    exact: true,
  });
  await expect(
    replacement.getByRole('button', { name: 'Retry Original Request' }),
  ).toHaveCount(0);
  await expect(
    replacement.getByRole('button', { name: 'Stop', exact: true }),
  ).toBeEnabled();
  expect(mutations).toBe(1);
});

test('legacy inspectors without a recorded host remain unavailable after restore', async ({
  page,
  request,
}) => {
  const state = fixture();
  const manager = await host(page, state);
  await manager
    .getByRole('button', { name: 'Build VM lima', exact: true })
    .click();
  await manager.getByRole('button', { name: 'Inspect', exact: true }).click();
  await expect
    .poll(
      async () =>
        (await persistedLayout(page, request))?.windows.filter(
          (window: { appId: string }) => window.appId === 'machine-inspector',
        ).length,
    )
    .toBe(1);
  const layout = await persistedLayout(page, request);
  const inspector = layout.windows.find(
    (window: { appId: string }) => window.appId === 'machine-inspector',
  );
  delete inspector.params.hostId;
  const clientId = await page.evaluate(() =>
    localStorage.getItem('nextstep-browser-id'),
  );
  const response = await request.put('/api/layout', {
    data: { clientId, layout },
  });
  expect(response.ok()).toBe(true);
  await page.reload();
  const restored = page.locator('[data-app-id="machine-inspector"]');
  await expect(
    restored.getByRole('heading', { name: 'VM identity unavailable' }),
  ).toBeVisible();
  await expect(
    restored.getByRole('button', { name: 'Start VM', exact: true }),
  ).toHaveCount(0);
  expect(state.writes).toEqual([]);
  expect(state.guestReads).toEqual([]);
});

test('the More menu keeps destructive actions named and cancellation focused', async ({
  page,
  hasTouch,
}) => {
  const state = fixture();
  const manager = await host(page, state);
  await manager
    .getByRole('button', { name: 'Task VM lima', exact: true })
    .click();
  const more = manager.getByRole('button', { name: 'More VM actions' });
  if (hasTouch) await more.tap();
  else {
    await more.focus();
    await more.press('Enter');
  }
  await menuItem(page, 'Delete').click();
  const dialog = page.getByRole('dialog', { name: 'Delete VM?' });
  await expect(dialog).toContainText(
    'Delete “Task VM” and its root disk? Its project volume will be retained.',
  );
  await expect(
    dialog.getByRole('button', { name: 'Cancel', exact: true }),
  ).toBeFocused();
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  expect(state.writes).toEqual([]);
  await more.click();
  await menuItem(page, 'Force Stop').click();
  const force = page.getByRole('dialog', { name: 'Force Stop?' });
  await expect(force).toContainText('Power off “Task VM” immediately?');
  await force.getByRole('button', { name: 'Cancel', exact: true }).click();
  expect(state.writes).toEqual([]);
});
