import type { Page } from '@playwright/test';
import { expect, menuItem, test } from './helpers';
import type { VMProject } from '../apps/dashboard/src/features/project-files/vm-api';

const project: VMProject = {
  id: 'backup-source-project',
  hostId: 'backup-host',
  kind: 'vm',
  name: 'Backup source',
  volumeId: 'source-volume',
  root: 'repo',
  rootIdentity: 'original-root',
  revision: 1,
  createdAt: '2026-10-22T00:00:00Z',
};
const projectWindow = (page: Page) =>
  page.locator(
    `[data-window-id="project-files:vm:${project.hostId}:${project.id}"]`,
  );

async function fixture(page: Page, loseBackup = false, holdOperation = false) {
  const submissions: unknown[] = [];
  const operationRequests = new Map<string, string>();
  const operation = (id: string, requestId: string) => {
    operationRequests.set(id, requestId);
    return { id, requestId, status: 'queued' };
  };
  const volume = {
    id: project.volumeId,
    name: 'Source volume',
    revision: 3,
    provider: 'fixture',
    sizeGiB: 1,
    retain: true,
    machineId: 'source-vm',
    status: 'ready',
  };
  const machine = {
    id: 'source-vm',
    hostId: project.hostId,
    revision: 5,
    status: 'stopped',
    desired: 'stopped',
    ready: false,
    guestState: 'stopped',
    guestCapabilities: ['project-files', 'project-restore'],
    createdAt: project.createdAt,
    updatedAt: project.createdAt,
    spec: {
      name: 'Source VM',
      provider: 'fixture',
      imageId: 'test-image',
      kind: 'development',
      resources: { cpus: 1, memoryMiB: 512, diskGiB: 4 },
      projectGiB: 1,
      retainVolume: true,
    },
  };
  const backup = {
    ...volume,
    id: 'backup-volume',
    name: 'Saved backup',
    machineId: '',
    revision: 2,
    backup: {
      hostId: project.hostId,
      sourceVolumeId: volume.id,
      sourceVolumeRevision: volume.revision,
      sourceMachineId: machine.id,
      sourceMachineRevision: machine.revision,
      projects: [project],
      createdAt: project.createdAt,
    },
  };
  const restored = {
    ...backup,
    id: 'restored-volume',
    name: 'Recovered',
    restoredFrom: backup.id,
  };
  let exists = false;
  let restoredExists = false;
  let restoredVM = false;
  let bound = false;
  const next = {
    ...project,
    id: 'restored-project',
    volumeId: restored.id,
    name: 'Recovered',
  };
  await page.route('**/api/compute/**', async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    const json = (value: unknown, status = 200) =>
      route.fulfill({ status, json: value });
    if (path.endsWith('/info'))
      return json({
        configured: true,
        hostId: project.hostId,
        providers: [{ provider: 'fixture', available: true, volumeCopy: true }],
        images: [],
      });
    if (path.includes('/project') || req.method() === 'POST')
      expect(req.headers()['x-axv-host-id']).toBe(project.hostId);
    if (path.startsWith('/api/compute/operations/')) {
      const id = path.split('/').at(-1)!;
      return json({
        id,
        requestId: operationRequests.get(id),
        status: holdOperation ? 'running' : 'succeeded',
        volumeId:
          id === 'backup-op'
            ? backup.id
            : id === 'restore-op'
              ? restored.id
              : undefined,
      });
    }
    if (path === '/api/compute/machines') {
      if (req.method() === 'POST') {
        expect(req.postDataJSON().spec.volumeId).toBe(restored.id);
        restoredVM = true;
        return json(
          {
            machine: { ...machine, id: 'restored-vm' },
            operation: operation('create-new-vm', req.postDataJSON().requestId),
          },
          202,
        );
      }
      return json({
        machines: [
          machine,
          ...(restoredVM
            ? [
                {
                  ...machine,
                  id: 'restored-vm',
                  status: 'running',
                  desired: 'running',
                  ready: true,
                },
              ]
            : []),
        ],
      });
    }
    if (path.endsWith('/volumes'))
      return json({
        volumes: [
          volume,
          ...(exists ? [backup] : []),
          ...(restoredExists
            ? [{ ...restored, machineId: restoredVM ? 'restored-vm' : '' }]
            : []),
        ],
      });
    if (path === '/api/compute/project-backups')
      return json({ backups: exists ? [backup] : [] });
    if (path === '/api/compute/projects')
      return json({ projects: [project, ...(bound ? [next] : [])] });
    if (path.endsWith('/backups')) {
      submissions.push(req.postDataJSON());
      exists = true;
      if (loseBackup) {
        loseBackup = false;
        return route.abort('failed');
      }
      return json(
        {
          volume: backup,
          operation: operation('backup-op', req.postDataJSON().requestId),
        },
        202,
      );
    }
    if (path.endsWith('/restore')) {
      expect(req.postDataJSON().revision).toBe(backup.revision);
      restoredExists = true;
      return json(
        {
          volume: restored,
          operation: operation('restore-op', req.postDataJSON().requestId),
        },
        202,
      );
    }
    if (path.endsWith('/bind')) {
      expect(req.postDataJSON()).toMatchObject({
        sourceProjectId: project.id,
        volumeId: restored.id,
        machineId: 'restored-vm',
      });
      bound = true;
      return json({ project: next }, 201);
    }
    if (path.endsWith('/binding')) {
      const p = path.includes(next.id) ? next : project;
      return json({
        project: p,
        binding: {
          hostId: p.hostId,
          projectId: p.id,
          projectRevision: 1,
          volumeId: p.volumeId,
          volumeRevision: 3,
          machineId: p === next ? 'restored-vm' : machine.id,
          bootId: 'boot',
          instanceId: 'guest',
        },
      });
    }
    if (path.endsWith('/files/list')) return json({ project, entries: [] });
    return json({ code: 'fixture_unhandled', error: path }, 404);
  });
  return {
    submissions,
    complete: () => {
      holdOperation = false;
    },
  };
}

async function open(page: Page) {
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await menuItem(page, 'Project Files').click();
  const launcher = page.locator('[data-window-id="project-files:projects"]');
  await launcher.getByRole('tab', { name: 'VM', exact: true }).click();
  await launcher.getByRole('button', { name: /^Backup source ·/ }).click();
  await projectWindow(page)
    .getByRole('tab', { name: 'Backups', exact: true })
    .click();
  return projectWindow(page).locator('.vm-project-backups');
}

test('offline backup recovery retains its exact payload and restores a new project on narrow screens', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const state = await fixture(page, true);
  let panel = await open(page);
  await panel.getByLabel('Name').fill('Recovered');
  await panel.getByRole('button', { name: 'Back Up', exact: true }).click();
  await expect(panel.getByRole('button', { name: 'Recover' })).toBeVisible();
  expect(state.submissions).toHaveLength(1);
  await page.reload();
  panel = projectWindow(page).locator('.vm-project-backups');
  await projectWindow(page)
    .getByRole('tab', { name: 'Backups', exact: true })
    .click();
  await panel.getByRole('button', { name: 'Recover' }).click();
  await expect(panel.getByRole('button', { name: 'Recover' })).toHaveCount(0);
  expect(state.submissions).toHaveLength(2);
  expect(state.submissions[1]).toEqual(state.submissions[0]);
  await panel.getByLabel('Name').fill('Recovered');
  await panel
    .getByRole('combobox', { name: 'Backup', exact: true })
    .selectOption('backup-volume');
  await panel.getByRole('button', { name: 'Restore', exact: true }).click();
  await panel
    .getByRole('combobox', { name: 'Restored volume', exact: true })
    .selectOption('restored-volume');
  await panel
    .getByRole('button', {
      name: 'Create VM',
      exact: true,
    })
    .click();
  await panel
    .getByRole('button', {
      name: 'Open Project',
      exact: true,
    })
    .click();
  await expect(
    page.locator(
      `[data-window-id="project-files:vm:${project.hostId}:restored-project"]`,
    ),
  ).toBeVisible();
  await expect(projectWindow(page)).toHaveCount(1);
});

test('invalid backup recovery bytes remain intact until explicit release', async ({
  page,
}) => {
  await fixture(page);
  const key = await page.evaluate(
    async ({ hostId, projectId }) => {
      const clientId = localStorage.getItem('nextstep-browser-id');
      const bootstrap = await (
        await fetch(`/api/bootstrap?clientId=${encodeURIComponent(clientId!)}`)
      ).json();
      const key = JSON.stringify([
        'axv-project-backup-request',
        bootstrap.databaseId,
        clientId,
        hostId,
        projectId,
      ]);
      localStorage.setItem(key, '{broken original payload');
      return key;
    },
    { hostId: project.hostId, projectId: project.id },
  );
  const panel = await open(page);
  await expect(panel.getByRole('alert')).toContainText('could not be read');
  await expect(
    panel.getByRole('button', { name: 'Back Up', exact: true }),
  ).toBeDisabled();
  expect(await page.evaluate((key) => localStorage.getItem(key), key)).toBe(
    '{broken original payload',
  );
  await panel.getByRole('button', { name: 'Release' }).click();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  expect(await page.evaluate((key) => localStorage.getItem(key), key)).toBe(
    '{broken original payload',
  );
  await panel.getByRole('button', { name: 'Release' }).click();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Release Request', exact: true })
    .click();
  await expect
    .poll(() => page.evaluate((key) => localStorage.getItem(key), key))
    .toBeNull();
});

test('accepted backup operations retain their identity across reload without resubmission', async ({
  page,
}) => {
  const state = await fixture(page, false, true);
  let panel = await open(page);
  await panel.getByLabel('Name').fill('Tracked backup');
  await panel.getByRole('button', { name: 'Back Up', exact: true }).click();
  await panel.getByText('Request Details', { exact: true }).click();
  await expect(panel.getByText('Operation backup-op: running')).toBeVisible();
  await page.reload();
  await projectWindow(page)
    .getByRole('tab', { name: 'Backups', exact: true })
    .click();
  panel = projectWindow(page).locator('.vm-project-backups');
  await panel.getByText('Request Details', { exact: true }).click();
  await expect(panel.getByText('Operation backup-op: running')).toBeVisible();
  expect(state.submissions).toHaveLength(1);
  await expect(
    panel.getByRole('button', { name: 'Back Up', exact: true }),
  ).toBeDisabled();
  state.complete();
  await expect(
    panel.getByText('Create backup succeeded.', { exact: true }),
  ).toBeVisible();
  expect(state.submissions).toHaveLength(1);
});

test('a late backup response preserves newer recovery bytes', async ({
  page,
}) => {
  await fixture(page);
  let release: (() => void) | undefined;
  const barrier = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(
    `**/api/compute/projects/${project.id}/backups`,
    async (route) => {
      await barrier;
      await route.fulfill({
        status: 202,
        json: {
          operation: {
            id: 'late-operation',
            requestId: route.request().postDataJSON().requestId,
            status: 'queued',
          },
        },
      });
    },
  );
  const panel = await open(page);
  await panel.getByLabel('Name').fill('Original');
  await panel.getByRole('button', { name: 'Back Up', exact: true }).click();
  await expect(
    panel.getByText(/Retained request: Create backup/),
  ).toBeVisible();
  const key = await page.evaluate(() =>
    Object.keys(localStorage).find((key) =>
      key.includes('axv-project-backup-request'),
    )!,
  );
  const newer = '{newer recovery bytes to preserve';
  await page.evaluate(({ key, newer }) => localStorage.setItem(key, newer), {
    key,
    newer,
  });
  release!();
  await expect(panel.getByRole('alert')).toContainText(
    'recovery record changed',
  );
  expect(await page.evaluate((key) => localStorage.getItem(key), key)).toBe(
    newer,
  );
});

test('a closed backup window does not reopen a project after recovery storage unblocks', async ({
  page,
}) => {
  await fixture(page);
  const panel = await open(page);
  await panel.getByLabel('Name').fill('Recovered');
  await panel.getByRole('button', { name: 'Back Up', exact: true }).click();
  await panel
    .getByRole('combobox', { name: 'Backup', exact: true })
    .selectOption('backup-volume');
  await panel.getByRole('button', { name: 'Restore', exact: true }).click();
  await panel
    .getByRole('combobox', { name: 'Restored volume', exact: true })
    .selectOption('restored-volume');
  await panel
    .getByRole('button', {
      name: 'Create VM',
      exact: true,
    })
    .click();
  let blockedKey = '';
  await page.route(
    '**/api/compute/project-backups/backup-volume/bind',
    async (route) => {
      blockedKey = await page.evaluate(() =>
        Object.keys(localStorage).find((key) =>
          key.includes('axv-project-backup-request'),
        )!,
      );
      await page.evaluate(
        (key) =>
          new Promise<void>((resolve) => {
            void navigator.locks.request(key, async () => {
              resolve();
              await new Promise<void>((release) => {
                (
                  window as Window & { releaseBackupLock?: () => void }
                ).releaseBackupLock = release;
              });
            });
          }),
        blockedKey,
      );
      await route.fulfill({
        status: 201,
        json: {
          project: {
            ...project,
            id: 'restored-project',
            volumeId: 'restored-volume',
          },
        },
      });
    },
  );
  await panel
    .getByRole('button', {
      name: 'Open Project',
      exact: true,
    })
    .click();
  await expect
    .poll(async () =>
      page.evaluate(
        async (key) =>
          (await navigator.locks.query()).pending?.some(
            (lock) => lock.name === key,
          ),
        blockedKey,
      ),
    )
    .toBe(true);
  await projectWindow(page)
    .getByRole('button', { name: /^Close / })
    .click();
  await expect(projectWindow(page)).toHaveCount(0);
  await page.evaluate(() =>
    (
      window as Window & { releaseBackupLock?: () => void }
    ).releaseBackupLock?.(),
  );
  await expect
    .poll(async () =>
      page.evaluate(
        async (key) =>
          (await navigator.locks.query()).pending?.some(
            (lock) => lock.name === key,
          ),
        blockedKey,
      ),
    )
    .toBe(false);
  await expect(
    page.locator(
      `[data-window-id="project-files:vm:${project.hostId}:restored-project"]`,
    ),
  ).toHaveCount(0);
});
