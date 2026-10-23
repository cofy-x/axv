import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import type { Locator, Page } from '@playwright/test';
import { expect, menuItem, test } from './helpers';

const hostId = 'launcher-host';
const volumeId = '11111111-1111-4111-8111-111111111111';
const machineId = 'launcher-machine';

async function launcherFixture(page: Page) {
  const registrations: Record<string, unknown>[] = [];
  let release: (() => void) | undefined;
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/projects', (route) =>
    route.fulfill({
      json: {
        projects: Array.from({ length: 24 }, (_, index) => ({
          id: `host-project-${index}`,
          kind: 'host',
          name: `Host project ${index}`,
          rootPath: `/fixture/host-project-${index}`,
          createdAt: '2026-10-21T00:00:00Z',
        })),
      },
    }),
  );
  await page.route('**/api/host/directories**', (route) =>
    route.fulfill({
      json: {
        path: '/fixture/host-draft',
        parentPath: '/fixture',
        homePath: '/fixture',
        directories: [],
        truncated: false,
      },
    }),
  );
  await page.route('**/api/compute/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    const json = (value: unknown, status = 200) =>
      route.fulfill({ json: value, status });
    if (path === '/api/compute/info')
      return json({ configured: true, hostId, providers: [], images: [] });
    if (path === '/api/compute/machines')
      return json({
        machines: [
          {
            id: machineId,
            hostId,
            spec: {
              name: 'Project VM',
              provider: 'fixture',
              kind: 'development',
              resources: { cpus: 2, memoryMiB: 1024, diskGiB: 8 },
              imageId: 'fixture-image',
              projectGiB: 4,
              retainVolume: true,
            },
            revision: 1,
            desired: 'running',
            status: 'running',
            ready: true,
            guestState: 'ready',
            guestCapabilities: ['project-files'],
            reserved: true,
            createdAt: '2026-10-21T00:00:00Z',
            updatedAt: '2026-10-21T00:00:00Z',
          },
        ],
      });
    if (path === '/api/compute/volumes')
      return json({
        volumes: [volumeId, '22222222-2222-4222-8222-222222222222'].map(
          (id) => ({
            id,
            name: 'Work',
            revision: 4,
            provider: 'fixture',
            sizeGiB: 4,
            retain: true,
            machineId,
            status: 'ready',
          }),
        ),
      });
    if (path !== '/api/compute/projects') return route.continue();
    expect(route.request().headers()['x-axv-host-id']).toBe(hostId);
    if (route.request().method() === 'POST') {
      registrations.push(route.request().postDataJSON());
      if (registrations.length === 1) await pending;
      return json(
        { code: 'project_unavailable', error: 'The project is unavailable.' },
        503,
      );
    }
    return json({
      projects: [
        ['aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', volumeId],
        [
          'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
          '22222222-2222-4222-8222-222222222222',
        ],
      ].map(([id, projectVolume]) => ({
        id,
        hostId,
        kind: 'vm',
        name: 'Shared name',
        volumeId: projectVolume,
        root: 'repo',
        rootIdentity: `root-${id}`,
        revision: 1,
        createdAt: '2026-10-21T00:00:00Z',
      })),
    });
  });
  return { registrations, release: () => release?.() };
}

async function openLauncher(page: Page) {
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await menuItem(page, 'Project Files').click();
  return page.locator('[data-window-id="project-files:projects"]');
}

async function expectOnlyLauncherCode(page: Page) {
  const reportPath = process.env.AXV_BUNDLE_REPORT
    ? resolve(process.env.AXV_BUNDLE_REPORT)
    : resolve(
        process.env.AXV_TEST_TMPDIR || '.data/test',
        'dashboard-bundle.json',
      );
  const report = JSON.parse(await readFile(reportPath, 'utf8')) as {
    chunks: {
      fileName: string;
      modules: { id: string; renderedLength: number }[];
    }[];
  };
  const workspaceSources = [
    'VMProjectFiles.tsx',
    'VMProjectTasks.tsx',
    'HostProjectFiles.tsx',
  ].map((name) => `apps/dashboard/src/features/project-files/${name}`);
  const workspaceAssets = report.chunks.filter((chunk) =>
    chunk.modules.some(
      (module) =>
        module.renderedLength > 0 &&
        (workspaceSources.includes(module.id) ||
          module.id.includes('react-markdown')),
    ),
  );
  expect(workspaceAssets.length).toBeGreaterThan(0);
  const requested = await page.evaluate(() =>
    performance
      .getEntriesByType('resource')
      .map((entry) => new URL(entry.name).pathname),
  );
  expect(
    workspaceAssets
      .map((chunk) => `/${chunk.fileName}`)
      .filter((path) => requested.includes(path)),
    'Choosing a project must not download unopened file, task or Markdown views.',
  ).toEqual([]);
}

test('project location switching preserves both forms and an in-flight registration', async ({
  page,
  isMobile,
}, testInfo) => {
  const fixture = await launcherFixture(page);
  const launcher = await openLauncher(page);
  await expect(
    launcher.getByRole('tablist', { name: 'Project location' }),
  ).toBeVisible();
  await expectOnlyLauncherCode(page);
  const choose = (control: Locator) =>
    isMobile ? control.tap() : control.click();
  const locations = launcher.getByRole('tablist', { name: 'Project location' });
  const host = locations.getByRole('tab', { name: 'Host', exact: true });
  const vm = locations.getByRole('tab', { name: 'VM', exact: true });
  const hostPath = launcher.getByLabel('Host project directory', {
    exact: true,
  });
  const vmPath = launcher.getByLabel('Directory', { exact: true });
  await expect(host).toHaveAttribute('aria-selected', 'true');
  await expect(vm).toBeInViewport();
  await hostPath.fill('/fixture/host-draft');
  await launcher.screenshot({
    path: testInfo.outputPath('project-launcher-host.png'),
  });
  await choose(
    launcher.getByRole('button', { name: 'Choose Folder', exact: true }),
  );
  const picker = launcher.getByRole('region', {
    name: 'Choose a host folder',
    includeHidden: true,
  });
  const pickerPath = picker.getByRole('textbox', {
    name: 'Folder path',
    includeHidden: true,
  });
  await expect(pickerPath).toBeEnabled();
  await pickerPath.fill('/fixture/unfinished-picker');
  if (isMobile) await choose(vm);
  else {
    await host.focus();
    await host.press('ArrowRight');
  }
  await expect(vm).toBeFocused();
  await expect(hostPath).toBeHidden();
  await pickerPath.evaluate((element) => (element as HTMLInputElement).focus());
  await expect(vm).toBeFocused();
  await launcher
    .getByRole('combobox', { name: 'Project volume' })
    .selectOption(volumeId);
  await vmPath.fill(' repo ');
  await launcher
    .getByLabel('Project name', { exact: true })
    .fill('Draft project');
  await expect(
    launcher.getByRole('button', { name: /Shared name · aaaaaaaa/ }),
  ).toContainText('Work · 11111111 / repo');
  await expect(
    launcher.getByRole('button', { name: /Shared name · bbbbbbbb/ }),
  ).toContainText('Work · 22222222 / repo');
  await launcher.screenshot({
    path: testInfo.outputPath('project-launcher-vm.png'),
  });
  await choose(host);
  await expect(hostPath).toHaveValue('/fixture/host-draft');
  await expect(pickerPath).toHaveValue('/fixture/unfinished-picker');
  await expect(vmPath).toBeHidden();
  await vmPath.evaluate((element) => (element as HTMLInputElement).focus());
  await expect(host).toBeFocused();
  await choose(vm);
  await expect(vmPath).toHaveValue(' repo ');
  await expect(
    launcher.getByLabel('Project name', { exact: true }),
  ).toHaveValue('Draft project');
  await choose(launcher.getByRole('button', { name: 'Open', exact: true }));
  await expect.poll(() => fixture.registrations.length).toBe(1);
  try {
    await choose(host);
    await choose(vm);
    await expect(
      launcher.getByRole('button', { name: 'Opening…', exact: true }),
    ).toBeDisabled();
    expect(fixture.registrations).toHaveLength(1);
  } finally {
    fixture.release();
  }
  await expect(launcher.getByRole('alert')).toHaveText(
    'The project is unavailable.',
  );
  await choose(host);
  await choose(vm);
  await choose(launcher.getByRole('button', { name: 'Open', exact: true }));
  await expect.poll(() => fixture.registrations.length).toBe(2);
  expect(fixture.registrations[1]).toEqual(fixture.registrations[0]);
  expect(fixture.registrations[0]).toMatchObject({
    volumeId,
    volumeRevision: 4,
    machineId,
    root: ' repo ',
    name: 'Draft project',
    requestId: expect.any(String),
  });
  await expectOnlyLauncherCode(page);
});
