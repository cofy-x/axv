import type { Page } from '@playwright/test';
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

async function fixture(page: Page, initial: VMTaskResult[] = []) {
  const values = new Map(initial.map((item) => [item.task.id, item]));
  const submissions: VMTaskSubmission[] = [];
  const cancellations: {
    taskId: string;
    attemptId: string;
    binding: typeof binding;
  }[] = [];
  const retries: VMTaskSubmission[] = [];
  const queriedHosts: string[] = [];
  let reads = 0;
  let offline = false;
  let changedHost = false;
  let loseSubmission = false;
  let loseRetry = false;
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
    if (path.endsWith('/binding'))
      return changedHost
        ? json({ code: 'host_changed', error: 'The VM host changed.' }, 409)
        : json({ project, binding });
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
      const bytes = Buffer.from(output);
      const offset = Number(url.searchParams.get('offset'));
      const end = Math.min(offset + 5, bytes.length);
      return json({
        taskId,
        attemptId: parts[parts.indexOf('attempts') + 1],
        status: item.attempt.status,
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
    get reads() {
      return reads;
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
    .getByRole('button', { name: /^Task repository ·/ })
    .click();
  await filesWindow(page)
    .getByRole('button', { name: 'main.ts', exact: true })
    .click();
  const editor = filesWindow(page).getByRole('textbox', {
    name: 'VM project file text',
  });
  await expect(editor).toHaveValue('Saved file');
  await editor.fill('Keep unsaved file typing');
  await filesWindow(page)
    .getByRole('button', { name: 'Tasks', exact: true })
    .click();
  await expect(tasksWindow(page)).toBeVisible();
  return filesWindow(page).locator(
    'textarea[aria-label="VM project file text"]',
  );
}

test('project tasks preserve file drafts, submit an exact VM command and recover results after reload', async ({
  page,
  request,
}, testInfo) => {
  const state = await fixture(page);
  const editor = await openTasks(page);
  const panel = tasksWindow(page);
  await panel
    .getByRole('textbox', { name: 'Shell command', exact: true })
    .fill('npm test && npm run build');
  await panel.getByText('Execution environment', { exact: true }).click();
  await panel.getByLabel('Memory limit (MiB)', { exact: true }).fill('32');
  await panel.getByRole('button', { name: 'Run in VM', exact: true }).click();
  await expect(panel.getByRole('alert')).toContainText('Check task limits');
  expect(state.submissions).toHaveLength(0);
  await panel.getByLabel('Memory limit (MiB)', { exact: true }).fill('256');
  await panel.getByText('Execution environment', { exact: true }).click();
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
  await expect(editor).toHaveValue('Keep unsaved file typing');
  await expect(editor).toBeDisabled();
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
  await expect
    .poll(async () =>
      (await persistedLayout(page, request))?.windows.some(
        (item: { id: string }) => item.id === taskWindowId,
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
  await panel
    .getByRole('navigation', { name: 'Project tasks' })
    .getByRole('button', { name: /npm run build/ })
    .click();
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
  await expect(panel.getByLabel('Attempt', { exact: true })).toHaveValue(
    'second-attempt-1',
  );
});

test('a late cancel confirmation cannot target a replacement attempt', async ({
  page,
}) => {
  const item = result('first', 'npm test');
  const state = await fixture(page, [item]);
  await openTasks(page);
  const panel = tasksWindow(page);
  await panel
    .getByRole('navigation', { name: 'Project tasks' })
    .getByRole('button', { name: /npm test/ })
    .click();
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
  await expect(panel.getByLabel('Attempt', { exact: true })).toHaveValue(
    original.id,
  );
});

test('task polling pauses when hidden and keeps cached logs after connection and host failures', async ({
  page,
}) => {
  const state = await fixture(page, [result('first', 'npm test')]);
  await openTasks(page);
  const panel = tasksWindow(page);
  await panel
    .getByRole('navigation', { name: 'Project tasks' })
    .getByRole('button', { name: /npm test/ })
    .click();
  await expect(panel.getByLabel('Task output')).toHaveText(output);
  const command = panel.getByRole('textbox', {
    name: 'Shell command',
    exact: true,
  });
  await command.fill('Preserve the next command draft');
  await command.evaluate((element) =>
    (element as HTMLTextAreaElement).setSelectionRange(5, 13),
  );
  const scrollTop = await panel
    .locator('.vm-task-scroll')
    .evaluate((element) => element.scrollTop);
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
    .poll(() =>
      panel.locator('.vm-task-scroll').evaluate((element) => element.scrollTop),
    )
    .toBeCloseTo(scrollTop, 0);
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
  await panel
    .getByRole('navigation', { name: 'Project tasks' })
    .getByRole('button', { name: /npm test/ })
    .click();
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
  await expect(panel.getByLabel('Attempt', { exact: true })).toHaveValue(
    'first-attempt-2',
  );
});

test('missing selected attempt history never substitutes the current execution', async ({
  page,
}) => {
  const item = result('first', 'npm test');
  const state = await fixture(page, [item]);
  await openTasks(page);
  const panel = tasksWindow(page);
  await panel
    .getByRole('navigation', { name: 'Project tasks' })
    .getByRole('button', { name: /npm test/ })
    .click();
  await expect(
    panel.getByRole('combobox', { name: 'Attempt', exact: true }),
  ).toHaveValue(item.attempt.id);
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
  await expect(
    panel.getByRole('combobox', { name: 'Attempt', exact: true }),
  ).toHaveValue(replacement.id);
});

test('a lost retry response recovers its exact attempt after reload without launching again', async ({
  page,
  request,
}) => {
  const item = result('first', 'npm test', 'interrupted');
  const state = await fixture(page, [item]);
  await openTasks(page);
  const panel = tasksWindow(page);
  await panel
    .getByRole('navigation', { name: 'Project tasks' })
    .getByRole('button', { name: /npm test/ })
    .click();
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
  await expect(panel.getByLabel('Attempt', { exact: true })).toHaveValue(
    accepted.id,
  );
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
  await panel
    .getByRole('navigation', { name: 'Project tasks' })
    .getByRole('button', { name: /npm test/ })
    .click();
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
