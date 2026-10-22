import { preparationCheckFixture } from './machines-preparation-fixture';
import { creationDefaultsFixture } from './machines-creation-fixture';
import type { Page, Route } from '@playwright/test';
import type {
  HostInfo,
  ImageInfo,
  Machine,
  MachineSpec,
  Operation,
  Volume,
} from '../apps/dashboard/src/features/machines/api';
import { actionMenu, expect, launch, test } from './helpers';

function fixture() {
  const machine: Machine = {
    id: 'vm-form-review',
    hostId: 'host-form-review',
    revision: 1,
    spec: {
      name: 'Form VM',
      provider: 'lima',
      imageId: 'linux-one',
      kind: 'development',
      resources: { cpus: 2, memoryMiB: 1024, diskGiB: 8 },
      projectGiB: 4,
      retainVolume: true,
      volumeId: 'volume-form-review',
    },
    desired: 'stopped',
    status: 'stopped',
    ready: false,
    guestState: 'unavailable',
    guestCapabilities: [],
    reserved: false,
    createdAt: '2026-10-17T00:00:00Z',
    updatedAt: '2026-10-17T00:00:00Z',
  };
  const volume: Volume = {
    id: 'volume-form-review',
    name: 'Form project',
    provider: 'lima',
    revision: 1,
    sizeGiB: 4,
    retain: true,
    status: 'ready',
    machineId: machine.id,
  };
  const images: ImageInfo[] = [
    {
      id: 'linux-one',
      name: 'Linux One',
      provider: 'lima',
      arch: 'arm64',
      status: 'configured',
      minRootGiB: 4,
    },
    {
      id: 'linux-two',
      name: 'Linux Two',
      provider: 'lima',
      arch: 'arm64',
      status: 'configured',
      minRootGiB: 6,
    },
  ];
  return {
    hostId: 'host-form-review',
    generation: 1,
    defaults: creationDefaultsFixture('linux-one'),
    machines: [machine],
    volumes: [volume],
    images,
    operations: [] as Operation[],
    mutations: [] as {
      path: string;
      method: string;
      body: { revision: number; spec: MachineSpec; requestId: string };
    }[],
  };
}

async function host(
  page: Page,
  state: ReturnType<typeof fixture>,
  mutate?: (route: Route, path: string) => Promise<void>,
) {
  await page.route('**/api/compute/**', async (route) => {
    const path = new URL(route.request().url()).pathname.replace(
      '/api/compute',
      '',
    );
    const method = route.request().method();
    if (method !== 'GET') {
      state.mutations.push({
        path,
        method,
        body: route.request().postDataJSON(),
      });
      if (mutate) await mutate(route, path);
      else
        await route.fulfill({
          status: 409,
          json: {
            code: 'unexpected_mutation',
            error:
              'This form must not submit without an explicit valid selection.',
          },
        });
      return;
    }
    let value: unknown;
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
        runtime: {
          state: 'ready',
          mode: 'local',
          generation: state.generation,
          hostId: state.hostId,
        },
      };
    else if (path === '/info')
      value = {
        configured: true,
        defaults: state.defaults,
        hostId: state.hostId,
        hostArch: 'arm64',
        capacity: {
          cpus: 8,
          memoryMiB: 8192,
          diskGiB: 80,
          machines: 8,
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
            persistentVolumes: true,
            snapshots: false,
            guestExecution: true,
          },
        ],
        images: state.images,
      } satisfies HostInfo;
    else if (path === '/machines') value = { machines: state.machines };
    else if (path === '/volumes') value = { volumes: state.volumes };
    else if (path === '/operations') value = { operations: state.operations };
    else if (path === '/images') value = { images: state.images };
    else if (path.endsWith('/guest/terminals')) value = { terminals: [] };
    else throw new Error(`Unexpected compute request ${method} ${path}`);
    await route.fulfill({ json: value });
  });
  await launch(page, 'machines');
  const manager = page.locator('[data-window-id="machines:main"]');
  await expect(
    manager.getByRole('button', { name: 'Form VM lima', exact: true }),
  ).toBeVisible();
  return manager;
}

test('own configuration completion preserves later typing and advances the next save revision', async ({
  page,
}) => {
  const state = fixture();
  const manager = await host(page, state, async (route, path) => {
    expect(path).toBe(`/machines/${state.machines[0].id}`);
    expect(route.request().method()).toBe('PATCH');
    const body = route.request().postDataJSON() as {
      revision: number;
      spec: MachineSpec;
    };
    expect(body.revision).toBe(state.machines[0].revision);
    const operation: Operation = {
      id: `configure-${state.mutations.length}`,
      machineId: state.machines[0].id,
      action: 'configure',
      status: 'queued',
      createdAt: '2026-10-17T00:00:01Z',
    };
    state.machines[0] = {
      ...state.machines[0],
      revision: body.revision + 1,
      pendingSpec: body.spec,
    };
    state.operations = [operation];
    await route.fulfill({
      status: 202,
      json: { machine: state.machines[0], operation },
    });
  });
  await manager
    .getByRole('button', { name: 'Form VM lima', exact: true })
    .click();
  await manager.getByRole('button', { name: 'Inspect', exact: true }).click();
  const inspector = page.locator('[data-app-id="machine-inspector"]');
  await inspector
    .locator('summary')
    .filter({ hasText: /^Configuration$/ })
    .click();
  const name = inspector.getByLabel('VM name', { exact: true });
  const apply = inspector.getByRole('button', {
    name: 'Apply',
    exact: true,
  });
  await name.fill('Accepted VM name');
  await apply.click();
  await expect.poll(() => state.mutations.length).toBe(1);
  await expect(
    inspector.getByText(
      'Wait for the current VM or volume operation to finish before applying changes.',
      { exact: false },
    ),
  ).toBeVisible();
  await expect(name).toBeEnabled();
  await name.fill('Typed while configuration runs');
  await expect(apply).toBeDisabled();

  state.machines[0] = {
    ...state.machines[0],
    spec: state.machines[0].pendingSpec!,
    pendingSpec: undefined,
    revision: 3,
  };
  state.operations[0] = { ...state.operations[0], status: 'succeeded' };
  await inspector.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(name).toHaveValue('Typed while configuration runs');
  await expect(apply).toBeEnabled();
  await expect(
    inspector.getByText('The VM changed since this draft was loaded.', {
      exact: false,
    }),
  ).toHaveCount(0);
  await apply.click();
  await expect.poll(() => state.mutations.length).toBe(2);
  expect(state.mutations[1].body.revision).toBe(3);
  expect(state.mutations[1].body.spec.name).toBe(
    'Typed while configuration runs',
  );
  expect(state.mutations[1].body.requestId).not.toBe(
    state.mutations[0].body.requestId,
  );
});

test('removing the selected creation image requires a new explicit choice', async ({
  page,
}) => {
  const state = fixture();
  const manager = await host(page, state);
  await manager.getByRole('button', { name: 'New VM', exact: true }).click();
  const create = page.locator('[data-app-id="machine-create"]');
  const image = create.getByRole('combobox', { name: 'Image', exact: true });
  await expect(image).toHaveValue('linux-one');
  await image.selectOption('linux-two');
  state.images = state.images.filter((item) => item.id !== 'linux-two');
  await create.getByRole('button', { name: 'Check Host', exact: true }).click();
  await expect(
    create.getByText(
      'The selected image is no longer available. Choose an image explicitly.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(image).toHaveValue('linux-two');
  await expect(
    create.getByRole('button', { name: 'Create', exact: true }),
  ).toBeDisabled();
  expect(state.mutations).toHaveLength(0);
  await image.selectOption('linux-one');
  await expect(
    create.getByRole('button', { name: 'Create', exact: true }),
  ).toBeEnabled();
});

test('creation shares host defaults and preserves drafts while validating name claims', async ({
  page,
}, info) => {
  const state = fixture();
  state.defaults.spec.resources.cpus = 3;
  state.defaults.spec.resources.memoryMiB = 2048;
  state.images[0].minRootGiB = 12;
  state.machines[0].pendingSpec = {
    ...state.machines[0].spec,
    name: 'Reserved name',
  };
  const manager = await host(page, state, async (route) => {
    await route.fulfill({
      status: 409,
      json: {
        code: 'name_conflict',
        error: 'Another VM claimed this name before the request was accepted.',
      },
    });
  });
  await manager.getByRole('button', { name: 'New VM', exact: true }).click();
  const create = page.locator('[data-app-id="machine-create"]');
  const name = create.getByLabel('Name', { exact: true });
  const submit = create.getByRole('button', { name: 'Create', exact: true });
  await expect(name).toHaveValue('my-vm');
  await expect(create.getByLabel('CPUs', { exact: true })).toHaveValue('3');
  await expect(create.getByLabel('Memory (MiB)', { exact: true })).toHaveValue(
    '2048',
  );
  await expect(
    create.getByLabel('Root disk (GiB)', { exact: true }),
  ).toHaveValue('12');
  await expect(create.getByLabel('Start after creation')).toBeChecked();
  for (const claimed of [
    ' Form VM ',
    'Reserved name',
    `vm-${'a'.repeat(32)}`,
  ]) {
    await name.fill(claimed);
    await expect(submit).toBeDisabled();
  }
  await name.fill('form vm');
  await expect(submit).toBeEnabled();
  state.defaults.spec.name = 'A later host default';
  state.defaults.spec.resources.cpus = 6;
  await create.getByRole('button', { name: 'Check Host', exact: true }).click();
  await expect(name).toHaveValue('form vm');
  await expect(create.getByLabel('CPUs', { exact: true })).toHaveValue('3');
  await name.fill('  My own VM  ');
  await submit.click();
  await expect(create.getByRole('alert')).toContainText('Another VM claimed');
  expect(state.mutations).toHaveLength(1);
  expect(state.mutations[0].body.spec.name).toBe('My own VM');
  expect(state.mutations[0].body.spec.resources).toEqual({
    cpus: 3,
    memoryMiB: 2048,
    diskGiB: 12,
  });
  expect(state.mutations[0].body).toMatchObject({ start: true });
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
    await create.getByRole('alert').scrollIntoViewIfNeeded();
    await expect(create.getByRole('alert')).toBeVisible();
    await expect(submit).toBeVisible();
    const path = info.outputPath(`creation-name-conflict-${width}.png`);
    await page.screenshot({ path });
    await info.attach(`creation-name-conflict-${width}`, {
      path,
      contentType: 'image/png',
    });
  }
});

test('removing the chosen attachment target never selects another VM', async ({
  page,
}) => {
  const state = fixture();
  state.machines[0].spec.volumeId = undefined;
  state.volumes[0].machineId = undefined;
  state.machines.push({
    ...structuredClone(state.machines[0]),
    id: 'vm-second-target',
    spec: { ...state.machines[0].spec, name: 'Other VM' },
  });
  const manager = await host(page, state);
  await manager.getByRole('button', { name: 'Volumes', exact: true }).click();
  await manager
    .getByRole('button', { name: 'Form project', exact: true })
    .click();
  const target = manager.getByRole('combobox', {
    name: 'VM without a project volume',
    exact: true,
  });
  const attach = manager.getByRole('button', {
    name: 'Attach',
    exact: true,
  });
  await expect(target).toHaveValue('');
  await expect(attach).toBeDisabled();
  await target.selectOption('vm-form-review');
  await expect(attach).toBeEnabled();
  state.machines = state.machines.filter(
    (machine) => machine.id !== 'vm-form-review',
  );
  await manager.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(target).toHaveValue('vm-form-review');
  await expect(
    manager.getByText('The selected VM is no longer eligible.', {
      exact: false,
    }),
  ).toBeVisible();
  await expect(attach).toBeDisabled();
  expect(state.mutations).toHaveLength(0);
  await target.selectOption('vm-second-target');
  await expect(attach).toBeEnabled();
});

test('creation retries retain their host and survive a reconnect to that same host', async ({
  page,
}) => {
  const state = fixture();
  const headers: Record<string, string>[] = [];
  const manager = await host(page, state, async (route, path) => {
    expect(path).toBe('/machines');
    headers.push(route.request().headers());
    await route.fulfill({
      status: 503,
      json: {
        code: 'host_unavailable',
        error: 'Creation response interrupted.',
      },
    });
  });
  await manager.getByRole('button', { name: 'New VM', exact: true }).click();
  const create = page.locator('[data-window-id="machine-create:main"]');
  const name = create.getByLabel('Name', { exact: true });
  await name.fill('Preserved draft VM');
  await create.getByRole('button', { name: 'Create', exact: true }).click();
  const retry = create.getByRole('button', { name: 'Retry Original' });
  await expect(retry).toBeEnabled();
  state.generation++;
  await create.getByRole('button', { name: 'Check Host' }).click();
  await expect(retry).toBeEnabled();
  await retry.click();
  await expect.poll(() => state.mutations.length).toBe(2);
  expect(state.mutations[1].body).toEqual(state.mutations[0].body);
  expect(headers.map((header) => header['x-axv-host-id'])).toEqual([
    'host-form-review',
    'host-form-review',
  ]);
  expect(headers.every((header) => !header['x-axv-compute-generation'])).toBe(
    true,
  );
  state.hostId = 'another-host';
  state.generation++;
  state.machines = state.machines.map((machine) => ({
    ...machine,
    hostId: state.hostId,
  }));
  await create.getByRole('button', { name: 'Check Host' }).click();
  await expect(
    create.getByText('The VM host changed.', { exact: false }),
  ).toBeVisible();
  await expect(retry).toBeDisabled();
  await expect(name).toHaveValue('Preserved draft VM');
  expect(state.mutations).toHaveLength(2);
  state.hostId = 'host-form-review';
  state.generation++;
  state.machines = state.machines.map((machine) => ({
    ...machine,
    hostId: state.hostId,
  }));
  await create.getByRole('button', { name: 'Check Host' }).click();
  await expect(retry).toBeEnabled();
  await expect(name).toHaveValue('Preserved draft VM');
});

for (const resource of ['volume', 'image'] as const) {
  test(`${resource} drafts and unresolved retries stay bound to their original host`, async ({
    page,
  }) => {
    const state = fixture();
    const headers: Record<string, string>[] = [];
    const resourceId =
      resource === 'volume' ? state.volumes[0].id : 'image-form-imported';
    const operation: Operation = {
      id: `operation-${resource}-original`,
      machineId: '',
      action: resource === 'volume' ? 'rename-volume' : 'import-image',
      ...(resource === 'volume' ? { volumeId: resourceId } : { resourceId }),
      status: 'queued',
      createdAt: '2026-10-17T00:01:00Z',
    };
    const manager = await host(page, state, async (route) => {
      headers.push(route.request().headers());
      if (headers.length === 3) {
        state.operations = [
          {
            id: `operation-${resource}-other`,
            machineId: '',
            action: 'inspect',
            resourceId: 'unrelated-resource',
            status: 'succeeded',
            createdAt: '2026-10-17T00:00:00Z',
          },
          operation,
        ];
        if (resource === 'volume') {
          state.volumes[0] = {
            ...state.volumes[0],
            revision: state.volumes[0].revision + 1,
            name: 'Preserved resource draft',
          };
          await route.fulfill({
            status: 202,
            json: { volume: state.volumes[0], operation },
          });
        } else {
          const image: ImageInfo = {
            id: resourceId,
            name: 'Preserved resource draft',
            provider: 'lima',
            arch: 'arm64',
            status: 'importing',
          };
          state.images.push(image);
          await route.fulfill({ status: 202, json: { image, operation } });
        }
        return;
      }
      await route.fulfill({
        status: headers.length === 2 ? 409 : 503,
        json:
          headers.length === 2
            ? {
                code: 'binding_changed',
                error: 'The gateway changed during recovery.',
              }
            : {
                code: 'host_unavailable',
                error: 'The original response was interrupted.',
              },
      });
    });
    await manager
      .getByRole('button', {
        name: resource === 'volume' ? 'Volumes' : 'Images',
        exact: true,
      })
      .click();
    if (resource === 'volume') {
      await manager
        .getByRole('button', { name: 'Form project', exact: true })
        .click();
      await actionMenu(page, manager, 'More volume actions', 'Rename');
    } else {
      await manager
        .getByRole('button', { name: 'Add Image', exact: true })
        .click();
      await manager
        .getByLabel('Image URL', { exact: true })
        .fill('https://example.com/pinned.img');
      await manager
        .getByLabel('Expected SHA-256', { exact: true })
        .fill('b'.repeat(64));
    }
    const name = manager.getByLabel(
      resource === 'volume' ? 'Volume name' : 'Image name',
      { exact: true },
    );
    const submit = manager.getByRole('button', {
      name: resource === 'volume' ? 'Rename' : 'Download and Verify',
      exact: true,
    });
    const retry = manager.getByRole('button', {
      name: resource === 'volume' ? 'Retry Original' : 'Retry Original Request',
      exact: true,
    });
    await name.fill('Preserved resource draft');
    await expect(submit).toBeEnabled();
    const changeHost = async (id: string) => {
      state.hostId = id;
      state.generation++;
      state.machines = state.machines.map((machine) => ({
        ...machine,
        hostId: id,
      }));
      const refresh = manager.getByRole('button', {
        name: 'Refresh',
        exact: true,
      });
      await expect(refresh).toBeEnabled();
      await refresh.click();
    };
    await changeHost('replacement-host');
    await expect(
      manager.getByText('This draft belongs to another VM host.', {
        exact: false,
      }),
    ).toBeVisible();
    await expect(name).toHaveValue('Preserved resource draft');
    await expect(submit).toBeDisabled();
    expect(state.mutations).toHaveLength(0);
    await changeHost('host-form-review');
    await expect(submit).toBeEnabled();
    await submit.click();
    await expect(retry).toBeEnabled();
    await changeHost('host-form-review');
    await expect(retry).toBeEnabled();
    await retry.click();
    await expect(manager.getByRole('alert')).toContainText('gateway changed');
    await expect(retry).toBeEnabled();
    await changeHost('replacement-host');
    await expect(retry).toBeDisabled();
    await expect(name).toHaveValue('Preserved resource draft');
    expect(state.mutations).toHaveLength(2);
    await changeHost('host-form-review');
    await expect(retry).toBeEnabled();
    await retry.click();
    await expect.poll(() => state.mutations.length).toBe(3);
    expect(state.mutations[2].body).toEqual(state.mutations[0].body);
    expect(
      headers.every((header) => header['x-axv-host-id'] === 'host-form-review'),
    ).toBe(true);
    expect(headers.every((header) => !header['x-axv-compute-generation'])).toBe(
      true,
    );
    await expect(retry).toHaveCount(0);
    await manager
      .getByRole('button', { name: 'View Activity', exact: true })
      .click();
    await expect(
      manager.getByRole('button', { name: 'Activity', exact: true }),
    ).toBeFocused();
    const selectedOperation = manager
      .getByRole('table')
      .locator('tr[data-selected="true"]');
    await expect(selectedOperation).toBeVisible();
    await expect(selectedOperation).toContainText(operation.id);
    await expect(selectedOperation).toContainText(
      resource === 'volume' ? 'Preserved resource draft' : resourceId,
    );
    await expect(selectedOperation).toContainText('Queued');
  });
}
