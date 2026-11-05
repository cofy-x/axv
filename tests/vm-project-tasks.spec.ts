import {
  codeEditor,
  editVMFile,
  expectEditorText,
} from './code-editor-helpers';
import type { Locator, Page } from '@playwright/test';
import {
  expect,
  menuItem,
  persistedLayout,
  switchWorkspace,
  test,
} from './helpers';
import type {
  VMTaskResult,
  VMTaskSubmission,
} from '../apps/dashboard/src/features/project-files/vm-task-api';

const project = {
  id: 'task-project',
  hostId: 'task-host',
  name: 'Task repository',
  kind: 'vm',
  volumeId: 'retained-volume',
  root: 'repo',
  rootIdentity: 'root-one',
  revision: 1,
  createdAt: '2026-10-20T00:00:00Z',
};
const binding = {
  hostId: project.hostId,
  projectId: project.id,
  projectRevision: 1,
  volumeId: project.volumeId,
  volumeRevision: 2,
  machineId: 'task-vm',
  bootId: 'task-boot',
  instanceId: 'task-guest',
};
const taskWindowId = `project-files:vm-tasks:${project.hostId}:${project.id}`;
const tasksWindow = (page: Page) =>
  page.locator(`[data-window-id="${taskWindowId}"]`);
const filesWindow = (page: Page) =>
  page.locator(
    `[data-window-id="project-files:vm:${project.hostId}:${project.id}"]`,
  );
const output = 'Build: 你好🙂\n<script>window.taskLogExecuted=true</script>\n';

function result(id: string, command: string, status = 'running'): VMTaskResult {
  const attempt = {
    id: `${id}-attempt-1`,
    taskId: id,
    number: 1,
    binding: { ...binding },
    root: project.root,
    rootIdentity: project.rootIdentity,
    requestId: `${id}-request`,
    status,
    createdAt: project.createdAt,
    logBytes: Buffer.byteLength(output),
    cachedBytes: Buffer.byteLength(output),
    logLimited: false,
    guestFinal: ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(
      status,
    ),
    cancelRequested: false,
  };
  return {
    task: {
      id,
      hostId: project.hostId,
      projectId: project.id,
      requestId: attempt.requestId,
      currentAttemptId: attempt.id,
      createdAt: project.createdAt,
      spec: {
        argv: ['/bin/sh', '-lc', command],
        directory: '',
        limits: {
          timeoutSeconds: 900,
          cpus: 1,
          memoryMiB: 512,
          maxProcesses: 64,
          maxLogBytes: 1 << 20,
        },
      },
    },
    attempt,
    attempts: [attempt],
  };
}

async function fixture(
  page: Page,
  initial: VMTaskResult[] = [],
  options: { logText?: string; logChunkBytes?: number } = {},
) {
  const values = new Map(initial.map((item) => [item.task.id, item]));
  const taskOutputs = new Map<string, string>();
  const logText = options.logText ?? output;

  function setOutput(taskId: string, text: string) {
    taskOutputs.set(taskId, text);
    const item = values.get(taskId);
    if (item)
      item.attempt.logBytes = item.attempt.cachedBytes =
        Buffer.byteLength(text);
  }

  for (const item of initial) setOutput(item.task.id, logText);
  const submissions: VMTaskSubmission[] = [];
  const cancellations: {
    taskId: string;
    attemptId: string;
    binding: typeof binding;
  }[] = [];
  const retries: VMTaskSubmission[] = [];
  const queriedHosts: string[] = [];
  const logRequests: { taskId: string; offset: number; limit: number }[] = [];
  let reads = 0;
  let bindingReads = 0;
  let offline = false;
  let changedHost = false;
  let loseSubmission = false;
  let loseRetry = false;
  let cancelFailure = false;
  let listWait: Promise<void> | undefined;
  let releaseList: (() => void) | undefined;
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
    if (path === '/api/compute/machines') return json({ machines: [] });
    if (path === '/api/compute/volumes') return json({ volumes: [] });
    if (path === '/api/compute/projects') return json({ projects: [project] });
    if (!path.startsWith(`/api/compute/projects/${project.id}/`))
      return route.continue();
    expect(route.request().headers()['x-axv-host-id']).toBe(project.hostId);
    if (path.endsWith('/binding')) {
      bindingReads++;
      return changedHost
        ? json({ code: 'host_changed', error: 'The VM host changed.' }, 409)
        : json({ project, binding });
    }
    if (path.endsWith('/files/list'))
      return json({
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
      });
    if (path.endsWith('/files/read'))
      return json({
        project,
        binding,
        path: 'main.ts',
        content: 'Saved file',
        sha256: 'saved-sha',
        size: 10,
        modifiedAt: project.createdAt,
      });
    reads++;
    queriedHosts.push(route.request().headers()['x-axv-host-id']);
    if (changedHost)
      return json({ code: 'host_changed', error: 'The VM host changed.' }, 409);
    if (offline)
      return json(
        { code: 'unavailable', error: 'Task controller unavailable.' },
        503,
      );
    if (path.includes('/task-requests/')) {
      const requestId = path.split('/').at(-1);
      const found = [...values.values()].find(
        (item) =>
          item.task.requestId === requestId ||
          item.attempts.some((attempt) => attempt.requestId === requestId),
      );
      return found
        ? json(found)
        : json({ code: 'not_found', error: 'Submission not accepted.' }, 404);
    }
    const parts = path.split('/');
    const taskIndex = parts.indexOf('tasks');
    const taskId = parts[taskIndex + 1];
    if (!taskId) {
      if (route.request().method() === 'GET') {
        await listWait;
        return json({ tasks: [...values.values()] });
      }
      const data = route.request().postDataJSON() as VMTaskSubmission;
      submissions.push(data);
      let item = [...values.values()].find(
        (value) => value.task.requestId === data.requestId,
      );
      if (!item) {
        item = result(`task-${values.size + 1}`, data.spec!.argv[2]);
        item.task.requestId = item.attempt.requestId = data.requestId;
        item.task.spec = data.spec!;
        item.attempt.binding = data.binding;
        values.set(item.task.id, item);
        setOutput(item.task.id, logText);
      }
      if (loseSubmission) {
        loseSubmission = false;
        return route.abort('failed');
      }
      return json(item, 202);
    }
    const item = values.get(taskId);
    if (!item)
      return json({ code: 'not_found', error: 'Task not found.' }, 404);
    if (path.endsWith('/retry')) {
      const data = route.request().postDataJSON() as VMTaskSubmission;
      retries.push(data);
      const attempt = {
        ...item.attempt,
        id: `${taskId}-attempt-${item.attempt.number + 1}`,
        number: item.attempt.number + 1,
        binding: data.binding,
        requestId: data.requestId,
        status: 'running',
        guestFinal: false,
        cancelRequested: false,
      };
      item.attempt = attempt;
      item.attempts.push(attempt);
      item.task.currentAttemptId = attempt.id;
      if (loseRetry) {
        loseRetry = false;
        return route.abort('failed');
      }
      return json(item, 202);
    }
    if (path.endsWith('/cancel')) {
      const attemptId = parts[parts.indexOf('attempts') + 1];
      cancellations.push({
        taskId,
        attemptId,
        binding: route.request().postDataJSON().binding,
      });
      if (cancelFailure)
        return json(
          {
            code: 'guest_unavailable',
            error: 'Cancellation could not reach the original VM.',
          },
          503,
        );
      const target = item.attempts.find((attempt) => attempt.id === attemptId)!;
      if (
        !['succeeded', 'failed', 'cancelled', 'interrupted'].includes(
          target.status,
        )
      ) {
        target.status = 'cancelled';
        target.cancelRequested = true;
      }
      return json(item);
    }
    if (path.endsWith('/logs')) {
      const bytes = Buffer.from(taskOutputs.get(taskId) ?? logText);
      const offset = Number(url.searchParams.get('offset'));
      const limit = Number(url.searchParams.get('limit')) || 65536;
      logRequests.push({ taskId, offset, limit });
      const end = Math.min(
        offset + Math.min(limit, options.logChunkBytes ?? 65536),
        bytes.length,
      );
      const attemptId = parts[parts.indexOf('attempts') + 1];
      const attempt = item.attempts.find((value) => value.id === attemptId);
      if (!attempt)
        return json({ code: 'not_found', error: 'Attempt not found.' }, 404);
      return json({
        taskId,
        attemptId,
        status: attempt.status,
        offset,
        nextOffset: end,
        logBytes: bytes.length,
        data: bytes.subarray(offset, end).toString('base64'),
      });
    }
    return json(item);
  });
  return {
    values,
    submissions,
    cancellations,
    retries,
    queriedHosts,
    logRequests,
    setOutput,
    get reads() {
      return reads;
    },
    get bindingReads() {
      return bindingReads;
    },
    offline() {
      offline = true;
    },
    online() {
      offline = false;
    },
    changeHost() {
      changedHost = true;
    },
    loseSubmission() {
      loseSubmission = true;
    },
    loseRetry() {
      loseRetry = true;
    },
    failCancellation() {
      cancelFailure = true;
    },
    pauseList() {
      listWait = new Promise<void>((resolve) => {
        releaseList = resolve;
      });
    },
    resumeList() {
      releaseList?.();
      listWait = undefined;
    },
  };
}

async function openTasks(page: Page) {
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await menuItem(page, 'Project Files').click();
  await page
    .locator('[data-window-id="project-files:projects"]')
    .getByRole('tab', { name: 'VM', exact: true })
    .click();
  await page
    .locator('[data-window-id="project-files:projects"]')
    .getByRole('button', { name: /^Task repository ·/ })
    .click();
  await filesWindow(page)
    .getByRole('button', { name: 'main.ts', exact: true })
    .click();
  const editor = codeEditor(filesWindow(page));
  await expectEditorText(editor, 'Saved file');
  await editVMFile(filesWindow(page));
  await editor.fill('Keep unsaved file typing');
  await filesWindow(page)
    .getByRole('button', { name: 'Tasks', exact: true })
    .click();
  await expect(tasksWindow(page)).toBeVisible();
  return codeEditor(filesWindow(page));
}

async function selectTask(page: Page, command: string | RegExp) {
  const panel = tasksWindow(page);
  await expect(
    panel.getByRole('button', { name: 'New Task', exact: true }),
  ).toBeVisible();
  const back = panel.getByRole('button', {
    name: 'Back to Tasks',
    exact: true,
  });
  const navigation = panel.getByRole('navigation', {
    name: 'Project tasks',
    includeHidden: true,
  });
  const item = navigation.getByRole('button', {
    name: command,
    includeHidden: true,
  });
  await expect(item).toBeAttached();
  if (await back.isVisible()) await back.click();
  await expect(navigation).toBeVisible();
  await item.click();
}

async function expectSelectedAttempt(panel: Locator, attemptId: string) {
  await expect(
    panel.getByText('Execution details', { exact: true }).locator('..'),
  ).toContainText(attemptId);
  const selector = panel.getByRole('combobox', {
    name: 'Attempt',
    exact: true,
  });
  if (await selector.count()) await expect(selector).toHaveValue(attemptId);
}

test('task workspace comparison shows a realistic build and a new command', async ({
  page,
}, testInfo) => {
  const build = result('build4b9c', 'pnpm run build');
  const tests = result('tests8d2a', 'pnpm test', 'succeeded');
  build.task.createdAt = build.attempt.createdAt = '2026-10-20T09:42:00Z';
  tests.task.createdAt = tests.attempt.createdAt = '2026-10-20T09:38:00Z';
  tests.attempt.exitCode = 0;
  const buildOutput = [
    '> task-repository@1.0.0 build /workspace/repo',
    '> tsc -b && vite build',
    '',
    'vite v7.2.0 building client environment for production...',
    'transforming...',
    '✓ 248 modules transformed.',
    'rendering chunks...',
    'computing gzip size...',
    'dist/index.html                   0.48 kB │ gzip:  0.31 kB',
    'dist/assets/index-4ba93c.css      14.62 kB │ gzip:  3.54 kB',
    'dist/assets/index-7ad08f.js      186.43 kB │ gzip: 58.21 kB',
    '',
    'Checking generated assets...',
  ].join('\n');
  await fixture(page, [build, tests], { logText: buildOutput });
  await openTasks(page);
  const panel = tasksWindow(page);
  await selectTask(page, 'pnpm run build');
  await expect(panel.getByLabel('Task output')).toHaveText(buildOutput);
  const overview = testInfo.outputPath('vm-task-overview.png');
  await panel.screenshot({ path: overview });
  await testInfo.attach('VM task overview comparison', {
    path: overview,
    contentType: 'image/png',
  });
  await panel.getByRole('button', { name: 'New Task', exact: true }).click();
  await panel
    .getByRole('textbox', { name: 'Shell command', exact: true })
    .fill('pnpm install --frozen-lockfile && pnpm test');
  const composer = testInfo.outputPath('vm-task-compose.png');
  await panel.screenshot({ path: composer });
  await testInfo.attach('VM task composer comparison', {
    path: composer,
    contentType: 'image/png',
  });
});

test('task overview keeps secondary actions and detailed output metadata out of the main workflow', async ({
  page,
}) => {
  const item = result('compact-build', 'pnpm run build');
  const state = await fixture(page, [item]);
  const editor = await openTasks(page);
  const panel = tasksWindow(page);
  await selectTask(page, /pnpm run build/);
  await expect(panel.getByLabel('Task output')).toHaveText(output);
  await expect(panel.getByLabel('Attempt status', { exact: true })).toHaveText(
    'running · Attempt 1',
  );
  await expect(
    panel.getByRole('combobox', { name: 'Attempt', exact: true }),
  ).toHaveCount(0);
  for (const name of ['First', 'Previous', 'Next'])
    await expect(panel.getByRole('button', { name, exact: true })).toHaveCount(
      0,
    );
  await expect(
    panel.getByRole('button', { name: 'Latest', exact: true }),
  ).toBeVisible();
  await expect(panel.getByText(/^Bytes /)).toBeHidden();
  await expect(
    panel.getByRole('button', { name: 'Project Files', exact: true }),
  ).toHaveCount(0);
  await expect(
    panel.getByRole('button', { name: 'Reconnect VM', exact: true }),
  ).toHaveCount(0);
  const more = panel.getByRole('button', {
    name: 'More task actions',
    exact: true,
  });
  await more.click();
  await expect(menuItem(page, 'Reconnect VM')).toBeVisible();
  await expect(menuItem(page, 'Project Files')).toBeVisible();
  await expect(menuItem(page, 'Task Help')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(more).toBeFocused();
  const readsBeforeReconnect = state.bindingReads;
  await more.click();
  await menuItem(page, 'Reconnect VM').click();
  await expect.poll(() => state.bindingReads).toBe(readsBeforeReconnect + 1);
  await more.click();
  await menuItem(page, 'Task Help').click();
  const help = page.getByRole('dialog');
  await expect(help).toContainText('/bin/sh -lc');
  await expect(help).toContainText('browser');
  await page.keyboard.press('Escape');
  await expect(help).toHaveCount(0);
  await expect(more).toBeFocused();
  await more.click();
  await menuItem(page, 'Task Help').click();
  await help.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(help).toHaveCount(0);
  await expect(more).toBeFocused();
  await more.click();
  await menuItem(page, 'Project Files').click();
  await expect(editor).toBeVisible();
  await expect(editor).toBeEditable();
  await expectEditorText(editor, 'Keep unsaved file typing');
  await expect(panel).toHaveCount(1);
});

test('a finished single-page attempt shows its result without paging or follow controls', async ({
  page,
}) => {
  const item = result('finished-build', 'pnpm test', 'succeeded');
  item.attempt.exitCode = 0;
  await fixture(page, [item]);
  await openTasks(page);
  const panel = tasksWindow(page);
  await selectTask(page, /pnpm test/);
  await expect(panel.getByLabel('Task output')).toHaveText(output);
  await expect(panel.getByLabel('Attempt status', { exact: true })).toHaveText(
    'succeeded · Attempt 1 · Exit 0',
  );
  for (const name of ['First', 'Previous', 'Next', 'Latest'])
    await expect(panel.getByRole('button', { name, exact: true })).toHaveCount(
      0,
    );
  await expect(
    panel.getByRole('button', { name: 'Retry Task', exact: true }),
  ).toBeVisible();
  await panel.getByText('Execution details', { exact: true }).click();
  await expect(panel.getByText(/^Bytes /)).toContainText(
    `Bytes 0–${Buffer.byteLength(output)} of ${Buffer.byteLength(output)}`,
  );
});

test('project tasks preserve file drafts, submit an exact VM command and recover results after reload', async ({
  page,
  request,
}, testInfo) => {
  const state = await fixture(page, [], { logChunkBytes: 5 });
  const editor = await openTasks(page);
  const panel = tasksWindow(page);
  await panel
    .getByRole('textbox', { name: 'Shell command', exact: true })
    .fill('npm test && npm run build');
  await panel.getByText('Execution options', { exact: true }).click();
  await panel.getByLabel('Memory limit (MiB)', { exact: true }).fill('32');
  await panel.getByRole('button', { name: 'Run in VM', exact: true }).click();
  await expect(panel.getByRole('alert')).toContainText('Check task limits');
  expect(state.submissions).toHaveLength(0);
  await panel.getByLabel('Memory limit (MiB)', { exact: true }).fill('256');
  await panel.getByText('Execution options', { exact: true }).click();
  await panel.getByRole('button', { name: 'Run in VM', exact: true }).click();
  await expect(panel.getByLabel('Task output')).toHaveText(output);
  await expect(
    panel.getByRole('region', { name: 'Task results', exact: true }),
  ).toBeFocused();
  expect(state.submissions).toEqual([
    expect.objectContaining({
      binding,
      spec: expect.objectContaining({
        argv: ['/bin/sh', '-lc', 'npm test && npm run build'],
        limits: {
          cpus: 1,
          memoryMiB: 256,
          maxProcesses: 64,
          timeoutSeconds: 900,
          maxLogBytes: 1 << 20,
        },
      }),
    }),
  ]);
  await expectEditorText(editor, 'Keep unsaved file typing');
  await expect(editor).not.toBeEditable();
  expect(
    await page.evaluate(
      () =>
        (window as typeof window & { taskLogExecuted?: boolean })
          .taskLogExecuted,
    ),
  ).toBeUndefined();
  const screenshot = testInfo.outputPath('vm-project-tasks.png');
  await panel.screenshot({ path: screenshot });
  await testInfo.attach('VM project tasks', {
    path: screenshot,
    contentType: 'image/png',
  });
  const [accepted] = state.values.values();
  await expect
    .poll(async () =>
      (await persistedLayout(page, request))?.windows.some(
        (item: {
          id: string;
          viewState: { taskId?: string; attemptId?: string };
        }) =>
          item.id === taskWindowId &&
          item.viewState.taskId === accepted.task.id &&
          item.viewState.attemptId === accepted.attempt.id,
      ),
    )
    .toBe(true);
  page.on('dialog', (dialog) => void dialog.accept());
  await page.reload();
  await expect(panel.getByLabel('Task output')).toHaveText(output);
  expect(state.submissions).toHaveLength(1);
});

test('cancellation stops only the selected task while another task continues', async ({
  page,
}) => {
  const first = result('first', 'npm test');
  const second = result('second', 'npm run build');
  const state = await fixture(page, [first, second]);
  await openTasks(page);
  const panel = tasksWindow(page);
  await selectTask(page, /npm run build/);
  await panel
    .getByRole('button', { name: 'Cancel Attempt', exact: true })
    .click();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Cancel Attempt', exact: true })
    .click();
  await expect.poll(() => state.cancellations.length).toBe(1);
  expect(state.cancellations).toEqual([
    { taskId: 'second', attemptId: 'second-attempt-1', binding },
  ]);
  expect(first.attempt.status).toBe('running');
  expect(second.attempt.status).toBe('cancelled');
  await expectSelectedAttempt(panel, 'second-attempt-1');
});

test('a late cancel confirmation cannot target a replacement attempt', async ({
  page,
}) => {
  const item = result('first', 'npm test');
  const state = await fixture(page, [item]);
  await openTasks(page);
  const panel = tasksWindow(page);
  await selectTask(page, /npm test/);
  await panel
    .getByRole('button', { name: 'Cancel Attempt', exact: true })
    .click();
  const original = item.attempt;
  original.status = 'succeeded';
  item.attempt = {
    ...original,
    id: 'replacement-attempt',
    number: 2,
    status: 'running',
    binding: { ...binding, bootId: 'replacement-boot' },
  };
  item.attempts.push(item.attempt);
  item.task.currentAttemptId = item.attempt.id;
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Cancel Attempt', exact: true })
    .click();
  await expect.poll(() => state.cancellations.length).toBe(1);
  expect(state.cancellations).toEqual([
    { taskId: 'first', attemptId: original.id, binding },
  ]);
  expect(item.attempt.status).toBe('running');
  expect(original.status).toBe('succeeded');
  await expectSelectedAttempt(panel, original.id);
});

test('task polling pauses when hidden and keeps cached logs after connection and host failures', async ({
  page,
}) => {
  const state = await fixture(page, [result('first', 'npm test')]);
  await openTasks(page);
  const panel = tasksWindow(page);
  await selectTask(page, /npm test/);
  await expect(panel.getByLabel('Task output')).toHaveText(output);
  await panel.getByRole('button', { name: 'New Task', exact: true }).click();
  const command = panel.getByRole('textbox', {
    name: 'Shell command',
    exact: true,
  });
  await command.fill('Preserve the next command draft');
  await command.evaluate((element) =>
    (element as HTMLTextAreaElement).setSelectionRange(5, 13),
  );
  const composer = panel.getByRole('form', { name: 'New task', exact: true });
  await composer.getByText('Execution options', { exact: true }).click();
  await composer.getByText('Execution environment', { exact: true }).click();
  await composer.evaluate((element) => {
    element.scrollTop = 80;
  });
  const scrollTop = await composer.evaluate((element) => element.scrollTop);
  expect(scrollTop).toBeGreaterThan(0);
  await switchWorkspace(page, 2);
  const hiddenReads = state.reads;
  await page.waitForTimeout(2300);
  expect(state.reads).toBe(hiddenReads);
  await switchWorkspace(page, 1);
  await expect(command).toHaveValue('Preserve the next command draft');
  expect(
    await command.evaluate((element) => [
      (element as HTMLTextAreaElement).selectionStart,
      (element as HTMLTextAreaElement).selectionEnd,
    ]),
  ).toEqual([5, 13]);
  await expect
    .poll(() => composer.evaluate((element) => element.scrollTop))
    .toBeCloseTo(scrollTop, 0);
  await panel.getByRole('button', { name: 'New Task', exact: true }).click();
  state.offline();
  await panel.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(panel.getByRole('alert')).toContainText(
    'Task controller unavailable',
  );
  await expect(panel.getByLabel('Task output')).toHaveText(output);
  await expect(
    panel.getByText('Last known task data.', { exact: false }),
  ).toBeVisible();
  state.online();
  state.changeHost();
  await panel.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(panel.getByRole('alert')).toContainText('The VM host changed');
  await expect(panel.getByLabel('Task output')).toHaveText(output);
  expect(state.queriedHosts.every((id) => id === binding.hostId)).toBe(true);
});

test('a lost submission response recovers the original receipt after reload without launching twice', async ({
  page,
  request,
}) => {
  const state = await fixture(page);
  await openTasks(page);
  const panel = tasksWindow(page);
  state.loseSubmission();
  await panel
    .getByRole('textbox', { name: 'Shell command', exact: true })
    .fill('npm ci');
  await panel.getByRole('button', { name: 'Run in VM', exact: true }).click();
  await expect(
    panel.getByRole('button', { name: 'Recover Submission', exact: true }),
  ).toBeEnabled();
  await expect
    .poll(async () =>
      (await persistedLayout(page, request))?.windows.some(
        (item: { id: string }) => item.id === taskWindowId,
      ),
    )
    .toBe(true);
  page.on('dialog', (dialog) => void dialog.accept());
  await page.reload();
  await panel
    .getByRole('button', { name: 'Recover Submission', exact: true })
    .click();
  await expect(panel.getByLabel('Task output')).toHaveText(output);
  expect(state.submissions).toHaveLength(1);
  expect(state.values.size).toBe(1);
  expect(state.submissions[0].binding).toEqual(binding);
});

test('unknown attempts are not retried and finished attempts require explicit confirmation', async ({
  page,
}) => {
  const item = result('first', 'npm test', 'unknown');
  const state = await fixture(page, [item]);
  await openTasks(page);
  const panel = tasksWindow(page);
  await selectTask(page, /npm test/);
  await expect(
    panel.getByRole('button', { name: 'Retry Task', exact: true }),
  ).toBeDisabled();
  await expect(
    panel.getByText('The original execution could not be verified.', {
      exact: false,
    }),
  ).toBeVisible();
  item.attempt.status = 'interrupted';
  item.attempt.reason = 'The original VM stopped.';
  await panel.getByRole('button', { name: 'Refresh', exact: true }).click();
  await panel.getByRole('button', { name: 'Retry Task', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'Retry task?' })).toContainText(
    `VM “${binding.machineId}”`,
  );
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  expect(state.retries).toHaveLength(0);
  await panel.getByRole('button', { name: 'Retry Task', exact: true }).click();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Retry', exact: true })
    .click();
  await expect.poll(() => state.retries.length).toBe(1);
  expect(state.retries[0]).toMatchObject({
    binding,
    requestId: expect.any(String),
  });
  await expectSelectedAttempt(panel, 'first-attempt-2');
});

test('missing selected attempt history never substitutes the current execution', async ({
  page,
}) => {
  const item = result('first', 'npm test');
  const state = await fixture(page, [item]);
  await openTasks(page);
  const panel = tasksWindow(page);
  await selectTask(page, /npm test/);
  await expectSelectedAttempt(panel, item.attempt.id);
  const replacement = { ...item.attempt, id: 'replacement-attempt', number: 2 };
  item.attempt = replacement;
  item.attempts = [replacement];
  item.task.currentAttemptId = replacement.id;
  await panel.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(panel.getByRole('alert')).toContainText(
    'The selected attempt is unavailable',
  );
  await expect(
    panel.getByRole('button', { name: 'Cancel Attempt', exact: true }),
  ).toHaveCount(0);
  await expect(
    panel.getByRole('combobox', { name: 'Attempt', exact: true }),
  ).toHaveValue('');
  expect(state.cancellations).toEqual([]);
  await panel
    .getByRole('combobox', { name: 'Attempt', exact: true })
    .selectOption(replacement.id);
  await expect(
    panel.getByRole('button', { name: 'Cancel Attempt', exact: true }),
  ).toBeEnabled();
  await expectSelectedAttempt(panel, replacement.id);
});

test('a lost retry response recovers its exact attempt after reload without launching again', async ({
  page,
  request,
}) => {
  const item = result('first', 'npm test', 'interrupted');
  const state = await fixture(page, [item]);
  await openTasks(page);
  const panel = tasksWindow(page);
  await selectTask(page, /npm test/);
  state.loseRetry();
  await panel.getByRole('button', { name: 'Retry Task', exact: true }).click();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Retry', exact: true })
    .click();
  await expect(
    panel.getByRole('button', { name: 'Recover Submission', exact: true }),
  ).toBeEnabled();
  expect(state.retries).toHaveLength(1);
  const accepted = item.attempt;
  await expect
    .poll(async () =>
      (await persistedLayout(page, request))?.windows.some(
        (value: { id: string }) => value.id === taskWindowId,
      ),
    )
    .toBe(true);
  page.on('dialog', (dialog) => void dialog.accept());
  await page.reload();
  await panel
    .getByRole('button', { name: 'Recover Submission', exact: true })
    .click();
  await expectSelectedAttempt(panel, accepted.id);
  await expect(
    panel.getByRole('button', { name: 'Recover Submission', exact: true }),
  ).toHaveCount(0);
  expect(state.retries).toHaveLength(1);
  expect(item.attempts).toHaveLength(2);
  expect(state.submissions).toHaveLength(0);
  expect(accepted.binding).toEqual(binding);
});

test('delayed restored history does not move focus away from command typing', async ({
  page,
  request,
}) => {
  const state = await fixture(page, [result('first', 'npm test')]);
  await openTasks(page);
  const panel = tasksWindow(page);
  await selectTask(page, /npm test/);
  await expect(panel.getByLabel('Task output')).toHaveText(output);
  await expect
    .poll(async () =>
      (await persistedLayout(page, request))?.windows.some(
        (value: { id: string; viewState: { taskId?: string } }) =>
          value.id === taskWindowId && value.viewState.taskId === 'first',
      ),
    )
    .toBe(true);
  state.pauseList();
  page.on('dialog', (dialog) => void dialog.accept());
  await page.reload();
  await panel.getByRole('button', { name: 'New Task', exact: true }).click();
  const command = panel.getByRole('textbox', {
    name: 'Shell command',
    exact: true,
  });
  await command.fill('Continue editing the next command');
  state.resumeList();
  await expect(panel.getByLabel('Task output')).toHaveText(output);
  await expect(command).toHaveValue('Continue editing the next command');
  await expect(command).toBeFocused();
});

test('deferred task history focus respects a newly activated window', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await fixture(page, [result('first', 'npm test')]);
  const editor = await openTasks(page);
  const panel = tasksWindow(page);
  const bounds = (await panel.boundingBox())!;
  const edge = (await panel.locator('[data-resize="e"]').boundingBox())!;
  await page.mouse.move(edge.x + edge.width / 2, edge.y + edge.height / 2);
  await page.mouse.down();
  await page.mouse.move(bounds.x + 550, edge.y + edge.height / 2);
  await page.mouse.up();
  await selectTask(page, /npm test/);
  const back = panel.getByRole('button', {
    name: 'Back to Tasks',
    exact: true,
  });
  await expect(back).toBeVisible();
  const frames = await back.evaluate((element) => {
    const callbacks: FrameRequestCallback[] = [];
    const original = window.requestAnimationFrame;
    window.requestAnimationFrame = (callback) => {
      callbacks.push(callback);
      return 0;
    };
    try {
      (element as HTMLButtonElement).click();
    } finally {
      window.requestAnimationFrame = original;
    }
    (
      window as Window & { flushTaskFocusFrames?: () => void }
    ).flushTaskFocusFrames = () => {
      for (const callback of callbacks) callback(performance.now());
    };
    return callbacks.length;
  });
  expect(frames).toBeGreaterThan(0);
  await filesWindow(page).dispatchEvent('pointerdown');
  await expect(editor).toBeEditable();
  await editor.focus();
  await expect(editor).toBeFocused();
  await page.evaluate(() => {
    const scope = window as Window & { flushTaskFocusFrames?: () => void };
    scope.flushTaskFocusFrames?.();
    delete scope.flushTaskFocusFrames;
  });
  await expect(editor).toBeFocused();
  await expect(
    panel.getByRole('navigation', { name: 'Project tasks' }),
  ).not.toBeFocused();
});

test('accepted work opens its results and New Task restores the command draft', async ({
  page,
}, testInfo) => {
  const state = await fixture(page);
  await openTasks(page);
  const panel = tasksWindow(page);
  const command = panel.getByRole('textbox', {
    name: 'Shell command',
    exact: true,
  });
  await command.fill('npm test && npm run build');
  for (const label of [
    'Directory in project',
    'Timeout (seconds)',
    'CPU limit',
    'Memory limit (MiB)',
    'Process limit',
    'Log limit (KiB)',
  ])
    await expect(panel.getByLabel(label, { exact: true })).toBeHidden();
  await panel.getByText('Execution options', { exact: true }).click();
  await panel.getByLabel('Directory in project', { exact: true }).fill('web');
  await panel.getByLabel('Timeout (seconds)', { exact: true }).fill('1200');
  await panel.getByLabel('CPU limit', { exact: true }).fill('2');
  await panel.getByLabel('Memory limit (MiB)', { exact: true }).fill('768');
  await panel.getByLabel('Process limit', { exact: true }).fill('96');
  await panel.getByLabel('Log limit (KiB)', { exact: true }).fill('2048');
  const environment = panel.getByText('Execution environment', { exact: true });
  await environment.click();
  const identities = environment.locator('..');
  for (const identity of [
    binding.hostId,
    binding.projectId,
    binding.volumeId,
    binding.machineId,
    binding.bootId,
    binding.instanceId,
  ])
    await expect(identities).toContainText(identity);
  await panel.getByText('Execution options', { exact: true }).click();
  const submit = panel.getByRole('button', { name: 'Run in VM', exact: true });
  await expect(submit).toBeEnabled();
  const composerScreenshot = testInfo.outputPath('vm-task-composer.png');
  await panel.screenshot({ path: composerScreenshot });
  await testInfo.attach('VM task composer', {
    path: composerScreenshot,
    contentType: 'image/png',
  });
  await submit.click();
  await expect(panel.getByLabel('Task output')).toHaveText(output);
  await expect(command).toBeHidden();
  await expect(
    panel.getByRole('button', { name: 'Cancel Attempt', exact: true }),
  ).toBeInViewport();
  expect(state.submissions).toHaveLength(1);
  expect(state.submissions[0].spec).toMatchObject({
    argv: ['/bin/sh', '-lc', 'npm test && npm run build'],
    directory: 'web',
    limits: {
      timeoutSeconds: 1200,
      cpus: 2,
      memoryMiB: 768,
      maxProcesses: 96,
      maxLogBytes: 2048 * 1024,
    },
  });
  await panel.getByRole('button', { name: 'New Task', exact: true }).click();
  await expect(command).toBeVisible();
  await expect(command).toHaveValue('npm test && npm run build');
  await panel.getByText('Execution options', { exact: true }).click();
  await expect(
    panel.getByLabel('Directory in project', { exact: true }),
  ).toHaveValue('web');
  await expect(
    panel.getByLabel('Timeout (seconds)', { exact: true }),
  ).toHaveValue('1200');
  await expect(
    panel.getByLabel('Memory limit (MiB)', { exact: true }),
  ).toHaveValue('768');
  await command.fill('npm run lint');
  await panel.getByRole('button', { name: 'New Task', exact: true }).click();
  await expect(command).toBeHidden();
  await panel.getByRole('button', { name: 'New Task', exact: true }).click();
  await expect(command).toHaveValue('npm run lint');
  expect(state.submissions).toHaveLength(1);
});

test('narrow task results keep output and cancellation reachable with a way back to history', async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await fixture(page, [
    result('first', 'npm test'),
    result('second', 'npm run build'),
  ]);
  await openTasks(page);
  const panel = tasksWindow(page);
  await selectTask(page, /npm test/);
  await expect(panel.getByLabel('Task output')).toHaveText(output);
  await expect(
    panel.getByRole('navigation', { name: 'Project tasks' }),
  ).toBeHidden();
  const cancel = panel.getByRole('button', {
    name: 'Cancel Attempt',
    exact: true,
  });
  const back = panel.getByRole('button', {
    name: 'Back to Tasks',
    exact: true,
  });
  await expect(cancel).toBeInViewport();
  await expect(back).toBeInViewport();
  const screenshot = testInfo.outputPath('vm-task-narrow-results.png');
  await panel.screenshot({ path: screenshot });
  await testInfo.attach('VM task narrow results', {
    path: screenshot,
    contentType: 'image/png',
  });
  await back.click();
  const navigation = panel.getByRole('navigation', {
    name: 'Project tasks',
  });
  await expect(navigation).toBeVisible();
  await expect(navigation).toBeFocused();
  await navigation.getByRole('button', { name: /npm run build/ }).click();
  await expectSelectedAttempt(panel, 'second-attempt-1');
  await expect(cancel).toBeInViewport();
  const bounds = await cancel.boundingBox();
  expect(bounds).not.toBeNull();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390);

  await page.setViewportSize({ width: 318, height: 420 });
  const surface = panel.locator('.vm-project-tasks');
  await expect
    .poll(() => surface.evaluate((element) => element.clientWidth))
    .toBeLessThanOrEqual(310);
  expect(
    await surface.evaluate(
      (element) => element.scrollWidth - element.clientWidth,
    ),
  ).toBeLessThanOrEqual(1);

  async function expectContainedContent() {
    const bounds = (await surface.boundingBox())!;
    const overflowing = await surface.locator('*:visible').evaluateAll(
      (elements, bounds) =>
        elements
          .map((item) => ({
            element: `${item.tagName}.${item.className}`,
            left: item.getBoundingClientRect().left,
            right: item.getBoundingClientRect().right,
          }))
          .filter(
            (item) =>
              item.left < bounds.x - 1 ||
              item.right > bounds.x + bounds.width + 1,
          ),
      bounds,
    );
    expect(overflowing).toEqual([]);
  }

  await expectContainedContent();
  const details = panel.getByText('Execution details', { exact: true });
  await details.click();
  await expectContainedContent();
  await details.click();
  await expectSelectedAttempt(panel, 'second-attempt-1');
  await cancel.scrollIntoViewIfNeeded();
  await cancel.focus();
  await expect(cancel).toBeFocused();
  await expect(cancel).toBeInViewport();
  await page.keyboard.press('Tab');
  const logs = panel.getByLabel('Task output');
  await expect(logs).toBeFocused();
  await page.keyboard.press('Tab');
  const latest = panel.getByRole('button', { name: 'Latest', exact: true });
  await expect(latest).toBeFocused();
  await latest.scrollIntoViewIfNeeded();
  await expect(latest).toBeInViewport();
  const compactScreenshot = testInfo.outputPath('vm-task-compact-results.png');
  await panel.screenshot({ path: compactScreenshot });
  await testInfo.attach('VM task compact results', {
    path: compactScreenshot,
    contentType: 'image/png',
  });
  await expect(back).toBeInViewport();
  await back.focus();
  await expect(back).toBeInViewport();
  await page.keyboard.press('Enter');
  await expect(navigation).toBeVisible();
  await expect(navigation).toBeFocused();
});

test('identical commands have distinct history labels and refreshing keeps their order', async ({
  page,
}) => {
  const older = result('older001', 'npm test');
  const newer = result('newer002', 'npm test');
  older.task.createdAt = older.attempt.createdAt = '2026-10-20T08:00:00Z';
  newer.task.createdAt = newer.attempt.createdAt = '2026-10-20T09:00:00Z';
  await fixture(page, [older, newer]);
  await openTasks(page);
  const panel = tasksWindow(page);
  const back = panel.getByRole('button', {
    name: 'Back to Tasks',
    exact: true,
  });
  const navigation = panel.getByRole('navigation', {
    name: 'Project tasks',
    includeHidden: true,
  });
  const rows = navigation.locator('button');
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText('older001');
  await expect(rows.nth(1)).toContainText('newer002');
  // Loading the first task list makes the compact Back action available.
  // Decide which pane to open only after that response has populated the rows.
  if (await back.isVisible()) await back.click();
  await expect(navigation).toBeVisible();
  await rows.nth(0).click();
  await expect(panel.getByLabel('Task output')).toHaveText(output);
  older.attempt.status = 'failed';
  older.attempt.reason = 'The test command failed.';
  await panel.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(rows.filter({ hasText: 'older001' })).toContainText('failed');
  await expect(rows.nth(0)).toContainText('older001');
  await expect(rows.nth(0)).toContainText('failed');
  await expect(rows.nth(1)).toContainText('newer002');
  await expectSelectedAttempt(panel, older.attempt.id);
});

test('large task logs page to the final result and Latest follows running output as plain text', async ({
  page,
}) => {
  const item = result('build', 'npm run build');
  const initial =
    'FIRST BUILD LINE\n' +
    'Compiling project modules and dependencies.\n'.repeat(9000) +
    'CURRENT BUILD TAIL\n';
  const finalLine =
    'FINAL ERROR: the production build failed\n<script>window.longTaskLogExecuted=true</script>\n';
  const state = await fixture(page, [item], { logText: initial });
  await openTasks(page);
  const panel = tasksWindow(page);
  await selectTask(page, /npm run build/);
  const logs = panel.getByLabel('Task output');
  await expect(logs).toContainText('FIRST BUILD LINE');
  await expect(logs).not.toContainText('CURRENT BUILD TAIL');
  await panel.getByRole('button', { name: 'Latest', exact: true }).click();
  await expect(logs).toContainText('CURRENT BUILD TAIL');
  await expect
    .poll(() => logs.evaluate((element) => element.scrollTop))
    .toBeGreaterThan(0);
  const nextOutput = initial + 'NEW BUILD PROGRESS\n';
  state.setOutput(item.task.id, nextOutput);
  await panel.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(logs).toContainText('NEW BUILD PROGRESS');
  await panel.getByRole('button', { name: 'First', exact: true }).click();
  await expect(logs).toContainText('FIRST BUILD LINE');
  await expect
    .poll(() => logs.evaluate((element) => element.scrollTop))
    .toBe(0);
  await logs.evaluate((element) => {
    element.scrollTop = 120;
  });
  const earlierPageScroll = await logs.evaluate((element) => element.scrollTop);
  expect(earlierPageScroll).toBeGreaterThan(0);
  state.setOutput(
    item.task.id,
    nextOutput.replace('FIRST BUILD LINE', 'FIRST BUILD LINE REFRESHED') +
      finalLine,
  );
  item.attempt.status = 'failed';
  item.attempt.exitCode = 1;
  await panel.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(
    panel.getByRole('region', { name: 'Task results', exact: true }),
  ).toContainText('failed');
  await expect(logs).toContainText('FIRST BUILD LINE REFRESHED');
  await expect
    .poll(() => logs.evaluate((element) => element.scrollTop))
    .toBeCloseTo(earlierPageScroll, 0);
  await expect(logs).not.toContainText('FINAL ERROR');
  await panel.getByRole('button', { name: 'Next', exact: true }).click();
  await expect(logs).not.toContainText('FIRST BUILD LINE');
  await expect(logs).not.toContainText('FINAL ERROR');
  await panel.getByRole('button', { name: 'Previous', exact: true }).click();
  await expect(logs).toContainText('FIRST BUILD LINE');
  await panel.getByRole('button', { name: 'Latest', exact: true }).click();
  await expect(logs).toContainText(finalLine.trim());
  expect(
    Buffer.byteLength((await logs.textContent()) || ''),
  ).toBeLessThanOrEqual(128 * 1024);
  expect(state.logRequests.every((request) => request.limit <= 65536)).toBe(
    true,
  );
  expect(
    state.logRequests.some((request) => request.offset >= 128 * 1024),
  ).toBe(true);
  expect(
    await page.evaluate(
      () =>
        (window as typeof window & { longTaskLogExecuted?: boolean })
          .longTaskLogExecuted,
    ),
  ).toBeUndefined();
});

test('Latest toggles following a short running log without showing paging controls', async ({
  page,
}) => {
  await fixture(page, [result('short-log', 'npm test')]);
  await openTasks(page);
  const panel = tasksWindow(page);
  await selectTask(page, /npm test/);
  await expect(panel.getByLabel('Task output')).toHaveText(output);
  const latest = panel.getByRole('button', { name: 'Latest', exact: true });
  for (const name of ['First', 'Previous', 'Next'])
    await expect(panel.getByRole('button', { name, exact: true })).toHaveCount(
      0,
    );
  await latest.click();
  await expect(latest).toHaveAttribute('aria-pressed', 'true');
  await latest.click();
  await expect(latest).toHaveAttribute('aria-pressed', 'false');
  await expect(panel.getByLabel('Task output')).toHaveText(output);
});

test('an unavailable output page keeps the displayed page and can return to its cached start', async ({
  page,
}) => {
  const logText =
    'FIRST RETAINED PAGE\n' +
    'Compiler output.\n'.repeat(12000) +
    'SECOND PAGE END\n';
  const state = await fixture(page, [result('large-log', 'npm run build')], {
    logText,
  });
  await openTasks(page);
  const panel = tasksWindow(page);
  await selectTask(page, /npm run build/);
  const logs = panel.getByLabel('Task output');
  await expect(logs).toContainText('FIRST RETAINED PAGE');
  const retained = await logs.textContent();
  state.offline();
  await panel.getByRole('button', { name: 'Next', exact: true }).click();
  await expect(panel.getByRole('alert')).toContainText(
    'Task controller unavailable.',
  );
  await expect(logs).toHaveText(retained!);
  await expect(
    panel.getByText('Requested page unavailable', { exact: false }),
  ).toBeVisible();
  await panel.getByText('Execution details', { exact: true }).click();
  await expect(panel.getByText(/Bytes 0–131072 of /)).toBeVisible();
  await panel.getByText('Execution details', { exact: true }).click();
  await panel.getByRole('button', { name: 'First', exact: true }).click();
  await expect(logs).toHaveText(retained!);
  await expect(
    panel.getByText('Requested page unavailable', { exact: false }),
  ).toHaveCount(0);
  state.online();
  await panel.getByRole('button', { name: 'Next', exact: true }).click();
  await expect(logs).toContainText('SECOND PAGE END');
  await expect(logs).not.toContainText('FIRST RETAINED PAGE');
});

test('cancellation identifies its command and keeps a failed request visible beside retained output', async ({
  page,
}) => {
  const command = 'npm run build -- --target=production';
  const item = result('build', command);
  const state = await fixture(page, [item]);
  state.failCancellation();
  await openTasks(page);
  const panel = tasksWindow(page);
  await selectTask(page, command);
  await expect(panel.getByLabel('Task output')).toHaveText(output);
  await panel
    .getByRole('button', { name: 'Cancel Attempt', exact: true })
    .click();
  const dialog = page.getByRole('dialog', { name: 'Cancel attempt 1?' });
  await expect(dialog).toContainText(command);
  await dialog
    .getByRole('button', { name: 'Cancel Attempt', exact: true })
    .click();
  const error = panel.getByRole('alert').filter({
    hasText: 'Cancellation could not reach the original VM.',
  });
  await expect(error).toBeInViewport();
  await expect(panel.getByLabel('Task output')).toHaveText(output);
  expect(item.attempt.status).toBe('running');
  expect(state.cancellations).toEqual([
    { taskId: 'build', attemptId: 'build-attempt-1', binding },
  ]);
});
