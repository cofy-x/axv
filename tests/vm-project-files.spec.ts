import type { Page, Route } from '@playwright/test';
import {
  actionMenu,
  activate,
  expect,
  menuItem,
  persistedLayout,
  switchWorkspace,
  test,
} from './helpers';
import type {
  VMProject,
  VMProjectBinding,
} from '../apps/dashboard/src/features/project-files/vm-api';

const project: VMProject = {
  id: 'vm-project-one',
  hostId: 'vm-project-host',
  kind: 'vm',
  name: 'VM repository',
  volumeId: 'project-volume',
  root: 'repo',
  rootIdentity: 'root-identity',
  revision: 1,
  createdAt: '2026-10-19T00:00:00Z',
};
const originalBinding: VMProjectBinding = {
  hostId: project.hostId,
  projectId: project.id,
  projectRevision: 1,
  volumeId: project.volumeId,
  volumeRevision: 4,
  machineId: 'project-machine',
  bootId: 'original-boot',
  instanceId: 'original-instance',
};
const browserId = `project-files:vm:${project.hostId}:${project.id}`;
const browser = (page: Page) => page.locator(`[data-window-id="${browserId}"]`);

function blocksTabClose(page: Page) {
  return page.evaluate(() => {
    const event = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(event);
    return event.defaultPrevented;
  });
}

async function fixture(page: Page, registered = true) {
  let exists = registered;
  let binding = { ...originalBinding };
  let content = 'Original VM file\n';
  let sha256 = 'original-sha256';
  let removedPath = '';
  const writes: {
    binding: VMProjectBinding;
    path: string;
    content: string;
    expectedSha256: string;
  }[] = [];
  const registrations: unknown[] = [];
  const fileBindings: VMProjectBinding[] = [];
  let bindingRequests = 0;
  let stale = false;
  let nextWrite: ((route: Route) => Promise<void>) | undefined;
  await page.route('**/api/compute/**', async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const json = (value: unknown, status = 200) =>
      route.fulfill({ status, json: value });
    if (path === '/api/compute/info')
      return json({
        configured: true,
        hostId: project.hostId,
        providers: [],
        images: [],
      });
    if (path === '/api/compute/machines')
      return json({
        machines: [
          {
            id: originalBinding.machineId,
            hostId: project.hostId,
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
            createdAt: project.createdAt,
            updatedAt: project.createdAt,
          },
        ],
      });
    if (path === '/api/compute/volumes')
      return json({
        volumes: [
          {
            id: project.volumeId,
            name: 'Retained project',
            revision: originalBinding.volumeRevision,
            provider: 'fixture',
            sizeGiB: 4,
            retain: true,
            machineId: originalBinding.machineId,
            status: 'ready',
          },
        ],
      });
    if (!path.startsWith('/api/compute/projects')) return route.continue();
    expect(route.request().headers()['x-axv-host-id']).toBe(project.hostId);
    if (path === '/api/compute/projects') {
      if (route.request().method() === 'POST') {
        registrations.push(route.request().postDataJSON());
        exists = true;
        return json({ project }, 201);
      }
      return json({ projects: exists ? [project] : [] });
    }
    if (path.endsWith('/binding')) {
      bindingRequests++;
      return json({ project, binding });
    }
    const data = route.request().postDataJSON();
    fileBindings.push(data.binding);
    if (stale || data.binding.bootId !== binding.bootId)
      return json(
        {
          code: 'binding_changed',
          error: 'The VM project binding changed. Reconnect explicitly.',
        },
        409,
      );
    if (path.endsWith('/list'))
      return json({
        project,
        binding,
        entries: [
          {
            name: 'main.ts',
            path: 'main.ts',
            kind: 'file',
            size: content.length,
            modifiedAt: project.createdAt,
          },
          {
            name: 'linked',
            path: 'linked',
            kind: 'symlink',
            size: 0,
            modifiedAt: project.createdAt,
          },
          ...(data.path === ''
            ? [
                {
                  name: 'src',
                  path: 'src',
                  kind: 'folder',
                  size: 0,
                  modifiedAt: project.createdAt,
                },
              ]
            : []),
        ],
      });
    if (path.endsWith('/read')) {
      if (data.path === removedPath)
        return json({ code: 'not_found', error: 'The file was removed.' }, 404);
      return json({
        project,
        binding,
        path: data.path,
        content,
        sha256,
        size: content.length,
        modifiedAt: project.createdAt,
      });
    }
    if (path.endsWith('/write')) {
      writes.push(data);
      if (nextWrite) {
        const handle = nextWrite;
        nextWrite = undefined;
        return handle(route);
      }
      if (data.expectedSha256 !== sha256 && data.expectedSha256 !== '')
        return json(
          {
            code: 'revision_conflict',
            error: 'The file changed since it was read.',
          },
          409,
        );
      content = data.content;
      sha256 = `saved-sha256-${writes.length}`;
      if (data.path === removedPath) removedPath = '';
      return json({
        project,
        binding,
        path: data.path,
        content,
        sha256,
        size: content.length,
        modifiedAt: project.createdAt,
      });
    }
    if (path.endsWith('/mkdir'))
      return json({
        project,
        binding,
        name: data.path,
        path: data.path,
        kind: 'folder',
        size: 0,
        modifiedAt: project.createdAt,
      });
    return json({ project });
  });
  return {
    writes,
    registrations,
    fileBindings,
    get bindingRequests() {
      return bindingRequests;
    },
    setStale() {
      stale = true;
    },
    replaceVM() {
      stale = false;
      binding = {
        ...originalBinding,
        machineId: 'replacement-machine',
        bootId: 'replacement-boot',
        instanceId: 'replacement-instance',
        volumeRevision: 6,
      };
    },
    conflict() {
      sha256 = 'external-sha256';
    },
    removeFile(path: string) {
      removedPath = path;
    },
    delayWrite() {
      let release!: () => void;
      let received!: () => void;
      const pending = new Promise<void>((resolve) => {
        received = resolve;
      });
      const gate = new Promise<void>((resolve) => {
        release = resolve;
      });
      nextWrite = async (route) => {
        received();
        await gate;
        const data = route.request().postDataJSON();
        content = data.content;
        sha256 = 'delayed-sha256';
        await route.fulfill({
          json: {
            project,
            binding,
            path: data.path,
            content,
            sha256,
            size: content.length,
            modifiedAt: project.createdAt,
          },
        });
      };
      return { pending, release: () => release() };
    },
  };
}

async function launcher(page: Page) {
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await menuItem(page, 'Project Files').click();
  const projects = page.locator('[data-window-id="project-files:projects"]');
  await projects.getByRole('tab', { name: 'VM', exact: true }).click();
  return projects;
}

async function openProject(page: Page) {
  const projects = await launcher(page);
  await projects.getByRole('button', { name: /^VM repository ·/ }).click();
  await expect(browser(page)).toBeVisible();
  await browser(page)
    .getByRole('button', { name: 'main.ts', exact: true })
    .click();
  const text = browser(page).locator(
    'textarea[aria-label="VM project file text"]',
  );
  await expect(text).toHaveValue('Original VM file\n');
  return text;
}

test('VM project actions reopen Projects without closing the draft', async ({
  page,
  isMobile,
}) => {
  await fixture(page);
  const text = await openProject(page);
  await text.fill('Keep this project draft');
  const projects = page.locator('[data-window-id="project-files:projects"]');
  await activate(page, projects);
  await projects
    .getByRole('button', { name: 'Close Project Files', exact: true })
    .click();
  await expect(projects).toHaveCount(0);
  await activate(page, browser(page));
  await actionMenu(
    page,
    browser(page),
    'More VM project actions',
    'Projects',
    isMobile,
  );
  await expect(projects).toBeVisible();
  await expect(text).toHaveValue('Keep this project draft');
  await expect(text).toBeDisabled();
  await projects.getByRole('tab', { name: 'VM', exact: true }).click();
  await expect(
    projects.getByRole('button', { name: /^VM repository ·/ }),
  ).toBeVisible();
});

test('narrow VM project Back returns focus to the selected file', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await fixture(page);
  const text = await openProject(page);
  await text.fill('Keep the selected file draft');
  await browser(page)
    .getByRole('button', { name: 'Back', exact: true })
    .click();
  await expect(
    browser(page).getByRole('button', { name: 'main.ts', exact: true }),
  ).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(text).toBeVisible();
  await expect(text).toHaveValue('Keep the selected file draft');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(
    browser(page).getByRole('region', { name: 'VM project file editor' }),
  ).toBeFocused();
});

test('selecting the same VM file retries a failed read', async ({ page }) => {
  await fixture(page);
  const text = await openProject(page);
  await page.route(
    '**/api/compute/projects/*/files/read',
    (route) =>
      route.fulfill({
        status: 503,
        json: {
          code: 'project_unavailable',
          error: 'Transient guest file read failure.',
        },
      }),
    { times: 1 },
  );
  await actionMenu(page, browser(page), 'More VM project actions', 'Reload');
  await expect(browser(page).getByRole('alert')).toContainText(
    'Transient guest file read failure.',
  );
  const back = browser(page).getByRole('button', { name: 'Back', exact: true });
  if (await back.isVisible()) await back.click();
  await browser(page)
    .getByRole('button', { name: 'main.ts', exact: true })
    .click();
  await expect(text).toHaveValue('Original VM file\n');
  await expect(browser(page).getByRole('alert')).toHaveCount(0);
});

test('VM file recovery retries the original read and offers explicit reconnect for a changed binding', async ({
  page,
}) => {
  const state = await fixture(page);
  const text = await openProject(page);
  await expect(
    browser(page).getByRole('button', { name: 'Reconnect', exact: true }),
  ).toHaveCount(0);
  await expect(
    browser(page).getByRole('button', { name: 'Reload', exact: true }),
  ).toHaveCount(0);
  const reads: unknown[] = [];
  page.on('request', (request) => {
    if (new URL(request.url()).pathname.endsWith('/files/read'))
      reads.push(request.postDataJSON());
  });
  await page.route(
    '**/api/compute/projects/*/files/read',
    (route) =>
      route.fulfill({
        status: 503,
        json: {
          code: 'project_unavailable',
          error: 'The guest could not read this file.',
        },
      }),
    { times: 1 },
  );
  await actionMenu(page, browser(page), 'More VM project actions', 'Reload');
  await expect(browser(page).getByRole('alert')).toContainText(
    'The guest could not read this file.',
  );
  await browser(page)
    .getByRole('button', { name: 'Retry', exact: true })
    .click();
  await expect(text).toHaveValue('Original VM file\n');
  await expect(
    browser(page).getByRole('region', { name: 'VM project file editor' }),
  ).toBeFocused();
  expect(reads).toEqual([
    { binding: originalBinding, path: 'main.ts' },
    { binding: originalBinding, path: 'main.ts' },
  ]);
  expect(state.bindingRequests).toBe(1);

  await page.route(
    '**/api/compute/projects/*/files/read',
    (route) =>
      route.fulfill({
        status: 409,
        // Recovery follows the code and operation, independently of message wording.
        json: {
          code: 'binding_changed',
          error: 'The selected environment is unavailable.',
        },
      }),
    { times: 1 },
  );
  await actionMenu(page, browser(page), 'More VM project actions', 'Reload');
  await expect(
    browser(page).getByRole('button', { name: 'Retry', exact: true }),
  ).toHaveCount(0);
  await browser(page)
    .getByRole('button', { name: 'Reconnect', exact: true })
    .click();
  await expect(text).toHaveValue('Original VM file\n');
  expect(state.bindingRequests).toBe(2);
});

test('recreating the removed selected VM file opens its new contents', async ({
  page,
}) => {
  const state = await fixture(page);
  const text = await openProject(page);
  state.removeFile('main.ts');
  await actionMenu(page, browser(page), 'New', 'New File');
  const dialog = page.getByRole('dialog');
  await dialog
    .getByRole('textbox', { name: 'Name', exact: true })
    .fill('main.ts');
  await dialog.getByRole('button', { name: 'OK', exact: true }).click();
  await expect(text).toHaveValue('');
  expect(state.writes).toEqual([
    expect.objectContaining({
      path: 'main.ts',
      content: '',
      expectedSha256: '',
      binding: originalBinding,
    }),
  ]);
});

test('VM project registration retains host controls, exact root bytes and the reviewed volume revision', async ({
  page,
  isMobile,
}, testInfo) => {
  test.skip(
    isMobile,
    'Desktop registration; narrow editing is covered separately.',
  );
  const state = await fixture(page, false);
  const projects = await launcher(page);
  await projects.getByRole('tab', { name: 'Host', exact: true }).click();
  await expect(
    projects.getByLabel('Host project directory', { exact: true }),
  ).toBeVisible();
  await expect(
    projects.getByRole('button', { name: 'Open', exact: true }),
  ).toBeDisabled();
  await projects.getByRole('tab', { name: 'VM', exact: true }).click();
  await projects
    .getByRole('combobox', { name: 'Project volume', exact: true })
    .selectOption(project.volumeId);
  await projects
    .getByLabel('Directory in volume', { exact: true })
    .fill(' repo ');
  const launcherImage = testInfo.outputPath('vm-project-registration.png');
  await projects.screenshot({ path: launcherImage });
  await testInfo.attach('VM project registration', {
    path: launcherImage,
    contentType: 'image/png',
  });
  await projects.getByRole('button', { name: 'Open', exact: true }).click();
  await expect(browser(page)).toBeVisible();
  expect(state.registrations).toEqual([
    expect.objectContaining({
      volumeId: project.volumeId,
      volumeRevision: 4,
      machineId: originalBinding.machineId,
      root: ' repo ',
      requestId: expect.any(String),
    }),
  ]);
  await expect(
    browser(page).getByRole('button', {
      name: 'linked (symlink)',
      exact: true,
    }),
  ).toBeDisabled();
  await actionMenu(page, browser(page), 'New', 'New File');
  await page
    .getByRole('dialog')
    .getByRole('textbox', { name: 'Name', exact: true })
    .fill('new.ts');
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'OK', exact: true })
    .click();
  await expect(
    browser(page).getByRole('textbox', { name: 'VM project file text' }),
  ).toHaveValue('');
  expect(state.writes[0]).toMatchObject({
    path: 'new.ts',
    content: '',
    expectedSha256: '',
    binding: originalBinding,
  });
});

test('a delayed new-file response preserves navigation back to the original file', async ({
  page,
  isMobile,
}) => {
  test.skip(isMobile, 'Desktop navigation generation acceptance.');
  const state = await fixture(page);
  const text = await openProject(page);
  const delayed = state.delayWrite();
  await actionMenu(page, browser(page), 'New', 'New File');
  await page
    .getByRole('dialog')
    .getByRole('textbox', { name: 'Name', exact: true })
    .fill('late.ts');
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'OK', exact: true })
    .click();
  await delayed.pending;
  await browser(page).getByRole('button', { name: 'src', exact: true }).click();
  await browser(page).getByRole('button', { name: 'Up', exact: true }).click();
  await browser(page)
    .getByRole('button', { name: 'main.ts', exact: true })
    .click();
  await expect(text).toHaveValue('Original VM file\n');
  delayed.release();
  await expect(
    browser(page)
      .getByRole('status')
      .filter({ hasText: 'Created late.ts in VM.' }),
  ).toBeVisible();
  await expect(text).toHaveValue('Original VM file\n');
  await expect(
    browser(page).locator('.project-preview-heading code'),
  ).toHaveText('main.ts');
  expect(state.writes[0]).toMatchObject({
    path: 'late.ts',
    expectedSha256: '',
    binding: originalBinding,
  });
});

test('unreadable VM draft recovery is retained until explicit discard', async ({
  page,
  request,
  isMobile,
}) => {
  test.skip(isMobile, 'Desktop recovery corruption acceptance.');
  await fixture(page);
  const text = await openProject(page);
  await text.fill('Recoverable text');
  const corrupt = '{"editor":{"text":"Keep this unknown record"}';
  const key = await page.evaluate((replacement) => {
    const key = Object.keys(sessionStorage).find((name) =>
      name.includes('axv-vm-project-draft'),
    );
    if (!key) throw new Error('Expected a VM project draft record.');
    sessionStorage.setItem(key, replacement);
    return key;
  }, corrupt);
  await expect
    .poll(async () =>
      (await persistedLayout(page, request))?.windows.some(
        (item: { id: string }) => item.id === browserId,
      ),
    )
    .toBe(true);
  page.on('dialog', (dialog) => void dialog.accept());
  await page.reload();
  await expect(text).toBeDisabled();
  expect(await blocksTabClose(page)).toBe(true);
  await expect(
    browser(page).getByRole('button', { name: 'Save', exact: true }),
  ).toBeDisabled();
  expect(await page.evaluate((key) => sessionStorage.getItem(key), key)).toBe(
    corrupt,
  );
  await browser(page)
    .getByRole('button', { name: 'Discard Recovery', exact: true })
    .click();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  await expect(text).toBeDisabled();
  expect(await page.evaluate((key) => sessionStorage.getItem(key), key)).toBe(
    corrupt,
  );
  await browser(page)
    .getByRole('button', { name: 'Discard Recovery', exact: true })
    .click();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Discard', exact: true })
    .click();
  await expect(text).toBeEnabled();
  expect(await blocksTabClose(page)).toBe(false);
  expect(
    await page.evaluate((key) => sessionStorage.getItem(key), key),
  ).toBeNull();
  await text.fill('New draft after explicit discard');
  expect(
    await page.evaluate((key) => sessionStorage.getItem(key), key),
  ).toContain('New draft after explicit discard');
});

test('VM saves capture a snapshot and preserve newer typing and recovery after reload', async ({
  page,
  request,
  isMobile,
}) => {
  test.skip(
    isMobile,
    'Delayed snapshot acceptance is covered on desktop browsers.',
  );
  const state = await fixture(page);
  const text = await openProject(page);
  await text.fill('Snapshot to save');
  const delayed = state.delayWrite();
  await browser(page)
    .getByRole('button', { name: 'Save', exact: true })
    .click();
  await delayed.pending;
  await text.fill('Newer unsaved typing');
  delayed.release();
  await expect(
    browser(page)
      .getByRole('status')
      .filter({ hasText: 'Snapshot saved in VM' }),
  ).toBeVisible();
  await expect(text).toHaveValue('Newer unsaved typing');
  expect(state.writes[0]).toMatchObject({
    content: 'Snapshot to save',
    expectedSha256: 'original-sha256',
    binding: originalBinding,
  });
  await expect
    .poll(async () =>
      (await persistedLayout(page, request))?.windows.some(
        (item: { id: string }) => item.id === browserId,
      ),
    )
    .toBe(true);
  page.on('dialog', (dialog) => void dialog.accept());
  await page.reload();
  await expect(
    browser(page).getByRole('button', { name: 'Restore Draft', exact: true }),
  ).toBeVisible();
  expect(await blocksTabClose(page)).toBe(true);
  await browser(page)
    .getByRole('button', { name: 'Restore Draft', exact: true })
    .click();
  await expect(text).toHaveValue('Newer unsaved typing');
  await browser(page)
    .getByRole('button', { name: 'Save', exact: true })
    .click();
  await expect(
    browser(page).getByRole('status').filter({ hasText: 'Saved in VM.' }),
  ).toBeVisible();
  expect(state.writes[1]).toMatchObject({
    content: 'Newer unsaved typing',
    expectedSha256: 'delayed-sha256',
    binding: originalBinding,
  });
  expect(await blocksTabClose(page)).toBe(false);
});

test('stale VM bindings and conflicts keep drafts until an explicit reconnect', async ({
  page,
  isMobile,
}, testInfo) => {
  if (isMobile) await page.setViewportSize({ width: 390, height: 844 });
  const state = await fixture(page);
  const text = await openProject(page);
  await text.fill('Keep this draft');
  const editorImage = testInfo.outputPath('vm-project-editor.png');
  await browser(page).screenshot({ path: editorImage });
  await testInfo.attach('VM project editor', {
    path: editorImage,
    contentType: 'image/png',
  });
  await switchWorkspace(page, 2);
  await expect(text).toBeDisabled();
  await expect(text).toHaveValue('Keep this draft');
  await switchWorkspace(page, 1);
  await expect(text).toBeEnabled();
  state.conflict();
  await browser(page)
    .getByRole('button', { name: 'Save', exact: true })
    .click();
  await expect(browser(page).getByRole('alert')).toContainText(
    'The file changed',
  );
  await expect(text).toHaveValue('Keep this draft');
  expect(state.bindingRequests).toBe(1);
  await browser(page)
    .getByRole('button', { name: 'Reload', exact: true })
    .click();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  await expect(text).toHaveValue('Keep this draft');
  expect(state.bindingRequests).toBe(1);
  state.setStale();
  await browser(page)
    .getByRole('button', { name: 'Save', exact: true })
    .click();
  await expect(browser(page).getByRole('alert')).toContainText(
    'binding changed',
  );
  expect(state.writes).toHaveLength(1);
  expect(state.bindingRequests).toBe(1);
  await browser(page)
    .getByRole('button', { name: 'Reconnect', exact: true })
    .click();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  await expect(text).toHaveValue('Keep this draft');
  expect(state.bindingRequests).toBe(1);
  const beforeReplacement = state.fileBindings.length;
  state.replaceVM();
  await actionMenu(page, browser(page), 'More VM project actions', 'Reconnect');
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Discard', exact: true })
    .click();
  await expect(text).toHaveValue('Original VM file\n');
  await expect.poll(() => state.bindingRequests).toBe(2);
  await text.fill('Replacement VM edit');
  await browser(page)
    .getByRole('button', { name: 'Save', exact: true })
    .click();
  await expect(
    browser(page).getByRole('status').filter({ hasText: 'Saved in VM.' }),
  ).toBeVisible();
  expect(state.writes.at(-1)?.binding).toMatchObject({
    machineId: 'replacement-machine',
    bootId: 'replacement-boot',
    volumeId: project.volumeId,
  });
  expect(
    state.fileBindings
      .slice(0, beforeReplacement)
      .every((item) => item.bootId === originalBinding.bootId),
  ).toBe(true);
  expect(
    state.fileBindings
      .slice(beforeReplacement)
      .every((item) => item.bootId === 'replacement-boot'),
  ).toBe(true);
});
