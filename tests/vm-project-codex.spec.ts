import type { Page } from '@playwright/test';
import { activate, expect, menuItem, persistedLayout, test } from './helpers';
import type { CodexSessionRecord } from '../apps/dashboard/src/features/codex/api';
import type {
  VMProject,
  VMProjectBinding,
} from '../apps/dashboard/src/features/project-files/vm-api';

const project: VMProject = {
  id: 'codex-vm-project',
  hostId: 'codex-vm-host',
  kind: 'vm',
  name: 'VM coding project',
  volumeId: 'retained-code',
  root: 'repo',
  rootIdentity: 'verified-root',
  revision: 1,
  createdAt: '2026-10-21T00:00:00Z',
};
const originalBinding: VMProjectBinding = {
  hostId: project.hostId,
  projectId: project.id,
  projectRevision: 1,
  volumeId: project.volumeId,
  volumeRevision: 2,
  machineId: 'original-vm',
  bootId: 'original-boot',
  instanceId: 'original-instance',
};
const files = (page: Page) =>
  page.locator(
    `[data-window-id="project-files:vm:${project.hostId}:${project.id}"]`,
  );
const manager = (page: Page) =>
  page.locator(`[data-window-id="codex:vm:${project.hostId}:${project.id}"]`);
const runWindow = (page: Page, runId = 'vm-run-1') =>
  page.locator(`[data-window-id="codex:vm-session:${runId}"]`);

async function fixture(page: Page) {
  let binding = { ...originalBinding };
  let latestRun = '';
  let loseCreate = false;
  let loseResume = false;
  let rejectStart = false;
  let failFreshStart = false;
  let databaseChanged = false;
  let codexReady = true;
  let holdCreate = false;
  let releaseCreate: (() => void) | undefined;
  let nextStartError:
    { status: number; code: string; error: string } | undefined;
  let nextReservedStartError:
    { status: number; code: string; error: string } | undefined;
  let bindingReads = 0;
  let writes = 0;
  const starts: {
    requestId: string;
    projectPath?: string;
    vmProject: { binding: VMProjectBinding };
  }[] = [];
  const resumes: {
    requestId: string;
    expectedRunId: string;
    vmProject: { binding: VMProjectBinding };
  }[] = [];
  const records = new Map<string, CodexSessionRecord>();
  const startDatabases: string[] = [];
  const resumeDatabases: string[] = [];
  const receipts = new Map<string, CodexSessionRecord>();
  const makeRun = (
    runId: string,
    captured: VMProjectBinding,
  ): CodexSessionRecord => ({
    id: 'vm-session',
    runId,
    target: 'host',
    projectPath: '/axv-owned/empty-host-workspace',
    vmProject: { project, binding: { ...captured } },
    title: '',
    metadataRevision: 1,
    status: 'running',
    createdAt: project.createdAt,
    connections: 1,
    tmuxSession: `owned-${runId}`,
    tmuxSocket: '/owned-socket',
    attachCommand: '',
    conversationId: 'verified-provider-thread',
    continuation: { state: 'active', activeRunId: runId },
  });
  await page.routeWebSocket(
    /\/api\/host\/codex\/sessions\/.*\/terminal/,
    (socket) => {
      socket.send(JSON.stringify({ ready: true }));
      socket.send(Buffer.from('Host Codex fixture using AXV VM project\r\n'));
    },
  );
  await page.route('**/api/host/codex/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith('/info'))
      return route.fulfill({
        json: {
          supported: true,
          installed: true,
          ready: codexReady,
          reason: codexReady ? undefined : 'Codex is currently unavailable.',
          guidance: [],
          defaultProjectPath: '/default-host',
          configPath: '/fixture-config',
          network: { proxySource: 'direct' },
        },
      });
    if (path.endsWith('/projects'))
      return route.fulfill({ json: { projects: [] } });
    if (
      path === '/api/host/codex/sessions' &&
      route.request().method() === 'GET'
    )
      return route.fulfill({
        json: { sessions: latestRun ? [records.get(latestRun)] : [] },
      });
    if (
      path === '/api/host/codex/sessions' &&
      route.request().method() === 'POST'
    ) {
      const data = route.request().postDataJSON();
      starts.push(data);
      startDatabases.push(route.request().headers()['x-axv-database-id'] || '');
      if (nextStartError) {
        const error = nextStartError;
        nextStartError = undefined;
        return route.fulfill({ status: error.status, json: error });
      }
      if (databaseChanged)
        return route.fulfill({
          status: 409,
          json: {
            code: 'database_changed',
            error:
              'The desktop database changed. The original request remains in its original store.',
          },
        });
      if (rejectStart)
        return route.fulfill({
          status: 409,
          json: {
            code: 'vm_project_changed',
            error: 'The reviewed VM environment changed. Reconnect explicitly.',
          },
        });
      let session = receipts.get(data.requestId);
      if (!session) {
        session = makeRun('vm-run-1', data.vmProject.binding);
        if (failFreshStart) {
          failFreshStart = false;
          session = {
            ...session,
            status: 'failed',
            reason: 'The fixture provider could not start.',
            continuation: { state: 'available' },
            connections: 0,
          };
        }
        records.set(session.runId, session);
        receipts.set(data.requestId, session);
        latestRun = session.runId;
      }
      if (nextReservedStartError) {
        const error = nextReservedStartError;
        nextReservedStartError = undefined;
        return route.fulfill({ status: error.status, json: error });
      }
      if (loseCreate) {
        loseCreate = false;
        return route.abort('failed');
      }
      if (holdCreate) {
        holdCreate = false;
        await new Promise<void>((resolve) => {
          releaseCreate = resolve;
        });
      }
      return route.fulfill({ json: { session } });
    }
    if (path.endsWith('/resume')) {
      const data = route.request().postDataJSON();
      resumes.push(data);
      resumeDatabases.push(
        route.request().headers()['x-axv-database-id'] || '',
      );
      if (databaseChanged)
        return route.fulfill({
          status: 409,
          json: {
            code: 'database_changed',
            error: 'The desktop database changed.',
          },
        });
      let session = receipts.get(data.requestId);
      if (!session) {
        session = makeRun('vm-run-2', data.vmProject.binding);
        receipts.set(data.requestId, session);
        records.set(session.runId, session);
        latestRun = session.runId;
        const previous = records.get(data.expectedRunId)!;
        records.set(previous.runId, {
          ...previous,
          continuation: { state: 'active', activeRunId: latestRun },
        });
      }
      if (loseResume) {
        loseResume = false;
        return route.abort('failed');
      }
      return route.fulfill({ json: { session } });
    }
    const runId = path.match(/\/runs\/([^/]+)/)?.[1] || latestRun;
    return route.fulfill({ json: { session: records.get(runId) } });
  });
  await page.route('**/api/compute/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/compute/info')
      return route.fulfill({
        json: {
          configured: true,
          hostId: project.hostId,
          providers: [],
          images: [],
        },
      });
    if (path === '/api/compute/machines')
      return route.fulfill({
        json: {
          machines: [
            {
              id: binding.machineId,
              hostId: project.hostId,
              spec: {
                name: 'Coding VM',
                provider: 'fixture',
                kind: 'development',
                resources: { cpus: 2, memoryMiB: 1024, diskGiB: 8 },
                imageId: 'fixture',
                projectGiB: 4,
                retainVolume: true,
              },
              revision: 1,
              status: 'running',
              desired: 'running',
              ready: true,
              guestState: 'ready',
              guestCapabilities: ['project-files', 'project-tasks'],
              reserved: true,
              createdAt: project.createdAt,
              updatedAt: project.createdAt,
            },
          ],
        },
      });
    if (path === '/api/compute/volumes')
      return route.fulfill({
        json: {
          volumes: [
            {
              id: project.volumeId,
              name: 'Retained code',
              revision: 2,
              provider: 'fixture',
              sizeGiB: 4,
              retain: true,
              machineId: binding.machineId,
              status: 'ready',
            },
          ],
        },
      });
    if (path === '/api/compute/projects')
      return route.fulfill({ json: { projects: [project] } });
    if (path.endsWith('/binding')) {
      bindingReads++;
      return route.fulfill({ json: { project, binding } });
    }
    if (path.endsWith('/files/list'))
      return route.fulfill({
        json: {
          project,
          binding,
          entries: [
            {
              name: 'main.ts',
              path: 'main.ts',
              kind: 'file',
              size: 8,
              modifiedAt: project.createdAt,
            },
          ],
        },
      });
    if (path.endsWith('/files/read'))
      return route.fulfill({
        json: {
          project,
          binding,
          path: 'main.ts',
          content: 'Saved VM code\n',
          sha256: 'reviewed-hash',
          size: 14,
          modifiedAt: project.createdAt,
        },
      });
    if (path.endsWith('/files/write')) writes++;
    return route.fulfill({ json: { tasks: [] } });
  });
  await page.reload();
  return {
    starts,
    resumes,
    startDatabases,
    resumeDatabases,
    get launches() {
      return receipts.size;
    },
    get bindingReads() {
      return bindingReads;
    },
    get writes() {
      return writes;
    },
    loseNextCreate() {
      loseCreate = true;
    },
    holdNextCreate() {
      holdCreate = true;
    },
    releaseHeldCreate() {
      const release = releaseCreate;
      releaseCreate = undefined;
      release?.();
    },
    loseNextResume() {
      loseResume = true;
    },
    replaceVM() {
      binding = {
        ...binding,
        machineId: 'replacement-vm',
        bootId: 'replacement-boot',
        instanceId: 'replacement-instance',
        volumeRevision: 3,
      };
    },
    rejectStart(value: boolean) {
      rejectStart = value;
    },
    failNextFreshStart() {
      failFreshStart = true;
    },
    replaceDesktopStore() {
      databaseChanged = true;
    },
    makeCodexUnavailable() {
      codexReady = false;
      const current = records.get(latestRun)!;
      records.set(latestRun, {
        ...current,
        status: 'ended',
        connections: 0,
        continuation: {
          state: 'unavailable',
          reason: 'The saved provider history is currently unavailable.',
        },
      });
    },
    refuseNextStartDuringShutdown() {
      nextStartError = {
        status: 503,
        code: 'server_stopping',
        error: 'AXV is stopping. The original request outcome is unconfirmed.',
      };
    },
    conflictNextReservedStart() {
      nextReservedStartError = {
        status: 409,
        code: 'history_identity_conflict',
        error: 'The reserved run reported conflicting conversation identity.',
      };
    },
    endRun() {
      const current = records.get(latestRun)!;
      records.set(latestRun, {
        ...current,
        status: 'ended',
        connections: 0,
        continuation: { state: 'available' },
      });
    },
  };
}

async function openProject(page: Page) {
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await menuItem(page, 'Project Files').click();
  const projects = page.locator('[data-window-id="project-files:projects"]');
  await projects.getByRole('button', { name: /^VM coding project ·/ }).click();
  await expect(files(page)).toBeVisible();
  await files(page)
    .getByRole('button', { name: 'main.ts', exact: true })
    .click();
  await expect(
    files(page).getByRole('textbox', { name: 'VM project file text' }),
  ).toBeVisible();
}

async function openManager(page: Page, touch = false) {
  const open = files(page).getByRole('button', {
    name: 'Open with Codex',
    exact: true,
  });
  if (touch) await open.tap();
  else await open.click();
  await expect(
    manager(page).getByRole('button', { name: 'New Session', exact: true }),
  ).toBeEnabled();
}

test('VM project Codex keeps drafts and routes files and tasks to the captured project across reload', async ({
  page,
  request,
}, testInfo) => {
  const state = await fixture(page);
  await openProject(page);
  const text = files(page).getByRole('textbox', {
    name: 'VM project file text',
  });
  await text.fill('Unsaved VM draft');
  const touch = !!testInfo.project.use.hasTouch;
  await openManager(page, touch);
  await expect(
    manager(page).getByRole('region', { name: 'VM project context' }),
  ).toContainText('Codex runs on the host');
  await expect(
    manager(page).getByRole('button', { name: 'New in Folder', exact: true }),
  ).toHaveCount(0);
  expect(state.starts).toHaveLength(0);
  const start = manager(page).getByRole('button', {
    name: 'New Session',
    exact: true,
  });
  if (touch) await start.tap();
  else {
    await start.focus();
    await start.press('Enter');
  }
  await expect(runWindow(page).getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  expect(state.starts).toHaveLength(1);
  expect(state.starts[0].vmProject.binding).toEqual(originalBinding);
  expect(state.starts[0].projectPath).toBeUndefined();
  expect(state.startDatabases[0]).not.toBe('');
  await expect(runWindow(page).getByTestId('codex-project-name')).toHaveText(
    'VM · VM coding project',
  );
  await runWindow(page)
    .getByRole('button', { name: 'Project Files', exact: true })
    .click();
  await expect(text).toHaveValue('Unsaved VM draft');
  expect(state.writes).toBe(0);
  await activate(page, runWindow(page));
  await runWindow(page)
    .getByRole('button', { name: 'Tasks', exact: true })
    .click();
  await expect(
    page.locator(
      `[data-window-id="project-files:vm-tasks:${project.hostId}:${project.id}"]`,
    ),
  ).toBeVisible();
  await activate(page, runWindow(page));
  const screenshot = testInfo.outputPath('vm-project-codex.png');
  await page.screenshot({ path: screenshot });
  await testInfo.attach('VM project Codex', {
    path: screenshot,
    contentType: 'image/png',
  });
  const layout = await persistedLayout(page, request);
  expect(JSON.stringify(layout)).not.toContain(
    '/axv-owned/empty-host-workspace',
  );
  page.once('dialog', (dialog) => void dialog.accept());
  await page.reload();
  await activate(page, runWindow(page));
  await expect(runWindow(page).getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  expect(state.starts).toHaveLength(1);
  await runWindow(page)
    .getByRole('button', { name: 'Project Files', exact: true })
    .click();
  await files(page)
    .getByRole('button', { name: 'Restore Draft', exact: true })
    .click();
  await expect(text).toHaveValue('Unsaved VM draft');
  expect(state.writes).toBe(0);
});

test('an unknown VM Codex start reuses its full original request after reload and VM replacement', async ({
  page,
}) => {
  const state = await fixture(page);
  state.loseNextCreate();
  await openProject(page);
  await openManager(page);
  await manager(page)
    .getByRole('button', { name: 'New Session', exact: true })
    .click();
  await expect(
    manager(page).getByRole('button', { name: 'Resolve Start', exact: true }),
  ).toBeEnabled();
  state.refuseNextStartDuringShutdown();
  await manager(page)
    .getByRole('button', { name: 'Resolve Start', exact: true })
    .click();
  await expect(manager(page).getByRole('alert')).toContainText(
    'AXV is stopping',
  );
  await expect(
    manager(page).getByRole('button', { name: 'Resolve Start', exact: true }),
  ).toBeEnabled();
  state.replaceVM();
  await page.reload();
  await activate(page, manager(page));
  await expect(
    manager(page).getByRole('button', {
      name: 'Reconnect Project',
      exact: true,
    }),
  ).toBeDisabled();
  await manager(page)
    .getByRole('button', { name: 'Resolve Start', exact: true })
    .click();
  await expect(runWindow(page).getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  expect(state.starts).toHaveLength(3);
  expect(state.starts[1]).toEqual(state.starts[0]);
  expect(state.starts[2]).toEqual(state.starts[0]);
  expect(state.startDatabases[1]).toBe(state.startDatabases[0]);
  await expect(runWindow(page).locator('.codex-vm-target')).toContainText(
    'original-vm',
  );
});

test('a reopened manager does not turn a stale Resolve Start action into another conversation', async ({
  page,
}) => {
  const state = await fixture(page);
  await openProject(page);
  await openManager(page);
  state.holdNextCreate();
  try {
    await manager(page)
      .getByRole('button', { name: 'New Session', exact: true })
      .click();
    await expect.poll(() => state.launches).toBe(1);
    await manager(page)
      .getByRole('button', { name: 'Close Codex Sessions · VM coding project' })
      .click();
    await expect(manager(page)).toHaveCount(0);
    await activate(page, files(page));
    await files(page)
      .getByRole('button', { name: 'Open with Codex', exact: true })
      .click();
    const resolve = manager(page).getByRole('button', {
      name: 'Resolve Start',
      exact: true,
    });
    await expect(resolve).toBeEnabled();
    const key = await page.evaluate(() =>
      Object.keys(sessionStorage).find((item) =>
        item.includes('axv-codex-vm-start'),
      ),
    );
    expect(key).toBeTruthy();
    state.releaseHeldCreate();
    await expect
      .poll(() =>
        page.evaluate(() =>
          Object.keys(sessionStorage).some((key) =>
            key.includes('axv-codex-vm-start'),
          ),
        ),
      )
      .toBe(false);
    await resolve.click();
    await expect(manager(page).getByRole('alert')).toContainText(
      'start request changed in another window',
    );
    await expect(
      manager(page).getByRole('button', { name: 'New Session', exact: true }),
    ).toBeEnabled();
    expect(state.starts).toHaveLength(1);
    expect(state.launches).toBe(1);
    await page.evaluate(
      ({ key, binding }) => {
        sessionStorage.setItem(
          key,
          JSON.stringify({ requestId: crypto.randomUUID(), binding }),
        );
      },
      { key: key!, binding: originalBinding },
    );
    await manager(page)
      .getByRole('button', { name: 'New Session', exact: true })
      .click();
    await expect(resolve).toBeEnabled();
    await expect(manager(page).getByRole('alert')).toContainText(
      'start request changed in another window',
    );
    expect(state.starts).toHaveLength(1);
  } finally {
    state.releaseHeldCreate();
  }
});

test('a reserved history conflict and desktop store replacement preserve the original VM start request', async ({
  page,
}) => {
  const state = await fixture(page);
  state.conflictNextReservedStart();
  await openProject(page);
  await openManager(page);
  await manager(page)
    .getByRole('button', { name: 'New Session', exact: true })
    .click();
  const resolve = manager(page).getByRole('button', {
    name: 'Resolve Start',
    exact: true,
  });
  await expect(resolve).toBeEnabled();
  const original = state.starts[0];
  const sourceDatabase = state.startDatabases[0];
  expect(sourceDatabase).not.toBe('');
  state.replaceDesktopStore();
  await resolve.click();
  await expect(manager(page).getByRole('alert')).toContainText(
    'desktop database changed',
  );
  await expect(resolve).toBeEnabled();
  await expect(
    manager(page).getByRole('button', {
      name: 'Reconnect Project',
      exact: true,
    }),
  ).toBeDisabled();
  await page.reload();
  await expect(resolve).toBeEnabled();
  await resolve.click();
  await expect(manager(page).getByRole('alert')).toContainText(
    'desktop database changed',
  );
  expect(state.starts).toEqual([original, original, original]);
  expect(state.startDatabases).toEqual([
    sourceDatabase,
    sourceDatabase,
    sourceDatabase,
  ]);
  expect(state.launches).toBe(1);
});

test('a rejected VM start keeps its binding until reconnect and a fresh failed start remains visible', async ({
  page,
}) => {
  const state = await fixture(page);
  await openProject(page);
  await openManager(page);
  state.replaceVM();
  state.rejectStart(true);
  await manager(page)
    .getByRole('button', { name: 'New Session', exact: true })
    .click();
  await expect(manager(page).getByRole('alert')).toContainText(
    'Reconnect explicitly',
  );
  expect(state.starts[0].vmProject.binding.machineId).toBe('original-vm');
  await expect(
    manager(page).getByRole('region', { name: 'VM project context' }),
  ).toContainText('original-vm');
  state.rejectStart(false);
  await manager(page)
    .getByRole('button', { name: 'Reconnect Project', exact: true })
    .click();
  await expect(
    manager(page).getByRole('region', { name: 'VM project context' }),
  ).toContainText('replacement-vm');
  state.failNextFreshStart();
  await manager(page)
    .getByRole('button', { name: 'New Session', exact: true })
    .click();
  await expect(manager(page).getByRole('alert')).toContainText(
    'The fixture provider could not start.',
  );
  await expect(runWindow(page)).toHaveCount(0);
  await expect(
    manager(page).getByRole('button', { name: 'New Session', exact: true }),
  ).toBeEnabled();
  expect(state.starts[1].vmProject.binding.machineId).toBe('replacement-vm');
  expect(state.starts[1].requestId).not.toBe(state.starts[0].requestId);
});

test('resolving an unknown VM start opens its exact run after that run already ended', async ({
  page,
}) => {
  const state = await fixture(page);
  state.loseNextCreate();
  await openProject(page);
  await openManager(page);
  await manager(page)
    .getByRole('button', { name: 'New Session', exact: true })
    .click();
  await expect(
    manager(page).getByRole('button', { name: 'Resolve Start', exact: true }),
  ).toBeEnabled();
  state.endRun();
  await page.reload();
  await manager(page)
    .getByRole('button', { name: 'Resolve Start', exact: true })
    .click();
  await expect(runWindow(page).getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'ended',
  );
  await expect(runWindow(page)).toHaveClass(/is-active/);
  expect(state.starts).toHaveLength(2);
  expect(state.starts[1]).toEqual(state.starts[0]);
  expect(state.launches).toBe(1);
  await expect(manager(page).getByRole('alert')).toHaveCount(0);
});

test('resolving an unknown VM continuation opens its exact run after that run already ended', async ({
  page,
}) => {
  const state = await fixture(page);
  await openProject(page);
  await openManager(page);
  await manager(page)
    .getByRole('button', { name: 'New Session', exact: true })
    .click();
  await expect(runWindow(page).getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  state.endRun();
  await page.reload();
  await expect(runWindow(page)).toHaveAttribute(
    'aria-label',
    /Ended \(vm-run-1\)$/,
  );
  await activate(page, runWindow(page));
  state.loseNextResume();
  await runWindow(page)
    .getByRole('button', { name: 'Continue', exact: true })
    .click();
  const dialog = page.getByRole('dialog', { name: 'Continue Conversation?' });
  await dialog.getByRole('button', { name: 'Continue', exact: true }).click();
  await expect(
    runWindow(page).getByRole('button', {
      name: 'Resolve Continue',
      exact: true,
    }),
  ).toBeEnabled();
  state.endRun();
  await page.reload();
  await expect(runWindow(page)).toHaveAttribute(
    'aria-label',
    /Ended \(vm-run-1\)$/,
  );
  await activate(page, manager(page));
  const row = manager(page).locator('[data-session-id="vm-session"]');
  await expect(
    row.getByRole('button', { name: 'Resolve Continue', exact: true }),
  ).toBeEnabled();
  await activate(page, runWindow(page));
  await runWindow(page)
    .getByRole('button', { name: 'Resolve Continue', exact: true })
    .click();
  await dialog.getByRole('button', { name: 'Continue', exact: true }).click();
  const resolved = runWindow(page, 'vm-run-2');
  await expect(resolved.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'ended',
  );
  await expect(resolved).toHaveClass(/is-active/);
  expect(state.resumes).toHaveLength(2);
  expect(state.resumes[1]).toEqual(state.resumes[0]);
  expect(state.launches).toBe(2);
  await expect(runWindow(page).getByTestId('codex-session')).toHaveAttribute(
    'data-run-id',
    'vm-run-1',
  );
  await activate(page, manager(page));
  await manager(page)
    .getByRole('button', { name: 'Refresh', exact: true })
    .click();
  await expect(
    row.getByRole('button', { name: 'Resolve Continue', exact: true }),
  ).toHaveCount(0);
  await expect(
    row.getByRole('button', { name: 'Continue', exact: true }),
  ).toBeEnabled();
  expect(state.resumes).toHaveLength(2);
});

test('the manager resolves an unknown continuation after reload when fresh continuations are unavailable', async ({
  page,
}) => {
  const state = await fixture(page);
  await openProject(page);
  await openManager(page);
  await manager(page)
    .getByRole('button', { name: 'New Session', exact: true })
    .click();
  await expect(runWindow(page).getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  state.endRun();
  await page.reload();
  await activate(page, manager(page));
  const row = manager(page).locator('[data-session-id="vm-session"]');
  state.loseNextResume();
  await row.getByRole('button', { name: 'Continue', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Continue Conversation?' });
  await dialog.getByRole('button', { name: 'Continue', exact: true }).click();
  await expect(
    row.getByRole('button', { name: 'Resolve Continue', exact: true }),
  ).toBeEnabled();
  state.makeCodexUnavailable();
  state.replaceVM();
  await page.reload();
  await activate(page, manager(page));
  await expect(
    manager(page).getByRole('button', { name: 'New Session', exact: true }),
  ).toBeDisabled();
  await expect(
    manager(page).getByRole('region', { name: 'VM project context' }),
  ).toContainText('replacement-vm');
  const bindingReads = state.bindingReads;
  const resolve = row.getByRole('button', {
    name: 'Resolve Continue',
    exact: true,
  });
  await expect(resolve).toBeEnabled();
  await resolve.click();
  await expect(dialog).toContainText('original-vm');
  await dialog.getByRole('button', { name: 'Continue', exact: true }).click();
  await expect(runWindow(page, 'vm-run-2')).toHaveClass(/is-active/);
  expect(state.resumes).toHaveLength(2);
  expect(state.resumes[1]).toEqual(state.resumes[0]);
  expect(state.bindingReads).toBe(bindingReads);
  expect(state.launches).toBe(2);
});

test('the manager preserves damaged continuation recovery and blocks a fresh request', async ({
  page,
}) => {
  const state = await fixture(page);
  await openProject(page);
  await openManager(page);
  await manager(page)
    .getByRole('button', { name: 'New Session', exact: true })
    .click();
  await expect(runWindow(page).getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  state.endRun();
  await page.reload();
  await activate(page, manager(page));
  const row = manager(page).locator('[data-session-id="vm-session"]');
  state.loseNextResume();
  await row.getByRole('button', { name: 'Continue', exact: true }).click();
  await page
    .getByRole('dialog', { name: 'Continue Conversation?' })
    .getByRole('button', { name: 'Continue', exact: true })
    .click();
  await expect(
    row.getByRole('button', { name: 'Resolve Continue', exact: true }),
  ).toBeEnabled();
  const key = await page.evaluate(() => {
    const saved = Object.keys(sessionStorage).find((item) =>
      item.includes('axv-codex-vm-resume'),
    );
    if (!saved) throw new Error('The original continuation was not recorded.');
    sessionStorage.setItem(saved, '{damaged');
    return saved;
  });
  state.endRun();
  await page.reload();
  await activate(page, manager(page));
  await expect(row.getByRole('alert')).toContainText('could not be read');
  await expect(
    row.getByRole('button', { name: 'Continue', exact: true }),
  ).toBeDisabled();
  expect(await page.evaluate((item) => sessionStorage.getItem(item), key)).toBe(
    '{damaged',
  );
  expect(state.resumes).toHaveLength(1);
});

test('VM continuation confirms the replacement explicitly and resolves an unknown request after reload', async ({
  page,
}) => {
  const state = await fixture(page);
  await openProject(page);
  await openManager(page);
  await manager(page)
    .getByRole('button', { name: 'New Session', exact: true })
    .click();
  await expect(runWindow(page).getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  state.endRun();
  state.replaceVM();
  await page.reload();
  await expect(runWindow(page).getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'ended',
  );
  await expect(runWindow(page)).toHaveAttribute(
    'aria-label',
    /Ended \(vm-run-1\)$/,
  );
  await activate(page, runWindow(page));
  await runWindow(page)
    .getByRole('button', { name: 'Continue', exact: true })
    .click();
  const dialog = page.getByRole('dialog', { name: 'Continue Conversation?' });
  await expect(dialog).toContainText('replacement-vm');
  await expect(dialog).toContainText('original-vm');
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  expect(state.resumes).toHaveLength(0);
  state.loseNextResume();
  await runWindow(page)
    .getByRole('button', { name: 'Continue', exact: true })
    .click();
  await dialog.getByRole('button', { name: 'Continue', exact: true }).click();
  await expect(
    runWindow(page).getByRole('button', {
      name: 'Resolve Continue',
      exact: true,
    }),
  ).toBeEnabled();
  expect(state.resumes[0].vmProject.binding.machineId).toBe('replacement-vm');
  state.replaceVM();
  await page.reload();
  await expect(runWindow(page)).toHaveAttribute(
    'aria-label',
    /Ended \(vm-run-1\)$/,
  );
  await activate(page, runWindow(page));
  const reads = state.bindingReads;
  await runWindow(page)
    .getByRole('button', { name: 'Resolve Continue', exact: true })
    .click();
  await expect(dialog).toContainText('Original requested VM');
  await dialog.getByRole('button', { name: 'Continue', exact: true }).click();
  await expect(
    runWindow(page, 'vm-run-2').getByTestId('codex-status'),
  ).toHaveAttribute('data-state', 'connected');
  expect(state.resumes).toHaveLength(2);
  expect(state.resumes[1]).toEqual(state.resumes[0]);
  expect(state.resumeDatabases[0]).not.toBe('');
  expect(state.resumeDatabases[1]).toBe(state.resumeDatabases[0]);
  expect(state.bindingReads).toBe(reads);
  await activate(page, runWindow(page));
  await expect(runWindow(page).getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'ended',
  );
  await expect(runWindow(page).locator('.codex-vm-target')).toContainText(
    'original-vm',
  );
});

test.describe('fractional narrow VM project Codex', () => {
  test.use({ viewport: { width: 320, height: 740 }, deviceScaleFactor: 1.25 });

  test('keeps VM context and actions reachable with keyboard and dialog focus return', async ({
    page,
  }, testInfo) => {
    await fixture(page);
    await openProject(page);
    await openManager(page);
    const scoped = manager(page);
    const overflow = (target: ReturnType<typeof manager>) =>
      target
        .locator('.ns-window-content')
        .evaluate((element) => element.scrollWidth > element.clientWidth + 1);
    await expect.poll(() => overflow(scoped)).toBe(false);
    const managerScreenshot = testInfo.outputPath(
      'vm-project-codex-manager-narrow.png',
    );
    await page.screenshot({ path: managerScreenshot });
    await testInfo.attach('VM project Codex manager at 320px and DPR 1.25', {
      path: managerScreenshot,
      contentType: 'image/png',
    });
    const start = scoped.getByRole('button', {
      name: 'New Session',
      exact: true,
    });
    await start.focus();
    await start.press('Enter');
    const run = runWindow(page);
    await expect(run.getByTestId('codex-status')).toHaveAttribute(
      'data-state',
      'connected',
    );
    await expect
      .poll(
        async () =>
          (await run.getByTestId('codex-project-name').boundingBox())?.width ||
          0,
      )
      .toBeGreaterThan(80);
    await expect.poll(() => overflow(run)).toBe(false);
    const details = run.getByRole('button', { name: 'Details', exact: true });
    await details.focus();
    await details.press('Enter');
    const dialog = page.getByRole('dialog', { name: 'Codex Session Details' });
    await expect(dialog).toContainText('VM coding project');
    await expect(run.getByLabel('Codex terminal input')).not.toBeFocused();
    await dialog.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(details).toBeFocused();
    const runScreenshot = testInfo.outputPath(
      'vm-project-codex-run-narrow.png',
    );
    await page.screenshot({ path: runScreenshot });
    await testInfo.attach('VM project Codex run at 320px and DPR 1.25', {
      path: runScreenshot,
      contentType: 'image/png',
    });
  });
});
