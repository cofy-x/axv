import type { Locator, Page, TestInfo } from '@playwright/test';
import type {
  ComputeEnvironment,
  HostInfo,
  PreparationCheck,
  PreparationJob,
  PreparationPlan,
  PreparationRequest,
} from '../apps/dashboard/src/features/machines/api';
import { activate, bounds, expect, launch, menuItem, test } from './helpers';
import { applicationMenu } from './desktop-application-helpers';
import { preparationCheckFixture } from './machines-preparation-fixture';
import { refreshMachines } from './machines-helpers';

interface PreparationFixture {
  check: PreparationCheck;
  executablePath?: string;
  job?: PreparationJob;
  plan?: PreparationPlan;
  runtimeError?: string;
  unavailableProviders?: HostInfo['providers'];
  hostInfo?: Partial<HostInfo>;
  hostError?: 'host_unavailable';
  generation?: number;
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
  const missingSocket =
    state.blocked &&
    state.check.localService?.status === 'missing' &&
    request.socket === state.check.localService.socket;
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
        id: linux ? 'local_service' : 'service',
        ...(missingSocket && { code: 'missing' }),
        label: linux ? 'Local service connection' : 'Storage and runtime',
        status: state.blocked ? 'blocked' : 'ready',
        detail: missingSocket
          ? 'No listener is present on the selected socket.'
          : state.blocked
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
    if (
      state.hostError &&
      ['/info', '/machines', '/volumes', '/operations', '/images'].includes(
        path,
      )
    ) {
      await route.fulfill({
        status: 503,
        json: {
          code: state.hostError,
          error: 'The VM service temporarily stopped serving inventory.',
        },
      });
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
        logPath: '/test/axv/logs/daemon.log',
        executablePath: state.executablePath ?? '/test/bin/axv',
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
          hostId: state.hostInfo?.hostId,
          generation: state.generation ?? 1,
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
        ...state.hostInfo,
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
    else if (path === '/projects') value = { projects: [] };
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

async function recordCopiedText(page: Page, rejectWrites = false) {
  await page.evaluate((rejectWrites) => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: async (value: string) => {
          if (rejectWrites)
            throw new DOMException('Clipboard write denied', 'NotAllowedError');
          document.documentElement.dataset.vmClipboard = value;
        },
        readText: async () =>
          document.documentElement.dataset.vmClipboard ?? '',
      },
    });
  }, rejectWrites);
  return page.locator('html');
}

async function reviewMac(manager: Locator) {
  await manager.getByRole('button', { name: 'Review', exact: true }).click();
  await expect(
    manager.getByRole('heading', { name: 'Review VM preparation' }),
  ).toBeVisible();
}

test('unconfigured VM menus keep setup visible and unavailable views disabled', async ({
  page,
  hasTouch,
}) => {
  const manager = await host(page, fixture());
  await expect(
    manager.getByRole('heading', { name: 'Enable virtual machines' }),
  ).toBeVisible();
  await expect(manager.locator('.machines-toolbar')).toHaveCount(0);
  const steps = manager.getByRole('list', { name: 'Setup steps', exact: true });
  await expect(steps.getByRole('listitem')).toHaveText([
    '1. Settings',
    '2. Review',
    '3. Prepare',
  ]);
  await expect(steps.locator('[aria-current="step"]')).toHaveText(
    '1. Settings',
  );
  await applicationMenu(page, 'View', hasTouch);
  await expect(menuItem(page, 'VM Host')).toHaveAttribute(
    'aria-checked',
    'true',
  );
  for (const label of ['VMs', 'Volumes', 'Images', 'Activity']) {
    await expect(menuItem(page, label)).toBeDisabled();
    await expect(menuItem(page, label)).toHaveAttribute(
      'aria-checked',
      'false',
    );
  }
  await menuItem(page, 'VM Host').click();
  await expect(
    manager.getByRole('heading', { name: 'Enable virtual machines' }),
  ).toBeVisible();
});

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
    manager.getByRole('heading', { name: 'VM service connected' }),
  ).toBeVisible();
  await expect(
    manager.getByRole('button', { name: 'New VM', exact: true }),
  ).toBeEnabled();
  await capture(page, info, 'preparation-ready');
  await manager.getByRole('button', { name: 'New VM', exact: true }).click();
  await expect(
    page
      .locator('[data-window-id="machine-create:main"]')
      .getByRole('button', { name: 'Create & Start', exact: true }),
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

test(
  'Linux diagnoses local access before connecting an administrator-managed service',
  { tag: '@vm-ui' },
  async ({ page, hasTouch }, info) => {
    const state = fixture('linux');
    const socket = '/run/axv-host/control.sock';
    state.check.localService = {
      status: 'missing',
      socket,
      detail: 'No listener is present on the default socket.',
    };
    await page.setViewportSize({ width: 320, height: 568 });
    const clipboard = await recordCopiedText(page);
    const manager = await host(page, state);
    const choose = (control: Locator) =>
      hasTouch ? control.tap() : control.click();
    const heading = manager.getByRole('heading', { level: 2 });
    await expect(heading).toHaveText('Check the VM service');
    await expect(heading).toBeInViewport();
    await expect(manager.getByText(socket, { exact: true })).toHaveCount(0);
    await expect(
      manager.getByRole('list', { name: 'Setup steps', exact: true }),
    ).toHaveCount(0);
    await expect(manager.locator('.machines-toolbar')).toHaveCount(0);
    await expect(
      manager.getByLabel('VM storage directory', { exact: true }),
    ).toHaveCount(0);
    const edit = manager.getByRole('button', {
      name: 'Edit connection',
      exact: true,
    });
    const source = manager.getByRole('combobox', {
      name: 'Read connection from',
      exact: true,
    });
    const checkConnection = manager.getByRole('button', {
      name: 'Check connection',
      exact: true,
    });
    const connect = manager.getByRole('button', {
      name: 'Connect',
      exact: true,
    });
    const command = 'systemctl status axv-host.service --no-pager';
    const copyCommand = manager.getByRole('button', {
      name: 'Copy command',
      exact: true,
    });
    const openTerminal = manager.getByRole('button', {
      name: 'Copy & Open Terminal',
      exact: true,
    });
    const diagnostics = manager.getByRole('button', {
      name: 'Diagnostics',
      exact: true,
    });
    await expect(edit).toBeInViewport();
    await expect(checkConnection).toBeInViewport();
    await expect(source).toBeHidden();
    await expect(
      manager.getByText('Technical details', { exact: true }),
    ).toHaveCount(0);
    await expect(manager.getByText(command, { exact: true })).toBeInViewport();
    await expect(openTerminal).toBeEnabled();
    await expect(
      manager.getByRole('link', { name: 'Linux VM setup guide', exact: true }),
    ).toBeVisible();
    expect(state.writes).toEqual([]);
    await capture(page, info, 'preparation-linux-default-connection');
    await choose(copyCommand);
    await expect(clipboard).toHaveAttribute('data-vm-clipboard', command);

    await diagnostics.press('Enter');
    const diagnosticDialog = page.getByRole('dialog', {
      name: 'VM Diagnostics',
      exact: true,
    });
    await expect(
      diagnosticDialog.locator('.ns-dialog-main > .ns-pixel-icon'),
    ).toHaveCount(0);
    await expect(
      diagnosticDialog.getByText(socket, { exact: true }),
    ).toBeVisible();
    await expect(diagnosticDialog).toContainText('/test/axv/config.json');
    await expect(diagnosticDialog).toContainText('/test/axv/logs/daemon.log');
    const copyDiagnostics = diagnosticDialog.getByRole('button', {
      name: 'Copy diagnostics',
      exact: true,
    });
    await expect(copyDiagnostics).toBeInViewport({ ratio: 1 });
    await expect(
      diagnosticDialog.getByRole('button', { name: 'Close', exact: true }),
    ).toBeInViewport({ ratio: 1 });
    await capture(page, info, 'preparation-linux-diagnostics');
    await choose(copyDiagnostics);
    await expect(clipboard).toHaveAttribute(
      'data-vm-clipboard',
      /\/test\/axv\/logs\/daemon\.log/,
    );
    await page.keyboard.press('Escape');
    await expect(diagnosticDialog).toBeHidden();
    await expect(diagnostics).toBeFocused();
    expect(state.writes).toEqual([]);

    const previousChecks = state.checks;
    state.check.localService = {
      status: 'ready',
      socket,
      detail: 'The default Firecracker service is available.',
    };
    await refreshMachines(page, hasTouch);
    const ready = manager.getByRole('heading', {
      name: 'Firecracker service detected',
    });
    await expect(ready).toBeInViewport();
    await expect(ready).toHaveAttribute('aria-live', 'polite');
    await expect(connect).toBeInViewport({ ratio: 1 });
    await expect(edit).toBeInViewport({ ratio: 1 });
    await expect(diagnostics).toBeInViewport({ ratio: 1 });
    const [statusBox, connectBox, editBox, diagnosticsBox] = await Promise.all([
      bounds(ready),
      bounds(connect),
      bounds(edit),
      bounds(diagnostics),
    ]);
    expect(connectBox.y).toBeGreaterThan(statusBox.y + statusBox.height);
    expect(Math.min(editBox.y, diagnosticsBox.y)).toBeGreaterThan(
      connectBox.y + connectBox.height,
    );
    await expect(manager.getByText(socket, { exact: true })).toHaveCount(0);
    await expect(
      manager.getByText('Linux · arm64', { exact: true }),
    ).toHaveCount(0);
    await expect(
      manager.getByText(state.check.localService.detail, { exact: true }),
    ).toHaveCount(0);
    await capture(page, info, 'preparation-linux-ready');
    await choose(diagnostics);
    await expect(
      diagnosticDialog.getByText(socket, { exact: true }),
    ).toBeVisible();
    await diagnosticDialog
      .getByRole('button', { name: 'Close', exact: true })
      .click();
    await expect(diagnostics).toBeFocused();
    await expect(copyCommand).toHaveCount(0);
    await expect(openTerminal).toHaveCount(0);
    expect(state.checks).toBeGreaterThan(previousChecks);
    expect(state.writes).toEqual([]);

    await expect(connect).toBeEnabled();
    await expect(checkConnection).toHaveCount(0);

    const cancel = manager.getByRole('button', { name: 'Cancel', exact: true });
    await edit.press('Enter');
    await expect(source).toBeFocused();
    await expect(manager.getByText(socket, { exact: true })).toHaveCount(0);
    await source.selectOption('configuration');
    await expect(
      manager.getByLabel('Local service configuration', { exact: true }),
    ).toHaveValue('/etc/axv/host.json');
    await expect(connect).toHaveCount(0);
    await choose(cancel);
    await expect(source).toBeHidden();
    await expect(edit).toBeFocused();
    await expect(manager.getByText(socket, { exact: true })).toHaveCount(0);
    await expect(connect).toBeEnabled();
    expect(state.writes).toHaveLength(0);

    await choose(edit);
    await source.selectOption('socket');
    const customSocket = '/run/team/axv-control.sock';
    const socketInput = manager.getByLabel('Local service socket', {
      exact: true,
    });
    await socketInput.fill(customSocket);
    await expect(connect).toHaveCount(0);
    const beforeCustomCheck = state.checks;
    state.check.localService = {
      status: 'missing',
      socket,
      detail: 'No listener is present on the default socket.',
    };
    await refreshMachines(page, hasTouch);
    await expect.poll(() => state.checks).toBeGreaterThan(beforeCustomCheck);
    await expect(socketInput).toHaveValue(customSocket);
    state.blocked = true;
    await choose(checkConnection);
    await expect.poll(() => state.writes.length).toBe(1);
    expect(state.writes[0]!.path).toBe('/preparation/plan');
    expect(state.writes[0]!.body).toEqual({ socket: customSocket });
    await expect(
      manager.getByText('Permission denied opening the local service socket.', {
        exact: false,
      }),
    ).toBeVisible();
    await expect(source).toBeHidden();
    await expect(manager.getByText(customSocket, { exact: true })).toHaveCount(
      1,
    );
    await expect(copyCommand).toHaveCount(0);
    await expect(openTerminal).toHaveCount(0);
    await expect(connect).toHaveCount(0);
    await expect(
      manager.getByRole('heading', { name: 'Downloads', exact: true }),
    ).toHaveCount(0);
    await expect(
      manager
        .locator('dt')
        .filter({ hasText: /^(VM storage|Lima state|Resource limits)$/ }),
    ).toHaveCount(0);
    await capture(page, info, 'preparation-linux-permissions');
    state.blocked = false;
    await choose(checkConnection);
    await expect(connect).toBeEnabled();
    expect(
      state.writes.filter((write) => write.path === '/preparation/jobs'),
    ).toHaveLength(0);
    await choose(connect);
    await expect(
      manager.getByRole('heading', {
        name: 'Connecting VM service',
        exact: true,
      }),
    ).toBeVisible();
    expect(
      state.writes.filter((write) => write.path === '/preparation/jobs'),
    ).toMatchObject([{ body: { planId: state.plan!.id } }]);
    expect(state.plan!.downloads).toEqual([]);
  },
);

test(
  'Linux VM setup guides the host owner from the detected unit state back to the connection check',
  { tag: '@vm-ui' },
  async ({ page, hasTouch }, info) => {
    await page.setViewportSize(
      hasTouch ? { width: 390, height: 844 } : { width: 1180, height: 820 },
    );
    const state = fixture('linux');
    state.executablePath = "/test/AXV's build!/one\\two/axv";
    const socket = '/run/axv-host/control.sock';
    state.check.localService = {
      socket,
      status: 'missing',
      systemdState: 'not_installed',
      detail: 'No listener is present on the default socket.',
    };
    const terminalInputs: Buffer[][] = [];
    await page.route('**/api/terminal/info', (route) =>
      route.fulfill({ json: { supported: true, shell: '/bin/sh' } }),
    );
    await page.routeWebSocket(
      (url) => url.pathname === '/api/terminal',
      (connection) => {
        const input: Buffer[] = [];
        terminalInputs.push(input);
        connection.onMessage((frame) => {
          if (Buffer.isBuffer(frame)) input.push(frame);
        });
        connection.send(JSON.stringify({ ready: true }));
      },
    );

    await launch(page, 'terminal');
    const terminals = page.locator('[data-app-id="terminal"]');
    await expect(terminals.getByTestId('terminal-status')).toHaveAttribute(
      'data-state',
      'connected',
    );
    await expect(
      terminals.getByText('VM service setup', { exact: true }),
    ).toHaveCount(0);
    const clipboard = await recordCopiedText(page);
    const manager = await host(page, state);
    const choose = (control: Locator) =>
      hasTouch ? control.tap() : control.click();
    const copyAndOpen = manager.getByRole('button', {
      name: 'Copy & Open Terminal',
      exact: true,
    });
    const openWithoutCopy = manager.getByRole('button', {
      name: 'Open Host Terminal',
      exact: true,
    });
    const copy = manager.getByRole('button', {
      name: 'Copy command',
      exact: true,
    });
    const startCommand = 'sudo axv daemon start --only vm';
    const installCommand = String.raw`sudo '/test/AXV'\''s build'\!'/one'\\'two/axv' host install-service --apply-sysctl`;
    const statusCommand = 'systemctl status axv-host.service --no-pager';
    const start = manager.getByText(startCommand, { exact: true });
    const status = manager.getByText(statusCommand, { exact: true });
    const setupGuide = manager.getByRole('link', {
      name: 'Linux VM setup guide',
    });
    const checkConnection = manager.getByRole('button', {
      name: 'Check connection',
      exact: true,
    });
    const inputs = () =>
      terminalInputs.map((input) => Buffer.concat(input).toString());

    const installInstruction =
      'The command installs Firecracker and the default Guest image, then starts axv-host.service.';

    async function openCleanShell(button: Locator) {
      const previousInputs = inputs();
      await choose(button);
      await expect(terminals).toHaveCount(previousInputs.length + 1);
      const shell = page.locator('[data-app-id="terminal"].is-active');
      await expect(shell.getByTestId('terminal-status')).toHaveAttribute(
        'data-state',
        'connected',
      );
      await expect(
        shell.getByText('VM service setup', { exact: true }),
      ).toHaveCount(0);
      await expect(
        shell.getByRole('link', { name: 'Linux VM setup guide' }),
      ).toHaveCount(0);
      await expect(
        shell.getByRole('button', { name: 'Copy command', exact: true }),
      ).toHaveCount(0);
      await expect.poll(inputs).toEqual([...previousInputs, '']);
      return shell;
    }

    async function pasteCopiedCommand(command: string) {
      const previousInputs = inputs();
      await clipboard.evaluate((root) =>
        root.removeAttribute('data-vm-clipboard'),
      );
      const shell = await openCleanShell(copyAndOpen);
      await expect(clipboard).toHaveAttribute('data-vm-clipboard', command);
      await choose(shell.getByRole('button', { name: 'Paste', exact: true }));
      await expect.poll(inputs).toEqual([...previousInputs, command]);
      await activate(page, manager, hasTouch);
    }

    await expect(
      manager.getByRole('heading', {
        name: 'Install the VM service',
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      manager.getByText(/built-in installer supports Linux amd64/),
    ).toBeVisible();
    await expect(copy).toHaveCount(0);
    await expect(copyAndOpen).toHaveCount(0);

    state.check.arch = 'amd64';
    const runningExecutable = state.executablePath;
    state.executablePath = '/test/newline\n/axv';
    await refreshMachines(page, hasTouch);
    await expect(
      manager.getByText(/could not identify a usable path/),
    ).toBeVisible();
    await expect(copy).toHaveCount(0);
    await expect(copyAndOpen).toHaveCount(0);
    state.executablePath = runningExecutable;
    await refreshMachines(page, hasTouch);
    await expect(copyAndOpen).toBeVisible();
    await expect(openWithoutCopy).toHaveCount(0);
    await expect(setupGuide).toBeVisible();
    await expect(copy).toBeEnabled();
    await expect(
      manager.getByText(installCommand, { exact: true }),
    ).toBeVisible();
    await expect(start).toHaveCount(0);
    await expect(manager).toContainText(installInstruction);
    await expect(status).toHaveCount(0);
    await expect(manager.getByRole('heading', { level: 2 })).toHaveText(
      'Install the VM service',
    );
    await expect(manager.getByText(socket, { exact: true })).toHaveCount(0);
    await expect(checkConnection).toBeInViewport({ ratio: 1 });
    await expect(copy).toBeInViewport({ ratio: 1 });
    const connectionActions = manager.locator('.machines-connection-actions');
    const [actionsBox, buttonBox] = await Promise.all([
      connectionActions.boundingBox(),
      checkConnection.boundingBox(),
    ]);
    expect(buttonBox!.y - actionsBox!.y).toBeGreaterThanOrEqual(8);
    await capture(page, info, 'preparation-linux-install');
    await choose(copy);
    await expect(clipboard).toHaveAttribute(
      'data-vm-clipboard',
      installCommand,
    );
    await expect(manager).toHaveClass(/is-active/);
    await expect(terminals).toHaveCount(1);
    await expect(
      manager.getByText(
        'Command copied. Paste and run it in the host terminal.',
        {
          exact: true,
        },
      ),
    ).toBeVisible();
    await pasteCopiedCommand(installCommand);

    const permission =
      'The AXV desktop account cannot read /etc/axv/host.json.';
    state.check.localService = {
      socket,
      status: 'permission_denied',
      systemdState: 'inactive',
      detail: permission,
    };
    await refreshMachines(page, hasTouch);
    await expect(
      manager.getByRole('heading', {
        name: 'Start the VM service',
        exact: true,
      }),
    ).toBeVisible();
    await expect(start).toBeVisible();
    await expect(manager.getByText(permission, { exact: true })).toBeVisible();
    await expect(copy).toBeEnabled();
    await expect(status).toHaveCount(0);
    await pasteCopiedCommand(startCommand);
    await expect(start).toBeVisible();

    for (const [systemdState, headline] of [
      ['failed', 'The VM service failed to start'],
      ['active', 'Check the VM service socket'],
    ] as const) {
      state.check.localService = {
        socket,
        status: 'missing',
        systemdState,
        detail: 'No listener is present on the default socket.',
      };
      await refreshMachines(page, hasTouch);
      await expect(
        manager.getByRole('heading', { name: headline, exact: true }),
      ).toBeVisible();
      await expect(status).toBeVisible();
      await expect(start).toHaveCount(0);
      await expect(copy).toBeEnabled();
    }
    await pasteCopiedCommand(statusCommand);
    await page.evaluate(() =>
      navigator.clipboard.writeText('Previous clipboard'),
    );
    await recordCopiedText(page, true);
    const beforeBlockedCopy = inputs();
    await choose(copyAndOpen);
    await expect(
      manager.getByRole('alert').filter({ hasText: /copy/i }),
    ).toBeVisible();
    await expect(manager).toHaveClass(/is-active/);
    await expect(terminals).toHaveCount(beforeBlockedCopy.length);
    await expect(status).toHaveCSS('user-select', /^(text|all)$/);
    await openCleanShell(openWithoutCopy);
    await expect(clipboard).toHaveAttribute(
      'data-vm-clipboard',
      'Previous clipboard',
    );
    await activate(page, manager, hasTouch);
    expect(state.writes).toHaveLength(0);
    state.blocked = true;
    await choose(checkConnection);
    await expect(manager.getByText(/default socket is missing/)).toBeVisible();
    state.check.localService = {
      status: 'ready',
      socket,
      detail: 'Service detected.',
    };
    await refreshMachines(page, hasTouch);
    await expect(
      manager.getByRole('heading', {
        name: 'Firecracker service detected',
        exact: true,
      }),
    ).toBeVisible();
    await capture(page, info, 'preparation-linux-ready');
    await expect(copyAndOpen).toHaveCount(0);
    await expect(openWithoutCopy).toHaveCount(0);
    state.blocked = false;
    const connect = manager.getByRole('button', {
      name: 'Connect',
      exact: true,
    });
    if (hasTouch) await connect.tap();
    else await connect.press('Enter');
    await expect(
      manager.getByRole('heading', { name: 'Connecting VM service' }),
    ).toBeVisible();
    await expect(copyAndOpen).toHaveCount(0);
    expect(state.writes.map(({ path, body }) => ({ path, body }))).toEqual([
      { path: '/preparation/plan', body: { socket } },
      { path: '/preparation/plan', body: { socket } },
      {
        path: '/preparation/jobs',
        body: {
          planId: state.plan!.id,
          confirmed: true,
          requestId: expect.any(String),
        },
      },
    ]);
  },
);

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
  await manager.getByRole('button', { name: 'VM Host', exact: true }).click();
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
  const linux = process.platform === 'linux';
  if (linux) {
    await manager.getByRole('button', { name: 'Edit connection' }).click();
    await manager.getByLabel('Read connection from').selectOption('socket');
    await manager
      .getByLabel('Local service socket')
      .fill('/run/axv-host/control.sock');
  }
  const review = manager.getByRole('button', {
    name: linux ? 'Check connection' : 'Review',
    exact: true,
  });
  const [response] = await Promise.all([
    page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname === '/api/compute/preparation/plan',
    ),
    review.click(),
  ]);
  expect(response.ok()).toBe(true);
  if (linux) {
    const plan: PreparationPlan = await response.json();
    const connect = manager.getByRole('button', {
      name: 'Connect',
      exact: true,
    });
    if (plan.canStart) {
      await expect(connect).toBeEnabled();
      await expect(
        manager.getByText('Connection verified', { exact: true }),
      ).toBeVisible();
    } else {
      const blocked = plan.checks.find((check) => check.status === 'blocked');
      expect(blocked).toBeDefined();
      if (
        blocked!.id === 'local_service' &&
        blocked!.code === 'missing' &&
        plan.request.socket === '/run/axv-host/control.sock'
      ) {
        await expect(
          manager.getByText(
            'Connection checked. The default socket is missing.',
            { exact: true },
          ),
        ).toBeVisible();
        await manager
          .getByRole('button', { name: 'Diagnostics', exact: true })
          .click();
        const dialog = page.getByRole('dialog', { name: 'VM Diagnostics' });
        await expect(
          dialog.getByText(blocked!.detail, { exact: true }),
        ).toBeVisible();
        await dialog
          .getByRole('button', { name: 'Close', exact: true })
          .click();
      } else {
        await expect(
          manager.getByText(blocked!.detail, { exact: true }),
        ).toBeVisible();
      }
      await expect(connect).toHaveCount(0);
      await expect(review).toBeEnabled();
    }
  } else {
    await expect(
      manager.getByRole('heading', { name: 'Review VM preparation' }),
    ).toBeVisible({ timeout: 30000 });
    await expect(
      manager.getByRole('heading', { name: 'Actions', exact: true }),
    ).toBeVisible();
    await expect(
      manager.getByRole('button', { name: /^(Prepare|Connect)$/ }),
    ).toBeVisible();
  }
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
  await manager.getByRole('button', { name: 'VM Host', exact: true }).click();
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
