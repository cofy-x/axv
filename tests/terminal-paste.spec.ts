import type {
  Locator,
  Page,
  WebSocket as PlaywrightWebSocket,
} from '@playwright/test';
import { expect, menuItem, test } from './helpers';
import {
  applicationMenu,
  openTerminal,
  run,
  terminalAction,
} from './terminal-helpers';
import {
  choose,
  codexSessionAction,
  session,
  startCodex,
  test as codexTest,
} from './codex-helpers';

interface TerminalTraffic {
  socket: PlaywrightWebSocket;
  input: Buffer[];
}

interface ClipboardHarness {
  calls: number;
  resolve?: (text: string) => void;
}

interface PasteTestWindow extends Window {
  axvPasteClipboard: ClipboardHarness;
  axvPasteSockets: globalThis.WebSocket[];
}

function recordInput(page: Page, path: string) {
  const traffic: TerminalTraffic[] = [];
  page.on('websocket', (socket) => {
    if (!new URL(socket.url()).pathname.startsWith(path)) return;
    const current = { socket, input: [] as Buffer[] };
    traffic.push(current);
    socket.on('framesent', ({ payload }) => {
      if (Buffer.isBuffer(payload)) current.input.push(payload);
    });
  });
  return traffic;
}

function sent(traffic: TerminalTraffic) {
  return Buffer.concat(traffic.input).toString('utf8');
}

function withoutCapabilityReplies(traffic: TerminalTraffic) {
  // Reattaching tmux queries xterm's device attributes and foreground/background
  // colors. Those replies are binary terminal traffic too. Remove only complete
  // reports with these protocol shapes; preserve every other input byte.
  const escape = String.fromCharCode(27);
  const deviceAttributes = new RegExp(`${escape}\\[[?>][\\d;]*c`, 'g');
  const color = '[\\da-fA-F]{4}/[\\da-fA-F]{4}/[\\da-fA-F]{4}';
  const colors = new RegExp(`${escape}\\]1[01];rgb:${color}${escape}\\\\`, 'g');
  return sent(traffic).replace(deviceAttributes, '').replace(colors, '');
}

async function mockClipboard(
  page: Page,
  mode: 'resolved' | 'rejected' | 'missing' | 'delayed',
  text = '',
) {
  // Controlled API responses test AXV's flow, not the device clipboard or its
  // browser permission prompt. Physical Safari acceptance remains separate.
  await page.evaluate(
    ({ mode, text }) => {
      const state: ClipboardHarness = { calls: 0 };
      (window as unknown as PasteTestWindow).axvPasteClipboard = state;
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value:
          mode === 'missing'
            ? undefined
            : {
                readText: () => {
                  state.calls++;
                  if (mode === 'rejected')
                    return Promise.reject(
                      new DOMException('Test read rejected', 'NotAllowedError'),
                    );
                  if (mode === 'delayed')
                    return new Promise<string>((resolve) => {
                      state.resolve = resolve;
                    });
                  return Promise.resolve(text);
                },
              },
      });
    },
    { mode, text },
  );
}

async function resolveClipboard(page: Page, text: string) {
  await page.evaluate(async (text) => {
    (window as unknown as PasteTestWindow).axvPasteClipboard.resolve!(text);
    // Let the awaiting command and its resulting render finish before checking
    // that it did not route input into a replacement connection.
    await new Promise<void>((resolve) =>
      requestAnimationFrame(() => resolve()),
    );
  }, text);
}

async function expectClipboardRead(page: Page) {
  await expect
    .poll(() =>
      page.evaluate(
        () => (window as unknown as PasteTestWindow).axvPasteClipboard.calls,
      ),
    )
    .toBe(1);
}

function pasteDialog(page: Page) {
  return page.getByRole('dialog', { name: 'Paste text', exact: true });
}

async function pressPaste(terminal: Locator, touch: boolean) {
  await choose(
    terminal.getByRole('button', { name: 'Paste', exact: true }),
    touch,
  );
}

async function recordBrowserSockets(page: Page, path: string) {
  await page.evaluate((path) => {
    const sockets: globalThis.WebSocket[] = [];
    (window as unknown as PasteTestWindow).axvPasteSockets = sockets;
    const NativeWebSocket = window.WebSocket;
    window.WebSocket = class extends NativeWebSocket {
      constructor(url: string | URL, protocols?: string | string[]) {
        super(url, protocols);
        if (new URL(String(url)).pathname.startsWith(path)) sockets.push(this);
      }
    };
  }, path);
}

test('the Paste button sends mocked Unicode clipboard text without adding Enter', async ({
  page,
  isMobile,
}) => {
  const traffic = recordInput(page, '/api/terminal');
  const terminal = await openTerminal(page, isMobile);
  const text = '你好 ✓🙂';
  await mockClipboard(page, 'resolved', text);
  await pressPaste(terminal, isMobile);
  await expectClipboardRead(page);
  await expect.poll(() => sent(traffic[0]!)).toBe(text);
  await expect(pasteDialog(page)).toHaveCount(0);
  await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
});

for (const mode of ['missing', 'rejected'] as const) {
  test(`a ${mode} clipboard read opens a native editor and requires explicit Send`, async ({
    page,
    isMobile,
  }, info) => {
    const traffic = recordInput(page, '/api/terminal');
    const terminal = await openTerminal(page, isMobile);
    await mockClipboard(page, mode);
    await pressPaste(terminal, isMobile);
    const dialog = pasteDialog(page);
    await expect(dialog).toBeVisible();
    const text = dialog.getByRole('textbox', {
      name: 'Text to paste',
      exact: true,
    });
    await expect(text).toBeEditable();
    await expect(text).toBeFocused();
    expect(await text.evaluate((element) => element.tagName)).toBe('TEXTAREA');
    if (mode === 'rejected') {
      const path = info.outputPath('paste-text-dialog.png');
      await page.screenshot({ path, fullPage: true });
      await info.attach('paste-text-dialog', {
        path,
        contentType: 'image/png',
      });
    }
    await text.fill('# first line');
    await text.press('End');
    await text.press('Enter');
    await text.pressSequentially('# second line');
    await expect(text).toHaveValue('# first line\n# second line');
    await text.press('Tab');
    await expect(
      dialog.getByRole('button', { name: 'Send', exact: true }),
    ).toBeFocused();
    await page.keyboard.press('Tab');
    await expect(
      dialog.getByRole('button', { name: 'Close', exact: true }),
    ).toBeFocused();
    await page.keyboard.press('Tab');
    await expect(text).toBeFocused();
    expect(sent(traffic[0]!)).toBe('');
    await choose(
      dialog.getByRole('button', { name: 'Close', exact: true }),
      isMobile,
    );
    await expect(dialog).toHaveCount(0);
    await expect(
      terminal.getByRole('button', { name: 'Paste', exact: true }),
    ).toBeFocused();
    expect(sent(traffic[0]!)).toBe('');

    // The fallback is independently reachable, including without first asking
    // Safari to read the clipboard again.
    await terminalAction(page, terminal, 'Paste text', isMobile);
    await expect(dialog).toBeVisible();
    await text.fill('# 你好 ✓🙂\n# second line');
    await choose(
      dialog.getByRole('button', { name: 'Send', exact: true }),
      isMobile,
    );
    await expect(dialog).toContainText('Text queued for the terminal.');
    await expect(
      dialog.getByRole('button', { name: 'Send', exact: true }),
    ).toBeDisabled();
    await expect
      .poll(() => sent(traffic[0]!))
      .toBe('# 你好 ✓🙂\r# second line');
    await choose(
      dialog.getByRole('button', { name: 'Close', exact: true }),
      isMobile,
    );
    await expect(dialog).toHaveCount(0);
    expect(sent(traffic[0]!)).toBe('# 你好 ✓🙂\r# second line');
    expect(
      await page.evaluate(
        () => (window as unknown as PasteTestWindow).axvPasteClipboard.calls,
      ),
    ).toBe(mode === 'missing' ? 0 : 1);
  });
}

test('a delayed clipboard result cannot enter a replacement Host shell', async ({
  page,
  isMobile,
}) => {
  test.skip(process.platform === 'win32', 'Shell exit uses the POSIX fixture.');
  const traffic = recordInput(page, '/api/terminal');
  const terminal = await openTerminal(page, isMobile);
  await mockClipboard(page, 'delayed');
  await pressPaste(terminal, isMobile);
  await expectClipboardRead(page);
  await run(terminal, 'exit');
  await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'ended',
  );
  await choose(
    terminal.getByRole('button', { name: 'New Session', exact: true }),
    isMobile,
  );
  await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  expect(traffic).toHaveLength(2);
  await resolveClipboard(page, 'stale clipboard input');
  expect(sent(traffic[1]!)).toBe('');
  await expect(pasteDialog(page)).toHaveCount(0);
  await mockClipboard(page, 'resolved', '# current shell');
  await pressPaste(terminal, isMobile);
  await expect.poll(() => sent(traffic[1]!)).toBe('# current shell');
});

test('a rejected clipboard paste shows one current error and keeps an explicit retry', async ({
  page,
  isMobile,
}) => {
  const traffic = recordInput(page, '/api/terminal');
  await recordBrowserSockets(page, '/api/terminal');
  const terminal = await openTerminal(page, isMobile);
  const draft = '# retry this paste';
  await mockClipboard(page, 'resolved', draft);
  await page.evaluate(() => {
    const socket = (window as unknown as PasteTestWindow).axvPasteSockets[0]!;
    Object.defineProperty(socket, 'bufferedAmount', {
      configurable: true,
      get: () => 2 * 1024 * 1024,
    });
  });
  await pressPaste(terminal, isMobile);
  const dialog = pasteDialog(page);
  const send = dialog.getByRole('button', { name: 'Send', exact: true });
  const rejection = dialog.getByText(
    'Text was not queued. Check the connection, wait, or try a smaller paste.',
    { exact: true },
  );
  await expect(rejection).toHaveCount(1);
  await choose(send, isMobile);
  await expect(rejection).toHaveCount(1);
  await expect(dialog.getByRole('alert')).toContainText('Text was not queued.');
  await expect(
    dialog.getByRole('textbox', { name: 'Text to paste', exact: true }),
  ).toHaveValue(draft);
  expect(sent(traffic[0]!)).toBe('');
  await page.evaluate(() => {
    const socket = (window as unknown as PasteTestWindow).axvPasteSockets[0]!;
    Reflect.deleteProperty(socket, 'bufferedAmount');
  });
  await choose(send, isMobile);
  await expect(dialog).toContainText('Text queued for the terminal.');
  await expect(rejection).toHaveCount(0);
  await expect(send).toBeDisabled();
  await expect.poll(() => sent(traffic[0]!)).toBe(draft);
  await choose(
    dialog.getByRole('button', { name: 'Close', exact: true }),
    isMobile,
  );
  await expect(dialog).toHaveCount(0);
  expect(sent(traffic[0]!)).toBe(draft);
});

test('Paste text preserves edits after a rejected send and disables Send after disconnection', async ({
  page,
  isMobile,
}, info) => {
  const traffic = recordInput(page, '/api/terminal');
  await recordBrowserSockets(page, '/api/terminal');
  const terminal = await openTerminal(page, isMobile);
  await terminalAction(page, terminal, 'Paste text', isMobile);
  const dialog = pasteDialog(page);
  const text = dialog.getByRole('textbox', {
    name: 'Text to paste',
    exact: true,
  });
  const draft = '# keep 你好 ✓🙂';
  await text.fill(draft);
  await page.evaluate(() => {
    const socket = (window as unknown as PasteTestWindow).axvPasteSockets[0]!;
    // Model browser backpressure without accepting any input on the wire.
    Object.defineProperty(socket, 'bufferedAmount', {
      configurable: true,
      get: () => 2 * 1024 * 1024,
    });
  });
  const send = dialog.getByRole('button', { name: 'Send', exact: true });
  await choose(send, isMobile);
  await expect(dialog.getByRole('alert')).toContainText('Text was not queued.');
  await expect(text).toHaveValue(draft);
  await expect(text).toBeEditable();
  await expect(send).toBeEnabled();
  expect(sent(traffic[0]!)).toBe('');
  const path = info.outputPath('paste-text-send-error.png');
  await page.screenshot({ path, fullPage: true });
  await info.attach('paste-text-send-error', {
    path,
    contentType: 'image/png',
  });

  await page.evaluate(() => {
    const socket = (window as unknown as PasteTestWindow).axvPasteSockets[0]!;
    Reflect.deleteProperty(socket, 'bufferedAmount');
    socket.close();
  });
  await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    /^(ended|error)$/,
  );
  await expect(dialog.getByRole('alert')).toContainText(
    'The terminal connection changed.',
  );
  await expect(text).toHaveValue(draft);
  await expect(text).toBeEditable();
  await expect(send).toBeDisabled();
  await choose(
    dialog.getByRole('button', { name: 'Close', exact: true }),
    isMobile,
  );
  await expect(dialog).toHaveCount(0);
  await expect(
    terminal.getByRole('button', { name: 'New Session', exact: true }),
  ).toBeEnabled();
  if (
    (await terminal
      .getByTestId('terminal-status')
      .getAttribute('data-state')) === 'error'
  )
    await expect(terminal.locator('.terminal-message')).toContainText(
      'Choose New Session',
    );
  await expect(terminal.locator('.terminal-message')).toContainText(
    'Text was not queued.',
  );
  expect(sent(traffic[0]!)).toBe('');
});

test('a fixed Edit menu pastes into its owner while another terminal stays active', async ({
  page,
  isMobile,
}) => {
  test.skip(isMobile, 'Fixed menu placement is covered at desktop dimensions.');
  const traffic = recordInput(page, '/api/terminal');
  const first = await openTerminal(page);
  const firstID = await first.getAttribute('data-window-id');
  await applicationMenu(page, 'Edit');
  await page
    .getByRole('button', { name: 'Pin Edit menu', exact: true })
    .click();
  const pinned = page.locator(
    `.ns-pinned-menu[data-owner-window-id="${firstID}"]`,
  );
  await applicationMenu(page, 'File');
  await menuItem(page, 'New Terminal Window').click();
  const second = page.locator('[data-app-id="terminal"]').last();
  await expect(second.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  await mockClipboard(page, 'resolved', '# owner clipboard');
  await pinned.getByRole('menuitem', { name: 'Paste', exact: true }).click();
  await expect.poll(() => sent(traffic[0]!)).toBe('# owner clipboard');
  expect(sent(traffic[1]!)).toBe('');
  await expect(second).toHaveClass(/is-active/);
  await expect(first.getByLabel('Host terminal input')).not.toBeFocused();

  await pinned
    .getByRole('menuitem', { name: 'Paste text', exact: true })
    .click();
  const dialog = pasteDialog(page);
  await expect(dialog.locator('strong')).toHaveText(
    (await first.getAttribute('aria-label'))!,
  );
  await dialog
    .getByRole('textbox', { name: 'Text to paste', exact: true })
    .fill(' ✓🙂');
  await dialog.getByRole('button', { name: 'Send', exact: true }).click();
  await expect.poll(() => sent(traffic[0]!)).toBe('# owner clipboard ✓🙂');
  expect(sent(traffic[1]!)).toBe('');
  await dialog.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(second).toHaveClass(/is-active/);
  await expect(first.getByLabel('Host terminal input')).not.toBeFocused();
});

codexTest(
  'Paste text identifies same-named Codex runs and sends only to its selected target',
  async ({ page, isMobile }) => {
    const traffic = recordInput(page, '/api/host/codex/');
    const first = await startCodex(page, isMobile);
    const firstTitle = await first.window.getAttribute('aria-label');
    for (const index of [0, 1]) {
      const target = index === 0 ? first : await startCodex(page, isMobile);
      expect(await target.window.getAttribute('aria-label')).toBe(firstTitle);
      await codexSessionAction(page, target.window, 'Paste text', isMobile);
      const dialog = pasteDialog(page);
      await expect(dialog.locator('strong')).toHaveText(
        `${firstTitle} · Run ${target.runId.slice(0, 8)}`,
      );
      const text = `# paste target ${index + 1}`;
      await dialog
        .getByRole('textbox', { name: 'Text to paste', exact: true })
        .fill(text);
      await choose(
        dialog.getByRole('button', { name: 'Send', exact: true }),
        isMobile,
      );
      await expect.poll(() => sent(traffic[index]!)).toContain(text);
      if (index === 1) expect(sent(traffic[0]!)).not.toContain(text);
      await choose(
        dialog.getByRole('button', { name: 'Close', exact: true }),
        isMobile,
      );
    }
  },
);

codexTest(
  'a delayed clipboard read cannot enter a reconnected attachment of the same Codex run',
  async ({ page, request, isMobile }) => {
    const traffic = recordInput(page, '/api/host/codex/');
    await recordBrowserSockets(page, '/api/host/codex/');
    const current = await startCodex(page, isMobile);
    await mockClipboard(page, 'delayed');
    await codexSessionAction(page, current.window, 'Paste', isMobile);
    await expectClipboardRead(page);
    await page.evaluate(() => {
      (window as unknown as PasteTestWindow).axvPasteSockets[0]!.close();
    });
    await expect.poll(() => traffic.length).toBe(2);
    await expect(current.window.getByTestId('codex-status')).toHaveAttribute(
      'data-state',
      'connected',
    );
    expect((await session(request, current.id)).runId).toBe(current.runId);
    const staleText = 'stale before reconnect';
    await resolveClipboard(page, staleText);
    expect(sent(traffic[1]!)).not.toContain(staleText);
    expect(withoutCapabilityReplies(traffic[1]!)).toBe('');
    await expect(pasteDialog(page)).toHaveCount(0);
    await mockClipboard(page, 'resolved', 'echo 你好 ✓🙂');
    await codexSessionAction(page, current.window, 'Paste', isMobile);
    // tmux enables bracketed paste on this attachment. Preserve its exact
    // wrappers while ensuring AXV does not append an Enter key.
    await expect
      .poll(() => withoutCapabilityReplies(traffic[1]!))
      .toBe('\u001b[200~echo 你好 ✓🙂\u001b[201~');
    expect(sent(traffic[1]!)).not.toContain(staleText);
  },
);
