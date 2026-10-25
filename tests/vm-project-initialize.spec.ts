import type { Page } from '@playwright/test';
import { expect, menuItem, persistedLayout, test } from './helpers';
import type {
  VMProject,
  VMProjectBinding,
} from '../apps/dashboard/src/features/project-files/vm-api';
import type { VMProjectInitialization } from '../apps/dashboard/src/features/project-files/vm-init-api';
import type { VMTaskResult } from '../apps/dashboard/src/features/project-files/vm-task-api';

const parent: VMProject = {
  id: 'volume-workspace',
  hostId: 'initialize-host',
  kind: 'vm',
  name: 'Work',
  volumeId: 'initialize-volume',
  root: '',
  rootIdentity: 'volume-root',
  revision: 1,
  createdAt: '2026-10-22T00:00:00Z',
};
const binding: VMProjectBinding = {
  hostId: parent.hostId,
  projectId: parent.id,
  volumeId: parent.volumeId,
  volumeRevision: 4,
  projectRevision: 1,
  machineId: 'initialize-vm',
  bootId: 'initialize-boot',
  instanceId: 'initialize-guest',
};
const projects = (page: Page) =>
  page.locator('[data-window-id="project-files:projects"]');

async function fixture(page: Page, loseReply = false, changeBinding = false) {
  const submissions: VMProjectInitialization[] = [];
  let receipt: VMTaskResult | null = null;
  let finished: VMProject | null = null;
  let registered = false;
  await page.route('**/api/compute/**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const json = (value: unknown, status = 200) =>
      route.fulfill({ status, json: value });
    if (path === '/api/compute/info')
      return json({
        configured: true,
        hostId: parent.hostId,
        providers: [],
        images: [],
      });
    if (path === '/api/compute/machines')
      return json({
        machines: [
          {
            id: binding.machineId,
            hostId: parent.hostId,
            spec: {
              name: 'Project VM',
              provider: 'fixture',
              kind: 'development',
              resources: { cpus: 2, memoryMiB: 1024, diskGiB: 8 },
              imageId: 'fixture-image',
              projectGiB: 4,
              retainVolume: true,
            },
            status: 'running',
            desired: 'running',
            ready: true,
            guestCapabilities: [
              'project-files',
              'project-tasks',
              'project-initialization',
            ],
            revision: 1,
            createdAt: parent.createdAt,
            updatedAt: parent.createdAt,
          },
        ],
      });
    if (path === '/api/compute/volumes')
      return json({
        volumes: [
          {
            id: parent.volumeId,
            name: 'Work',
            revision: 4,
            provider: 'fixture',
            sizeGiB: 4,
            retain: true,
            machineId: binding.machineId,
            status: 'ready',
          },
        ],
      });
    if (path.endsWith('/guest/health'))
      return json({
        machineId: binding.machineId,
        bootId: binding.bootId,
        instanceId: binding.instanceId,
        ready: true,
      });
    if (!path.startsWith('/api/compute/projects')) return route.continue();
    expect(request.headers()['x-axv-host-id']).toBe(parent.hostId);
    if (path === '/api/compute/projects') {
      if (request.method() === 'POST') {
        expect(request.postDataJSON()).toMatchObject({
          volumeId: parent.volumeId,
          volumeRevision: 4,
          machineId: binding.machineId,
          root: '',
        });
        registered = true;
        return json({ project: parent }, 201);
      }
      return json({
        projects: [
          ...(registered ? [parent] : []),
          ...(finished ? [finished] : []),
        ],
      });
    }
    if (path.endsWith('/binding'))
      return json({
        project: finished && path.includes(finished.id) ? finished : parent,
        binding:
          finished && path.includes(finished.id)
            ? { ...binding, projectId: finished.id }
            : {
                ...binding,
                ...(changeBinding ? { bootId: 'replacement-boot' } : {}),
              },
      });
    if (path.endsWith('/initialize')) {
      const submission = request.postDataJSON() as VMProjectInitialization;
      expect(submission.binding).toEqual(binding);
      submissions.push(submission);
      const attempt = {
        id: 'initialize-attempt',
        taskId: 'initialize-task',
        number: 1,
        binding,
        root: '',
        rootIdentity: parent.rootIdentity,
        requestId: submission.requestId,
        status: 'running',
        guestFinal: false,
        createdAt: parent.createdAt,
        logBytes: 0,
        cachedBytes: 0,
        logLimited: false,
        cancelRequested: false,
      };
      receipt = {
        task: {
          id: attempt.taskId,
          hostId: parent.hostId,
          projectId: parent.id,
          requestId: submission.requestId,
          spec: {
            argv: [],
            initialization: {
              directory: submission.directory,
              repository: submission.repository,
              name: submission.name,
            },
            directory: '',
            limits: {
              timeoutSeconds: 900,
              cpus: 1,
              memoryMiB: 512,
              maxProcesses: 64,
              maxLogBytes: 1048576,
            },
          },
          currentAttemptId: attempt.id,
          createdAt: parent.createdAt,
        },
        attempt,
        attempts: [attempt],
      };
      if (loseReply) {
        loseReply = false;
        return json(
          {
            code: 'guest_unavailable',
            error: 'The response was lost. Recover the original submission.',
          },
          503,
        );
      }
      return json(receipt, 202);
    }
    if (path.includes('/task-requests/'))
      return receipt
        ? json(receipt)
        : json({ code: 'not_found', error: 'No receipt yet.' }, 404);
    if (path.endsWith('/finish')) {
      expect(receipt?.attempt.status).toBe('succeeded');
      expect(request.postDataJSON()).toEqual({ binding });
      finished = {
        ...parent,
        id: 'initialized-project',
        name: submissions[0].name || submissions[0].directory,
        root: submissions[0].directory,
        rootIdentity: 'initialized-root',
      };
      return json({ project: finished });
    }
    if (path.endsWith('/files/list'))
      return json({
        project: finished,
        binding: { ...binding, projectId: finished?.id },
        entries: [],
      });
    return route.continue();
  });
  return {
    submissions,
    makeOrdinary: () => {
      if (!receipt) throw new Error('No initialization submitted');
      delete receipt.task.spec.initialization;
      receipt.task.spec.argv = ['/bin/true'];
    },
    succeed: () => {
      if (!receipt) throw new Error('No initialization submitted');
      receipt.attempt.status = 'succeeded';
      receipt.attempt.guestFinal = true;
      receipt.attempt.createdRoot = {
        root: submissions[0].directory,
        rootIdentity: 'initialized-root',
      };
    },
  };
}

async function launcher(page: Page) {
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await menuItem(page, 'Project Files').click();
  await projects(page).getByRole('tab', { name: 'VM', exact: true }).click();
  return projects(page);
}

test('clone recovers its original task after reload and opens the initialized project', async ({
  page,
  request,
}) => {
  const state = await fixture(page, true);
  let panel = await launcher(page);
  await panel.getByLabel('Project action').selectOption('clone');
  await panel.getByLabel('Project volume').selectOption(parent.volumeId);
  await panel.getByLabel('New directory').fill('new-repository');
  await panel
    .getByLabel('Repository URL')
    .fill('https://example.com/repository.git');
  await panel.getByRole('button', { name: 'Clone', exact: true }).click();
  await expect(panel.getByRole('alert')).toContainText('The response was lost');
  expect(state.submissions).toHaveLength(1);
  await expect
    .poll(async () =>
      (await persistedLayout(page, request))?.windows.some(
        (item: { id: string }) => item.id === 'project-files:projects',
      ),
    )
    .toBe(true);
  await page.reload();
  panel = projects(page);
  await panel.getByRole('tab', { name: 'VM', exact: true }).click();
  await expect(panel.getByText('Original VM:', { exact: false })).toContainText(
    binding.machineId,
  );
  await panel.getByRole('button', { name: 'Recover', exact: true }).click();
  await expect(
    panel
      .getByRole('region', { name: 'Initialize VM project', exact: true })
      .getByRole('status'),
  ).toHaveText('Initialization: running · initiali');
  await expect(
    panel.getByRole('button', { name: /^Volume workspace · Work/ }),
  ).toBeVisible();
  expect(state.submissions).toHaveLength(1);
  state.succeed();
  await panel
    .getByRole('button', { name: 'Open Project', exact: true })
    .click();
  const files = page.locator(
    `[data-window-id="project-files:vm:${parent.hostId}:initialized-project"]`,
  );
  await expect(files).toBeVisible();
  await expect(files.locator('.ns-window-title')).toHaveText(
    'VM · new-repository',
  );
});

test('empty project creation exposes the volume workspace and supports narrow screens', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const state = await fixture(page);
  const panel = await launcher(page);
  await panel.getByLabel('Project action').selectOption('create');
  await panel.getByLabel('Project volume').selectOption(parent.volumeId);
  await panel.getByLabel('New directory').fill('empty-project');
  let releaseProjectsReload!: () => void;
  const projectsReload = new Promise<void>((resolve) => {
    releaseProjectsReload = resolve;
  });
  await page.route('**/api/compute/projects', async (route) => {
    if (route.request().method() === 'GET') await projectsReload;
    await route.fallback();
  });
  try {
    await panel.getByRole('button', { name: 'Create', exact: true }).click();
    await expect(
      panel.getByRole('status').filter({ hasText: 'Loading VM projects…' }),
    ).toHaveText('Loading VM projects…');
    await expect(
      panel
        .getByRole('region', { name: 'Initialize VM project', exact: true })
        .getByRole('status'),
    ).toHaveText('Initialization: running · initiali');
    expect(state.submissions[0].repository).toBe('');
  } finally {
    releaseProjectsReload();
  }
  await expect(
    panel.getByRole('button', { name: /^Volume workspace · Work/ }),
  ).toBeVisible();
  state.succeed();
  await panel
    .getByRole('button', { name: 'Open Project', exact: true })
    .click();
  await expect(
    page.locator(
      `[data-window-id="project-files:vm:${parent.hostId}:initialized-project"]`,
    ),
  ).toBeVisible();
});

test('inline Git credentials never reach submission or browser recovery', async ({
  page,
}) => {
  const state = await fixture(page);
  const panel = await launcher(page);
  await panel.getByLabel('Project action').selectOption('clone');
  await panel.getByLabel('Project volume').selectOption(parent.volumeId);
  await panel.getByLabel('New directory').fill('credential-test');
  await panel
    .getByLabel('Repository URL')
    .fill('https://supersecret@example.com/repository.git');
  await panel.getByRole('button', { name: 'Clone', exact: true }).click();
  await expect(panel.getByRole('alert')).toContainText('credential-free');
  expect(state.submissions).toHaveLength(0);
  expect(
    await page.evaluate(() => JSON.stringify({ ...localStorage })),
  ).not.toContain('supersecret');
});

test('switching project modes preserves the draft and keeps hidden input inactive', async ({
  page,
}) => {
  await fixture(page);
  const panel = await launcher(page);
  const action = panel.getByLabel('Project action');
  await action.selectOption('clone');
  const directory = panel.getByLabel('New directory');
  await directory.fill('unfinished-repository');
  await panel
    .getByLabel('Repository URL')
    .fill('https://example.com/repository.git');
  await action.selectOption('open');
  await expect(directory).toBeHidden();
  await expect(directory).toBeDisabled();
  await directory.evaluate((element) => (element as HTMLInputElement).focus());
  await expect(directory).not.toBeFocused();
  await action.selectOption('clone');
  await expect(directory).toHaveValue('unfinished-repository');
  await expect(panel.getByLabel('Repository URL')).toHaveValue(
    'https://example.com/repository.git',
  );
  await panel.getByRole('tab', { name: 'Host', exact: true }).click();
  await expect(directory).toBeHidden();
  await expect(directory).toBeDisabled();
  await directory.evaluate((element) => (element as HTMLInputElement).focus());
  await expect(directory).not.toBeFocused();
  await panel.getByRole('tab', { name: 'VM', exact: true }).click();
  await expect(directory).toHaveValue('unfinished-repository');
});

test('initialization does not adopt a new boot between workspace registration and submission', async ({
  page,
}) => {
  const state = await fixture(page, false, true);
  const panel = await launcher(page);
  await panel.getByLabel('Project action').selectOption('create');
  await panel.getByLabel('Project volume').selectOption(parent.volumeId);
  await panel.getByLabel('New directory').fill('original-boot-only');
  await panel.getByRole('button', { name: 'Create', exact: true }).click();
  await expect(panel.getByRole('alert')).toContainText('was not redirected');
  expect(state.submissions).toHaveLength(0);
  const saved = await page.evaluate(
    () =>
      Object.entries(localStorage).find(([key]) =>
        key.includes('axv-vm-project-initialization'),
      )?.[1],
  );
  expect(JSON.parse(saved!)).toMatchObject({
    bootId: binding.bootId,
    instanceId: binding.instanceId,
    directory: 'original-boot-only',
  });
  await panel.getByRole('button', { name: 'Recover', exact: true }).click();
  await expect(panel.getByRole('alert')).toContainText('was not redirected');
  expect(state.submissions).toHaveLength(0);
});

test('recovery refuses an ordinary task receipt with the same request identity', async ({
  page,
}) => {
  const state = await fixture(page, true);
  const panel = await launcher(page);
  await panel.getByLabel('Project action').selectOption('create');
  await panel.getByLabel('Project volume').selectOption(parent.volumeId);
  await panel.getByLabel('New directory').fill('typed-only');
  await panel.getByRole('button', { name: 'Create', exact: true }).click();
  await expect(panel.getByRole('alert')).toContainText('The response was lost');
  state.makeOrdinary();
  await panel.getByRole('button', { name: 'Recover', exact: true }).click();
  await expect(panel.getByRole('alert')).toContainText(
    'original typed initialization',
  );
  await expect(
    panel.getByRole('button', { name: 'Open Project', exact: true }),
  ).toHaveCount(0);
  expect(state.submissions).toHaveLength(1);
});
