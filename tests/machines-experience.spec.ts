import { preparationCheckFixture } from './machines-preparation-fixture';
import { creationDefaultsFixture } from './machines-creation-fixture';
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
  expect,
  launch,
  menuItem,
  persistedLayout,
  test,
} from './helpers';

interface HostFixture {
  environment: ComputeEnvironment;
  info: HostInfo;
  machines: Machine[];
  volumes: Volume[];
  images: ImageInfo[];
  operations: Operation[];
  offline: boolean;
  writes: string[];
  guestReads: string[];
  gate?: Promise<void>;
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

async function host(page: Page, state: HostFixture) {
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
      }),
      '/environment': state.environment,
      '/info': state.info,
      '/machines': { machines: state.machines },
      '/volumes': { volumes: state.volumes },
      '/images': { images: state.images },
      '/operations': { operations: state.operations },
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
  await launch(page, 'machines');
  return page.locator('[data-window-id="machines:main"]');
}

async function refresh(manager: Locator) {
  const button = manager.getByRole('button', {
    name: 'Refresh',
    exact: true,
  });
  await expect(button).toBeEnabled();
  await button.click();
  await expect(button).toBeEnabled();
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
  await refresh(manager);
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
    manager.getByText(
      'Connect to a local Firecracker/KVM service set up by an administrator.',
      { exact: false },
    ),
  ).toBeVisible();
  await expect(
    manager.getByLabel('Local service configuration', { exact: true }),
  ).toHaveValue('/etc/axv/host.json');
  await expect(
    manager.getByRole('button', { name: 'Review', exact: true }),
  ).toBeEnabled();

  await captureSizes(page, info, heading, 'vm-linux-setup');

  state.environment = {
    ...state.environment,
    platform: 'windows',
  };
  await refresh(manager);
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

test('empty hosts distinguish unavailable providers, missing images and configured images', async ({
  page,
}) => {
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
    manager.getByText(
      'The VM provider needs attention before you can create a VM.',
    ),
  ).toBeVisible();
  await expect(newVM).toBeDisabled();
  await manager.getByRole('button', { name: 'View environment' }).click();
  await expect(
    manager.getByText('Install the required Lima release.', {
      exact: false,
    }),
  ).toBeVisible();
  await manager.getByRole('button', { name: 'VMs', exact: true }).click();

  state.info.providers![0]!.available = true;
  state.info.providers![0]!.reason = undefined;
  await refresh(manager);
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
  await manager.getByRole('button', { name: 'Activity', exact: true }).click();
  await expect(
    manager.getByText('No activity yet.', { exact: false }),
  ).toBeVisible();

  state.images = [image];
  state.info.images = [image];
  await refresh(manager);
  await manager.getByRole('button', { name: 'VMs', exact: true }).click();
  await expect(
    manager.getByRole('heading', { name: 'Create your first VM' }),
  ).toBeVisible();
  await expect(newVM).toBeEnabled();
  await newVM.click();
  const create = page.locator('[data-window-id="machine-create:main"]');
  await expect(
    create.getByRole('combobox', { name: 'Image', exact: true }),
  ).toHaveValue(image.id);
  await expect(
    create.getByRole('button', { name: 'Create VM', exact: true }),
  ).toBeEnabled();
  expect(state.writes).toEqual([]);
});

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
  const start = manager.getByRole('button', { name: 'Start', exact: true });
  await expect(start).toBeEnabled();
  await captureSizes(page, info, row, 'vm-selected-inventory');
  await manager.getByRole('button', { name: 'Images', exact: true }).click();
  await manager.getByRole('button', { name: 'Add Image' }).click();
  const draft = manager.getByLabel('Image name', { exact: true });
  await draft.fill('Offline image draft');
  await manager.getByRole('button', { name: 'VMs', exact: true }).click();

  state.offline = true;
  await refresh(manager);
  await expect(manager.getByRole('alert')).toContainText(
    'VM service unavailable.',
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
  ).toBeDisabled();
  await expect(
    manager.getByRole('button', { name: 'Inspect', exact: true }),
  ).toBeEnabled();
  await expect(
    manager.getByRole('heading', { name: 'Create your first VM' }),
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
  await refresh(manager);
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
      ).toContainText('deleting');
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
  await refresh(manager);
  await expect(row).toBeVisible();
  await expect(manager.getByRole('alert')).toContainText('No host configured');
  await manager
    .getByRole('button', { name: 'Environment', exact: true })
    .click();
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
    manager.getByRole('button', { name: 'Start', exact: true }),
  ).toBeEnabled();
  const search = manager.getByLabel('Search VMs', { exact: true });
  await search.fill('Task VM');
  await expect(row).toHaveCount(0);
  await expect(
    manager.getByRole('button', { name: 'Task VM lima', exact: true }),
  ).toHaveAttribute('aria-pressed', 'false');
  for (const name of ['Start', 'Inspect', 'New Terminal', 'More VM actions']) {
    await expect(
      manager.getByRole('button', { name, exact: true }),
    ).toBeDisabled();
  }
  await search.fill('no matching VM');
  await expect(
    manager.getByRole('heading', { name: 'No VMs match your filters' }),
  ).toBeVisible();
  await expect(
    manager.getByRole('heading', { name: 'Create your first VM' }),
  ).toHaveCount(0);
  await manager.getByRole('button', { name: 'Clear filters' }).click();
  await expect(row).toHaveAttribute('aria-pressed', 'true');
  await manager
    .getByRole('combobox', { name: 'State', exact: true })
    .selectOption('running');
  await expect(row).toHaveCount(0);
  await expect(
    manager.getByRole('button', { name: 'Inspect', exact: true }),
  ).toBeDisabled();
  await manager
    .getByRole('combobox', { name: 'State', exact: true })
    .selectOption('all');
  await expect(row).toHaveAttribute('aria-pressed', 'true');
  expect(state.writes).toEqual([]);
});

test('Environment and tabs preserve mounted volume and image drafts', async ({
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
    name: 'Environment',
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
  await expect(
    manager.getByRole('heading', { name: 'VM environment' }),
  ).toBeVisible();
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
  await expect(
    manager.getByRole('heading', { name: 'VM environment' }),
  ).toHaveCount(0);
  await expect(
    manager.getByRole('button', { name: 'Environment', exact: true }),
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
  await manager.getByRole('button', { name: 'Start', exact: true }).click();
  await expect(
    manager.getByRole('button', { name: 'Retry Original Request' }),
  ).toBeVisible();
  await refresh(manager);
  await expect(
    manager.getByRole('button', { name: 'Build VM lima', exact: true }),
  ).toContainText('Running');
  await expect(
    manager.getByRole('button', { name: 'Start', exact: true }),
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
  await viewActivity.click();
  await expect(
    manager.getByRole('button', { name: 'Activity', exact: true }),
  ).toBeFocused();
  const selectedOperation = manager.locator('tr[data-selected="true"]');
  await expect(selectedOperation).toContainText(stopOperation.id);
  await expect(selectedOperation).toContainText(original.spec.name);
  await expect(selectedOperation).toContainText('Queued');
  await expect(viewActivity).toHaveCount(0);

  state.operations[0] = {
    ...stopOperation,
    status: 'failed',
    phase: 'shutdown',
    error: 'Guest shutdown failed; the VM is still running.',
  };
  await refresh(manager);
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
  await viewActivity.click();
  await expect(
    manager.getByRole('button', { name: 'Activity', exact: true }),
  ).toBeFocused();
  await expect(selectedOperation).toContainText(stopOperation.id);
  await expect(viewActivity).toHaveCount(0);
  await manager
    .getByRole('button', { name: 'Environment', exact: true })
    .click();
  await expect(viewActivity).toBeVisible();
  await viewActivity.click();
  await expect(
    manager.getByRole('button', { name: 'Environment', exact: true }),
  ).toHaveAttribute('aria-pressed', 'false');
  await expect(
    manager.getByRole('button', { name: 'Activity', exact: true }),
  ).toBeFocused();
  await expect(selectedOperation).toBeVisible();
  await expect(selectedOperation).toContainText(stopOperation.id);
  await expect(viewActivity).toHaveCount(0);
  await feedback.getByRole('button', { name: 'Dismiss', exact: true }).click();
  await expect(feedback).toHaveCount(0);
  await expect(selectedOperation).toContainText(
    'Guest shutdown failed; the VM is still running.',
  );
});

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
  const replacement = inspectors.filter({
    has: page.getByRole('heading', {
      name: 'Replacement Task VM',
      exact: true,
    }),
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
    restored.getByRole('button', { name: 'Start', exact: true }),
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
