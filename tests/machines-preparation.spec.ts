import type { Locator, Page, TestInfo } from '@playwright/test';
import type {
  ComputeEnvironment,
  HostInfo,
  PreparationCheck,
  PreparationJob,
  PreparationPlan,
  PreparationRequest,
} from '../apps/dashboard/src/features/machines/api';
import { expect, launch, test } from './helpers';
import { preparationCheckFixture } from './machines-preparation-fixture';

interface PreparationFixture {
  check: PreparationCheck;
  job?: PreparationJob;
  plan?: PreparationPlan;
  runtimeError?: string;
  unavailableProviders?: HostInfo['providers'];
  ready: boolean;
  blocked: boolean;
  loseStartResponse: boolean;
  instantCancel?: boolean;
  checks: number;
  writes: { path: string; body: Record<string, unknown>; instance: string }[];
}

function fixture(platform = 'darwin'): PreparationFixture {
  return {
    check: preparationCheckFixture({
      platform,
      configured: false,
      configurationSource: 'Not configured',
      automaticSetupSupported: platform === 'darwin',
      checks: [
        {
          id: 'platform',
          label: 'Operating system',
          status: 'ready',
          detail:
            platform === 'darwin'
              ? 'macOS ARM64 supports private Lima.'
              : 'Linux uses the administrator-managed Firecracker/KVM service.',
        },
      ],
      ...(platform === 'linux'
        ? { defaults: { hostConfig: '/etc/axv/host.json' } }
        : {}),
    }),
    ready: false,
    blocked: false,
    loseStartResponse: false,
    checks: 0,
    writes: [],
  };
}

function makePlan(
  state: PreparationFixture,
  request: PreparationRequest,
): PreparationPlan {
  const linux = state.check.platform === 'linux';
  return {
    cacheHome: '/test/shared-cache',
    id: `plan-${state.writes.length}`,
    createdAt: new Date().toISOString(),
    expiresAt: new Date(Date.now() + 3600000).toISOString(),
    platform: state.check.platform,
    arch: state.check.arch,
    provider: linux ? 'firecracker' : 'lima',
    request: linux
      ? {
          storageRoot: '/unused/storage',
          limaHome: '/unused/lima',
          cpus: 4,
          memoryMiB: 4096,
          diskGiB: 80,
          machines: 4,
          ...request,
        }
      : request,
    actions: state.check.configured
      ? ['Verify the saved host identity and start its local VM service.']
      : linux
        ? [
            'Verify local service identity and socket access.',
            'Save this profile’s local connection.',
          ]
        : [
            'Download and verify the pinned private Lima runtime.',
            'Download and verify the Linux cloud image.',
            'Save the selected storage and admission limits.',
            'Start and verify the local VM service.',
          ],
    downloads:
      linux || state.check.configured
        ? []
        : [
            {
              id: 'lima',
              name: 'Lima 2.2.1',
              url: 'https://example.test/lima-2.2.1-Darwin-arm64.tar.gz',
              sha256: 'a'.repeat(64),
              bytes: 25000000,
              cached: false,
            },
            {
              id: 'image',
              name: 'Linux cloud image',
              url: 'https://example.test/linux-cloud.qcow2',
              sha256: 'b'.repeat(64),
              bytes: 0,
              cached: false,
            },
          ],
    checks: [
      {
        id: 'service',
        label: linux ? 'Local service connection' : 'Storage and runtime',
        status: state.blocked ? 'blocked' : 'ready',
        detail: state.blocked
          ? 'Permission denied opening the local service socket. Ask the administrator to grant this user access.'
          : 'The reviewed preparation can proceed.',
      },
    ],
    canStart: !state.blocked,
    configPath: '/test/axv/config.json',
    hostConfigPath: '/test/axv/host.json',
    activation: 'ready',
  };
}

function makeJob(
  state: PreparationFixture,
  body: Record<string, unknown>,
): PreparationJob {
  return {
    id: state.job ? 'job-new-preparation' : 'job-shared-preparation',
    requestId: String(body.requestId),
    planId: state.plan!.id,
    revision: 1,
    status: 'running',
    attempt: 1,
    phase: 'downloads',
    committed: false,
    canCancel: true,
    canRetry: false,
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
    steps: [
      {
        id: 'download',
        label: 'Download runtime and image',
        status: 'running',
        bytesDone: 4 * 1024 * 1024,
        bytesTotal: 20 * 1024 * 1024,
      },
      { id: 'configuration', label: 'Save configuration', status: 'pending' },
      { id: 'activation', label: 'Verify local VM service', status: 'pending' },
    ],
    plan: state.plan!,
  };
}

async function host(page: Page, state: PreparationFixture) {
  await page.route('**/api/compute/**', async (route) => {
    const path = new URL(route.request().url()).pathname.replace(
      '/api/compute',
      '',
    );
    const method = route.request().method();
    if (method !== 'GET') {
      const body = route.request().postDataJSON() as Record<string, unknown>;
      state.writes.push({
        path,
        body,
        instance: route.request().headers()['x-axv-instance-id'] || '',
      });
      if (path === '/preparation/plan') {
        state.plan = makePlan(state, body as PreparationRequest);
        await route.fulfill({ json: state.plan });
      } else if (path === '/preparation/jobs') {
        if (!state.job || state.job.planId !== body.planId)
          state.job = makeJob(state, body);
        if (state.instantCancel)
          state.job = {
            ...state.job!,
            status: 'cancelled',
            revision: 2,
            canCancel: false,
          };
        if (state.loseStartResponse) {
          state.loseStartResponse = false;
          await route.abort('failed');
        } else await route.fulfill({ status: 202, json: state.job });
      } else if (path === `/preparation/jobs/${state.job?.id}/retry`) {
        state.job = {
          ...state.job!,
          status: 'running',
          revision: state.job!.revision + 1,
          attempt: state.job!.attempt + 1,
          error: undefined,
          canRetry: false,
          canCancel: !state.job!.committed,
        };
        await route.fulfill({ status: 202, json: state.job });
      } else if (path === `/preparation/jobs/${state.job?.id}/cancel`) {
        state.job = {
          ...state.job!,
          status: 'cancelled',
          revision: state.job!.revision + 1,
          canCancel: false,
        };
        await route.fulfill({ json: state.job });
      } else
        throw new Error(`Unexpected preparation mutation ${method} ${path}`);
      return;
    }
    let value: unknown;
    if (path === '/preparation/check') {
      state.checks++;
      value = {
        ...state.check,
        ...(state.job
          ? ['queued', 'running'].includes(state.job.status)
            ? { activeJob: state.job }
            : { latestJob: state.job }
          : {}),
      };
    } else if (path === `/preparation/jobs/${state.job?.id}`) value = state.job;
    else if (path === '/environment')
      value = {
        platform: state.check.platform,
        configured: state.check.configured,
        configPath: '/test/axv/config.json',
        profileHome: '/test/axv',
        executablePath: '/test/bin/axv',
        requiredLimaVersion: '2.2.1',
        automaticSetupSupported: state.check.automaticSetupSupported,
        runtime: {
          state: state.ready
            ? 'ready'
            : state.runtimeError
              ? 'failed'
              : state.check.configured
                ? 'starting'
                : 'not_configured',
          error: state.runtimeError,
          mode: state.check.platform === 'linux' ? 'external' : 'local',
          generation: 1,
          readOnly: false,
        },
      } satisfies ComputeEnvironment;
    else if (path === '/info')
      value = {
        configured: state.check.configured,
        defaults: creationDefaultsFixture(
          state.ready ? 'prepared-image' : '',
          state.check.platform === 'linux' ? 'firecracker' : 'lima',
        ),
        hostId: 'host-preparation',
        hostArch: 'arm64',
        providers: state.ready
          ? [
              {
                provider:
                  state.check.platform === 'linux' ? 'firecracker' : 'lima',
                available: true,
                persistentVolumes: true,
                snapshots: false,
                guestExecution: true,
                volumes: true,
                images: true,
                configuration: true,
                forceStop: true,
              },
            ]
          : state.unavailableProviders || [],
        capacity: {
          cpus: 4,
          memoryMiB: 4096,
          diskGiB: 80,
          machines: 4,
          workers: 1,
        },
        images: state.ready
          ? [
              {
                id: 'prepared-image',
                name: 'Prepared Linux',
                provider:
                  state.check.platform === 'linux' ? 'firecracker' : 'lima',
                arch: 'arm64',
                status: 'configured',
              },
            ]
          : [],
      } satisfies HostInfo;
    else if (path === '/machines') value = { machines: [] };
    else if (path === '/volumes') value = { volumes: [] };
    else if (path === '/operations') value = { operations: [] };
    else if (path === '/images')
      value = {
        images: state.ready
          ? [
              {
                id: 'prepared-image',
                name: 'Prepared Linux',
                provider:
                  state.check.platform === 'linux' ? 'firecracker' : 'lima',
                arch: 'arm64',
                status: 'configured',
              },
            ]
          : [],
      };
    else throw new Error(`Unexpected preparation read ${path}`);
    await route.fulfill({ json: value });
  });
  await launch(page, 'machines');
  return page.locator('[data-window-id="machines:main"]');
}

async function capture(page: Page, info: TestInfo, name: string) {
  const path = info.outputPath(`${name}.png`);
  await page.screenshot({ path });
  await info.attach(name, { path, contentType: 'image/png' });
}

async function reviewMac(manager: Locator) {
  await manager.getByRole('button', { name: 'Review', exact: true }).click();
  await expect(
    manager.getByRole('heading', { name: 'Review VM preparation' }),
  ).toBeVisible();
}

test('macOS preparation reviews before changes, preserves drafts and resumes one background job', async ({
  page,
}, info) => {
  const state = fixture();
  const manager = await host(page, state);
  await expect(
    manager.getByRole('combobox', { name: 'Image source', exact: true }),
  ).toHaveValue('ubuntu-base');
  await expect(
    manager.getByLabel('Custom bundle manifest', { exact: true }),
  ).toHaveCount(0);
  const storage = manager.getByLabel('VM storage directory', { exact: true });
  await expect(storage).toHaveValue('/test/axv/compute');
  expect(state.writes).toEqual([]);
  await storage.fill('/test/Selected VM storage');
  await manager.getByLabel('CPU budget', { exact: true }).fill('6');
  const before = state.checks;
  await expect
    .poll(() => state.checks, { timeout: 12000 })
    .toBeGreaterThan(before);
  await expect(storage).toHaveValue('/test/Selected VM storage');
  await expect(manager.getByLabel('CPU budget', { exact: true })).toHaveValue(
    '6',
  );
  await capture(page, info, 'preparation-settings');
  await reviewMac(manager);
  expect(state.writes).toHaveLength(1);
  expect(state.writes[0]).toMatchObject({
    path: '/preparation/plan',
    instance: state.check.instanceId,
    body: { storageRoot: '/test/Selected VM storage', cpus: 6 },
  });
  await expect(
    manager.getByRole('link', {
      name: 'https://example.test/lima-2.2.1-Darwin-arm64.tar.gz',
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    manager.getByText('Size not yet known', { exact: true }),
  ).toBeVisible();
  await manager.getByText('SHA-256', { exact: true }).first().click();
  await expect(
    manager.getByText('a'.repeat(64), { exact: true }),
  ).toBeVisible();
  await capture(page, info, 'preparation-review');
  await manager
    .getByRole('button', { name: 'Prepare', exact: true })
    .dblclick();
  await expect(
    manager.getByRole('heading', { name: 'Preparing virtual machines' }),
  ).toBeVisible();
  expect(
    state.writes.filter((write) => write.path === '/preparation/jobs'),
  ).toHaveLength(1);
  expect(state.writes[1]!.body).toMatchObject({
    planId: state.plan!.id,
    confirmed: true,
  });
  await expect(
    manager.getByRole('progressbar', {
      name: 'Download runtime and image progress',
    }),
  ).toHaveAttribute('value', String(4 * 1024 * 1024));
  await capture(page, info, 'preparation-progress');
  await manager
    .getByRole('button', { name: 'Close Virtual Machines', exact: true })
    .click();
  await expect(manager).toHaveCount(0);
  await launch(page, 'machines');
  await expect(
    manager.getByRole('heading', { name: 'Preparing virtual machines' }),
  ).toBeVisible();
  await page.reload();
  await expect(
    manager.getByRole('heading', { name: 'Preparing virtual machines' }),
  ).toBeVisible();
  expect(
    state.writes.filter((write) => write.path === '/preparation/jobs'),
  ).toHaveLength(1);

  state.job = {
    ...state.job!,
    status: 'failed',
    revision: 3,
    canRetry: true,
    canCancel: false,
    error: {
      code: 'download_failed',
      message: 'Download interrupted. Verified files remain cached.',
      retryable: true,
    },
  };
  await expect(
    manager.getByRole('heading', { name: 'Preparation failed' }),
  ).toBeVisible();
  await expect(manager.getByRole('alert')).toContainText(
    'Download interrupted.',
  );
  await manager.getByRole('button', { name: 'Retry', exact: true }).click();
  expect(state.writes.at(-1)).toMatchObject({
    path: '/preparation/jobs/job-shared-preparation/retry',
    body: { expectedRevision: 3 },
  });
  await expect(
    manager.getByText('Running · Downloads · Attempt 2', { exact: true }),
  ).toBeVisible();
  state.check.configured = true;
  state.job = {
    ...state.job!,
    revision: 5,
    committed: true,
    canCancel: false,
    phase: 'activation',
  };
  await expect(
    manager.getByRole('button', { name: 'Cancel', exact: true }),
  ).toBeDisabled();
  state.ready = true;
  state.job = {
    ...state.job!,
    revision: 6,
    status: 'succeeded',
    phase: 'ready',
    result: 'ready',
    steps: state.job!.steps.map((step) => ({ ...step, status: 'succeeded' })),
  };
  await expect(
    manager.getByRole('heading', { name: 'VM environment ready' }),
  ).toBeVisible();
  await expect(
    manager.getByRole('button', { name: 'New VM', exact: true }),
  ).toBeEnabled();
  await capture(page, info, 'preparation-ready');
  await manager.getByRole('button', { name: 'New VM', exact: true }).click();
  await expect(
    page
      .locator('[data-window-id="machine-create:main"]')
      .getByRole('button', { name: 'Create', exact: true }),
  ).toBeEnabled();
});

test('lost start responses retain the exact request and cancellation only affects that job', async ({
  page,
}) => {
  const state = fixture();
  state.loseStartResponse = true;
  const manager = await host(page, state);
  await reviewMac(manager);
  await manager.getByRole('button', { name: 'Prepare', exact: true }).click();
  await expect(
    manager.getByRole('button', {
      name: 'Retry Original Request',
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    manager.getByRole('button', { name: 'Cancel', exact: true }),
  ).toBeDisabled();
  const original = state.writes[1]!;
  await manager
    .getByRole('button', { name: 'Retry Original Request', exact: true })
    .click();
  await expect(
    manager.getByRole('button', {
      name: 'Retry Original Request',
      exact: true,
    }),
  ).toHaveCount(0);
  expect(state.writes[2]).toEqual(original);
  await manager.getByRole('button', { name: 'Cancel', exact: true }).click();
  expect(state.writes.at(-1)).toMatchObject({
    path: '/preparation/jobs/job-shared-preparation/cancel',
    body: { expectedRevision: 1 },
  });
  await expect(
    manager.getByText(
      'Preparation cancelled. Verified downloads are kept for the next attempt.',
      { exact: true },
    ),
  ).toBeVisible();
  await manager.getByRole('button', { name: 'Settings', exact: true }).click();
  await expect(
    manager.getByLabel('VM storage directory', { exact: true }),
  ).toHaveValue('/test/axv/compute');
});

test('Linux diagnoses local access before connecting an administrator-managed service', async ({
  page,
}, info) => {
  const state = fixture('linux');
  state.blocked = true;
  const manager = await host(page, state);
  await expect(
    manager.getByText('Linux · arm64', { exact: true }),
  ).toBeVisible();
  await expect(
    manager.getByLabel('VM storage directory', { exact: true }),
  ).toHaveCount(0);
  await manager
    .getByRole('combobox', { name: 'Read connection from', exact: true })
    .selectOption('socket');
  await manager
    .getByLabel('Local service socket', { exact: true })
    .fill('/run/team/axv-control.sock');
  await manager.getByRole('button', { name: 'Review', exact: true }).click();
  expect(state.writes).toHaveLength(1);
  expect(state.writes[0]!.body).toEqual({
    socket: '/run/team/axv-control.sock',
  });
  await expect(
    manager.getByText('Permission denied opening the local service socket.', {
      exact: false,
    }),
  ).toBeVisible();
  await expect(
    manager.getByRole('button', { name: 'Connect', exact: true }),
  ).toBeDisabled();
  await expect(
    manager.getByText('No downloads are required for this plan.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    manager
      .locator('dt')
      .filter({ hasText: /^(VM storage|Lima state|Resource limits)$/ }),
  ).toHaveCount(0);
  await capture(page, info, 'preparation-linux-permissions');
  state.blocked = false;
  await manager
    .getByRole('button', { name: 'Refresh Review', exact: true })
    .click();
  await expect(
    manager.getByRole('button', { name: 'Connect', exact: true }),
  ).toBeEnabled();
  await manager.getByRole('button', { name: 'Connect', exact: true }).click();
  await expect(
    manager.getByRole('heading', { name: 'Preparing virtual machines' }),
  ).toBeVisible();
  expect(
    state.writes.filter((write) => write.path === '/preparation/jobs'),
  ).toHaveLength(1);
  expect(state.plan!.downloads).toEqual([]);
});

test('configured service review preserves existing locations and budgets', async ({
  page,
}) => {
  const state = fixture();
  state.check.configured = true;
  state.check.configurationSource = 'Existing profile configuration';
  state.runtimeError = 'The saved VM service could not be started.';
  const unavailable = {
    available: false,
    persistentVolumes: false,
    snapshots: false,
    guestExecution: false,
  };
  state.unavailableProviders = [
    { ...unavailable, provider: 'lima', reason: state.runtimeError },
    {
      ...unavailable,
      provider: 'firecracker',
      reason: 'The Firecracker executable is unavailable.',
    },
  ];
  const manager = await host(page, state);
  await manager
    .getByRole('button', { name: 'Environment', exact: true })
    .click();
  const environment = manager.locator('.machines-environment');
  await expect(
    environment.getByText(state.runtimeError, { exact: true }),
  ).toHaveCount(1);
  await expect(
    environment.getByText('The Firecracker executable is unavailable.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    manager.getByLabel('VM storage directory', { exact: true }),
  ).toHaveCount(0);
  await manager.getByRole('button', { name: 'Review', exact: true }).click();
  expect(state.writes).toHaveLength(1);
  expect(state.writes[0]!.body).toEqual(state.check.defaults);
  await expect(
    manager.getByText('No downloads are required for this plan.', {
      exact: true,
    }),
  ).toBeVisible();
  await manager
    .getByRole('button', { name: 'Start Service', exact: true })
    .click();
  await expect(
    manager.getByRole('heading', { name: 'Preparing virtual machines' }),
  ).toBeVisible();
  expect(state.job!.plan.request).toEqual(state.check.defaults);
});

test('a definitive failed plan can be reviewed again without silently retrying or changing its settings', async ({
  page,
}) => {
  const state = fixture();
  state.plan = makePlan(state, state.check.defaults);
  state.job = {
    ...makeJob(state, { requestId: 'failed-original' }),
    status: 'failed',
    revision: 3,
    canRetry: false,
    canCancel: false,
    error: {
      code: 'configuration_changed',
      message:
        'VM configuration changed after review. Existing settings were preserved.',
      retryable: false,
    },
  };
  const original = structuredClone(state.job);
  const manager = await host(page, state);
  await expect(
    manager.getByRole('heading', { name: 'Preparation failed' }),
  ).toBeVisible();
  await expect(
    manager.getByRole('button', { name: 'Retry', exact: true }),
  ).toHaveCount(0);
  await manager.getByRole('button', { name: 'Settings', exact: true }).click();
  await expect(
    manager.getByLabel('VM storage directory', { exact: true }),
  ).toHaveValue('/test/axv/compute');
  await manager.getByLabel('CPU budget', { exact: true }).fill('5');
  await reviewMac(manager);
  expect(state.writes).toHaveLength(1);
  expect(state.writes[0]).toMatchObject({
    path: '/preparation/plan',
    body: { cpus: 5 },
  });
  expect(state.job).toEqual(original);
  await manager.getByRole('button', { name: 'Prepare', exact: true }).click();
  await expect(
    manager.getByRole('heading', { name: 'Preparing virtual machines' }),
  ).toBeVisible();
  expect(state.job!.id).not.toBe(original.id);
  expect(state.job!.plan.request.cpus).toBe(5);
  expect(
    state.writes.filter((write) => write.path.endsWith('/retry')),
  ).toHaveLength(0);
});

test('the real server exposes a read-only review before preparation is confirmed', async ({
  page,
}) => {
  const starts: string[] = [];
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      new URL(request.url()).pathname === '/api/compute/preparation/jobs'
    )
      starts.push(request.postData() || '');
  });
  await launch(page, 'machines');
  const manager = page.locator('[data-window-id="machines:main"]');
  await manager.getByRole('button', { name: /^Review$/ }).click();
  await expect(
    manager.getByRole('heading', { name: 'Review VM preparation' }),
  ).toBeVisible({ timeout: 30000 });
  await expect(
    manager.getByRole('heading', { name: 'Actions', exact: true }),
  ).toBeVisible();
  await expect(
    manager.getByRole('button', {
      name: /^(Prepare|Connect)$/,
    }),
  ).toBeVisible();
  expect(starts).toEqual([]);
});

test('a lost start response recovers a job that finished before its first poll', async ({
  page,
}) => {
  const state = fixture();
  state.loseStartResponse = true;
  state.instantCancel = true;
  const manager = await host(page, state);
  await reviewMac(manager);
  await manager.getByRole('button', { name: 'Prepare', exact: true }).click();
  await expect(
    manager.getByRole('button', {
      name: 'Retry Original Request',
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    manager.getByRole('heading', { name: 'Preparation cancelled' }),
  ).toBeVisible();
  await manager
    .getByRole('button', { name: 'Retry Original Request', exact: true })
    .click();
  await expect(
    manager.getByRole('button', {
      name: 'Retry Original Request',
      exact: true,
    }),
  ).toHaveCount(0);
  await expect(
    manager.getByRole('button', {
      name: 'Prepare',
      exact: true,
    }),
  ).toHaveCount(0);
  await expect(
    manager.getByRole('button', { name: 'Settings', exact: true }),
  ).toBeEnabled();
  expect(state.writes[2]).toEqual(state.writes[1]);
  expect(state.job!.id).toBe('job-shared-preparation');
});

test('an existing Linux service is connected without offering to start it', async ({
  page,
}) => {
  const state = fixture('linux');
  state.check.configured = true;
  state.check.defaults = { socket: '/run/existing/axv-control.sock' };
  const manager = await host(page, state);
  await manager
    .getByRole('button', { name: 'Environment', exact: true })
    .click();
  await manager.getByRole('button', { name: 'Review', exact: true }).click();
  expect(state.writes[0]!.body).toEqual(state.check.defaults);
  await expect(
    manager.getByRole('button', { name: 'Start Service', exact: true }),
  ).toHaveCount(0);
  await expect(
    manager.getByRole('button', { name: 'Connect', exact: true }),
  ).toBeEnabled();
  await expect(
    manager.locator('dt').filter({
      hasText: /^(VM storage|Lima state|Resource limits|Required Lima)$/,
    }),
  ).toHaveCount(0);
});
import { creationDefaultsFixture } from './machines-creation-fixture';
