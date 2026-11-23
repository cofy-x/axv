import { randomUUID } from 'node:crypto';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import type { Locator, Page } from '@playwright/test';
import {
  actionMenu,
  expect,
  expectedDownloadFilename,
  menuItem,
  test,
} from './helpers';
import { choose } from './codex-helpers';
import {
  codeEditor,
  copyEditorText,
  expectEditorText,
} from './code-editor-helpers';

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

test('VM projects link to host setup when project actions are unavailable', async ({
  page,
  hasTouch,
}) => {
  await page.route('**/api/compute/info', (route) =>
    route.fulfill({
      json: { configured: false, reason: 'VM hosting is not configured.' },
    }),
  );
  const launcher = await openLauncher(page);
  await choose(
    launcher.getByRole('tab', { name: 'VM', exact: true }),
    hasTouch,
  );
  await expect(launcher.getByRole('alert')).toHaveText(
    'VM hosting is not configured.',
  );
  await expect(launcher.getByLabel('Project action')).toBeHidden();
  await choose(
    launcher.getByRole('button', { name: 'Open Virtual Machines' }),
    hasTouch,
  );
  await expect(page.locator('[data-app-id="machines"]')).toHaveClass(
    /is-active/,
  );
});

async function openRegisteredHostProject(
  page: Page,
  rootPath: string,
  touch: boolean,
) {
  const launcher = await openLauncher(page);
  await launcher
    .getByLabel('Host project directory', { exact: true })
    .fill(rootPath);
  const registration = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === '/api/projects' &&
      response.request().method() === 'POST',
  );
  await choose(
    launcher.getByRole('button', { name: 'Open', exact: true }),
    touch,
  );
  const response = await registration;
  expect(response.ok(), await response.text()).toBeTruthy();
  const { project } = (await response.json()) as {
    project: { id: string; rootPath: string };
  };
  return {
    project,
    browser: page.locator(`[data-window-id="project-files:${project.id}"]`),
  };
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
  const hostProjects = launcher.getByRole('list', {
    name: 'Registered host projects',
  });
  const hostRows = hostProjects.getByRole('button');
  await expect(hostRows.first()).toBeInViewport();
  await expect(hostPath).toBeInViewport();
  expect((await hostProjects.boundingBox())!.y).toBeLessThan(
    (await hostPath.boundingBox())!.y,
  );
  await hostPath.fill('/fixture/host-draft');
  await launcher.screenshot({
    path: testInfo.outputPath('project-launcher-host.png'),
  });
  await hostRows.last().focus();
  await expect(hostRows.last()).toBeFocused();
  await expect(hostRows.last()).toBeInViewport();
  await expect(hostPath).toBeInViewport();
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
  const vmProjects = launcher.getByRole('list', {
    name: 'Registered VM projects',
  });
  const savedVM = vmProjects.getByRole('button', {
    name: /Shared name · aaaaaaaa/,
  });
  await expect(savedVM).toBeInViewport();
  expect((await vmProjects.boundingBox())!.y).toBeLessThan(
    (await vmPath.boundingBox())!.y,
  );
  await launcher
    .getByRole('combobox', { name: 'Project volume' })
    .selectOption(volumeId);
  await vmPath.fill(' repo ');
  await launcher
    .getByLabel('Project name', { exact: true })
    .fill('Draft project');
  await expect(savedVM).toContainText('Work · 11111111 / repo');
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
  const welcome = page.locator('[data-app-id="welcome"]');
  const codex = welcome.getByRole('button', { name: 'Codex', exact: true });
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
    await codex.focus();
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
    await expect(codex).toBeFocused();
  } finally {
    release();
    await page.unroute(endpoint);
  }
});

test('host source highlighting remains read-only and treats HTML as literal text', async ({
  page,
  hasTouch,
}) => {
  const projectPath = process.env.E2E_CODEX_PROJECT || '';
  test.skip(
    !projectPath,
    'Host projects require the isolated POSIX workspace.',
  );
  const suffix = randomUUID();
  const samples = [
    {
      name: `readonly-${suffix}.ts`,
      content: [
        '// Host source is displayed without running it.',
        'export const markup = \'<script>globalThis.axvHostSourceExecuted = true</script><img src="/axv-host-source-probe" onerror="globalThis.axvHostSourceExecuted = true">\';',
        'export const enabled: boolean = true;',
        '',
      ].join('\n'),
      highlighted: true,
    },
    {
      name: `readonly-${suffix}.axvtext`,
      content:
        '<script>globalThis.axvHostSourceExecuted = true</script>\nUnknown extension: keep this source as plain text.\n',
      highlighted: false,
    },
  ];
  const paths = samples.map((sample) => join(projectPath, sample.name));
  const createdPaths: string[] = [];
  const probeRequests: string[] = [];
  page.on('request', (request) => {
    if (new URL(request.url()).pathname === '/axv-host-source-probe')
      probeRequests.push(request.url());
  });
  try {
    for (const [index, sample] of samples.entries()) {
      await writeFile(paths[index], sample.content, { flag: 'wx' });
      createdPaths.push(paths[index]);
    }
    const { browser } = await openRegisteredHostProject(
      page,
      projectPath,
      hasTouch,
    );
    for (const [index, sample] of samples.entries()) {
      if (index && hasTouch)
        await choose(
          browser.getByRole('button', { name: 'Back', exact: true }),
          true,
        );
      await choose(
        browser.getByRole('button', { name: sample.name, exact: true }),
        hasTouch,
      );
      const source = codeEditor(browser, 'Project file source');
      await expect(source).toBeVisible();
      await expect(source).toHaveAttribute('aria-readonly', 'true');
      await expectEditorText(source, sample.content);
      expect(await copyEditorText(source)).toBe(sample.content);
      if (sample.highlighted)
        await expect
          .poll(() =>
            source
              .locator('span')
              .evaluateAll(
                (spans) =>
                  new Set(
                    spans
                      .filter((span) => span.textContent)
                      .map((span) => getComputedStyle(span).color),
                  ).size,
              ),
          )
          .toBeGreaterThan(1);
      await source.press('ControlOrMeta+End');
      await source.pressSequentially('READ_ONLY_PROBE');
      await expectEditorText(source, sample.content);
      await expect(
        browser
          .getByRole('region', { name: 'Project file preview' })
          .locator('script, img'),
      ).toHaveCount(0);
      expect(
        await page.evaluate(() =>
          Reflect.get(globalThis, 'axvHostSourceExecuted'),
        ),
      ).toBeUndefined();
      expect(await readFile(paths[index])).toEqual(Buffer.from(sample.content));
    }
    expect(probeRequests).toEqual([]);
  } finally {
    await Promise.all(createdPaths.map((path) => rm(path, { force: true })));
  }
});

test('project download reports API failures inline and preserves the original file bytes', async ({
  page,
  browserName,
  hasTouch,
}) => {
  const projectPath = process.env.E2E_CODEX_PROJECT || '';
  test.skip(
    !projectPath,
    'Host projects require the isolated POSIX workspace.',
  );
  const path = `download-${randomUUID()}.md`;
  const fixture = join(projectPath, path);
  const content = '# Original project file\n\nRECOVERED_ORIGINAL';
  const expectedFilename = expectedDownloadFilename(path, browserName);
  await writeFile(fixture, content, { flag: 'wx' });
  try {
    const { browser, project } = await openRegisteredHostProject(
      page,
      projectPath,
      hasTouch,
    );
    await choose(
      browser
        .locator('.project-directory')
        .getByRole('button', { name: path, exact: true }),
      hasTouch,
    );
    await expect(
      browser.getByRole('heading', { name: 'Original project file' }),
    ).toBeVisible();
    const originalURL = page.url();
    let downloads = 0;
    page.on('download', () => downloads++);
    await page.route(
      `**/api/projects/${project.id}/download?*`,
      async (route) => {
        await route.fulfill({
          status: 409,
          json: {
            code: 'file_changed',
            error:
              'This file changed before the download. Refresh and try again.',
          },
        });
      },
      { times: 1 },
    );
    const more = browser.getByRole('button', {
      name: 'More project actions',
      exact: true,
    });
    await actionMenu(
      page,
      browser,
      'More project actions',
      'Download',
      hasTouch,
    );
    await expect(browser.getByRole('alert')).toHaveText(
      'This file changed before the download. Refresh and try again.',
    );
    await expect(page).toHaveURL(originalURL);
    await expect(browser).toBeVisible();
    await expect(more).toBeEnabled();
    expect(downloads).toBe(0);
    await choose(more, hasTouch);
    const downloadItem = page
      .locator('.ns-menu-layer')
      .getByRole('menuitem', { name: 'Download', exact: true });
    await expect(downloadItem).toBeEnabled();
    const downloaded = page.waitForEvent('download');
    await choose(downloadItem, hasTouch);
    const download = await downloaded;
    expect(download.suggestedFilename()).toBe(expectedFilename);
    const saved = await download.path();
    expect(saved).not.toBeNull();
    expect(await readFile(saved!)).toEqual(Buffer.from(content));
    expect(downloads).toBe(1);
    await expect(page).toHaveURL(originalURL);
    await expect(browser.getByRole('alert')).toHaveCount(0);

    await page.route(`**/api/projects/${project.id}/file?*`, async (route) => {
      await route.fulfill({
        status: 415,
        json: {
          code: 'unsupported_file',
          error: 'This file cannot be previewed.',
        },
      });
    });
    await choose(
      browser.getByRole('button', { name: 'Refresh', exact: true }),
      hasTouch,
    );
    await expect(browser.getByRole('alert')).toHaveText(
      'This file cannot be previewed.',
    );
    const fallbackDownload = browser.getByRole('button', {
      name: 'Download',
      exact: true,
    });
    await expect(fallbackDownload).toBeInViewport();
    const fallbackDownloaded = page.waitForEvent('download');
    await choose(fallbackDownload, hasTouch);
    const fallback = await fallbackDownloaded;
    expect(fallback.suggestedFilename()).toBe(expectedFilename);
    const fallbackPath = await fallback.path();
    expect(fallbackPath).not.toBeNull();
    expect(await readFile(fallbackPath!)).toEqual(Buffer.from(content));
    expect(downloads).toBe(2);
    await expect(browser.getByRole('alert')).toHaveText(
      'This file cannot be previewed.',
    );

    await page.evaluate(() => {
      const attempts: string[] = [];
      Reflect.set(globalThis, 'closedProjectDownloadRequests', attempts);
      const fetch = globalThis.fetch;
      globalThis.fetch = (...args) => {
        const input = args[0];
        const url = input instanceof Request ? input.url : String(input);
        if (url.includes('/download?')) attempts.push(url);
        return fetch(...args);
      };
    });
    await choose(more, hasTouch);
    await expect(downloadItem).toBeVisible();
    // Close the owner while the menu is active.
    await browser.locator('.ns-close').evaluate((button) => {
      (button as HTMLButtonElement).click();
    });
    await expect(browser).toHaveCount(0);
    await expect(page.locator('.ns-menu-layer')).toHaveCount(0);
    expect(
      await page.evaluate(() =>
        Reflect.get(globalThis, 'closedProjectDownloadRequests'),
      ),
    ).toEqual([]);
    expect(downloads).toBe(2);
  } finally {
    await rm(fixture, { force: true });
  }
});

test('host project paths preserve trailing spaces through registration and the folder chooser', async ({
  page,
  request,
  hasTouch,
}) => {
  const projectPath = process.env.E2E_CODEX_PROJECT || '';
  test.skip(
    !projectPath,
    'Host projects require the isolated POSIX workspace.',
  );
  const fixture = await mkdtemp(join(projectPath, 'path-identity-'));
  const exact = join(fixture, 'repo ');
  const sibling = join(fixture, 'repo');
  try {
    await Promise.all([mkdir(exact), mkdir(sibling)]);
    const response = await request.get('/api/host/directories', {
      params: { path: exact },
    });
    expect(response.ok()).toBeTruthy();
    expect((await response.json()).path).toBe(exact);

    const { project, browser } = await openRegisteredHostProject(
      page,
      exact,
      hasTouch,
    );
    expect(project.rootPath).toBe(exact);
    await choose(browser.getByRole('button', { name: /^Close / }), hasTouch);

    const launcher = page.locator('[data-window-id="project-files:projects"]');
    await choose(
      launcher.getByRole('button', { name: 'Choose Folder', exact: true }),
      hasTouch,
    );
    const picker = launcher.getByRole('region', {
      name: 'Choose a host folder',
    });
    const path = picker.getByRole('textbox', { name: 'Folder path' });
    const useFolder = picker.getByRole('button', { name: 'Use Folder' });
    await expect(useFolder).toBeEnabled();
    await expect(path).toHaveValue(exact);

    await choose(
      picker.getByRole('button', { name: 'Up', exact: true }),
      hasTouch,
    );
    await expect(path).toHaveValue(fixture);
    await path.fill(exact);
    await choose(
      picker.getByRole('button', { name: 'Go', exact: true }),
      hasTouch,
    );
    await expect(useFolder).toBeEnabled();
    await expect(path).toHaveValue(exact);
    await choose(useFolder, hasTouch);
    await expect(launcher.getByLabel('Host project directory')).toHaveValue(
      exact,
    );
  } finally {
    await rm(fixture, { recursive: true });
  }
});
