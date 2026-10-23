import { switchWorkspace } from './helpers';
import {
  choose,
  codexWindow,
  endCodex,
  expect,
  openCodexEnvironment,
  openCodexFolderPicker,
  openCodexManager,
  projectPath,
  runCodex,
  selectCodexProject,
  session,
  startCodex,
  test,
} from './codex-helpers';
import type { CodexSession } from './codex-helpers';

test('closing and reopening the folder picker preserves an unsubmitted path', async ({
  page,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  const manager = await selectCodexProject(page, touch);
  const draft = `${projectPath}/nested`;
  await manager.getByLabel('Project directory', { exact: true }).fill(draft);
  await choose(
    manager.getByRole('button', { name: 'Cancel', exact: true }),
    touch,
  );
  await expect(
    manager.getByRole('button', { name: 'New in Folder', exact: true }),
  ).toBeFocused();
  await openCodexFolderPicker(page, touch);
  await expect(
    manager.getByLabel('Project directory', { exact: true }),
  ).toHaveValue(draft);
  await expect(
    manager.getByTestId('codex-directory-context').locator('code'),
  ).toHaveText(projectPath);
  await expect(
    manager.getByRole('button', { name: 'Start Here', exact: true }),
  ).toBeDisabled();
  await manager.getByLabel('Project directory', { exact: true }).focus();
  await page.keyboard.press('Escape');
  await expect(
    manager.getByRole('region', { name: 'New Codex session', exact: true }),
  ).toHaveCount(0);
  await expect(
    manager.getByRole('button', { name: 'New in Folder', exact: true }),
  ).toBeFocused();
  await openCodexFolderPicker(page, touch);
  await manager.getByLabel('Project directory', { exact: true }).fill('');
  await page.keyboard.press('Escape');
  await openCodexFolderPicker(page, touch);
  await expect(
    manager.getByLabel('Project directory', { exact: true }),
  ).toHaveValue('');
  await expect(
    manager.getByTestId('codex-directory-context').locator('code'),
  ).toHaveText(projectPath);
  await expect(
    manager.getByRole('button', { name: 'Start Here', exact: true }),
  ).toBeDisabled();
});

test('a delayed directory result preserves newer typing and the last confirmed location', async ({
  page,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  const manager = await selectCodexProject(page, touch);
  const selected = `${projectPath}/nested`;
  let held = false;
  let release!: () => void;
  const released = new Promise<void>((resolve) => {
    release = resolve;
  });
  const url = '**/api/host/directories?*';
  await page.route(url, async (route) => {
    if (new URL(route.request().url()).searchParams.get('path') !== selected)
      return route.continue();
    const response = await route.fetch();
    held = true;
    await released;
    await route.fulfill({ response });
  });
  try {
    const input = manager.getByLabel('Project directory', { exact: true });
    await input.fill(selected);
    await input.press('Enter');
    await expect.poll(() => held).toBe(true);
    await expect(input).toBeEditable();
    await input.fill(`${projectPath}/unfinished-draft`);
    release();
    await expect(
      manager.getByRole('button', { name: 'Go', exact: true }),
    ).toBeEnabled();
    await expect(input).toHaveValue(`${projectPath}/unfinished-draft`);
    await expect(input).toBeFocused();
    await expect(
      manager.getByTestId('codex-directory-context').locator('code'),
    ).toHaveText(selected);
    await expect(
      manager.getByRole('button', { name: 'Start Here', exact: true }),
    ).toBeDisabled();
  } finally {
    release();
    await page.unroute(url);
  }
});

test('failed directory reads retain outdated contents without allowing stale navigation', async ({
  page,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  const manager = await selectCodexProject(page, touch);
  const input = manager.getByLabel('Project directory', { exact: true });
  await input.fill(`${projectPath}/missing-directory`);
  await input.press('Enter');
  await expect(manager.getByRole('alert')).toBeVisible();
  await expect(
    manager.getByText('Previous folder contents · Outdated', { exact: true }),
  ).toBeVisible();
  for (const name of ['Up', 'Open folder nested', 'Start Here'])
    await expect(
      manager.getByRole('button', { name, exact: true }),
    ).toBeDisabled();
  await expect(input).toBeFocused();
  await input.fill(projectPath);
  await input.press('Enter');
  await expect(
    manager.getByRole('button', { name: 'Start Here', exact: true }),
  ).toBeEnabled();
  await expect(manager.getByRole('alert')).toHaveCount(0);
  await expect(
    manager.getByText('Previous folder contents · Outdated', { exact: true }),
  ).toHaveCount(0);
  await expect(
    manager.getByRole('button', { name: 'Open folder nested', exact: true }),
  ).toBeEnabled();
  await expect(input).toBeFocused();
});

test('late creation preserves newer workspace and list choices instead of activating a run', async ({
  page,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  const manager = await openCodexManager(page, touch);
  const changeWorkspace = async (next: boolean) => {
    if (touch)
      await choose(
        page.getByRole('button', {
          name: next ? 'Next workspace' : 'Previous workspace',
          exact: true,
        }),
        true,
      );
    else await switchWorkspace(page, next ? 2 : 1);
  };
  for (const scenario of ['workspace', 'filter']) {
    let result: CodexSession | undefined;
    let release!: () => void;
    const released = new Promise<void>((resolve) => {
      release = resolve;
    });
    const url = '**/api/host/codex/sessions';
    await page.route(url, async (route) => {
      if (route.request().method() !== 'POST') return route.continue();
      const response = await route.fetch();
      result = (await response.json()).session;
      await released;
      await route.fulfill({ response });
    });
    try {
      await choose(
        manager.getByRole('button', { name: 'New Session', exact: true }),
        touch,
      );
      await expect.poll(() => !!result).toBe(true);
      if (scenario === 'workspace') {
        await changeWorkspace(true);
        await expect(manager).toBeHidden();
      } else
        await choose(
          manager.getByRole('button', { name: 'Archived', exact: true }),
          touch,
        );
      release();
      await expect(manager.getByTestId('codex-start-notice')).toContainText(
        'Session started.',
      );
      await expect(codexWindow(page, result!.id, result!.runId)).toHaveCount(0);
      if (scenario === 'workspace') {
        await expect(manager).toBeHidden();
        await changeWorkspace(false);
        await openCodexManager(page, touch);
      } else
        await expect(
          manager.getByRole('button', { name: 'Archived', exact: true }),
        ).toHaveAttribute('aria-pressed', 'true');
      await choose(
        manager.getByRole('button', { name: 'Unarchived', exact: true }),
        touch,
      );
      const row = manager.locator(
        `[data-session-id="${result!.id}"][data-run-id="${result!.runId}"]`,
      );
      await expect(row).toBeVisible();
      await choose(
        row.getByRole('button', { name: 'Open', exact: true }),
        touch,
      );
      await expect(codexWindow(page, result!.id, result!.runId)).toHaveClass(
        /is-active/,
      );
      await choose(
        codexWindow(page, result!.id, result!.runId).getByRole('button', {
          name: /^Close /,
        }),
        touch,
      );
      await openCodexManager(page, touch);
    } finally {
      release();
      await page.unroute(url);
    }
  }
});

test('a late continuation preserves the selected workspace and the previous run identity', async ({
  page,
  request,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  const original = await startCodex(page, touch);
  await runCodex(original.window, 'message Retain the original conversation');
  await endCodex(page, original.window, touch);
  const manager = await openCodexManager(page, touch);
  const row = manager.locator(
    `[data-session-id="${original.id}"][data-run-id="${original.runId}"]`,
  );
  const url = `**/api/host/codex/sessions/${original.id}/resume`;
  let result: CodexSession | undefined;
  let release!: () => void;
  const released = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(url, async (route) => {
    const response = await route.fetch();
    result = (await response.json()).session;
    await released;
    await route.fulfill({ response });
  });
  try {
    const action = row.getByRole('button', { name: 'Continue', exact: true });
    await expect(action).toBeEnabled();
    await choose(action, touch);
    await choose(
      page
        .getByRole('dialog', { name: 'Continue Conversation?', exact: true })
        .getByRole('button', { name: 'Continue', exact: true }),
      touch,
    );
    await expect.poll(() => !!result).toBe(true);
    if (touch)
      await choose(
        page.getByRole('button', { name: 'Next workspace', exact: true }),
        true,
      );
    else await switchWorkspace(page, 2);
    await expect(manager).toBeHidden();
    release();
    await expect(manager.getByTestId('codex-start-notice')).toHaveText(
      'Continuation ready. Open it from Sessions.',
    );
    await expect(manager).toBeHidden();
    await expect(codexWindow(page, original.id, result!.runId)).toHaveCount(0);
    const previous = await session(request, original.id, original.runId);
    expect(previous.runId).toBe(original.runId);
    expect(previous.status).toBe('ended');
    expect(result!.runId).not.toBe(original.runId);
    expect(result!.conversationId).toBe(previous.conversationId);
    if (touch)
      await choose(
        page.getByRole('button', { name: 'Previous workspace', exact: true }),
        true,
      );
    else await switchWorkspace(page, 1);
    await openCodexManager(page, touch);
    const continued = manager.locator(
      `[data-session-id="${original.id}"][data-run-id="${result!.runId}"]`,
    );
    await expect(row).toHaveCount(0);
    await choose(
      continued.getByRole('button', { name: 'Open', exact: true }),
      touch,
    );
    await expect(
      codexWindow(page, original.id, result!.runId).getByTestId('codex-status'),
    ).toHaveAttribute('data-state', 'connected');
    await expect(original.window.getByTestId('codex-session')).toHaveAttribute(
      'data-run-id',
      original.runId,
    );
  } finally {
    release();
    await page.unroute(url);
  }
});

test('leaving the manager during continuation preflight prevents a late confirmation dialog', async ({
  page,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  const original = await startCodex(page, touch);
  await runCodex(original.window, 'message Keep this conversation available');
  await endCodex(page, original.window, touch);
  const manager = await openCodexManager(page, touch);
  const row = manager.locator(
    `[data-session-id="${original.id}"][data-run-id="${original.runId}"]`,
  );
  const url = `**/api/host/codex/sessions/${original.id}`;
  let held = false;
  let release!: () => void;
  const released = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(url, async (route) => {
    const response = await route.fetch();
    held = true;
    await released;
    await route.fulfill({ response });
  });
  try {
    const action = row.getByRole('button', { name: 'Continue', exact: true });
    await expect(action).toBeEnabled();
    await choose(action, touch);
    await expect.poll(() => held).toBe(true);
    if (touch)
      await choose(
        page.getByRole('button', { name: 'Next workspace', exact: true }),
        true,
      );
    else await switchWorkspace(page, 2);
    await expect(manager).toBeHidden();
    release();
    await expect(
      row.getByRole('button', {
        name: 'Continue',
        exact: true,
        includeHidden: true,
      }),
    ).toBeEnabled();
    await expect(
      page.getByRole('dialog', { name: 'Continue Conversation?', exact: true }),
    ).toHaveCount(0);
    await expect(manager).toBeHidden();
  } finally {
    release();
    await page.unroute(url);
  }
});

test('closing the picker after readiness fails returns focus to an available control', async ({
  page,
  request,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  const manager = await openCodexFolderPicker(page, touch);
  const available = await (await request.get('/api/host/codex/info')).json();
  const url = '**/api/host/codex/info';
  await page.route(url, (route) =>
    route.fulfill({
      json: { ...available, ready: false, reason: 'The host is unavailable.' },
    }),
  );
  try {
    const environment = await openCodexEnvironment(page, touch);
    await choose(
      environment.getByRole('button', { name: 'Refresh', exact: true }),
      touch,
    );
    await expect(
      manager.getByRole('button', {
        name: 'New Session',
        exact: true,
        includeHidden: true,
      }),
    ).toBeDisabled();
    await choose(environment.getByRole('button', { name: /^Close / }), touch);
    await openCodexManager(page, touch);
    await choose(
      manager.getByRole('button', { name: 'Cancel', exact: true }),
      touch,
    );
    await expect(
      manager.getByRole('button', { name: 'New in Folder', exact: true }),
    ).toBeDisabled();
    await expect(
      manager.getByRole('button', { name: 'Refresh', exact: true }),
    ).toBeFocused();
  } finally {
    await page.unroute(url);
  }
});
