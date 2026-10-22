import {
  codeEditor,
  copyEditorText,
  editVMFile,
  expectEditorText,
} from './code-editor-helpers';
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
const fileUpdates = (page: Page) =>
  browser(page).getByRole('status', { name: 'VM file updates', exact: true });

function blocksTabClose(page: Page) {
  return page.evaluate(() => {
    const event = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(event);
    return event.defaultPrevented;
  });
}

async function fixture(
  page: Page,
  registered = true,
  initialContent = 'Original VM file\n',
) {
  let exists = registered;
  let binding = { ...originalBinding };
  const disk = new Map([
    ['main.ts', { content: initialContent, sha256: 'original-sha256' }],
  ]);
  const writes: {
    binding: VMProjectBinding;
    path: string;
    content: string;
    expectedSha256: string;
  }[] = [];
  const registrations: unknown[] = [];
  const fileBindings: VMProjectBinding[] = [];
  const reads: { binding: VMProjectBinding; path: string }[] = [];
  let bindingRequests = 0;
  let stale = false;
  let nextWrite: ((route: Route) => Promise<void>) | undefined;
  let nextRead: ((route: Route) => Promise<void>) | undefined;
  let readFailure: { status: number; code: string; error: string } | null =
    null;
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
    if (path.endsWith('/read')) reads.push(data);
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
          ...Array.from(disk, ([path, file]) => ({
            name: path.slice(path.lastIndexOf('/') + 1),
            path,
            kind: 'file',
            size: file.content.length,
            modifiedAt: project.createdAt,
          })).filter(({ path }) =>
            data.path
              ? path.startsWith(`${data.path}/`) &&
                !path.slice(data.path.length + 1).includes('/')
              : !path.includes('/'),
          ),
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
      if (readFailure)
        return json(
          { code: readFailure.code, error: readFailure.error },
          readFailure.status,
        );
      if (nextRead) {
        const handle = nextRead;
        nextRead = undefined;
        return handle(route);
      }
      const file = disk.get(data.path);
      if (!file)
        return json({ code: 'not_found', error: 'The file was removed.' }, 404);
      return json({
        project,
        binding,
        path: data.path,
        ...file,
        size: file.content.length,
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
      const current = disk.get(data.path);
      if (
        (data.expectedSha256 === '' && current) ||
        (data.expectedSha256 !== '' && data.expectedSha256 !== current?.sha256)
      )
        return json(
          {
            code: 'file_conflict',
            error: 'The file changed since it was read.',
          },
          409,
        );
      const file = {
        content: data.content,
        sha256: `saved-sha256-${writes.length}`,
      };
      disk.set(data.path, file);
      return json({
        project,
        binding,
        path: data.path,
        ...file,
        size: file.content.length,
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
    reads,
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
      const file = disk.get('main.ts');
      if (!file) throw new Error('Expected the original fixture file.');
      disk.set('main.ts', { ...file, sha256: 'external-sha256' });
    },
    externalWrite(nextContent: string, nextSha256: string) {
      disk.set('main.ts', { content: nextContent, sha256: nextSha256 });
    },
    file(path: string) {
      const file = disk.get(path);
      return file ? { ...file } : undefined;
    },
    removeFile(path: string) {
      disk.delete(path);
    },
    failReads(status: number, code: string, error: string) {
      readFailure = { status, code, error };
    },
    resumeReads() {
      readFailure = null;
    },
    failNextRead(status: number, code: string, error: string) {
      nextRead = (route) => route.fulfill({ status, json: { code, error } });
    },
    delayRead() {
      let release!: () => void;
      let received!: () => void;
      const pending = new Promise<void>((resolve) => {
        received = resolve;
      });
      const gate = new Promise<void>((resolve) => {
        release = resolve;
      });
      nextRead = async (route) => {
        const path = route.request().postDataJSON().path;
        const file = disk.get(path);
        if (!file) throw new Error('Expected a file for the deferred read.');
        const value = {
          project,
          binding: { ...binding },
          path,
          ...file,
          size: file.content.length,
          modifiedAt: project.createdAt,
        };
        received();
        await gate;
        await route.fulfill({ json: value });
      };
      return { pending, release: () => release() };
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
        const file = { content: data.content, sha256: 'delayed-sha256' };
        disk.set(data.path, file);
        await route.fulfill({
          json: {
            project,
            binding,
            path: data.path,
            ...file,
            size: file.content.length,
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

async function openProjectFile(page: Page) {
  const projects = await launcher(page);
  await projects.getByRole('button', { name: /^VM repository ·/ }).click();
  await expect(browser(page)).toBeVisible();
  await browser(page)
    .getByRole('button', { name: 'main.ts', exact: true })
    .click();
  const text = codeEditor(browser(page));
  return text;
}

async function openProject(page: Page, expected = 'Original VM file\n') {
  const text = await openProjectFile(page);
  await expectEditorText(text, expected);
  return text;
}

test('VM code observes agent changes read-only before explicit manual editing', async ({
  page,
}) => {
  const state = await fixture(page);
  const text = await openProject(page);
  await expect(text).toHaveAttribute('aria-readonly', 'true');
  await expect(text).not.toBeEditable();
  await expect(
    browser(page).getByRole('button', { name: 'Save', exact: true }),
  ).toHaveCount(0);
  await text.focus();
  await text.pressSequentially('MUST_NOT_EDIT_THE_AGENT_RESULT');
  await text.press('ControlOrMeta+s');
  await expectEditorText(text, 'Original VM file\n');
  expect(state.writes).toHaveLength(0);

  const agentResult = 'export const result = "written by agent";\n';
  state.externalWrite(agentResult, 'agent-file-sha256');
  await expectEditorText(text, agentResult);
  await expect(text).toHaveAttribute('aria-readonly', 'true');
  await expect(text).not.toBeEditable();
  await expect(
    browser(page).getByRole('button', { name: 'Save', exact: true }),
  ).toHaveCount(0);
  expect(state.bindingRequests).toBe(1);
  expect(state.fileBindings.length).toBeGreaterThan(0);
  for (const binding of state.fileBindings)
    expect(binding).toEqual(originalBinding);
  expect(state.writes).toHaveLength(0);

  await editVMFile(browser(page));
  const manualResult = 'export const result = "manual adjustment";\n';
  await text.fill(manualResult);
  await browser(page)
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  const discard = page.getByRole('dialog', {
    name: 'Discard unsaved changes?',
    exact: true,
  });
  await discard.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expectEditorText(text, manualResult);
  await expect(text).toBeEditable();
  expect(state.writes).toHaveLength(0);
  await browser(page)
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  await discard.getByRole('button', { name: 'Discard', exact: true }).click();
  await expectEditorText(text, agentResult);
  await expect(text).not.toBeEditable();
  await expect(
    browser(page).getByRole('button', { name: 'Save', exact: true }),
  ).toHaveCount(0);
  expect(state.writes).toHaveLength(0);
  await editVMFile(browser(page));
  await text.fill(manualResult);
  await browser(page)
    .getByRole('button', { name: 'Save', exact: true })
    .click();
  await expect.poll(() => state.writes.length).toBe(1);
  expect(state.writes[0]).toEqual({
    binding: originalBinding,
    path: 'main.ts',
    content: manualResult,
    expectedSha256: 'agent-file-sha256',
  });
});

test('development previews retain the reviewed environment and editor draft', async ({
  page,
}) => {
  await fixture(page);
  const registrations: unknown[] = [];
  const stops: string[] = [];
  const preview = {
    id: 'preview-original',
    projectId: project.id,
    port: 5173,
    binding: originalBinding,
    state: 'ready',
    url: 'http://127.0.0.1:24000/.axv-open/original-preview-capability',
  };
  let registered = false;
  let stopped = false;
  await page.route('**/api/project-previews/**', async (route) => {
    expect(route.request().headers()['x-axv-host-id']).toBe(project.hostId);
    if (route.request().url().endsWith('/stop')) {
      stops.push(route.request().url());
      stopped = true;
      return route.fulfill({ json: { ...preview, state: 'stopped' } });
    }
    if (route.request().method() === 'POST') {
      registrations.push(route.request().postDataJSON());
      registered = true;
      return route.fulfill({ status: 201, json: preview });
    }
    return route.fulfill({
      json: {
        instanceId: 'preview-manager-original',
        supported: true,
        previews: registered
          ? [{ ...preview, state: stopped ? 'stopped' : 'ready' }]
          : [],
      },
    });
  });
  const text = await openProject(page);
  await editVMFile(browser(page));
  await text.fill('Unsaved work before preview');
  const panel = browser(page).locator('.vm-project-previews');
  await browser(page)
    .getByRole('tab', { name: 'Previews', exact: true })
    .click();
  await panel.getByLabel('Port').fill('5173');
  await panel.getByRole('button', { name: 'Add' }).click();
  const link = panel.getByRole('link', { name: 'Open' });
  await expect(link).toHaveAttribute('href', preview.url);
  await expect(link).toHaveAttribute('target', '_blank');
  await expect(link).toHaveAttribute('rel', 'noopener noreferrer');
  expect(registrations).toEqual([
    {
      instanceId: 'preview-manager-original',
      requestId: expect.any(String),
      binding: originalBinding,
      port: 5173,
    },
  ]);
  await expectEditorText(text, 'Unsaved work before preview');
  await panel.getByRole('button', { name: 'Stop' }).click();
  await expect(link).toHaveCount(0);
  expect(stops[0]).toContain(
    `/api/project-previews/${project.id}/${preview.id}/stop`,
  );
  await expectEditorText(text, 'Unsaved work before preview');
});

test('VM project actions reopen Projects without closing the draft', async ({
  page,
  isMobile,
}) => {
  await fixture(page);
  const text = await openProject(page);
  await editVMFile(browser(page));
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
  await expectEditorText(text, 'Keep this project draft');
  await expect(text).not.toBeEditable();
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
  await editVMFile(browser(page));
  await text.fill('Keep the selected file draft');
  await browser(page)
    .getByRole('button', { name: 'Back', exact: true })
    .click();
  await expect(
    browser(page).getByRole('button', { name: 'main.ts', exact: true }),
  ).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(text).toBeVisible();
  await expectEditorText(text, 'Keep the selected file draft');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(
    browser(page).getByRole('region', { name: 'VM project file editor' }),
  ).toBeFocused();
});

test('selecting the same VM file retries a failed read', async ({ page }) => {
  const state = await fixture(page);
  state.failNextRead(
    503,
    'project_unavailable',
    'Transient guest file read failure.',
  );
  const text = await openProjectFile(page);
  await expect(browser(page).getByRole('alert')).toContainText(
    'Transient guest file read failure.',
  );
  const back = browser(page).getByRole('button', { name: 'Back', exact: true });
  if (await back.isVisible()) await back.click();
  await browser(page)
    .getByRole('button', { name: 'main.ts', exact: true })
    .click();
  await expectEditorText(text, 'Original VM file\n');
  await expect(browser(page).getByRole('alert')).toHaveCount(0);
});

test('VM file recovery retries the original read and offers explicit reconnect for a changed binding', async ({
  page,
}) => {
  const state = await fixture(page);
  state.failNextRead(
    503,
    'project_unavailable',
    'The guest could not read this file.',
  );
  const text = await openProjectFile(page);
  await expect(
    browser(page).getByRole('button', { name: 'Reconnect', exact: true }),
  ).toHaveCount(0);
  await expect(
    browser(page).getByRole('button', { name: 'Reload', exact: true }),
  ).toHaveCount(0);
  await expect(browser(page).getByRole('alert')).toContainText(
    'The guest could not read this file.',
  );
  await browser(page)
    .getByRole('button', { name: 'Retry', exact: true })
    .click();
  await expectEditorText(text, 'Original VM file\n');
  await expect(
    browser(page).getByRole('region', { name: 'VM project file editor' }),
  ).toBeFocused();
  expect(state.reads.slice(0, 2)).toEqual([
    { binding: originalBinding, path: 'main.ts' },
    { binding: originalBinding, path: 'main.ts' },
  ]);
  expect(state.bindingRequests).toBe(1);

  // Recovery follows the code and operation, independently of message wording.
  state.failNextRead(
    409,
    'binding_changed',
    'The selected environment is unavailable.',
  );
  await expect(
    browser(page).getByRole('button', { name: 'Reconnect', exact: true }),
  ).toBeVisible();
  await expect(
    browser(page).getByRole('button', { name: 'Retry', exact: true }),
  ).toHaveCount(0);
  await browser(page)
    .getByRole('button', { name: 'Reconnect', exact: true })
    .click();
  await expectEditorText(text, 'Original VM file\n');
  expect(state.bindingRequests).toBe(2);
});

test('recreating the removed selected VM file opens its new contents', async ({
  page,
}) => {
  const state = await fixture(page);
  const text = await openProject(page);
  state.removeFile('main.ts');
  await actionMenu(page, browser(page), 'More VM project actions', 'New File');
  const dialog = page.getByRole('dialog');
  await dialog
    .getByRole('textbox', { name: 'Name', exact: true })
    .fill('main.ts');
  await dialog.getByRole('button', { name: 'OK', exact: true }).click();
  await expectEditorText(text, '');
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
  await projects.getByLabel('Directory', { exact: true }).fill(' repo ');
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
  await actionMenu(page, browser(page), 'More VM project actions', 'New File');
  await page
    .getByRole('dialog')
    .getByRole('textbox', { name: 'Name', exact: true })
    .fill('new.ts');
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'OK', exact: true })
    .click();
  await expectEditorText(codeEditor(browser(page)), '');
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
  await actionMenu(page, browser(page), 'More VM project actions', 'New File');
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
  await expectEditorText(text, 'Original VM file\n');
  delayed.release();
  await expect(
    browser(page)
      .getByRole('status')
      .filter({ hasText: 'Created late.ts in VM.' }),
  ).toBeVisible();
  await expectEditorText(text, 'Original VM file\n');
  await expect(
    browser(page).locator('.project-preview-heading code'),
  ).toHaveText('main.ts');
  expect(state.writes[0]).toMatchObject({
    path: 'late.ts',
    expectedSha256: '',
    binding: originalBinding,
  });
});

test('a late new-file response cannot replace the file while its save is still in flight', async ({
  page,
  isMobile,
}) => {
  const state = await fixture(page);
  const text = await openProject(page);
  const creation = state.delayWrite();
  await actionMenu(
    page,
    browser(page),
    'More VM project actions',
    'New File',
    isMobile,
  );
  await page
    .getByRole('dialog')
    .getByRole('textbox', { name: 'Name', exact: true })
    .fill('late.ts');
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'OK', exact: true })
    .click();
  await creation.pending;
  await editVMFile(browser(page));
  await text.fill('The explicitly captured save\n');
  await expect(
    browser(page).getByRole('button', { name: 'Save', exact: true }),
  ).toBeEnabled();
  const save = state.delayWrite();
  await text.press('ControlOrMeta+s');
  await save.pending;
  // Returning to the old baseline clears dirty, but does not cancel the save.
  await text.fill('Original VM file\n');
  creation.release();
  await expect(
    browser(page)
      .getByRole('status')
      .filter({ hasText: 'Created late.ts in VM.' }),
  ).toBeVisible();
  await expectEditorText(text, 'Original VM file\n');
  await expect(
    browser(page).locator('.project-preview-heading code'),
  ).toHaveText('main.ts');
  save.release();
  await expect(
    browser(page).getByRole('button', { name: 'Save', exact: true }),
  ).toBeEnabled();
  await expectEditorText(text, 'Original VM file\n');
  await text.press('ControlOrMeta+s');
  await expect.poll(() => state.writes.length).toBe(3);
  expect(state.writes).toEqual([
    {
      binding: originalBinding,
      path: 'late.ts',
      content: '',
      expectedSha256: '',
    },
    {
      binding: originalBinding,
      path: 'main.ts',
      content: 'The explicitly captured save\n',
      expectedSha256: 'original-sha256',
    },
    {
      binding: originalBinding,
      path: 'main.ts',
      content: 'Original VM file\n',
      expectedSha256: 'delayed-sha256',
    },
  ]);
});

for (const firstReply of ['older', 'newer'] as const) {
  test(`overlapping new-file requests keep the latest selection when the ${firstReply} reply arrives first`, async ({
    page,
    isMobile,
  }) => {
    const state = await fixture(page);
    await openProject(page);
    const createFile = async (name: string) => {
      await actionMenu(
        page,
        browser(page),
        'More VM project actions',
        'New File',
        isMobile,
      );
      const dialog = page.getByRole('dialog');
      await dialog
        .getByRole('textbox', { name: 'Name', exact: true })
        .fill(name);
      await dialog.getByRole('button', { name: 'OK', exact: true }).click();
    };
    const older = state.delayWrite();
    await createFile('older.ts');
    await older.pending;
    const newer = state.delayWrite();
    await createFile('newer.ts');
    await newer.pending;
    const heading = browser(page).locator('.project-preview-heading code');
    try {
      if (firstReply === 'older') {
        older.release();
        await expect(
          browser(page)
            .getByRole('status')
            .filter({ hasText: 'Created older.ts in VM.' }),
        ).toBeVisible();
        await expect(heading).toHaveText('main.ts');
        newer.release();
      } else {
        newer.release();
        await expect(heading).toHaveText('newer.ts');
        older.release();
      }
      await expect(
        browser(page)
          .getByRole('status')
          .filter({
            hasText: `Created ${firstReply === 'older' ? 'newer' : 'older'}.ts in VM.`,
          }),
      ).toBeVisible();
      await expect(heading).toHaveText('newer.ts');
      await expect(codeEditor(browser(page))).toBeEditable();
      expect(state.writes).toEqual([
        {
          binding: originalBinding,
          path: 'older.ts',
          content: '',
          expectedSha256: '',
        },
        {
          binding: originalBinding,
          path: 'newer.ts',
          content: '',
          expectedSha256: '',
        },
      ]);
    } finally {
      older.release();
      newer.release();
    }
  });
}

test('a completed file creation preserves the location and input of an open naming dialog', async ({
  page,
  isMobile,
}) => {
  const state = await fixture(page);
  await openProject(page);
  const older = state.delayWrite();
  await actionMenu(
    page,
    browser(page),
    'More VM project actions',
    'New File',
    isMobile,
  );
  let dialog = page.getByRole('dialog');
  await dialog
    .getByRole('textbox', { name: 'Name', exact: true })
    .fill('older.ts');
  await dialog.getByRole('button', { name: 'OK', exact: true }).click();
  await older.pending;
  try {
    await actionMenu(
      page,
      browser(page),
      'More VM project actions',
      'New File',
      isMobile,
    );
    dialog = page.getByRole('dialog');
    const name = dialog.getByRole('textbox', { name: 'Name', exact: true });
    await name.fill('newer.ts');
    older.release();
    await expect(browser(page).locator('.project-feedback')).toHaveText(
      'Created older.ts in VM.',
    );
    await expect(
      browser(page).locator('.project-preview-heading code'),
    ).toHaveText('main.ts');
    await expect(name).toHaveValue('newer.ts');
    await expect(name).toBeFocused();
    await dialog.getByRole('button', { name: 'OK', exact: true }).click();
    await expect(
      browser(page).locator('.project-preview-heading code'),
    ).toHaveText('newer.ts');
    await expect(codeEditor(browser(page))).toBeEditable();
    expect(state.writes).toEqual([
      {
        binding: originalBinding,
        path: 'older.ts',
        content: '',
        expectedSha256: '',
      },
      {
        binding: originalBinding,
        path: 'newer.ts',
        content: '',
        expectedSha256: '',
      },
    ]);
  } finally {
    older.release();
  }
});

test('unreadable VM draft recovery is retained until explicit discard', async ({
  page,
  request,
  isMobile,
}) => {
  test.skip(isMobile, 'Desktop recovery corruption acceptance.');
  await fixture(page);
  const text = await openProject(page);
  await editVMFile(browser(page));
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
  await expect(text).not.toBeEditable();
  expect(await blocksTabClose(page)).toBe(true);
  await expect(
    browser(page).getByRole('button', { name: 'Save', exact: true }),
  ).toHaveCount(0);
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
  await expect(text).not.toBeEditable();
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
  await expect(text).not.toBeEditable();
  expect(await blocksTabClose(page)).toBe(false);
  expect(
    await page.evaluate((key) => sessionStorage.getItem(key), key),
  ).toBeNull();
  await editVMFile(browser(page));
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
  await editVMFile(browser(page));
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
  await expectEditorText(text, 'Newer unsaved typing');
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
  await expectEditorText(text, 'Newer unsaved typing');
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
  await editVMFile(browser(page));
  await text.fill('Keep this draft');
  const editorImage = testInfo.outputPath('vm-project-editor.png');
  await browser(page).screenshot({ path: editorImage });
  await testInfo.attach('VM project editor', {
    path: editorImage,
    contentType: 'image/png',
  });
  await switchWorkspace(page, 2);
  await expect(text).not.toBeEditable();
  await expectEditorText(text, 'Keep this draft');
  await switchWorkspace(page, 1);
  await expect(text).toBeEditable();
  state.conflict();
  await browser(page)
    .getByRole('button', { name: 'Save', exact: true })
    .click();
  await expect(browser(page).getByRole('alert')).toContainText(
    'The file changed',
  );
  await expectEditorText(text, 'Keep this draft');
  expect(state.bindingRequests).toBe(1);
  await browser(page)
    .getByRole('button', { name: 'Reload', exact: true })
    .click();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  await expectEditorText(text, 'Keep this draft');
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
  await expectEditorText(text, 'Keep this draft');
  expect(state.bindingRequests).toBe(1);
  const beforeReplacement = state.fileBindings.length;
  state.replaceVM();
  await actionMenu(page, browser(page), 'More VM project actions', 'Reconnect');
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Discard', exact: true })
    .click();
  await expectEditorText(text, 'Original VM file\n');
  await expect.poll(() => state.bindingRequests).toBe(2);
  await editVMFile(browser(page));
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

test('lost preview replies recover the original registration after reload', async ({
  page,
  request,
}) => {
  await fixture(page);
  const registrations: unknown[] = [];
  const preview = {
    id: 'preview-recovered',
    projectId: project.id,
    port: 5173,
    binding: originalBinding,
    state: 'ready',
    url: 'http://recovered.localhost:24001/.axv-open/token',
  };
  await page.route('**/api/project-previews/**', async (route) => {
    if (route.request().method() === 'POST') {
      registrations.push(route.request().postDataJSON());
      if (registrations.length === 1)
        return route.fulfill({
          status: 503,
          json: {
            code: 'unavailable',
            error: 'Registration response was lost.',
          },
        });
      return route.fulfill({ status: 201, json: preview });
    }
    return route.fulfill({
      json: {
        instanceId: 'preview-original-instance',
        supported: true,
        previews: registrations.length ? [preview] : [],
      },
    });
  });
  await openProject(page);
  let panel = browser(page).locator('.vm-project-previews');
  await browser(page)
    .getByRole('tab', { name: 'Previews', exact: true })
    .click();
  await panel.getByRole('button', { name: 'Add' }).click();
  await expect(panel.getByRole('alert')).toContainText('response was lost');
  await expect
    .poll(async () =>
      (await persistedLayout(page, request))?.windows.some(
        (item: { id: string }) => item.id === browserId,
      ),
    )
    .toBe(true);
  await page.reload();
  panel = browser(page).locator('.vm-project-previews');
  await browser(page)
    .getByRole('tab', { name: 'Previews', exact: true })
    .click();
  await panel.getByRole('button', { name: 'Recover' }).click();
  await expect(panel.getByRole('link', { name: 'Open' })).toHaveAttribute(
    'href',
    preview.url,
  );
  expect(registrations).toHaveLength(2);
  expect(registrations[1]).toEqual(registrations[0]);
  await expect(panel.getByRole('button', { name: 'Recover' })).toHaveCount(0);
});

test('preview recovery never substitutes a restarted manager instance', async ({
  page,
  request,
}) => {
  await fixture(page);
  const registrations: Array<{ instanceId: string }> = [];
  let instanceId = 'preview-manager-before';
  await page.route('**/api/project-previews/**', async (route) => {
    if (route.request().method() === 'POST') {
      const registration = route.request().postDataJSON();
      registrations.push(registration);
      return route.fulfill({
        status: 409,
        json: {
          code: 'preview_instance_changed',
          error: 'The original AXV preview instance is unavailable.',
        },
      });
    }
    return route.fulfill({
      json: { instanceId, supported: true, previews: [] },
    });
  });
  await openProject(page);
  let panel = browser(page).locator('.vm-project-previews');
  await browser(page)
    .getByRole('tab', { name: 'Previews', exact: true })
    .click();
  await panel.getByRole('button', { name: 'Add' }).click();
  await expect(panel.getByRole('alert')).toContainText(
    'original AXV preview instance',
  );
  instanceId = 'preview-manager-after';
  await expect
    .poll(async () =>
      (await persistedLayout(page, request))?.windows.some(
        (item: { id: string }) => item.id === browserId,
      ),
    )
    .toBe(true);
  await page.reload();
  panel = browser(page).locator('.vm-project-previews');
  await browser(page)
    .getByRole('tab', { name: 'Previews', exact: true })
    .click();
  await panel.getByRole('button', { name: 'Recover' }).click();
  await expect(panel.getByRole('alert')).toContainText(
    'original AXV preview instance',
  );
  expect(registrations).toHaveLength(2);
  expect(registrations[1]).toEqual(registrations[0]);
  expect(registrations[1].instanceId).toBe('preview-manager-before');
});

test('remote preview availability is visible and registration stays disabled', async ({
  page,
}) => {
  await fixture(page);
  await page.route('**/api/project-previews/**', async (route) =>
    route.fulfill({
      json: {
        instanceId: 'local-instance',
        supported: false,
        reason: 'Open the local desktop to register previews.',
        previews: [],
      },
    }),
  );
  await openProject(page);
  const panel = browser(page).locator('.vm-project-previews');
  await browser(page)
    .getByRole('tab', { name: 'Previews', exact: true })
    .click();
  await expect(panel.getByRole('status')).toContainText('local desktop');
  await expect(panel.getByRole('button', { name: 'Add' })).toBeDisabled();
});

test('code editing keeps undo history and saves exact text against the newest acknowledged hash', async ({
  page,
}) => {
  const source = 'const answer = 41;\n// 草稿 🚀\n';
  const state = await fixture(page, true, source);
  await page.route('**/api/project-previews/**', (route) =>
    route.fulfill({
      json: {
        instanceId: 'editor-preview-instance',
        supported: true,
        previews: [],
      },
    }),
  );
  const text = await openProject(page, source);
  await editVMFile(browser(page));
  expect(await copyEditorText(text)).toBe(source);
  await text.press('ArrowLeft');
  await text.press('End');
  await text.press('ArrowLeft');
  await text.pressSequentially(' + 1');
  const firstEdit = 'const answer = 41 + 1;\n// 草稿 🚀\n';
  await expectEditorText(text, firstEdit);
  await browser(page)
    .getByRole('tab', { name: 'Previews', exact: true })
    .click();
  await expectEditorText(text, firstEdit);
  await text.evaluate((element) => (element as HTMLElement).focus());
  await expect(text).not.toBeFocused();
  await browser(page).getByRole('tab', { name: 'Files', exact: true }).click();
  await text.focus();
  await text.press('ControlOrMeta+z');
  await expectEditorText(text, source);
  await text.press('End');
  await text.press('ArrowLeft');
  await text.pressSequentially(' + 1');
  await expectEditorText(text, firstEdit);
  await text.press('ControlOrMeta+s');
  await expect.poll(() => state.writes.length).toBe(1);
  await expect(
    browser(page)
      .locator('.project-preview-heading')
      .getByRole('button', { name: 'Done', exact: true }),
  ).toBeVisible();
  expect(state.writes[0]).toEqual({
    binding: originalBinding,
    path: 'main.ts',
    content: firstEdit,
    expectedSha256: 'original-sha256',
  });
  expect(await copyEditorText(text)).toBe(firstEdit);
  await text.press('ArrowRight');
  await text.pressSequentially('// next edit');
  const secondEdit = `${firstEdit}// next edit`;
  await expectEditorText(text, secondEdit);
  await text.press('ControlOrMeta+s');
  await expect.poll(() => state.writes.length).toBe(2);
  await expect(
    browser(page)
      .locator('.project-preview-heading')
      .getByRole('button', { name: 'Done', exact: true }),
  ).toBeVisible();
  expect(state.writes[1]).toEqual({
    binding: originalBinding,
    path: 'main.ts',
    content: secondEdit,
    expectedSha256: 'saved-sha256-1',
  });
  await text.press('ControlOrMeta+z');
  await expectEditorText(text, firstEdit);
  await text.press('ControlOrMeta+s');
  await expect.poll(() => state.writes.length).toBe(3);
  expect(state.writes[2]).toEqual({
    binding: originalBinding,
    path: 'main.ts',
    content: firstEdit,
    expectedSha256: 'saved-sha256-2',
  });
});

test('opening another file starts independent undo history after an explicit draft discard', async ({
  page,
  isMobile,
}) => {
  const state = await fixture(page);
  const other = 'const other = "B-only";\n';
  await page.route('**/api/compute/projects/*/files/list', async (route) => {
    return route.fulfill({
      json: {
        project,
        binding: originalBinding,
        entries: ['main.ts', 'other.ts'].map((path) => ({
          name: path,
          path,
          kind: 'file',
          size: 32,
          modifiedAt: project.createdAt,
        })),
      },
    });
  });
  await page.route('**/api/compute/projects/*/files/read', async (route) => {
    const data = route.request().postDataJSON();
    if (data.path !== 'other.ts') return route.fallback();
    return route.fulfill({
      json: {
        project,
        binding: originalBinding,
        path: data.path,
        content: other,
        sha256: 'other-file-sha256',
        size: other.length,
        modifiedAt: project.createdAt,
      },
    });
  });
  const text = await openProject(page);
  await editVMFile(browser(page));
  await text.focus();
  await text.press('ControlOrMeta+a');
  await text.press('ArrowRight');
  await page.keyboard.insertText('A-only draft');
  await expectEditorText(text, 'Original VM file\nA-only draft');
  if (isMobile)
    await browser(page)
      .getByRole('button', { name: 'Back', exact: true })
      .click();
  await browser(page)
    .getByRole('button', { name: 'other.ts', exact: true })
    .click();
  const discard = page.getByRole('dialog', {
    name: 'Discard unsaved changes?',
    exact: true,
  });
  await expect(discard).toBeVisible();
  await discard.getByRole('button', { name: 'Discard', exact: true }).click();
  await expectEditorText(text, other);
  await editVMFile(browser(page));
  await text.focus();
  await text.press('ControlOrMeta+z');
  await expectEditorText(text, other);
  expect(await copyEditorText(text)).toBe(other);
  await expect(
    browser(page).getByRole('button', { name: 'Save', exact: true }),
  ).toBeDisabled();
  expect(state.writes).toHaveLength(0);
});

test('an unavailable editor keeps CRLF source read-only until a reload restores editing', async ({
  page,
  request,
}) => {
  const source = 'const answer = 41;\r\n// Keep these CRLF separators.\r\n';
  const displayed = 'const answer = 41;\n// Keep these CRLF separators.\n';
  const state = await fixture(page, true, source);
  const chunk = '**/assets/CodeEditor-*.js';
  await page.route(chunk, (route) => route.abort('failed'));
  const projects = await launcher(page);
  await projects.getByRole('button', { name: /^VM repository ·/ }).click();
  await browser(page)
    .getByRole('button', { name: 'main.ts', exact: true })
    .click();
  const text = codeEditor(browser(page));
  await expect(browser(page).getByRole('alert')).toContainText(
    'Code editor unavailable. Text is read-only; reload AXV to retry.',
  );
  await expect(text).toHaveJSProperty('readOnly', true);
  await expect(text).not.toBeEditable();
  await expect(text).toHaveValue(displayed);
  await text.focus();
  await text.press('ControlOrMeta+a');
  await expect(text).toHaveJSProperty('selectionStart', 0);
  await expect(text).toHaveJSProperty('selectionEnd', displayed.length);
  await text.evaluate((element) => {
    element.addEventListener('paste', (event) => {
      element.setAttribute(
        'data-test-pasted-text',
        (event as ClipboardEvent).clipboardData?.getData('text/plain') || '',
      );
    });
  });
  await text.press('ControlOrMeta+c');
  await text.press('ControlOrMeta+v');
  await expect(text).toHaveAttribute('data-test-pasted-text', displayed);
  await text.pressSequentially('MUST_NOT_CHANGE_SOURCE');
  await expect(text).toHaveValue(displayed);
  await expect(
    browser(page).getByRole('button', { name: 'Save', exact: true }),
  ).toHaveCount(0);
  expect(state.writes).toHaveLength(0);
  expect(await blocksTabClose(page)).toBe(false);
  await expect
    .poll(async () =>
      (await persistedLayout(page, request))?.windows.some(
        (item: { id: string; viewState?: { file?: string } }) =>
          item.id === browserId && item.viewState?.file === 'main.ts',
      ),
    )
    .toBe(true);
  await page.unroute(chunk);
  await page.reload();
  await expectEditorText(text, displayed);
  await editVMFile(browser(page));
  await expect(text).toBeEditable();
  await text.focus();
  await text.press('ControlOrMeta+Home');
  await text.press('End');
  await text.press('ArrowLeft');
  await text.pressSequentially(' + 1');
  await text.press('ControlOrMeta+s');
  await expect.poll(() => state.writes.length).toBe(1);
  expect(state.writes[0]).toEqual({
    binding: originalBinding,
    path: 'main.ts',
    content: 'const answer = 41 + 1;\r\n// Keep these CRLF separators.\r\n',
    expectedSha256: 'original-sha256',
  });
});

test('an invalid new-file name keeps its error visible without blocking clean file observation', async ({
  page,
  isMobile,
}) => {
  const state = await fixture(page);
  const text = await openProject(page);
  await actionMenu(
    page,
    browser(page),
    'More VM project actions',
    'New File',
    isMobile,
  );
  const dialog = page.getByRole('dialog');
  await dialog
    .getByRole('textbox', { name: 'Name', exact: true })
    .fill('bad/name');
  await dialog.getByRole('button', { name: 'OK', exact: true }).click();
  const error = browser(page).getByRole('alert');
  await expect(error).toContainText('Enter a single file or folder name.');
  await expectEditorText(text, 'Original VM file\n');
  expect(state.writes).toEqual([]);
  state.externalWrite(
    'Agent update after an unrelated create error\n',
    'agent-sha',
  );
  await expectEditorText(
    text,
    'Agent update after an unrelated create error\n',
  );
  await expect(error).toContainText('Enter a single file or folder name.');
  await expect(text).not.toBeEditable();
  await expect(
    browser(page).getByRole('button', { name: 'Save', exact: true }),
  ).toHaveCount(0);
  expect(state.bindingRequests).toBe(1);
  expect(state.writes).toEqual([]);
  for (const { binding, path } of state.reads) {
    expect(binding).toEqual(originalBinding);
    expect(path).toBe('main.ts');
  }
});

test('observing an external change preserves the draft recovery and original save expectation', async ({
  page,
}) => {
  const state = await fixture(page);
  const text = await openProject(page);
  await editVMFile(browser(page));
  await text.fill('Keep this exact manual draft\n');
  const recovery = await page.evaluate(() =>
    Object.entries(sessionStorage).find(([key]) =>
      key.includes('axv-vm-project-draft'),
    ),
  );
  expect(recovery).toBeDefined();
  state.externalWrite('Agent changed this file\n', 'agent-change-sha256');
  await expect(fileUpdates(page)).toContainText(
    'File changed in VM. Draft kept.',
  );
  await expectEditorText(text, 'Keep this exact manual draft\n');
  expect(
    await page.evaluate(([key]) => sessionStorage.getItem(key), recovery!),
  ).toBe(recovery![1]);
  await browser(page)
    .getByRole('button', { name: 'Save', exact: true })
    .click();
  await expect(browser(page).getByRole('alert')).toContainText(
    'The file changed since it was read.',
  );
  await expect(fileUpdates(page)).toHaveCount(0);
  expect(state.writes).toEqual([
    {
      binding: originalBinding,
      path: 'main.ts',
      content: 'Keep this exact manual draft\n',
      expectedSha256: 'original-sha256',
    },
  ]);
  await expectEditorText(text, 'Keep this exact manual draft\n');
  expect(
    await page.evaluate(([key]) => sessionStorage.getItem(key), recovery!),
  ).toBe(recovery![1]);
  expect(await blocksTabClose(page)).toBe(true);
  expect(state.file('main.ts')).toEqual({
    content: 'Agent changed this file\n',
    sha256: 'agent-change-sha256',
  });

  state.failReads(
    503,
    'file_unavailable',
    'Guest read failed after the save conflict.',
  );
  await expect(fileUpdates(page)).toContainText(
    'Guest read failed after the save conflict.',
  );
  await expect(browser(page).getByRole('alert')).toContainText(
    'The file changed since it was read.',
  );
  await expectEditorText(text, 'Keep this exact manual draft\n');
  expect(
    await page.evaluate(([key]) => sessionStorage.getItem(key), recovery!),
  ).toBe(recovery![1]);

  state.setStale();
  await expect(fileUpdates(page)).toContainText(
    'The VM project binding changed.',
  );
  await expect(browser(page).getByRole('alert')).toContainText(
    'The file changed since it was read.',
  );
  await browser(page)
    .getByRole('button', { name: 'Reconnect', exact: true })
    .click();
  await page
    .getByRole('dialog', { name: 'Discard unsaved changes?', exact: true })
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  await expectEditorText(text, 'Keep this exact manual draft\n');
  expect(state.bindingRequests).toBe(1);
  expect(state.writes).toHaveLength(1);
  expect(state.file('main.ts')).toEqual({
    content: 'Agent changed this file\n',
    sha256: 'agent-change-sha256',
  });
  expect(
    await page.evaluate(([key]) => sessionStorage.getItem(key), recovery!),
  ).toBe(recovery![1]);
});

test('a delayed observation cannot overwrite typing that began after the read started', async ({
  page,
}) => {
  const state = await fixture(page);
  const text = await openProject(page);
  state.externalWrite(
    'Agent result captured by a slow read\n',
    'slow-agent-sha',
  );
  const delayed = state.delayRead();
  try {
    await delayed.pending;
    const reads = state.reads.length;
    await page.waitForTimeout(2300);
    expect(state.reads).toHaveLength(reads);
    await editVMFile(browser(page));
    await text.fill('Typing while the old read was pending\n');
    delayed.release();
    await expect(fileUpdates(page)).toContainText(
      'File changed in VM. Draft kept.',
    );
    await expectEditorText(text, 'Typing while the old read was pending\n');
    await browser(page)
      .getByRole('button', { name: 'Save', exact: true })
      .click();
    await expect.poll(() => state.writes.length).toBe(1);
    expect(state.writes[0]).toEqual({
      binding: originalBinding,
      path: 'main.ts',
      content: 'Typing while the old read was pending\n',
      expectedSha256: 'original-sha256',
    });
    await expectEditorText(text, 'Typing while the old read was pending\n');
  } finally {
    delayed.release();
  }
});

test('an observation started before saving cannot replace the acknowledged bytes or hash', async ({
  page,
}) => {
  const state = await fixture(page);
  const text = await openProject(page);
  await editVMFile(browser(page));
  await text.fill('The snapshot being saved\n');
  const delayed = state.delayRead();
  try {
    await delayed.pending;
    await text.press('ControlOrMeta+s');
    await expect.poll(() => state.writes.length).toBe(1);
    await expect(
      browser(page)
        .locator('.project-preview-heading')
        .getByRole('button', { name: 'Done', exact: true }),
    ).toBeVisible();
    delayed.release();
    await expectEditorText(text, 'The snapshot being saved\n');
    const reads = state.reads.length;
    await expect.poll(() => state.reads.length).toBeGreaterThan(reads);
    await expectEditorText(text, 'The snapshot being saved\n');
    await text.fill('A later manual adjustment\n');
    await expect(
      browser(page).getByRole('button', { name: 'Save', exact: true }),
    ).toBeEnabled();
    await text.press('ControlOrMeta+s');
    await expect.poll(() => state.writes.length).toBe(2);
    expect(state.writes).toEqual([
      {
        binding: originalBinding,
        path: 'main.ts',
        content: 'The snapshot being saved\n',
        expectedSha256: 'original-sha256',
      },
      {
        binding: originalBinding,
        path: 'main.ts',
        content: 'A later manual adjustment\n',
        expectedSha256: 'saved-sha256-1',
      },
    ]);
  } finally {
    delayed.release();
  }
});

test('file observation pauses off the visible reading pane and resumes without reconnecting', async ({
  page,
  isMobile,
}) => {
  const state = await fixture(page);
  const text = await openProject(page);
  await expect.poll(() => state.reads.length).toBeGreaterThan(1);
  await switchWorkspace(page, 2);
  const hiddenReads = state.reads.length;
  state.externalWrite(
    'Updated while this workspace was hidden\n',
    'hidden-sha',
  );
  await page.waitForTimeout(2300);
  expect(state.reads).toHaveLength(hiddenReads);
  await expectEditorText(text, 'Original VM file\n');
  await switchWorkspace(page, 1);
  await expectEditorText(text, 'Updated while this workspace was hidden\n');
  if (isMobile) {
    await browser(page)
      .getByRole('button', { name: 'Back', exact: true })
      .click();
    const directoryReads = state.reads.length;
    state.externalWrite(
      'Updated while the directory was shown\n',
      'directory-sha',
    );
    await page.waitForTimeout(2300);
    expect(state.reads).toHaveLength(directoryReads);
    await browser(page)
      .getByRole('button', { name: 'main.ts', exact: true })
      .click();
    await expectEditorText(text, 'Updated while the directory was shown\n');
  }
  expect(state.bindingRequests).toBe(1);
  expect(await blocksTabClose(page)).toBe(false);
});

test('a visible file keeps observing while its Codex window owns focus', async ({
  page,
  isMobile,
}) => {
  test.skip(
    isMobile,
    'Two visible desktop windows exercise independent focus.',
  );
  const state = await fixture(page);
  const text = await openProject(page);
  await browser(page)
    .getByRole('button', { name: 'Codex', exact: true })
    .click();
  const codex = page.locator(
    `[data-window-id="codex:vm:${project.hostId}:${project.id}"]`,
  );
  await expect(codex).toBeVisible();
  await expect(codex).toHaveClass(/is-active/);
  await expect(text).toBeVisible();
  state.externalWrite(
    'Agent progress remains visible beside Codex\n',
    'live-sha',
  );
  await expectEditorText(text, 'Agent progress remains visible beside Codex\n');
  await expect(codex).toHaveClass(/is-active/);
  await expect(text).not.toBeFocused();
  for (const { binding } of state.reads)
    expect(binding).toEqual(originalBinding);
  expect(state.writes).toHaveLength(0);
});

test('an observed binding loss stops reads until the user explicitly reconnects', async ({
  page,
}) => {
  const state = await fixture(page);
  const text = await openProject(page);
  state.replaceVM();
  await expect(
    browser(page).getByRole('button', { name: 'Reconnect', exact: true }),
  ).toBeVisible();
  await expectEditorText(text, 'Original VM file\n');
  const rejectedReads = state.reads.length;
  await page.waitForTimeout(2300);
  expect(state.reads).toHaveLength(rejectedReads);
  expect(state.bindingRequests).toBe(1);
  for (const { binding } of state.reads)
    expect(binding).toEqual(originalBinding);
  await browser(page)
    .getByRole('button', { name: 'Reconnect', exact: true })
    .click();
  await expect.poll(() => state.bindingRequests).toBe(2);
  await expectEditorText(text, 'Original VM file\n');
  await expect
    .poll(() => state.reads.at(-1)?.binding.machineId)
    .toBe('replacement-machine');
  expect(state.reads.at(-1)?.binding).toMatchObject({
    machineId: 'replacement-machine',
    bootId: 'replacement-boot',
    instanceId: 'replacement-instance',
    volumeRevision: 6,
  });
});

test('observation failures and deletion retain the displayed code and can retry the original file', async ({
  page,
}) => {
  const state = await fixture(page);
  const text = await openProject(page);
  state.failReads(503, 'file_unavailable', 'Guest read temporarily failed.');
  await expect(fileUpdates(page)).toContainText(
    'Guest read temporarily failed.',
  );
  await expectEditorText(text, 'Original VM file\n');
  state.resumeReads();
  state.externalWrite(
    'Recovered without replacing the binding\n',
    'recovered-sha',
  );
  await browser(page)
    .getByRole('button', { name: 'Retry check', exact: true })
    .click();
  await expectEditorText(text, 'Recovered without replacing the binding\n');
  state.removeFile('main.ts');
  await expect(fileUpdates(page)).toContainText('The file was removed.');
  await expectEditorText(text, 'Recovered without replacing the binding\n');
  state.externalWrite('Recreated by an external process\n', 'recreated-sha');
  await browser(page)
    .getByRole('button', { name: 'Retry check', exact: true })
    .click();
  await expectEditorText(text, 'Recreated by an external process\n');
  expect(state.bindingRequests).toBe(1);
  for (const { binding } of state.reads)
    expect(binding).toEqual(originalBinding);
  expect(state.writes).toHaveLength(0);
});

test('a clean manual editor accepts a new external baseline without restoring obsolete undo history', async ({
  page,
}) => {
  const state = await fixture(page, true, 'export const initial = 1;\n');
  const text = await openProject(page, 'export const initial = 1;\n');
  await editVMFile(browser(page));
  await text.fill('export const initial = 2;\n');
  await expect(
    browser(page).getByRole('button', { name: 'Save', exact: true }),
  ).toBeEnabled();
  await text.press('ControlOrMeta+s');
  await expect.poll(() => state.writes.length).toBe(1);
  await expect(
    browser(page)
      .locator('.project-preview-heading')
      .getByRole('button', { name: 'Done', exact: true }),
  ).toBeVisible();
  await text.press('ControlOrMeta+f');
  const search = browser(page).getByRole('textbox', {
    name: 'Find',
    exact: true,
  });
  await search.fill('initial');
  state.externalWrite(
    'export const agent = 3;\r\n// Keep CRLF after the external update.\r\n',
    'new-external-baseline-sha',
  );
  const displayed =
    'export const agent = 3;\n// Keep CRLF after the external update.\n';
  await expectEditorText(text, displayed);
  await expect(search).toHaveValue('initial');
  await expect(search).toBeFocused();
  await search.press('Escape');
  await expect(text).toBeEditable();
  await text.focus();
  await text.press('ControlOrMeta+z');
  await expectEditorText(text, displayed);
  await expect(
    browser(page).getByRole('button', { name: 'Save', exact: true }),
  ).toBeDisabled();
  await text.press('ControlOrMeta+Home');
  await text.press('End');
  await text.press('ArrowLeft');
  await text.pressSequentially(' + 1');
  await text.press('ControlOrMeta+s');
  await expect.poll(() => state.writes.length).toBe(2);
  expect(state.writes[1]).toEqual({
    binding: originalBinding,
    path: 'main.ts',
    content:
      'export const agent = 3 + 1;\r\n// Keep CRLF after the external update.\r\n',
    expectedSha256: 'new-external-baseline-sha',
  });
});

test('automatic observation does not replace an unchanged document during native IME composition', async ({
  page,
  browserName,
}) => {
  test.skip(browserName !== 'chromium', 'Native IME composition requires CDP.');
  const source = '输：原始内容\n';
  const state = await fixture(page, true, source);
  const text = await openProject(page, source);
  await editVMFile(browser(page));
  await text.focus();
  await text.press('ControlOrMeta+Home');
  await text.press('Shift+ArrowRight');
  const protocol = await page.context().newCDPSession(page);
  try {
    await protocol.send('Input.imeSetComposition', {
      text: '输',
      selectionStart: 1,
      selectionEnd: 1,
    });
    await expectEditorText(text, source);
    state.externalWrite(
      'Agent changed the file during composition\n',
      'ime-sha',
    );
    await page.waitForTimeout(2300);
    await expectEditorText(text, source);
    await protocol.send('Input.insertText', { text: '输' });
    await expectEditorText(text, 'Agent changed the file during composition\n');
    expect(state.writes).toHaveLength(0);
    expect(await blocksTabClose(page)).toBe(false);
  } finally {
    await protocol.detach();
  }
});

test('an unchanged native composition guards replacement and resumes observation after cancellation', async ({
  page,
  browserName,
  isMobile,
}) => {
  test.skip(browserName !== 'chromium', 'Native IME composition requires CDP.');
  test.skip(isMobile, 'Desktop navigation and window-close acceptance.');
  const source = '输：原始内容\n';
  const state = await fixture(page, true, source);
  const text = await openProject(page, source);
  const creation = state.delayWrite();
  await actionMenu(page, browser(page), 'More VM project actions', 'New File');
  await page
    .getByRole('dialog')
    .getByRole('textbox', { name: 'Name', exact: true })
    .fill('late.ts');
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'OK', exact: true })
    .click();
  await creation.pending;
  await editVMFile(browser(page));
  await text.focus();
  await text.press('ControlOrMeta+Home');
  await text.press('Shift+ArrowRight');
  const protocol = await page.context().newCDPSession(page);
  try {
    await protocol.send('Input.imeSetComposition', {
      text: '输',
      selectionStart: 1,
      selectionEnd: 1,
    });
    await expectEditorText(text, source);
    expect(await blocksTabClose(page)).toBe(true);
    await expect(
      browser(page).getByRole('button', { name: 'Reload file', exact: true }),
    ).toBeDisabled();
    await expect(
      browser(page).getByRole('button', { name: 'src', exact: true }),
    ).toBeDisabled();
    await browser(page)
      .getByRole('button', { name: 'Close VM · VM repository', exact: true })
      .click();
    await expect(browser(page)).toBeVisible();
    await expect(page.getByRole('dialog')).toHaveCount(0);
    // The close button blurs the editor, which may naturally settle the IME.
    // Start a fresh composition before testing a late asynchronous response.
    await text.focus();
    await text.press('ControlOrMeta+Home');
    await text.press('Shift+ArrowRight');
    await protocol.send('Input.imeSetComposition', {
      text: '输',
      selectionStart: 1,
      selectionEnd: 1,
    });
    await expect(
      browser(page).getByRole('button', { name: 'Reload file', exact: true }),
    ).toBeDisabled();
    creation.release();
    await expect(
      browser(page)
        .getByRole('status')
        .filter({ hasText: 'Created late.ts in VM.' }),
    ).toBeVisible();
    await expect(
      browser(page).locator('.project-preview-heading code'),
    ).toHaveText('main.ts');
    await protocol.send('Input.insertText', { text: '输' });
    await expect(
      browser(page).getByRole('button', { name: 'Reload file', exact: true }),
    ).toBeEnabled();
    await text.focus();
    await text.press('ControlOrMeta+Home');
    // Cancelling an inserted preedit restores the exact clean baseline.
    await protocol.send('Input.imeSetComposition', {
      text: '临',
      selectionStart: 1,
      selectionEnd: 1,
    });
    await expectEditorText(text, '临输：原始内容\n');
    const reads = state.reads.length;
    state.externalWrite('Agent version after IME cancellation\n', 'cancel-sha');
    await page.waitForTimeout(2300);
    await expectEditorText(text, '临输：原始内容\n');
    expect(state.reads).toHaveLength(reads);
    await text.press('Escape');
    // Deliver the platform's cancellation through Chromium's native IME API.
    await protocol.send('Input.imeSetComposition', {
      text: '',
      selectionStart: 0,
      selectionEnd: 0,
    });
    await expectEditorText(text, 'Agent version after IME cancellation\n');
    await expect(
      browser(page).getByRole('button', { name: 'Reload file', exact: true }),
    ).toBeEnabled();
    expect(await blocksTabClose(page)).toBe(false);
    expect(state.writes).toEqual([
      {
        binding: originalBinding,
        path: 'late.ts',
        content: '',
        expectedSha256: '',
      },
    ]);
  } finally {
    creation.release();
    await protocol.detach();
  }
});

test('native composition settles across a desktop menu and preserves newer input during a save', async ({
  page,
  browserName,
  isMobile,
}) => {
  test.skip(browserName !== 'chromium', 'Native IME composition requires CDP.');
  test.skip(isMobile, 'Desktop menu focus transition acceptance.');
  const state = await fixture(page, true, 'x\n');
  const text = await openProject(page, 'x\n');
  await editVMFile(browser(page));
  await text.focus();
  await text.press('ControlOrMeta+Home');
  await text.press('Shift+ArrowRight');
  const protocol = await page.context().newCDPSession(page);
  try {
    await protocol.send('Input.imeSetComposition', {
      text: '组',
      selectionStart: 1,
      selectionEnd: 1,
    });
    await expectEditorText(text, '组\n');
    await text.evaluate((element) => {
      const context = (
        element as HTMLElement & { editContext?: EventTarget | null }
      ).editContext;
      const target = context || element;
      target.addEventListener(
        'compositionend',
        () => {
          const menu = document.querySelector<HTMLButtonElement>(
            'button[aria-label="Workspace menu"]',
          );
          if (!menu) throw new Error('Expected the desktop workspace menu.');
          menu.click();
        },
        { once: true },
      );
    });
    await protocol.send('Input.insertText', { text: '终' });
    await expect(menuItem(page, 'Applications')).toBeVisible();
    await expectEditorText(text, '终\n');
    await expect(text).not.toBeEditable();
    await text.evaluate((element) => (element as HTMLElement).focus());
    await expect(text).not.toBeFocused();
    await page.keyboard.insertText('blocked');
    await expectEditorText(text, '终\n');
    await page.keyboard.press('Escape');
    await expect(menuItem(page, 'Applications')).toHaveCount(0);
    await expect(text).toBeEditable();
    const save = state.delayWrite();
    await text.press('ControlOrMeta+s');
    await save.pending;
    await text.press('ControlOrMeta+Home');
    await text.press('Shift+ArrowRight');
    await protocol.send('Input.imeSetComposition', {
      text: '续',
      selectionStart: 1,
      selectionEnd: 1,
    });
    await protocol.send('Input.insertText', { text: '续' });
    await expectEditorText(text, '续\n');
    save.release();
    await expect(
      browser(page).getByRole('button', { name: 'Save', exact: true }),
    ).toBeEnabled();
    await expectEditorText(text, '续\n');
    await text.press('ControlOrMeta+s');
    await expect.poll(() => state.writes.length).toBe(2);
    expect(state.writes).toEqual([
      {
        binding: originalBinding,
        path: 'main.ts',
        content: '终\n',
        expectedSha256: 'original-sha256',
      },
      {
        binding: originalBinding,
        path: 'main.ts',
        content: '续\n',
        expectedSha256: 'delayed-sha256',
      },
    ]);
  } finally {
    await protocol.detach();
  }
});
