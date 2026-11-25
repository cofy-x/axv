import { bounds, menuItem, switchWorkspace } from './helpers';
import { applicationMenu } from './desktop-application-helpers';
import {
  choose,
  codexWindow,
  defaultProjectPath,
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

test('host launch keeps its original request after uncertain refusal and database protection', async ({
  page,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  const manager = await selectCodexProject(page, touch);
  const attempts: { requestId: string; projectPath: string }[] = [];
  await page.route('**/api/host/codex/sessions', (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    attempts.push(route.request().postDataJSON());
    return route.fulfill({
      status: 409,
      json: {
        code:
          attempts.length === 1
            ? 'history_identity_conflict'
            : 'database_changed',
        error:
          attempts.length === 1
            ? 'The original launch outcome is uncertain.'
            : 'The desktop database changed.',
      },
    });
  });
  await page.evaluate(() => {
    const generated: string[] = [];
    Object.defineProperty(globalThis, 'hostStartUUIDs', { value: generated });
    const randomUUID = crypto.randomUUID.bind(crypto);
    crypto.randomUUID = () => {
      const id = randomUUID();
      generated.push(id);
      return id;
    };
  });
  const startHere = manager.getByRole('button', {
    name: 'Start Here',
    exact: true,
  });
  await choose(startHere, touch);
  await expect(
    manager.getByText('The original launch outcome is uncertain.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(startHere).toBeEnabled();
  await choose(startHere, touch);
  await expect(
    manager.getByText('The desktop database changed.', { exact: true }),
  ).toBeVisible();
  await expect(startHere).toBeEnabled();
  expect(attempts).toHaveLength(2);
  expect(attempts[1]).toEqual(attempts[0]);
  // Choosing another project after protection must not replace the held key,
  // even though transport would reject its eventual request locally.
  const newSession = manager.getByRole('button', {
    name: 'New Session',
    exact: true,
  });
  await choose(newSession, touch);
  await expect(newSession).toBeEnabled();
  expect(attempts).toHaveLength(2);
  expect(
    await page.evaluate(
      () =>
        (globalThis as typeof globalThis & { hostStartUUIDs: string[] })
          .hostStartUUIDs,
    ),
  ).toEqual([attempts[0].requestId]);
});

test('the compact launch group keeps the default path and folder picker separate', async ({
  page,
  hasTouch,
}, info) => {
  const home = '/home/fixture';
  await page.route('**/api/host/directories*', (route) =>
    route.fulfill({
      json: {
        path: home,
        homePath: home,
        parentPath: '/home',
        directories: ['.cache', '.config', 'Apps', 'Projects'].map((name) => ({
          name,
          path: `${home}/${name}`,
        })),
        truncated: false,
      },
    }),
  );
  let directoryReads = 0;
  page.on('request', (request) => {
    if (request.url().includes('/api/host/directories')) directoryReads++;
  });
  for (const width of [590, 320]) {
    await page.setViewportSize({ width, height: 854 });
    const manager = await openCodexManager(page, hasTouch);
    const launch = manager.locator('.codex-manager-launch-context');
    const summary = launch.locator('.codex-default-project > summary');
    const indicator = () =>
      summary.evaluate((element) => {
        const area = getComputedStyle(element, '::before');
        return {
          rowBackground: getComputedStyle(element).backgroundColor,
          background: area.backgroundColor,
          width: parseFloat(area.width),
          inset: area.insetInlineEnd,
          direction: getComputedStyle(element, '::after').transform,
        };
      });
    const path = launch.getByTestId('codex-default-project');
    const start = manager.getByRole('button', {
      name: 'New Session',
      exact: true,
    });
    const folder = manager.getByRole('button', {
      name: 'New in Folder',
      exact: true,
    });
    const refresh = manager.getByRole('button', {
      name: 'Refresh',
      exact: true,
    });
    const sessions = manager.getByRole('region', { name: 'Codex sessions' });
    await expect(summary).toContainText('Default folder');
    await expect(path).toHaveText(defaultProjectPath);
    await expect(path).toBeHidden();
    const [
      startBox,
      folderBox,
      refreshBox,
      launchBox,
      summaryBox,
      sessionsBox,
    ] = await Promise.all(
      [start, folder, refresh, launch, summary, sessions].map(bounds),
    );
    expect(Math.abs(startBox.y - folderBox.y)).toBeLessThanOrEqual(1);
    expect(Math.abs(startBox.y - refreshBox.y)).toBeLessThanOrEqual(1);
    expect(summaryBox.y).toBeGreaterThanOrEqual(startBox.y + startBox.height);
    expect(summaryBox.width / launchBox.width).toBeGreaterThan(0.85);
    expect(sessionsBox.y).toBeGreaterThanOrEqual(
      launchBox.y + launchBox.height - 1,
    );
    const screenshot = info.outputPath(`codex-compact-${width}.png`);
    await page.screenshot({ path: screenshot });
    await info.attach(`Compact Codex at ${width}px`, {
      path: screenshot,
      contentType: 'image/png',
    });

    if (!hasTouch) await refresh.hover();
    const resting = await indicator();
    expect(resting.inset).toBe('0px');
    expect(resting.width).toBeGreaterThan(0);
    expect(resting.width).toBeLessThan(summaryBox.width / 10);
    if (!hasTouch) {
      await summary.hover({ position: { x: 12, y: 12 } });
      const hovered = await indicator();
      expect(hovered.rowBackground).toBe(resting.rowBackground);
      expect(hovered.background).not.toBe(resting.background);
    }
    const reads = directoryReads;
    await choose(summary, hasTouch);
    await expect(path).toBeVisible();
    await expect(path).toHaveCSS('user-select', 'text');
    const expanded = await indicator();
    expect(expanded.direction).not.toBe(resting.direction);
    if (hasTouch) expect(expanded.background).toBe(resting.background);
    await expect(folder).toHaveAttribute('aria-expanded', 'false');
    expect(directoryReads).toBe(reads);
    await refresh.focus();
    await page.keyboard.press('Tab');
    await expect(summary).toBeFocused();
    await expect(summary).toHaveCSS('outline-style', 'solid');
    await expect(summary).toHaveCSS('outline-width', '2px');
    await expect(summary).toHaveCSS('outline-offset', '-2px');
    await page.keyboard.press('Enter');
    await expect(path).toBeHidden();
    await page.keyboard.press('Enter');
    await expect(path).toBeVisible();
    if (width === 320) {
      await applicationMenu(page, 'Sessions', hasTouch);
      await choose(menuItem(page, 'New in Folder'), hasTouch);
    } else await choose(folder, hasTouch);
    const picker = launch.getByRole('region', { name: 'New Codex session' });
    await expect(
      picker.getByRole('button', { name: 'Start Here', exact: true }),
    ).toBeEnabled();
    await expect(path).toBeHidden();
    await expect(
      picker.getByLabel('Project directory', { exact: true }),
    ).toHaveValue(home);
    await expect(
      picker.getByLabel('Host folders').getByRole('button'),
    ).toHaveText(['Apps', 'Projects', '.cache', '.config']);
    const pickerScreenshot = info.outputPath(
      `codex-folder-picker-${width}.png`,
    );
    await page.screenshot({ path: pickerScreenshot });
    await info.attach(`Folder picker at ${width}px`, {
      path: pickerScreenshot,
      contentType: 'image/png',
    });
    await choose(summary, hasTouch);
    await expect(path).toBeVisible();
    await expect(picker).toBeVisible();
    await choose(summary, hasTouch);
    await choose(
      picker.getByRole('button', { name: 'Cancel', exact: true }),
      hasTouch,
    );
    await expect(folder).toBeFocused();
  }
});

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
  const changeWorkspace = (next: boolean) =>
    switchWorkspace(page, next ? 2 : 1, touch);
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
      await applicationMenu(page, 'Sessions', touch);
      await choose(menuItem(page, 'New Session'), touch);
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
    await switchWorkspace(page, 2, touch);
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
    await switchWorkspace(page, 1, touch);
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
    await switchWorkspace(page, 2, touch);
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
