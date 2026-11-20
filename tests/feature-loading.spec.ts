import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import type { APIRequestContext, Locator, Page, Route } from '@playwright/test';
import type { DesktopLayout } from '@cofy-x/axv-nextstep';
import {
  activate,
  expect,
  menuItem,
  persistedLayout,
  windowAction,
  test,
} from './helpers';
import { openCodexManager, test as codexTest } from './codex-helpers';
import { codeEditor, expectEditorText } from './code-editor-helpers';
import { openVMEditorFixture } from './vm-editor-fixture';

interface BundleChunk {
  fileName: string;
  modules: { id: string; renderedLength: number }[];
}

async function bundleChunks(): Promise<BundleChunk[]> {
  const path = process.env.AXV_BUNDLE_REPORT
    ? resolve(process.env.AXV_BUNDLE_REPORT)
    : resolve(
        process.env.AXV_TEST_TMPDIR || '.data/test',
        'dashboard-bundle.json',
      );
  const report = JSON.parse(await readFile(path, 'utf8')) as {
    chunks: BundleChunk[];
  };
  return report.chunks;
}

async function featureAsset(source: string) {
  const chunks = (await bundleChunks()).filter((chunk) =>
    chunk.modules.some(
      (module) => module.id === `apps/dashboard/src/features/${source}`,
    ),
  );
  expect(chunks, `${source} must identify one production module.`).toHaveLength(
    1,
  );
  return `/${chunks[0]!.fileName}`;
}

function routeAsset(path: string) {
  return (url: URL) => url.pathname === path;
}

async function choose(control: Locator, touch: boolean) {
  if (touch) await control.tap();
  else await control.click();
}

async function launchTerminal(page: Page, touch: boolean) {
  await choose(
    page.getByRole('button', { name: 'Workspace menu', exact: true }),
    touch,
  );
  await choose(menuItem(page, 'Applications'), touch);
  await choose(menuItem(page, 'Terminal'), touch);
  const window = page.locator('[data-app-id="terminal"]').last();
  await expect(window).toBeVisible();
  return window;
}

function moduleGate() {
  let release = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  let requests = 0;
  const handler = async (route: Route) => {
    requests += 1;
    await gate;
    await route.continue();
  };
  return { handler, release, requests: () => requests };
}

async function restoreLayoutOnNextLoad(page: Page, layout: DesktopLayout) {
  // The current desktop may still save during reload. Supply only the next
  // bootstrap layout so its keepalive write cannot overwrite this fixture.
  await page.route(
    (url) => url.pathname === '/api/bootstrap',
    async (route) => {
      const response = await route.fetch();
      expect(response.ok()).toBeTruthy();
      await route.fulfill({
        response,
        json: { ...(await response.json()), layout },
      });
    },
    { times: 1 },
  );
}

async function restoreProjectPreview(
  page: Page,
  request: APIRequestContext,
  kind: 'host' | 'vm',
) {
  const project = {
    id: 'loading-focus-project',
    hostId: 'loading-focus-host',
    kind,
    name: 'Focus project',
    rootPath: '/fixture/focus-project',
    volumeId: 'loading-focus-volume',
    root: 'repo',
    rootIdentity: 'loading-focus-root',
    revision: 1,
    createdAt: '2026-10-21T00:00:00Z',
  };
  const binding = {
    hostId: project.hostId,
    projectId: project.id,
    projectRevision: 1,
    volumeId: project.volumeId,
    volumeRevision: 1,
    machineId: 'loading-focus-vm',
    bootId: 'loading-focus-boot',
    instanceId: 'loading-focus-instance',
  };
  const content = 'print("focus")\n';
  const entry = {
    name: 'app.py',
    path: 'app.py',
    kind: 'file',
    size: content.length,
    modifiedAt: project.createdAt,
  };
  const prefix =
    kind === 'host'
      ? `/api/projects/${project.id}`
      : `/api/compute/projects/${project.id}`;
  await page.route(`**${prefix}/**`, async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (kind === 'vm') {
      expect(route.request().headers()['x-axv-host-id']).toBe(project.hostId);
      if (path.endsWith('/binding')) {
        await route.fulfill({ json: { project, binding } });
        return;
      }
      expect(route.request().postDataJSON().binding).toEqual(binding);
    }
    if (path.endsWith('/files') || path.endsWith('/list')) {
      await route.fulfill({ json: { project, binding, entries: [entry] } });
      return;
    }
    expect(path.endsWith('/file') || path.endsWith('/read')).toBe(true);
    await route.fulfill({
      json: {
        project,
        binding,
        projectId: project.id,
        ...entry,
        content,
        sha256: 'loading-focus-sha256',
      },
    });
  });
  await expect
    .poll(async () => !!(await persistedLayout(page, request)))
    .toBeTruthy();
  const layout = await persistedLayout(page, request);
  const id =
    kind === 'host'
      ? `project-files:${project.id}`
      : `project-files:vm:${project.hostId}:${project.id}`;
  layout.windows.push({
    ...layout.windows[0],
    id,
    appId: 'project-files',
    title: 'Focus project',
    params:
      kind === 'host'
        ? { projectId: project.id, file: entry.path }
        : { hostId: project.hostId, vmProjectId: project.id },
    viewState: { file: entry.path, reading: true },
    minimized: false,
    shaded: false,
  });
  layout.activeWindowId = id;
  const workspace = layout.workspaces.find(
    (item: { id: string }) => item.id === layout.currentWorkspaceId,
  );
  workspace.activeWindowId = id;
  workspace.order.push(id);
  await restoreLayoutOnNextLoad(page, layout);
  return { window: page.locator(`[data-window-id="${id}"]`), content };
}

test('closing a loading window cannot start a shell or revive the cancelled window', async ({
  page,
  hasTouch,
}) => {
  test.skip(process.platform === 'win32', 'Host shells require POSIX here.');
  const heavyChunks = (await bundleChunks()).filter((chunk) =>
    chunk.modules.some(
      (module) =>
        module.renderedLength > 0 &&
        (module.id.includes('@xterm/') || module.id.includes('react-markdown')),
    ),
  );
  expect(heavyChunks.length).toBeGreaterThan(0);
  const requested = await page.evaluate(() =>
    performance
      .getEntriesByType('resource')
      .map((entry) => new URL(entry.name).pathname),
  );
  expect(
    heavyChunks
      .map((chunk) => `/${chunk.fileName}`)
      .filter((path) => requested.includes(path)),
    'Unopened terminal and Markdown views must stay outside startup requests.',
  ).toEqual([]);

  const sockets: string[] = [];
  page.on('websocket', (socket) => {
    if (new URL(socket.url()).pathname === '/api/terminal')
      sockets.push(socket.url());
  });
  const asset = routeAsset(await featureAsset('terminal/TerminalApp.tsx'));
  const gate = moduleGate();
  await page.route(asset, gate.handler);
  try {
    const pending = await launchTerminal(page, hasTouch);
    const cancelledId = await pending.getAttribute('data-window-id');
    await expect(pending.getByRole('status')).toHaveText('Loading…');
    await expect.poll(gate.requests).toBe(1);
    expect(sockets).toEqual([]);
    await choose(pending.getByRole('button', { name: /^Close / }), hasTouch);
    await expect(page.locator('[data-app-id="terminal"]')).toHaveCount(0);
    gate.release();

    const fresh = await launchTerminal(page, hasTouch);
    await expect(fresh.getByTestId('terminal-status')).toHaveAttribute(
      'data-state',
      'connected',
    );
    await expect(fresh.getByLabel('Host terminal input')).toBeFocused();
    expect(await fresh.getAttribute('data-window-id')).not.toBe(cancelledId);
    await expect(page.locator('[data-app-id="terminal"]')).toHaveCount(1);
    expect(sockets).toHaveLength(1);
    expect(gate.requests()).toBe(1);
    const welcome = page.locator('[data-app-id="welcome"]');
    await activate(page, welcome, hasTouch);
    await expect(
      welcome.getByRole('button', { name: 'Project Files', exact: true }),
    ).toBeVisible();
  } finally {
    gate.release();
    await page.unroute(asset, gate.handler);
  }
});

for (const focus of ['desktop', 'close', 'window menu'] as const) {
  test(`loading a terminal preserves deliberate ${focus} focus`, async ({
    page,
    hasTouch,
  }) => {
    test.skip(process.platform === 'win32', 'Host shells require POSIX here.');
    const asset = routeAsset(await featureAsset('terminal/TerminalApp.tsx'));
    const gate = moduleGate();
    await page.route(asset, gate.handler);
    try {
      const terminal = await launchTerminal(page, hasTouch);
      await expect(terminal.getByRole('status')).toHaveText('Loading…');
      await expect.poll(gate.requests).toBe(1);
      const control =
        focus === 'desktop'
          ? page.getByRole('button', { name: 'Workspace menu', exact: true })
          : terminal.getByRole('button', {
              name: focus === 'close' ? /^Close / : /^Window menu for /,
            });
      await control.focus();
      await expect(control).toBeFocused();
      gate.release();
      await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
        'data-state',
        'connected',
      );
      await expect(control).toBeFocused();
      expect(gate.requests()).toBe(1);
    } finally {
      gate.release();
      await page.unroute(asset, gate.handler);
    }
  });
}

for (const kind of ['host', 'vm'] as const) {
  for (const focus of ['desktop', 'close'] as const) {
    test(`loading a ${kind} file preview retains ${focus} focus and explicit file navigation`, async ({
      page,
      request,
      hasTouch,
    }) => {
      await page.setViewportSize({ width: 390, height: 844 });
      const { window, content } = await restoreProjectPreview(
        page,
        request,
        kind,
      );
      const source =
        kind === 'host' ? 'HostProjectFiles.tsx' : 'VMProjectFiles.tsx';
      const asset = routeAsset(await featureAsset(`project-files/${source}`));
      const gate = moduleGate();
      await page.route(asset, gate.handler);
      try {
        await page.reload();
        await expect(window.getByRole('status')).toHaveText('Loading…');
        await expect.poll(gate.requests).toBe(1);
        const desktopControl = page.getByRole('button', {
          name: 'Workspace menu',
          exact: true,
        });
        const control =
          focus === 'desktop'
            ? desktopControl
            : window.getByRole('button', { name: /^Close / });
        await control.focus();
        await expect(control).toBeFocused();
        gate.release();
        const preview = window.getByRole('region', {
          name:
            kind === 'host' ? 'Project file preview' : 'VM project file editor',
          exact: true,
        });
        const editor = codeEditor(
          window,
          kind === 'host' ? 'Project file source' : 'VM project file text',
        );
        await expect(editor).toHaveClass(/cm-content/);
        await expectEditorText(editor, content);
        await expect(control).toBeFocused();

        await desktopControl.focus();
        await expect(desktopControl).toBeFocused();
        await choose(desktopControl, hasTouch);
        await choose(menuItem(page, 'Clock'), hasTouch);
        const dialog = page.getByRole('dialog', { name: 'Clock', exact: true });
        await choose(
          dialog.getByRole('button', { name: 'OK', exact: true }),
          hasTouch,
        );
        await expect(desktopControl).toBeFocused();

        await choose(
          window.getByRole('button', { name: 'Back', exact: true }),
          hasTouch,
        );
        const directory = window.getByRole('navigation', {
          name: kind === 'host' ? 'Project directory' : 'VM project directory',
          exact: true,
        });
        const file = directory.getByRole('button', {
          name: 'app.py',
          exact: true,
        });
        await expect(file).toBeFocused();
        await choose(file, hasTouch);
        await expect(preview).toBeFocused();

        let expected = content;
        if (kind === 'vm') {
          await choose(
            window.getByRole('button', { name: 'Edit', exact: true }),
            hasTouch,
          );
          await expect(editor).toBeEditable();
          await editor.press('ControlOrMeta+End');
          await page.keyboard.insertText('Protected draft');
          expected += 'Protected draft';
        }
        await expectEditorText(editor, expected);
        await windowAction(page, window, 'Maximize', hasTouch);
        await page.setViewportSize({ width: 1000, height: 844 });
        await expect(directory).toBeVisible();
        const resizeFocus = focus === 'desktop' ? desktopControl : editor;
        await resizeFocus.focus();
        await page.setViewportSize({ width: 390, height: 844 });
        await expect(directory).toBeHidden();
        await expect(resizeFocus).toBeFocused();
        await expectEditorText(editor, expected);
        expect(gate.requests()).toBe(1);
      } finally {
        gate.release();
        await page.unroute(asset, gate.handler);
      }
    });
  }
}

codexTest(
  'loading a Codex terminal preserves desktop focus and its exact run',
  async ({ page, hasTouch }) => {
    const manager = await openCodexManager(page, hasTouch);
    const asset = routeAsset(await featureAsset('codex/CodexTerminal.tsx'));
    const gate = moduleGate();
    await page.route(asset, gate.handler);
    try {
      const created = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === '/api/host/codex/sessions' &&
          response.request().method() === 'POST',
      );
      await choose(
        manager.getByRole('button', { name: 'New Session', exact: true }),
        hasTouch,
      );
      const response = await created;
      expect(response.ok()).toBeTruthy();
      const { session } = (await response.json()) as {
        session: { id: string; runId: string };
      };
      const terminal = page.locator(
        `[data-window-id="codex:${session.id}:${session.runId}"]`,
      );
      await expect(terminal.getByRole('status')).toHaveText('Loading…');
      await expect.poll(gate.requests).toBe(1);
      const desktopControl = page.getByRole('button', {
        name: 'Workspace menu',
        exact: true,
      });
      await desktopControl.focus();
      await expect(desktopControl).toBeFocused();
      gate.release();
      await expect(terminal.getByTestId('codex-status')).toHaveAttribute(
        'data-state',
        'connected',
      );
      await expect(desktopControl).toBeFocused();
      await expect(terminal.getByTestId('codex-session')).toHaveAttribute(
        'data-session-id',
        session.id,
      );
      await expect(terminal.getByTestId('codex-session')).toHaveAttribute(
        'data-run-id',
        session.runId,
      );
      expect(gate.requests()).toBe(1);
    } finally {
      gate.release();
      await page.unroute(asset, gate.handler);
    }
  },
);

test('a restored guest terminal preserves desktop focus and its recorded binding while loading', async ({
  page,
  request,
}) => {
  await expect
    .poll(async () => !!(await persistedLayout(page, request)))
    .toBeTruthy();
  const layout = await persistedLayout(page, request);
  const original = layout.windows[0];
  const params = {
    hostId: 'loading-focus-host',
    machineId: 'loading-focus-vm',
    terminalId: 'loading-focus-terminal',
    bootId: 'loading-focus-boot',
  };
  const id = 'guest-terminal:loading-focus';
  layout.windows.push({
    ...original,
    id,
    appId: 'guest-terminal',
    title: 'Guest loading focus',
    params,
    viewState: {},
    minimized: false,
    shaded: false,
  });
  layout.activeWindowId = id;
  const workspace = layout.workspaces.find(
    (item: { id: string }) => item.id === layout.currentWorkspaceId,
  );
  workspace.activeWindowId = id;
  workspace.order.push(id);
  await restoreLayoutOnNextLoad(page, layout);
  const prefix = `/api/compute/machines/${params.machineId}/guest/terminals/${params.terminalId}`;
  const attached: string[] = [];
  await page.route(
    (url) => url.pathname === prefix,
    async (route) => {
      expect(route.request().method()).toBe('GET');
      expect(route.request().headers()['x-axv-host-id']).toBe(params.hostId);
      await route.fulfill({
        json: {
          id: params.terminalId,
          bootId: params.bootId,
          status: 'running',
        },
      });
    },
  );
  await page.routeWebSocket(
    (url) => url.pathname === `${prefix}/attach`,
    (socket) => {
      const url = new URL(socket.url());
      expect(url.searchParams.get('hostId')).toBe(params.hostId);
      attached.push(url.pathname);
      socket.send(JSON.stringify({ ready: true, bootId: params.bootId }));
    },
  );
  const asset = routeAsset(await featureAsset('machines/GuestTerminalApp.tsx'));
  const gate = moduleGate();
  await page.route(asset, gate.handler);
  try {
    await page.reload();
    const terminal = page.locator(`[data-window-id="${id}"]`);
    await expect(terminal.getByRole('status')).toHaveText('Loading…');
    await expect.poll(gate.requests).toBe(1);
    expect(attached).toEqual([]);
    const desktopControl = page.getByRole('button', {
      name: 'Workspace menu',
      exact: true,
    });
    await desktopControl.focus();
    await expect(desktopControl).toBeFocused();
    gate.release();
    await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
      'data-state',
      'connected',
    );
    await expect(desktopControl).toBeFocused();
    expect(attached).toEqual([`${prefix}/attach`]);
    expect(gate.requests()).toBe(1);
  } finally {
    gate.release();
    await page.unroute(asset, gate.handler);
  }
});

test('a failed module retries without reloading or replacing an existing VM draft', async ({
  page,
  hasTouch,
}) => {
  test.skip(process.platform === 'win32', 'Host shells require POSIX here.');
  const source = 'Saved document baseline';
  const {
    window: editor,
    editor: text,
    saved,
  } = await openVMEditorFixture(page, {
    touch: hasTouch,
    content: source,
  });
  await text.fill('Protected draft');
  await text.press('End');
  await page.keyboard.insertText(' appended');
  const draft = 'Protected draft appended';
  await expectEditorText(text, draft);
  await text.evaluate((element) => {
    element.dataset.mountProbe = 'original-draft-editor';
  });
  const documentOrigin = await page.evaluate(() => performance.timeOrigin);
  const asset = routeAsset(await featureAsset('terminal/TerminalApp.tsx'));
  let attempts = 0;
  const handler = async (route: Route) => {
    attempts += 1;
    if (attempts === 1)
      await route.fulfill({
        status: 503,
        contentType: 'text/javascript',
        headers: { 'Cache-Control': 'no-store' },
        body: 'Temporarily unavailable',
      });
    else await route.continue();
  };
  await page.route(asset, handler);
  try {
    const terminal = await launchTerminal(page, hasTouch);
    await expect(terminal.getByRole('alert')).toContainText(
      'Could not load this window.',
    );
    const retry = terminal.getByRole('button', {
      name: 'Retry',
      exact: true,
    });
    await expect(retry).toBeEnabled();
    if (hasTouch) await retry.tap();
    else {
      await retry.focus();
      await retry.press('Enter');
    }
    await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
      'data-state',
      'connected',
    );
    expect(attempts).toBe(2);
    expect(await page.evaluate(() => performance.timeOrigin)).toBe(
      documentOrigin,
    );

    await activate(page, editor, hasTouch);
    await expect(text).toHaveAttribute(
      'data-mount-probe',
      'original-draft-editor',
    );
    await expectEditorText(text, draft);
    await text.press('ControlOrMeta+z');

    await text.press('ControlOrMeta+Shift+z');
    await expectEditorText(text, draft);
    await expect(
      editor.getByRole('status').filter({ hasText: ' · Unsaved changes' }),
    ).toBeVisible();
    expect(saved()).toBe(source);
  } finally {
    await page.unroute(asset, handler);
  }
});

test('persistent module failures retain recovery guidance and let other windows save their work', async ({
  page,
  hasTouch,
}, testInfo) => {
  const {
    window: editor,
    editor: text,
    saved,
  } = await openVMEditorFixture(page, {
    touch: hasTouch,
  });
  const draft = 'Keep this draft until I explicitly save.';
  await text.fill(draft);
  await text.evaluate((element) => {
    element.dataset.mountProbe = 'recovery-editor';
  });
  const documentOrigin = await page.evaluate(() => performance.timeOrigin);
  const sockets: string[] = [];
  page.on('websocket', (socket) => {
    if (new URL(socket.url()).pathname === '/api/terminal')
      sockets.push(socket.url());
  });
  const asset = routeAsset(await featureAsset('terminal/TerminalApp.tsx'));
  let attempts = 0;
  const handler = async (route: Route) => {
    attempts += 1;
    await route.fulfill({
      status: 503,
      contentType: 'text/javascript',
      headers: { 'Cache-Control': 'no-store' },
      body: 'Temporarily unavailable',
    });
  };
  await page.route(asset, handler);
  try {
    const terminal = await launchTerminal(page, hasTouch);
    const error = terminal.getByRole('alert');
    await expect(error).toContainText('Could not load this window.');
    expect(attempts).toBe(1);
    const retry = terminal.getByRole('button', { name: 'Retry', exact: true });
    await expect(retry).toBeEnabled();
    await choose(retry, hasTouch);
    await expect.poll(() => attempts).toBe(2);
    await expect(error).toContainText('Could not load this window.');
    await expect(
      terminal.getByText(
        'Save, download or copy unsaved work before reloading.',
        { exact: true },
      ),
    ).toBeVisible();
    await choose(
      terminal.locator('summary').filter({ hasText: /^Details$/ }),
      hasTouch,
    );
    await expect(
      terminal.getByText(
        'Save, download or copy unsaved work before reloading.',
        { exact: true },
      ),
    ).toBeVisible();
    expect(
      await error.evaluate(
        (element) => element.scrollWidth - element.clientWidth,
      ),
      'Load diagnostics and recovery actions must fit the window.',
    ).toBeLessThanOrEqual(1);
    await page.screenshot({
      path: testInfo.outputPath('feature-load-failure.png'),
    });

    await activate(page, editor, hasTouch);
    await expect(text).toHaveAttribute('data-mount-probe', 'recovery-editor');
    await expectEditorText(text, draft);
    await choose(
      editor.getByRole('button', { name: 'Save', exact: true }),
      hasTouch,
    );
    await expect(
      editor.getByText('Saved in VM.', { exact: true }),
    ).toBeVisible();
    expect(saved()).toBe(draft);
    expect(await page.evaluate(() => performance.timeOrigin)).toBe(
      documentOrigin,
    );
    expect(attempts).toBe(2);
    expect(sockets).toEqual([]);
  } finally {
    await page.unroute(asset, handler);
  }
});
