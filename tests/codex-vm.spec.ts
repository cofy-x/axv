import { expect, launch, test } from './helpers';

const hostId = 'host-vm-codex-test';
const machineId = 'vm-dev-box';

function mockSession(overrides: Record<string, unknown> = {}) {
  return {
    id: 'sess-vm-1',
    runId: 'run-vm-11112222',
    target: 'host',
    projectPath: '/projects/demo-app',
    title: 'VM refactor',
    metadataRevision: 1,
    tmuxSession: 'axv-codex-vm',
    tmuxSocket: '',
    attachCommand: 'tmux attach -t axv-codex-vm',
    status: 'running',
    createdAt: '2026-11-27T10:00:00Z',
    connections: 0,
    continuation: { state: 'active', activeRunId: 'run-vm-11112222' },
    activity: {
      observation: { instanceId: 'inst-vm-1', revision: 1 },
      state: 'idle',
      needsAttention: false,
    },
    ...overrides,
  };
}

test('unified Codex manager lists Host and VM sessions, installs VM Codex CLI, and starts a VM session', async ({
  page,
}) => {
  let installed = false;
  let installCalls = 0;
  const vmSessions: Record<string, unknown>[] = [
    mockSession({
      id: 'sess-vm-existing',
      runId: 'run-vm-existing1',
      title: 'Existing VM task',
    }),
  ];
  const hostSessions = [
    mockSession({
      id: 'sess-host-1',
      runId: 'run-host-1111',
      projectPath: '/tmp/host-repo',
      title: 'Host review',
    }),
  ];

  await page.route('**/api/host/codex/info', (route) =>
    route.fulfill({
      json: {
        supported: true,
        installed: true,
        ready: true,
        codexVersion: 'codex-cli 0.172.0',
        tmuxVersion: 'tmux 3.4',
        guidance: [],
        defaultProjectPath: '/tmp/host-repo',
        configPath: '/tmp/axv.json',
        network: { proxySource: 'direct' },
        vmProxy: {
          enabled: true,
          baseUrl: 'https://api.openai.com/v1',
          source: 'axv_env',
        },
      },
    }),
  );
  await page.route('**/api/host/codex/sessions', (route) =>
    route.fulfill({ json: { sessions: hostSessions } }),
  );
  await page.route('**/api/host/codex/projects', (route) =>
    route.fulfill({ json: { projects: [] } }),
  );
  await page.route('**/api/compute/machines', (route) =>
    route.fulfill({
      json: {
        machines: [
          {
            id: machineId,
            hostId,
            spec: {
              name: 'dev-box',
              provider: 'firecracker',
              imageId: 'axv-base',
              kind: 'development',
              resources: { cpus: 2, memoryMiB: 2048, diskGiB: 10 },
              projectGiB: 10,
              volumeId: 'vol-demo',
              retainVolume: true,
            },
            revision: 1,
            desired: 'running',
            status: 'running',
            ready: true,
            guestState: 'ready',
            guestCapabilities: ['exec', 'terminals', 'project-files', 'codex'],
            reserved: false,
            createdAt: '2026-11-27T09:00:00Z',
            updatedAt: '2026-11-27T09:00:00Z',
          },
        ],
      },
    }),
  );
  await page.route('**/api/compute/projects', (route) =>
    route.fulfill({
      json: {
        projects: [
          {
            id: 'proj-demo',
            hostId,
            kind: 'vm',
            name: 'demo-app',
            volumeId: 'vol-demo',
            root: 'demo-app',
            rootIdentity: 'id-demo',
            revision: 1,
            createdAt: '2026-11-27T09:00:00Z',
          },
        ],
      },
    }),
  );
  await page.route(
    `**/api/compute/machines/${machineId}/guest/codex/info`,
    (route) =>
      route.fulfill({
        json: {
          supported: true,
          installed,
          ready: installed,
          codexVersion: installed ? 'codex-cli 0.172.0' : '',
          tmuxVersion: 'tmux 3.4',
          reason: installed ? '' : 'The official Codex CLI is not installed.',
          guidance: [],
          defaultProjectPath: '/projects',
          configPath: '',
          network: { proxySource: 'direct' },
          authMode: 'proxy',
          hostProxyAvailable: true,
          fullAutoDefault: true,
          install: {
            state: installed ? 'succeeded' : 'idle',
            message: installed
              ? 'Official Codex CLI installed in ~/.local/bin/codex.'
              : '',
          },
        },
      }),
  );
  await page.route(
    `**/api/compute/machines/${machineId}/guest/codex/install`,
    (route) => {
      if (route.request().method() === 'POST') {
        installCalls++;
        installed = true;
        return route.fulfill({
          status: 202,
          json: {
            state: 'running',
            message: 'Installing official Codex CLI into VM root disk…',
          },
        });
      }
      return route.fulfill({
        json: {
          state: 'succeeded',
          message: 'Official Codex CLI installed in ~/.local/bin/codex.',
        },
      });
    },
  );
  await page.route(
    `**/api/compute/machines/${machineId}/guest/codex/sessions`,
    (route) => {
      if (route.request().method() === 'POST') {
        const body = route.request().postDataJSON() as {
          projectPath?: string;
        };
        const created = mockSession({
          id: 'sess-vm-new',
          runId: 'run-vm-new99999',
          projectPath: body.projectPath || '/projects/demo-app',
          title: 'demo-app',
        });
        vmSessions.unshift(created);
        return route.fulfill({ status: 201, json: { session: created } });
      }
      return route.fulfill({ json: { sessions: vmSessions } });
    },
  );
  await page.route(
    `**/api/compute/machines/${machineId}/guest/codex/sessions/sess-vm-new/runs/run-vm-new99999`,
    (route) => route.fulfill({ json: { session: vmSessions[0] } }),
  );

  await page.goto('/');
  await launch(page, 'codex');
  const manager = page.locator('.ns-window', {
    has: page.getByTestId('codex-manager'),
  });
  await expect(manager).toBeVisible();

  // Both Host and VM sessions appear with clear target badges.
  await expect(manager.getByText('Host review')).toBeVisible();
  await expect(manager.getByText('Existing VM task')).toBeVisible();
  await expect(manager.getByText('VM · dev-box')).toBeVisible();

  // Target filter switches cleanly between VM and Host.
  await manager.locator('[data-target-filter="vm"]').click();
  await expect(manager.getByText('Existing VM task')).toBeVisible();
  await expect(manager.getByText('Host review')).toHaveCount(0);
  await manager.locator('[data-target-filter="all"]').click();

  // Open New in VM panel, install CLI on demand, verify zero-credential proxy badge, and launch.
  await manager.getByRole('button', { name: 'New in VM' }).click();
  const picker = manager.getByTestId('codex-vm-picker');
  await expect(picker).toBeVisible();
  await expect(picker).toContainText('VM Full-Auto Sandbox');
  await expect(picker).toContainText('Zero-credential Host API Proxy');

  const startBtn = picker.getByRole('button', { name: 'Start in VM' });
  await expect(startBtn).toBeDisabled();

  await picker.getByRole('button', { name: 'Install Codex CLI in VM' }).click();
  await expect.poll(() => installCalls).toBe(1);
  await expect(startBtn).toBeEnabled({ timeout: 10_000 });

  await startBtn.click();

  // VM Codex session window opens with VM badge.
  const sessionWin = page.locator('.ns-window', {
    has: page.locator(
      '[data-testid="codex-session"][data-session-id="sess-vm-new"]',
    ),
  });
  await expect(sessionWin).toBeVisible();
  await expect(sessionWin.getByTestId('codex-target-badge')).toHaveText(
    'VM · dev-box',
  );
});
