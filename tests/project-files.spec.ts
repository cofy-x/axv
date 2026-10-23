import { readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import type { Locator, Page } from '@playwright/test';
import {
  activate,
  createEntry,
  editorWindow,
  expect,
  filesWindow,
  menuItem,
  openDocument,
  readContent,
  test,
} from './helpers';
import type { Entry } from './helpers';

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

test('closing the host launcher keeps a completed registration without reopening its project', async ({
  page,
  request,
  hasTouch,
}) => {
  const projectPath = process.env.E2E_CODEX_PROJECT || '';
  test.skip(
    !projectPath,
    'Host projects require the isolated POSIX workspace.',
  );
  const entry = await createEntry(request, 'E2E Registration owner draft.txt');
  await page.reload();
  const editor = await openDocument(page, entry);
  const text = editor.getByRole('textbox', { name: 'Document text' });
  const draft = 'Keep the other document draft and focus.';
  await text.fill(draft);
  const launcher = await openLauncher(page);
  await launcher
    .getByLabel('Host project directory', { exact: true })
    .fill(projectPath);
  let release = () => {};
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  let committedProject: { id: string; rootPath: string } | undefined;
  const endpoint = (url: URL) => url.pathname === '/api/projects';
  await page.route(endpoint, async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    const response = await route.fetch();
    expect(response.ok(), await response.text()).toBeTruthy();
    committedProject = (await response.json()).project;
    await pending;
    await route.fulfill({ response });
  });
  try {
    const finished = page.waitForResponse(
      (response) =>
        endpoint(new URL(response.url())) &&
        response.request().method() === 'POST',
    );
    const open = launcher.getByRole('button', { name: 'Open', exact: true });
    if (hasTouch) await open.tap();
    else await open.click();
    await expect.poll(() => committedProject?.id).toBeTruthy();
    const close = launcher.getByRole('button', { name: /^Close / });
    if (hasTouch) await close.tap();
    else await close.click();
    await expect(launcher).toHaveCount(0);
    await activate(page, editor);
    await text.focus();
    release();
    await (await finished).finished();
    // Settle the completed fetch and any mistakenly launched view before
    // asserting absence; the backend commit alone precedes the held response.
    await page.waitForLoadState('networkidle');
    const projects = await request.get('/api/projects');
    expect(projects.ok()).toBeTruthy();
    expect((await projects.json()).projects).toContainEqual(
      expect.objectContaining(committedProject!),
    );
    await expect(page.locator('[data-app-id="project-files"]')).toHaveCount(0);
    await expect(text).toBeFocused();
    await expect(text).toHaveValue(draft);
    expect(await readContent(request, entry.id)).toBe('Initial text');
  } finally {
    release();
    await page.unroute(endpoint);
  }
});

for (const held of ['import', 'refresh'] as const) {
  test(`closing a host copy owner during ${held} preserves the copy without opening Notes`, async ({
    page,
    request,
    hasTouch,
  }) => {
    const projectPath = process.env.E2E_CODEX_PROJECT || '';
    test.skip(
      !projectPath,
      'Host projects require the isolated POSIX workspace.',
    );
    const entry = await createEntry(
      request,
      `E2E Copy ${held} owner draft.txt`,
    );
    await page.reload();
    const editor = await openDocument(page, entry);
    const text = editor.getByRole('textbox', { name: 'Document text' });
    const draft = `Keep the other draft while ${held} completes.`;
    await text.fill(draft);
    const sourceName = `E2E copied-${held}-${entry.id}.txt`;
    const source = `The acknowledged ${held} copy must remain available.`;
    await writeFile(join(projectPath, sourceName), source);
    const launcher = await openLauncher(page);
    await launcher
      .getByLabel('Host project directory', { exact: true })
      .fill(projectPath);
    const opened = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === '/api/projects' &&
        response.request().method() === 'POST',
    );
    const open = launcher.getByRole('button', { name: 'Open', exact: true });
    if (hasTouch) await open.tap();
    else await open.click();
    const openedResponse = await opened;
    expect(openedResponse.ok()).toBeTruthy();
    const { project } = (await openedResponse.json()) as {
      project: { id: string };
    };
    const browser = page.locator(
      `[data-window-id="project-files:${project.id}"]`,
    );
    await expect(browser).toBeVisible();
    const selected = browser.getByRole('button', {
      name: sourceName,
      exact: true,
    });
    if (hasTouch) await selected.tap();
    else await selected.click();
    const copy = browser.getByRole('button', {
      name: 'Copy to Documents',
      exact: true,
    });
    await expect(copy).toBeEnabled();
    let release = () => {};
    const pending = new Promise<void>((resolve) => {
      release = resolve;
    });
    let copied: Entry | undefined;
    let requestId = '';
    let refreshes = 0;
    const importEndpoint = (url: URL) =>
      url.pathname === `/api/projects/${project.id}/import`;
    const refreshEndpoint = (url: URL) => url.pathname === '/api/entries';
    await page.route(importEndpoint, async (route) => {
      requestId = route.request().postDataJSON().requestId;
      const response = await route.fetch();
      expect(response.ok(), await response.text()).toBeTruthy();
      copied = (await response.json()).entry;
      if (held === 'import') await pending;
      await route.fulfill({ response });
    });
    await page.route(refreshEndpoint, async (route) => {
      const response = await route.fetch();
      expect(response.ok(), await response.text()).toBeTruthy();
      refreshes++;
      if (held === 'refresh') await pending;
      await route.fulfill({ response });
    });
    try {
      if (hasTouch) await copy.tap();
      else await copy.click();
      await expect
        .poll(() => (held === 'import' ? copied?.id : refreshes))
        .toBeTruthy();
      const close = browser.getByRole('button', { name: /^Close / });
      if (hasTouch) await close.tap();
      else await close.click();
      await expect(browser).toHaveCount(0);
      await activate(page, editor);
      await text.focus();
      release();
      const copiedEntry = copied!;
      // The shared list update is the completion barrier after both awaited
      // operations, and proves refresh still runs when the owner has closed.
      await expect(
        filesWindow(page).getByRole('option', {
          name: copiedEntry.name,
          exact: true,
          includeHidden: true,
        }),
      ).toHaveCount(1);
      expect(refreshes).toBe(1);
      expect(await readContent(request, copiedEntry.id)).toBe(source);
      await expect
        .poll(() =>
          page.evaluate(
            ({ projectId, completedId }) => {
              const key = Object.keys(sessionStorage).find(
                (key) =>
                  key.startsWith('axv-requests:') &&
                  key.endsWith(`:project-import:${projectId}`),
              );
              if (!key)
                throw new Error(
                  'The transfer identity record was not created.',
                );
              return Object.values(
                JSON.parse(sessionStorage.getItem(key)!),
              ).includes(completedId);
            },
            { projectId: project.id, completedId: requestId },
          ),
        )
        .toBe(false);
      await expect(editorWindow(page, copiedEntry.id)).toHaveCount(0);
      await expect(browser).toHaveCount(0);
      await expect(text).toBeFocused();
      await expect(text).toHaveValue(draft);
      expect(await readContent(request, entry.id)).toBe('Initial text');
    } finally {
      release();
      await page.unroute(importEndpoint);
      await page.unroute(refreshEndpoint);
    }
  });
}
