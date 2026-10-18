import type { WebSocket } from '@playwright/test';
import {
  activate,
  expect,
  menuItem,
  switchWorkspace,
  test,
  windowMenu,
} from './helpers';
import {
  applicationMenu,
  encoded,
  marker,
  openTerminal,
  output,
  run,
} from './terminal-helpers';

test('Terminal streams ANSI and control bytes, interrupts jobs, and explicitly starts a fresh shell', async ({
  page,
}) => {
  test.skip(
    process.platform === 'win32',
    'These commands use POSIX shell syntax.',
  );
  const sockets: WebSocket[] = [];
  page.on('websocket', (socket) => sockets.push(socket));
  const terminal = await openTerminal(page);
  const id = await terminal.getAttribute('data-window-id');
  await run(terminal, `printf '\\033[32m${encoded('pty-ansi-ok')}\\033[0m\\n'`);
  await expect(output(terminal)).toContainText('pty-ansi-ok');
  const more = terminal.getByRole('button', {
    name: 'More terminal actions',
    exact: true,
  });
  await more.focus();
  await more.press('Enter');
  await expect(menuItem(page, 'Clear')).toBeVisible();
  await expect(terminal.getByLabel('Host terminal input')).not.toBeFocused();
  await page.keyboard.press('Escape');
  await expect(page.locator('.ns-menu-layer')).toHaveCount(0);
  await expect(more).toBeFocused();
  await more.press('Enter');
  await page.keyboard.press('Home');
  await expect(menuItem(page, 'Clear')).toHaveClass(/is-selected/);
  await page.keyboard.press('Enter');
  await expect(output(terminal)).not.toContainText('pty-ansi-ok');
  expect(sockets).toHaveLength(1);
  await marker(terminal, 'same-shell-after-clear');

  const input = terminal.getByLabel('Host terminal input');
  await run(
    terminal,
    `stty raw -echo; printf '${encoded('raw-ready')}\\n'; dd bs=1 count=5 2>/dev/null | od -An -tu1; stty sane`,
  );
  await expect(output(terminal)).toContainText('raw-ready');
  for (const key of ['a', 'n', 's', 'w', 'l'])
    await input.press(`Control+${key}`);
  await expect(output(terminal)).toContainText(/1\s+14\s+19\s+23\s+12/);
  await expect(page.locator('[data-app-id="terminal"]')).toHaveCount(1);
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await run(terminal, `printf '${encoded('cat-ready')}\\n'; cat`);
  await expect(output(terminal)).toContainText('cat-ready');
  await input.press('Control+c');
  await marker(terminal, 'interrupted-shell-alive');
  await run(terminal, 'exit');
  await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'ended',
  );
  await expect.poll(() => sockets[0]!.isClosed()).toBe(true);
  expect(sockets).toHaveLength(1);
  await terminal
    .getByRole('button', { name: 'New Session', exact: true })
    .click();
  await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  expect(await terminal.getAttribute('data-window-id')).toBe(id);
  expect(sockets).toHaveLength(2);
  await marker(terminal, 'fresh-shell');
});

test('Terminal windows and pinned menus keep independent shell ownership and close guards', async ({
  page,
}) => {
  test.skip(
    process.platform === 'win32',
    'These commands use POSIX shell syntax.',
  );
  const sockets: WebSocket[] = [];
  page.on('websocket', (socket) => sockets.push(socket));
  const first = await openTerminal(page);
  const firstID = await first.getAttribute('data-window-id');
  await run(first, 'export NEXTSTEP_E2E_SESSION=first');
  await marker(first, 'first-shell-output');
  await applicationMenu(page, 'Edit');
  await page
    .getByRole('button', { name: 'Pin Edit menu', exact: true })
    .click();
  const pinned = page.locator(
    `.ns-pinned-menu[data-owner-window-id="${firstID}"]`,
  );
  await applicationMenu(page, 'File');
  await menuItem(page, 'New Terminal Window').click();
  const secondID = await page
    .locator('[data-app-id="terminal"]')
    .last()
    .getAttribute('data-window-id');
  const second = page.locator(`[data-window-id="${secondID}"]`);
  await expect(second.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  expect(await second.getAttribute('data-window-id')).not.toBe(firstID);
  await run(
    second,
    'printf "session=%s\\n" "${NEXTSTEP_E2E_SESSION:-independent}"',
  );
  await expect(output(second)).toContainText('session=independent');
  await pinned.getByRole('menuitem', { name: 'Clear', exact: true }).click();
  await expect(output(first)).not.toContainText('first-shell-output');
  await expect(output(second)).toContainText('session=independent');
  await second.getByRole('button', { name: /^Close Terminal/ }).click();
  const dialog = page.getByRole('dialog', { name: 'End host shell?' });
  await expect(
    dialog.getByRole('button', { name: 'Cancel', exact: true }),
  ).toBeFocused();
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  expect(sockets[1]!.isClosed()).toBe(false);
  await marker(second, 'cancel-preserves-shell');
  await second.getByRole('button', { name: /^Close Terminal/ }).click();
  await dialog
    .getByRole('button', { name: 'End Session', exact: true })
    .click();
  await expect(second).toHaveCount(0);
  await expect.poll(() => sockets[1]!.isClosed()).toBe(true);
  expect(sockets[0]!.isClosed()).toBe(false);
  await activate(page, first);
  await run(first, 'printf "session=%s\\n" "$NEXTSTEP_E2E_SESSION"');
  await expect(output(first)).toContainText('session=first');
  await first.getByRole('button', { name: /^Close Terminal/ }).click();
  await dialog
    .getByRole('button', { name: 'End Session', exact: true })
    .click();
  await expect(pinned).toHaveCount(0);
});

test('Terminal remains mounted through shade, miniature, workspace and sticky transitions', async ({
  page,
}) => {
  test.skip(
    process.platform === 'win32',
    'These commands use POSIX shell syntax.',
  );
  const sockets: WebSocket[] = [];
  page.on('websocket', (socket) => sockets.push(socket));
  const terminal = await openTerminal(page);
  const input = terminal.getByLabel('Host terminal input');
  await terminal
    .locator('.terminal-emulator')
    .evaluate((element) =>
      element.setAttribute('data-mount-probe', 'retained'),
    );
  await run(terminal, 'export NEXTSTEP_E2E_SESSION=persistent');
  await marker(terminal, 'before-hide');
  await terminal.getByRole('button', { name: /^Shade / }).click();
  await expect(input).not.toBeFocused();
  await terminal.getByRole('button', { name: /^Unshade / }).click();
  await terminal.getByRole('button', { name: /^Miniaturize / }).click();
  await expect(terminal).toBeHidden();
  await activate(page, terminal);
  await switchWorkspace(page, 2);
  await expect(terminal).toBeHidden();
  await expect(input).not.toBeFocused();
  await switchWorkspace(page, 1);
  await expect(terminal).toBeVisible();
  await windowMenu(page, terminal);
  await menuItem(page, 'All Workspaces').click();
  await switchWorkspace(page, 2);
  await expect(terminal).toBeVisible();
  await run(terminal, 'printf "session=%s\\n" "$NEXTSTEP_E2E_SESSION"');
  await expect(output(terminal)).toContainText('session=persistent');
  await expect(terminal.locator('.terminal-emulator')).toHaveAttribute(
    'data-mount-probe',
    'retained',
  );
  expect(sockets).toHaveLength(1);
  expect(sockets[0]!.isClosed()).toBe(false);
});

test('Terminal resizes the real PTY and fits a narrow window', async ({
  page,
}) => {
  test.skip(
    process.platform === 'win32',
    'PTY geometry is read through POSIX stty.',
  );
  const terminal = await openTerminal(page);
  const dimensions = terminal.locator('.ns-status span').last();
  const initial = await dimensions.innerText();
  await terminal.getByRole('button', { name: /^Maximize / }).click();
  await expect(dimensions).not.toHaveText(initial);
  const [cols, rows] = (await dimensions.innerText()).split(' × ');
  await run(terminal, 'stty size');
  await expect(output(terminal)).toContainText(`${rows} ${cols}`);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect
    .poll(async () => (await terminal.boundingBox())!.width)
    .toBeLessThanOrEqual(390);
  await expect(dimensions).not.toHaveText(`${cols} × ${rows}`);
  const [narrowCols, narrowRows] = (await dimensions.innerText()).split(' × ');
  await run(terminal, 'stty size');
  await expect(output(terminal)).toContainText(`${narrowRows} ${narrowCols}`);
  await marker(terminal, 'narrow-terminal');
  await expect
    .poll(async () => {
      const host = (await terminal
        .locator('.terminal-emulator')
        .boundingBox())!;
      const cells = (await terminal.locator('.xterm-screen').boundingBox())!;
      return Math.max(cells.width - host.width, cells.height - host.height);
    })
    .toBeLessThanOrEqual(1);
  await expect(
    terminal.getByRole('button', { name: /^Close / }),
  ).toBeInViewport();
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth),
  ).toBeLessThanOrEqual(390);
});

test('Terminal yields desktop navigation and blocks shell input behind a modal', async ({
  page,
}) => {
  test.skip(
    process.platform === 'win32',
    'These commands use POSIX shell syntax.',
  );
  const frames: (string | Buffer)[] = [];
  page.on('websocket', (socket) =>
    socket.on('framesent', (frame) => frames.push(frame.payload)),
  );
  const terminal = await openTerminal(page);
  const input = terminal.getByLabel('Host terminal input');
  await input.focus();
  await input.press('F10');
  await expect(page.getByRole('menu')).toBeVisible();
  await expect(input).not.toBeFocused();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('menu')).toHaveCount(0);
  await input.focus();
  await input.press('Control+F6');
  await expect(terminal).not.toHaveClass(/is-active/);
  await expect(page.locator('.ns-window.is-active')).toHaveCount(1);
  await activate(page, terminal);
  await input.focus();
  await input.press('Alt+2');
  await expect(terminal).toBeHidden();
  await page.keyboard.press('Alt+1');
  await expect(terminal).toBeVisible();
  await input.focus();
  await input.press('Meta+w');
  const dialog = page.getByRole('dialog', { name: 'End host shell?' });
  await expect(dialog).toBeVisible();
  const before = frames.length;
  await page.keyboard.press('Control+a');
  await page.keyboard.press('Control+c');
  await page.keyboard.press('Alt+2');
  await expect(
    page.getByRole('button', { name: /^Workspace 1:/ }),
  ).toBeVisible();
  expect(frames).toHaveLength(before);
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(input).toBeFocused();
  await page.keyboard.type(`printf '${encoded('modal-preserved-shell')}\\n'`);
  await page.keyboard.press('Enter');
  await expect(output(terminal)).toContainText('modal-preserved-shell');
});

test('Terminal exposes unavailable and failed connections without automatic retry', async ({
  page,
}) => {
  let requests = 0;
  await page.route('**/api/terminal/info', async (route) => {
    requests++;
    await route.fulfill({
      json: { supported: false, reason: 'E2E unsupported host' },
    });
  });
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await menuItem(page, 'Terminal').click();
  const terminal = page.locator('[data-app-id="terminal"]');
  await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'unsupported',
  );
  await expect(terminal.getByRole('alert')).toContainText(
    'E2E unsupported host',
  );
  expect(requests).toBe(1);
  await page.unroute('**/api/terminal/info');
  await page.route('**/api/terminal/info', async (route) => {
    requests++;
    await route.abort('failed');
  });
  await terminal
    .getByRole('button', { name: 'New Session', exact: true })
    .click();
  await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'error',
  );
  expect(requests).toBe(2);
  await page.unroute('**/api/terminal/info');
  await terminal
    .getByRole('button', { name: 'New Session', exact: true })
    .click();
  await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
});

test('Unicode paste and IME composition reach the host as exact UTF-8', async ({
  page,
  browserName,
}) => {
  test.skip(
    process.platform === 'win32',
    'The byte check uses POSIX printf and od.',
  );
  const terminal = await openTerminal(page);
  const input = terminal.getByLabel('Host terminal input');
  await input.focus();
  await input.pressSequentially("printf '%s' '");
  // Use the terminal's clipboard event handler. Playwright's Unicode typing
  // emits standalone insertText events, which xterm screen-reader mode ignores.
  await input.evaluate((element) => {
    const clipboard = new DataTransfer();
    clipboard.setData('text/plain', '你好 ✓🙂');
    const event = new ClipboardEvent('paste', {
      bubbles: true,
      cancelable: true,
    });
    // Firefox strips constructor-supplied clipboard data from synthetic events.
    Object.defineProperty(event, 'clipboardData', { value: clipboard });
    element.dispatchEvent(event);
  });
  await input.pressSequentially("' | od -An -tx1 | tr -d ' \\n'; printf '\\n'");
  await input.press('Enter');
  // The hex marker is generated by the shell, so echoed input cannot pass this.
  await expect(output(terminal)).toContainText('e4bda0e5a5bd20e29c93f09f9982');

  if (browserName === 'chromium') {
    await input.pressSequentially("printf '%s' '");
    // Ask Chromium to generate trusted composition events, then commit the IME
    // text. This exercises xterm's composition handler without a test-only API.
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
    // xterm commits composition on a later browser task. Wait for the PTY echo
    // before the next key, so it cannot interrupt that pending composition.
    await expect(output(terminal)).toContainText('输入🙂');
    await input.pressSequentially(
      "' | od -An -tx1 | tr -d ' \\n'; printf '\\n'",
    );
    await input.press('Enter');
    await expect(output(terminal)).toContainText('e8be93e585a5f09f9982');
  }
});
