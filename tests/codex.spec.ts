import type { Request, WebSocket } from '@playwright/test';
import {
  activate,
  menuItem,
  persistedLayout,
  switchWorkspace,
} from './helpers';
import { applicationMenu } from './terminal-helpers';
import {
  codexOutput,
  codexWindow,
  configPath,
  defaultProjectPath,
  endCodex,
  expect,
  openCodexManager,
  openCodexEnvironment,
  openCodexFolderPicker,
  networkDigest,
  projectPath,
  readState,
  resumeCodex,
  runCodex,
  requestEndCodex,
  selectCodexProject,
  session,
  sessions,
  startCodex,
  startCodexInFolder,
  test,
} from './codex-helpers';

test('New Session uses the default workspace immediately and exposes tmux details with redacted proxy diagnostics', async ({
  page,
  request,
}) => {
  const directoryRequests: string[] = [];
  page.on('request', (request) => {
    if (request.url().includes('/api/host/directories'))
      directoryRequests.push(request.url());
  });
  const manager = await openCodexManager(page);
  await expect(manager.getByTestId('codex-default-project')).toHaveText(
    defaultProjectPath,
  );
  await expect(
    manager.getByLabel('Project directory', { exact: true }),
  ).toHaveCount(0);
  await expect(manager.locator('.codex-environment-notice')).toHaveCount(0);
  await expect(manager.getByTestId('codex-environment-status')).toContainText(
    'CLI ready',
  );
  const environment = await openCodexEnvironment(page);
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(environment).not.toContainText('fixture-password');
  await expect(environment).toContainText(configPath);
  await expect(environment).toContainText('127.0.0.1:17890');
  const environmentId = await environment.getAttribute('data-window-id');
  await activate(
    page,
    page.locator('.ns-window:has([data-testid="codex-manager"])'),
  );
  await applicationMenu(page, 'File');
  await menuItem(page, 'Environment').click();
  await expect(environment).toHaveCount(1);
  await expect(environment).toHaveAttribute('data-window-id', environmentId!);
  await environment.getByRole('button', { name: /^Close / }).click();
  const { window, id } = await startCodex(page);
  expect(directoryRequests).toEqual([]);
  const current = await session(request, id);
  expect(current.projectPath).toBe(defaultProjectPath);
  expect(current.tmuxSession).toMatch(/^axv-codex-/);
  expect(current.tmuxSession.endsWith(current.runId)).toBeTruthy();
  expect(
    current.tmuxSocket.endsWith(`/${process.env.E2E_CODEX_TMUX_SOCKET}`),
  ).toBeTruthy();
  await expect(window.getByTestId('codex-project-name')).toHaveText(
    defaultProjectPath.split('/').filter(Boolean).at(-1)!,
  );
  await expect(window.getByTestId('codex-run-id')).toHaveText(
    `Run ${current.runId.slice(0, 8)}`,
  );
  await runCodex(window, 'network');
  await expect(codexOutput(window)).toContainText(`NETWORK ${networkDigest}`);
  const more = window.getByRole('button', {
    name: 'More Codex actions',
    exact: true,
  });
  await more.focus();
  await more.press('Enter');
  await expect(menuItem(page, 'Details')).toBeVisible();
  await expect(window.getByLabel('Codex terminal input')).not.toBeFocused();
  await page.keyboard.press('Escape');
  await expect(page.locator('.ns-menu-layer')).toHaveCount(0);
  await expect(more).toBeFocused();
  await more.press('Enter');
  await page.keyboard.press('End');
  await page.keyboard.press('ArrowUp');
  await expect(menuItem(page, 'Details')).toHaveClass(/is-selected/);
  await page.keyboard.press('Enter');
  const details = page.getByRole('dialog', { name: 'Codex Session Details' });
  await expect(details).toContainText(current.projectPath);
  await expect(details).toContainText(current.runId);
  await expect(
    details.getByText(current.tmuxSession, { exact: true }),
  ).toBeVisible();
  await expect(details).toContainText(current.attachCommand);
  await expect(details).toContainText(current.tmuxSocket);
  await expect(
    details.getByRole('button', { name: 'Copy Command', exact: true }),
  ).toBeVisible();
  await expect(window.getByLabel('Codex terminal input')).not.toBeFocused();
  await details.getByRole('button', { name: 'Close', exact: true }).click();
  await endCodex(page, window);
});

test('Codex selects a real project and preserves the same process through close, reopen and reload', async ({
  page,
  request,
}) => {
  const { window, id, runId } = await startCodexInFolder(page);
  expect((await session(request, id)).projectPath).toBe(projectPath);
  await runCodex(window, 'state retained-project-state');
  const original = await readState(window, 'before-close');
  expect(original.value).toBe('retained-project-state');
  await window.getByRole('button', { name: /^Close / }).click();
  await expect(window).toHaveCount(0);
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect
    .poll(async () => (await session(request, id)).connections)
    .toBe(0);
  expect((await session(request, id)).status).toBe('running');
  const manager = await openCodexManager(page);
  await manager
    .locator(`[data-session-id="${id}"]`)
    .getByRole('button', { name: 'Open', exact: true })
    .click();
  const resumed = codexWindow(page, id, runId);
  await expect(resumed.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  expect(await readState(resumed, 'after-close')).toEqual(original);
  await expect
    .poll(async () => JSON.stringify(await persistedLayout(page, request)))
    .toContain(id);
  await page.reload();
  await expect(resumed.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  expect(await readState(resumed, 'after-reload')).toEqual(original);
  await endCodex(page, resumed);
  await expect
    .poll(async () => (await session(request, id)).status)
    .toBe('ended');
  await expect(resumed.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'ended',
  );
});

test('Codex keeps separate sessions and lets multiple clients share one without taking it over', async ({
  page,
  request,
  browser,
}) => {
  const first = await startCodex(page);
  await runCodex(first.window, 'state first-project');
  const original = await readState(first.window, 'first');
  const second = await startCodex(page);
  const independent = await readState(second.window, 'second');
  expect(independent.pid).not.toBe(original.pid);
  expect(independent.value).toBe('initial');
  const context = await browser.newContext();
  try {
    const peer = await context.newPage();
    await peer.goto(process.env.E2E_BASE_URL!);
    const manager = await openCodexManager(peer);
    await manager
      .locator(`[data-session-id="${first.id}"]`)
      .getByRole('button', { name: 'Open', exact: true })
      .click();
    const peerWindow = codexWindow(peer, first.id, first.runId);
    await expect(peerWindow.getByTestId('codex-status')).toHaveAttribute(
      'data-state',
      'connected',
    );
    expect(await readState(peerWindow, 'peer')).toEqual(original);
    await expect
      .poll(async () => (await session(request, first.id)).connections)
      .toBe(2);
    await endCodex(page, second.window);
    await resumeCodex(page, first.id, first.runId);
    expect(await readState(first.window, 'unaffected')).toEqual(original);
    await endCodex(peer, peerWindow);
    await expect(first.window.getByTestId('codex-status')).toHaveAttribute(
      'data-state',
      'ended',
    );
  } finally {
    await context.close();
  }
});

test('Codex remains mounted while hidden and yields input to desktop navigation and end confirmation', async ({
  page,
}) => {
  const sockets: WebSocket[] = [];
  const frames: (string | Buffer)[] = [];
  page.on('websocket', (socket) => {
    if (!socket.url().includes('/api/host/codex/')) return;
    sockets.push(socket);
    socket.on('framesent', (frame) => frames.push(frame.payload));
  });
  const { window } = await startCodex(page);
  const input = window.getByLabel('Codex terminal input');
  await runCodex(window, 'state mounted');
  const before = await readState(window, 'before-hide');
  await window.getByRole('button', { name: /^Shade / }).click();
  await expect(input).not.toBeFocused();
  await window.getByRole('button', { name: /^Unshade / }).click();
  await window.getByRole('button', { name: /^Miniaturize / }).click();
  await expect(window).toBeHidden();
  await activate(page, window);
  await switchWorkspace(page, 2);
  await expect(window).toBeHidden();
  await expect(input).not.toBeFocused();
  await switchWorkspace(page, 1);
  expect(await readState(window, 'after-hide')).toEqual(before);
  expect(sockets).toHaveLength(1);
  await input.press('F10');
  await expect(page.getByRole('menu')).toBeVisible();
  await expect(input).not.toBeFocused();
  await page.keyboard.press('Escape');
  const dialog = await requestEndCodex(page, window);
  const frameCount = frames.length;
  await page.keyboard.press('Control+a');
  await page.keyboard.press('Alt+2');
  expect(frames).toHaveLength(frameCount);
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  expect(await readState(window, 'after-cancel')).toEqual(before);
});

test('Codex transmits Unicode paste and IME composition as exact UTF-8', async ({
  page,
  browserName,
}) => {
  const { window } = await startCodex(page);
  const input = window.getByLabel('Codex terminal input');
  await expect(input).toBeEditable();
  await input.focus();
  await input.pressSequentially('echo ');
  await input.evaluate((element) => {
    const clipboard = new DataTransfer();
    clipboard.setData('text/plain', '你好 ✓🙂');
    const event = new ClipboardEvent('paste', {
      bubbles: true,
      cancelable: true,
    });
    Object.defineProperty(event, 'clipboardData', { value: clipboard });
    element.dispatchEvent(event);
  });
  await input.press('Enter');
  await expect(codexOutput(window)).toContainText(
    'HEX e4bda0e5a5bd20e29c93f09f9982',
  );
  if (browserName === 'chromium') {
    await input.pressSequentially('echo ');
    const protocol = await page.context().newCDPSession(page);
    try {
      await protocol.send('Input.imeSetComposition', {
        text: '输入🙂',
        selectionStart: 4,
        selectionEnd: 4,
      });
      await protocol.send('Input.insertText', { text: '输入🙂' });
    } finally {
      await protocol.detach();
    }
    await expect(codexOutput(window)).toContainText('输入🙂');
    await input.press('Enter');
    await expect(codexOutput(window)).toContainText('HEX e8be93e585a5f09f9982');
  }
});

test('a fixed Codex Session menu ends its original session while another window is active', async ({
  page,
  request,
}) => {
  const first = await startCodex(page);
  const firstWindowId = await first.window.getAttribute('data-window-id');
  await applicationMenu(page, 'Session');
  await page
    .getByRole('button', { name: 'Pin Session menu', exact: true })
    .click();
  const pinned = page.locator(
    `.ns-pinned-menu[data-owner-window-id="${firstWindowId}"]`,
  );
  const second = await startCodex(page);
  const alive = await readState(second.window, 'other-window');
  await pinned
    .getByRole('menuitem', { name: 'End Session', exact: true })
    .click();
  const dialog = page.getByRole('dialog', { name: 'End Codex session?' });
  await dialog
    .getByRole('button', { name: 'End Session', exact: true })
    .click();
  await expect
    .poll(async () => (await session(request, first.id)).status)
    .toBe('ended');
  expect((await session(request, second.id)).status).toBe('running');
  await resumeCodex(page, second.id, second.runId);
  expect(await readState(second.window, 'still-running')).toEqual(alive);
  await resumeCodex(page, first.id, first.runId);
  await first.window.getByRole('button', { name: /^Close / }).click();
  await expect(pinned).toHaveCount(0);
});

test('Codex wheel scrolling moves tmux history without sending arrow keys to its composer', async ({
  page,
}) => {
  const binary: (string | Buffer)[] = [];
  page.on('websocket', (socket) => {
    if (socket.url().includes('/api/host/codex/'))
      socket.on('framesent', (frame) => {
        if (Buffer.isBuffer(frame.payload)) binary.push(frame.payload);
      });
  });
  const { window } = await startCodex(page);
  await runCodex(window, 'lines');
  await expect(codexOutput(window)).toContainText('HISTORY 099');
  await expect(codexOutput(window)).not.toContainText('HISTORY 030');
  const before = binary.length;
  const screen = window.locator('.xterm-screen');
  await screen.hover();
  await page.mouse.wheel(0, -1400);
  await expect(codexOutput(window)).not.toContainText('HISTORY 099');
  await expect(codexOutput(window)).toContainText(/HISTORY 0[0-7]\d/);
  expect(binary.slice(before)).toHaveLength(0);
  // Typing must leave copy mode and reach the CLI; accidental wheel-generated
  // Up keys would have recalled "lines" and corrupt this command.
  expect((await readState(window, 'after-scroll')).value).toBe('initial');
});

test('closing the Codex picker cancels pending directory loading and reopening ignores its stale response', async ({
  page,
}) => {
  const manager = await openCodexFolderPicker(page);
  await expect(
    manager.getByRole('button', { name: 'Go', exact: true }),
  ).toBeEnabled();
  let delayedRequest: Request | undefined;
  let canceled = false;
  let releaseResponse = () => {};
  let responseFinished = () => {};
  const responseHeld = new Promise<void>((resolve) => {
    releaseResponse = resolve;
  });
  const responseReleased = new Promise<void>((resolve) => {
    responseFinished = resolve;
  });
  page.on('requestfailed', (request) => {
    if (request === delayedRequest) canceled = true;
  });
  await page.route('**/api/host/directories?*', async (route) => {
    if (
      new URL(route.request().url()).searchParams.get('path') !== projectPath ||
      delayedRequest
    ) {
      await route.continue();
      return;
    }
    delayedRequest = route.request();
    const response = await route.fetch();
    await responseHeld;
    try {
      await route.fulfill({ response });
    } finally {
      responseFinished();
    }
  });
  try {
    await manager
      .getByLabel('Project directory', { exact: true })
      .fill(projectPath);
    await manager.getByRole('button', { name: 'Go', exact: true }).click();
    await expect.poll(() => !!delayedRequest).toBe(true);
    await expect(
      manager.getByLabel('Project directory', { exact: true }),
    ).toBeDisabled();
    for (const name of ['Go', 'Home', 'Up', 'Start Here'])
      await expect(
        manager.getByRole('button', { name, exact: true }),
      ).toBeDisabled();
    await expect(manager.getByLabel('Host folders')).toHaveAttribute(
      'aria-busy',
      'true',
    );
    await manager.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(
      manager.getByLabel('Project directory', { exact: true }),
    ).toHaveCount(0);
    const reopened = await openCodexFolderPicker(page);
    await expect(
      reopened.getByRole('button', { name: 'Go', exact: true }),
    ).toBeEnabled();
    const selected = `${projectPath}/nested`;
    await reopened
      .getByLabel('Project directory', { exact: true })
      .fill(selected);
    await reopened.getByRole('button', { name: 'Go', exact: true }).click();
    await expect(
      reopened.getByText('No subfolders. You can use this directory.'),
    ).toBeVisible();
    releaseResponse();
    await responseReleased;
    await expect.poll(() => canceled).toBe(true);
    await expect(
      reopened.getByLabel('Project directory', { exact: true }),
    ).toHaveValue(selected);
    await expect(reopened.locator('.codex-project-choice code')).toHaveText(
      selected,
    );
    await expect(
      reopened.getByRole('button', {
        name: 'Start Here',
        exact: true,
      }),
    ).toBeEnabled();
    await expect(
      reopened.getByRole('button', { name: 'Open folder nested', exact: true }),
    ).toHaveCount(0);
    await expect(reopened.getByRole('alert')).toHaveCount(0);
  } finally {
    releaseResponse();
  }
});

test('Codex reports a missing dependency and refreshes availability without starting a process', async ({
  page,
  request,
}) => {
  const response = await request.get('/api/host/codex/info');
  const available = (await response.json()) as Record<string, unknown>;
  await page.route('**/api/host/codex/info', (route) =>
    route.fulfill({
      json: {
        ...available,
        ready: false,
        reason: 'Install tmux with brew install tmux, then refresh.',
        tmuxPath: undefined,
        tmuxVersion: undefined,
      },
    }),
  );
  await page.reload();
  await expect(
    page.locator('[data-app-id="welcome"]').getByRole('button', {
      name: /^Codex /,
      exact: false,
    }),
  ).toHaveCount(1);
  const manager = await openCodexManager(page);
  await expect(manager).toContainText('Install tmux');
  await expect(
    manager.getByRole('button', { name: 'New Session', exact: true }),
  ).toBeDisabled();
  expect(
    (await sessions(request)).filter((item) => item.status === 'running'),
  ).toHaveLength(0);
  await page.unroute('**/api/host/codex/info');
  await manager
    .locator('.codex-environment-notice')
    .getByRole('button', { name: 'Environment', exact: true })
    .click();
  const environment = page.locator('[data-app-id="codex-environment"]');
  await environment
    .getByRole('button', { name: 'Check Again', exact: true })
    .click();
  await expect(environment.getByRole('status')).toHaveText('CLI ready');
  await environment.getByRole('button', { name: /^Close / }).click();
  await expect(manager.locator('.codex-environment-notice')).toHaveCount(0);
  await expect(manager.getByTestId('codex-environment-status')).toContainText(
    'CLI ready',
  );
  await expect(
    manager.getByRole('button', { name: 'New Session', exact: true }),
  ).toBeEnabled();
});

test('Codex marks natural exit final and does not create a replacement session', async ({
  page,
  request,
}) => {
  const { window, id } = await startCodex(page);
  const count = (await sessions(request)).length;
  await runCodex(window, 'exit');
  await expect(window.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'ended',
  );
  await expect
    .poll(async () => (await session(request, id)).status)
    .toBe('ended');
  await page.reload();
  await expect(window.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'ended',
  );
  expect((await sessions(request)).length).toBe(count);
});

test('Codex hides all launchers until installed and restores them after reloading', async ({
  page,
  request,
}) => {
  const response = await request.get('/api/host/codex/info');
  const available = (await response.json()) as Record<string, unknown>;
  await page.route('**/api/host/codex/info', (route) =>
    route.fulfill({
      json: {
        ...available,
        installed: false,
        ready: false,
        codexPath: undefined,
        codexVersion: undefined,
        reason: 'Codex is not installed on this host.',
        guidance: [
          'Install the official CLI with npm install -g @openai/codex, then check again.',
        ],
      },
    }),
  );
  await page.reload();
  const welcome = page.locator('[data-app-id="welcome"]');
  const welcomeLauncher = welcome.getByRole('button', {
    name: /^Codex /,
    exact: false,
  });
  await activate(page, welcome);
  await expect(
    welcome.getByRole('button', { name: 'Check Again', exact: true }),
  ).toHaveCount(0);
  await expect(welcomeLauncher).toHaveCount(0);
  await expect(
    page.getByRole('group', { name: 'Desktop icons' }).getByRole('button', {
      name: 'Codex',
      exact: true,
      includeHidden: true,
    }),
  ).toHaveCount(0);
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await expect(menuItem(page, 'Codex')).toHaveCount(0);
  await page.unroute('**/api/host/codex/info');
  await page.reload();
  await activate(page, welcome);
  await expect(welcomeLauncher).toBeVisible();
  await welcomeLauncher.click();
  const manager = page.getByTestId('codex-manager');
  await expect(manager).toBeVisible();
  await expect(manager.getByTestId('codex-environment-status')).toContainText(
    'CLI ready',
  );
  await expect(
    manager.getByRole('button', { name: 'New Session', exact: true }),
  ).toBeEnabled();
  await openCodexManager(page);
  expect(
    (await sessions(request)).filter((item) => item.status === 'running'),
  ).toHaveLength(0);
});

test('retrying a lost Codex creation response opens the original session instead of starting another process', async ({
  page,
  request,
}) => {
  const manager = await selectCodexProject(page);
  const requestIds: string[] = [];
  let firstSessionId = '';
  let firstRunId = '';
  await page.route('**/api/host/codex/sessions', async (route) => {
    if (route.request().method() !== 'POST') {
      await route.continue();
      return;
    }
    const body = route.request().postDataJSON() as { requestId: string };
    requestIds.push(body.requestId);
    if (requestIds.length === 1) {
      const response = await route.fetch();
      expect(response.ok()).toBeTruthy();
      const current = (await response.json()).session;
      firstSessionId = current.id as string;
      firstRunId = current.runId as string;
      await route.abort('failed');
    } else await route.continue();
  });
  await manager
    .getByRole('button', { name: 'Start Here', exact: true })
    .click();
  await expect(manager.getByRole('alert')).toBeVisible();
  await expect(
    manager.getByRole('button', { name: 'Start Here', exact: true }),
  ).toBeEnabled();
  await manager
    .getByRole('button', { name: 'Start Here', exact: true })
    .click();
  await expect.poll(() => requestIds.length).toBe(2);
  expect(requestIds[1]).toBe(requestIds[0]);
  const window = codexWindow(page, firstSessionId, firstRunId);
  await expect(window.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  expect((await readState(window, 'creation-retry')).value).toBe('initial');
  expect(
    (await sessions(request))
      .filter((item) => item.status === 'running')
      .map((item) => item.id),
  ).toEqual([firstSessionId]);
});
