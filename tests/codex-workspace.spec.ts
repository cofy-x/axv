import { activate } from './helpers';
import { encoded, output, run } from './terminal-helpers';
import {
  codexSessionAction,
  codexWindow,
  endCodex,
  expect,
  openCodexManager,
  openCodexFolderPicker,
  projectPath,
  readState,
  runCodex,
  session,
  sessions,
  startCodexInFolder,
  test,
} from './codex-helpers';

test('renaming and archiving a running Codex session preserve its identity and work through reload', async ({
  page,
  request,
}) => {
  const { window, id, runId } = await startCodexInFolder(page);
  const original = await session(request, id);
  const windowId = await window.getAttribute('data-window-id');
  await runCodex(window, 'state named-and-archived');
  const process = await readState(window, 'before-metadata');
  await codexSessionAction(page, window, 'Rename');
  const dialog = page.getByRole('dialog', { name: 'Rename Codex Session' });
  const name = 'Review API changes ✓';
  await dialog.getByLabel('Session name', { exact: true }).fill(name);
  await expect(window.getByLabel('Codex terminal input')).not.toBeFocused();
  await dialog.getByRole('button', { name: 'Rename', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(window).toHaveAttribute('aria-label', `Codex · ${name}`);
  await expect(window).toHaveAttribute('data-window-id', windowId!);
  const renamed = await session(request, id);
  expect(renamed.title).toBe(name);
  expect(renamed.metadataRevision).toBe(original.metadataRevision + 1);
  expect(renamed.tmuxSession).toBe(original.tmuxSession);
  expect(renamed.tmuxSocket).toBe(original.tmuxSocket);
  await codexSessionAction(page, window, 'Archive');
  await expect
    .poll(async () => !!(await session(request, id)).archivedAt)
    .toBe(true);
  expect((await session(request, id)).status).toBe('running');
  await expect(window.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  expect(await readState(window, 'after-archive')).toEqual(process);
  let manager = await openCodexManager(page);
  await expect(manager.locator(`[data-session-id="${id}"]`)).toHaveCount(0);
  await manager.getByRole('button', { name: 'Archived', exact: true }).click();
  await expect(manager.locator(`[data-session-id="${id}"]`)).toContainText(
    name,
  );
  await page.reload();
  manager = await openCodexManager(page);
  await manager.getByRole('button', { name: 'Archived', exact: true }).click();
  await manager
    .locator(`[data-session-id="${id}"]`)
    .getByRole('button', { name: 'Open', exact: true })
    .click();
  const restored = codexWindow(page, id, runId);
  await expect(restored).toHaveAttribute('data-window-id', windowId!);
  await expect(restored).toHaveAttribute('aria-label', `Codex · ${name}`);
  expect(await readState(restored, 'after-reload')).toEqual(process);
  await codexSessionAction(page, restored, 'Unarchive');
  await expect
    .poll(async () => (await session(request, id)).archivedAt)
    .toBeUndefined();
  await endCodex(page, restored);
});

test('a conflicting Codex rename keeps the draft and does not overwrite another client', async ({
  page,
  request,
}) => {
  const { window, id } = await startCodexInFolder(page);
  const original = await session(request, id);
  await codexSessionAction(page, window, 'Rename');
  const dialog = page.getByRole('dialog', { name: 'Rename Codex Session' });
  await dialog
    .getByLabel('Session name', { exact: true })
    .fill('My unsaved name');
  const changed = await request.patch(`/api/host/codex/sessions/${id}`, {
    headers: { Origin: process.env.E2E_BASE_URL! },
    data: {
      revision: original.metadataRevision,
      title: 'Name from another client',
    },
  });
  expect(changed.ok()).toBeTruthy();
  await dialog.getByRole('button', { name: 'Rename', exact: true }).click();
  await expect(dialog).toContainText('changed elsewhere');
  await expect(dialog.getByLabel('Session name', { exact: true })).toHaveValue(
    'My unsaved name',
  );
  expect((await session(request, id)).title).toBe('Name from another client');
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await endCodex(page, window);
});

test('a recent project selects a directory before explicitly starting a distinct Codex session', async ({
  page,
  request,
}) => {
  const first = await startCodexInFolder(page);
  await first.window.getByRole('button', { name: /^Close / }).click();
  const manager = await openCodexManager(page);
  await expect(
    manager.getByRole('region', { name: 'Recent projects' }),
  ).toHaveCount(0);
  await openCodexFolderPicker(page);
  const recent = manager.getByRole('button', {
    name: 'Recent Projects',
    exact: true,
  });
  await expect(recent).toBeVisible();
  const submissions: string[] = [];
  page.on('request', (request) => {
    if (
      request.url().endsWith('/api/host/codex/sessions') &&
      request.method() === 'POST'
    )
      submissions.push(request.url());
  });
  await recent.click();
  const menu = page.getByRole('menu', { name: 'Recent Projects', exact: true });
  await menu
    .getByRole('menuitem', {
      name: `${projectPath.split('/').at(-1)} ${projectPath.slice(0, projectPath.lastIndexOf('/'))}`,
      exact: true,
    })
    .click();
  await expect(
    manager.getByLabel('Project directory', { exact: true }),
  ).toHaveValue(projectPath);
  const start = manager.getByRole('button', {
    name: 'Start Here',
    exact: true,
  });
  await expect(start).toBeEnabled();
  expect(submissions).toEqual([]);
  await expect(manager.getByTestId('codex-directory-context')).toHaveCount(0);
  await expect(recent).toBeFocused();
  const created = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/host/codex/sessions') &&
      response.request().method() === 'POST',
  );
  await start.click();
  const response = await created;
  expect(response.ok()).toBeTruthy();
  const current = (await response.json()).session;
  expect(current.id).not.toBe(first.id);
  expect(current.projectPath).toBe(projectPath);
  await expect(
    codexWindow(page, current.id, current.runId).getByTestId('codex-status'),
  ).toHaveAttribute('data-state', 'connected');
  const projects = await request.get('/api/host/codex/projects');
  expect(projects.ok()).toBeTruthy();
  const rows = (await projects.json()).projects as { path: string }[];
  expect(rows[0].path).toBe(projectPath);
  expect(rows.filter((row) => row.path === projectPath)).toHaveLength(1);
  await expect
    .poll(async () =>
      (await sessions(request))
        .filter((row) => row.status === 'running')
        .map((row) => row.id)
        .sort(),
    )
    .toEqual([first.id, current.id].sort());
});

test('recent-project refresh errors stay visible outside the folder picker and recover without losing shortcuts', async ({
  page,
}) => {
  const first = await startCodexInFolder(page);
  await first.window.getByRole('button', { name: /^Close / }).click();
  const manager = await openCodexFolderPicker(page);
  const recent = manager.getByRole('button', {
    name: /^Recent Projects/,
    exact: true,
  });
  await expect(recent).toBeVisible();
  await manager.getByRole('button', { name: 'Cancel', exact: true }).click();
  const url = '**/api/host/codex/projects';
  const diagnostic = 'Recent projects could not be refreshed.';
  await page.route(url, (route) =>
    route.fulfill({
      status: 503,
      json: { code: 'fixture_unavailable', error: diagnostic },
    }),
  );
  try {
    await manager.getByRole('button', { name: 'Refresh', exact: true }).click();
    await expect(manager.getByRole('alert')).toHaveText(diagnostic);
    await expect(
      manager.getByRole('region', { name: 'Recent projects' }),
    ).toHaveCount(0);
    await openCodexFolderPicker(page);
    await expect(manager.getByRole('alert')).toHaveText(diagnostic);
    await expect(
      manager.getByRole('button', {
        name: 'Recent Projects · Outdated',
        exact: true,
      }),
    ).toBeVisible();
    await expect(recent).toBeEnabled();
  } finally {
    await page.unroute(url);
  }
  await manager.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(manager.getByRole('alert')).toHaveCount(0);
  await expect(
    manager.getByRole('button', { name: 'Recent Projects', exact: true }),
  ).toBeVisible();
  await expect(recent).toBeEnabled();
});

test('Host Terminal uses the Codex directory and closing it leaves Codex running', async ({
  page,
  request,
}) => {
  const { window, id } = await startCodexInFolder(page);
  const original = await readState(window, 'before-project-terminal');
  await codexSessionAction(page, window, 'Host Terminal');
  const terminal = page.locator('.ns-window.is-active[data-app-id="terminal"]');
  await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  await expect(terminal.getByTestId('terminal-project-name')).toHaveText(
    projectPath.split('/').filter(Boolean).at(-1)!,
  );
  await terminal.getByRole('button', { name: 'Details', exact: true }).click();
  const details = page.getByRole('dialog', {
    name: 'Host Terminal Details',
    exact: true,
  });
  await expect(details.getByTestId('terminal-project-path')).toHaveText(
    projectPath,
  );
  await details.getByRole('button', { name: 'Close', exact: true }).click();
  await run(terminal, `printf '${encoded('PROJECT_CWD:')}%s\\n' "$PWD"`);
  await expect(output(terminal)).toContainText(`PROJECT_CWD:${projectPath}`);
  await terminal.getByRole('button', { name: /^Close / }).click();
  const confirmation = page.getByRole('dialog');
  await confirmation
    .getByRole('button', { name: 'End Session', exact: true })
    .click();
  await expect(terminal).toHaveCount(0);
  expect((await session(request, id)).status).toBe('running');
  await activate(page, window);
  expect(await readState(window, 'after-project-terminal')).toEqual(original);
  await endCodex(page, window);
});
