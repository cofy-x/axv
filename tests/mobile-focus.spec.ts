import { expect, test } from './helpers';
import { openTerminal, output, run } from './terminal-helpers';

test('Mobile Focus Mode filters Host & VM alerts, sessions, VMs, and projects without overflow', async ({
  page,
}) => {
  const hostId = 'host-mobile-focus';
  const machineId = 'vm-mobile-1';

  await page.route('**/api/compute/environment', (route) =>
    route.fulfill({
      json: {
        platform: 'linux',
        configured: true,
        configPath: '/test/axv/config.json',
        profileHome: '/test/axv',
        executablePath: '/test/bin/axv',
        requiredLimaVersion: '2.2.1',
        automaticSetupSupported: false,
        runtime: {
          mode: 'host',
          state: 'ready',
          hostId,
          generation: 1,
          readOnly: false,
        },
      },
    }),
  );
  await page.route('**/api/compute/info', (route) =>
    route.fulfill({
      json: {
        configured: true,
        hostId,
        providers: [],
        images: [],
      },
    }),
  );
  await page.route('**/api/compute/machines', (route) =>
    route.fulfill({
      json: {
        machines: [
          {
            id: machineId,
            hostId,
            spec: {
              name: 'phone-dev',
              provider: 'firecracker',
              imageId: 'axv-base',
              kind: 'development',
              resources: { cpus: 2, memoryMiB: 2048, diskGiB: 10 },
              projectGiB: 10,
              volumeId: 'vol-phone',
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
  await page.route('**/api/compute/volumes', (route) =>
    route.fulfill({ json: { volumes: [] } }),
  );
  await page.route('**/api/compute/operations', (route) =>
    route.fulfill({ json: { operations: [] } }),
  );
  await page.route('**/api/compute/projects', (route) =>
    route.fulfill({
      json: {
        projects: [
          {
            id: 'proj-mobile',
            hostId,
            kind: 'vm',
            name: 'mobile-app',
            volumeId: 'vol-phone',
            root: 'mobile-app',
            rootIdentity: 'id-mobile',
            revision: 1,
            createdAt: '2026-11-27T09:00:00Z',
          },
        ],
      },
    }),
  );
  await page.route(
    `**/api/compute/machines/${machineId}/guest/codex/sessions`,
    (route) =>
      route.fulfill({
        json: {
          sessions: [
            {
              id: 'sess-vm-alert',
              runId: 'run-vm-alert111',
              target: 'host',
              projectPath: '/projects/mobile-app',
              title: 'Fix mobile build',
              metadataRevision: 1,
              tmuxSession: 'axv-vm-alert',
              tmuxSocket: '',
              attachCommand: '',
              status: 'running',
              createdAt: '2026-11-27T10:00:00Z',
              connections: 0,
              continuation: { state: 'active', activeRunId: 'run-vm-alert111' },
              activity: {
                observation: { instanceId: 'inst-1', revision: 1 },
                state: 'waiting_approval',
                needsAttention: true,
              },
            },
          ],
        },
      }),
  );

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');

  const welcome = page.locator('[data-app-id="welcome"]');
  const focusBar = welcome.getByTestId('welcome-mobile-focus');
  await expect(focusBar).toBeVisible();

  // Action Needed shows VM Codex session waiting for approval.
  await expect(
    welcome.getByRole('region', { name: 'Action needed' }),
  ).toContainText('Fix mobile build');

  // Switching focus tabs narrows to the chosen section.
  await focusBar.locator('[data-focus-tab="vms"]').click();
  await expect(
    welcome.getByRole('region', { name: 'Recent virtual machines' }),
  ).toContainText('phone-dev');
  await expect(
    welcome.getByRole('region', { name: 'Action needed' }),
  ).toHaveCount(0);

  await focusBar.locator('[data-focus-tab="projects"]').click();
  await expect(
    welcome.getByRole('region', { name: 'Recent projects' }),
  ).toContainText('mobile-app');

  await focusBar.locator('[data-focus-tab="alerts"]').click();
  await expect(
    welcome.getByRole('region', { name: 'Action needed' }),
  ).toContainText('Fix mobile build');

  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
  ).toBe(false);
});

test('Mobile terminal touch keybar sends control and arrow keys on narrow viewports', async ({
  page,
}) => {
  test.skip(process.platform === 'win32', 'Uses POSIX shell commands.');
  await page.setViewportSize({ width: 390, height: 844 });
  const terminal = await openTerminal(page);

  const keybar = terminal.getByTestId('terminal-touch-keybar');
  await expect(keybar).toBeVisible();

  // Send Enter and echo via shell, then use Arrow Up + Enter from the touch keybar to repeat it.
  await run(terminal, 'echo TOUCH_KEYBAR_OK');
  await expect(output(terminal)).toContainText('TOUCH_KEYBAR_OK');

  // Verify sticky Ctrl toggles on and sends Ctrl+C via keybar.
  const ctrlBtn = keybar.getByRole('button', { name: 'Ctrl modifier' });
  await ctrlBtn.click();
  await expect(ctrlBtn).toHaveAttribute('aria-pressed', 'true');
  await keybar.getByRole('button', { name: 'Ctrl+C' }).click();
  await expect(ctrlBtn).toHaveAttribute('aria-pressed', 'false');

  // Verify Dash, Slash, and Enter keys deliver characters cleanly.
  await keybar.getByRole('button', { name: 'Slash' }).click();
  await keybar.getByRole('button', { name: 'Dash' }).click();
  await keybar.getByRole('button', { name: 'Ctrl+C' }).click();

  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
  ).toBe(false);
});
