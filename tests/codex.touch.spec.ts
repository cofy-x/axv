import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import {
  codexOutput,
  codexSessionAction,
  codexWindow,
  endCodex,
  expect,
  openCodexManager,
  projectPath,
  readState,
  runCodex,
  session,
  startCodexInFolder,
  test,
} from './codex-helpers';

test('touch Codex supports project selection, history, session organization, project terminals and explicit continuation', async ({
  page,
  request,
  isMobile,
  browserName,
}, info) => {
  test.skip(
    !isMobile,
    'Runs in the explicit Chromium and WebKit touch projects.',
  );
  const screenshotDir = resolve('screenshots', `final-${info.project.name}`);
  await mkdir(screenshotDir, { recursive: true });
  const capture = async (name: string) => {
    const path = resolve(screenshotDir, `390x844-${name}.png`);
    await page.screenshot({ path, fullPage: true });
    await info.attach(name, { path, contentType: 'image/png' });
  };
  const { window, id, runId } = await startCodexInFolder(page, true);
  const input = window.getByLabel('Codex terminal input');
  await window.getByTestId('codex-screen').tap();
  await expect(input).toBeFocused();
  await runCodex(window, 'state touch');
  const original = await readState(window, 't1');
  await runCodex(window, 'lines');
  await expect(codexOutput(window)).toContainText('HISTORY 099');
  const screen = window.locator('.terminal-emulator');
  if (browserName === 'chromium') {
    const box = (await screen.boundingBox())!;
    const protocol = await page.context().newCDPSession(page);
    try {
      await protocol.send('Input.dispatchTouchEvent', {
        type: 'touchStart',
        touchPoints: [{ x: box.x + 40, y: box.y + 25 }],
      });
      for (let index = 1; index <= 5; index++)
        await protocol.send('Input.dispatchTouchEvent', {
          type: 'touchMove',
          touchPoints: [
            { x: box.x + 40, y: box.y + 25 + ((box.height - 50) * index) / 5 },
          ],
        });
      await protocol.send('Input.dispatchTouchEvent', {
        type: 'touchEnd',
        touchPoints: [],
      });
    } finally {
      await protocol.detach();
    }
  } else
    await screen.evaluate((element) => {
      const box = element.getBoundingClientRect();
      const startY = box.top + 25;
      const endY = box.bottom - 25;
      const dispatch = (type: string, y: number, active: boolean) => {
        const event = new Event(type, { bubbles: true, cancelable: true });
        Object.defineProperty(event, 'touches', {
          value: active
            ? [{ identifier: 1, clientX: box.left + 40, clientY: y }]
            : [],
        });
        element.dispatchEvent(event);
      };
      dispatch('touchstart', startY, true);
      dispatch('touchmove', endY, true);
      dispatch('touchend', endY, false);
    });
  await expect(codexOutput(window)).not.toContainText('HISTORY 099');
  await expect(codexOutput(window)).toContainText(/HISTORY 0[0-8]\d/);
  await window.getByRole('button', { name: 'Live', exact: true }).tap();
  await expect(codexOutput(window)).toContainText('HISTORY 099');
  await window.getByRole('button', { name: 'Page Up', exact: true }).tap();
  await expect(codexOutput(window)).not.toContainText('HISTORY 099');
  expect(await readState(window, 't2')).toEqual(original);
  await page.getByRole('button', { name: 'Next workspace', exact: true }).tap();
  await expect(window).toBeHidden();
  await expect(input).not.toBeFocused();
  await page
    .getByRole('button', { name: 'Previous workspace', exact: true })
    .tap();
  expect(await readState(window, 't3')).toEqual(original);
  await window.getByRole('button', { name: /^Close / }).tap();
  await expect(window).toHaveCount(0);
  expect((await session(request, id)).status).toBe('running');
  const manager = await openCodexManager(page, true);
  await manager
    .locator(`[data-session-id="${id}"]`)
    .getByRole('button', { name: 'Open', exact: true })
    .tap();
  const resumed = codexWindow(page, id, runId);
  await expect(resumed.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  expect(await readState(resumed, 't4')).toEqual(original);
  await expect(
    resumed.getByRole('button', { name: 'More Codex actions', exact: true }),
  ).toBeInViewport();
  await capture('touch-codex');
  await codexSessionAction(page, resumed, 'Rename', true);
  const rename = page.getByRole('dialog', { name: 'Rename Codex Session' });
  const name = 'Review project updates and next steps ✓';
  await rename.getByLabel('Session name', { exact: true }).fill(name);
  await expect(
    rename.getByRole('button', { name: 'Rename', exact: true }),
  ).toBeInViewport();
  await capture('touch-codex-rename');
  await rename.getByRole('button', { name: 'Rename', exact: true }).tap();
  await expect(resumed).toHaveAttribute('aria-label', `Codex · ${name}`);
  await openCodexManager(page, true);
  const row = manager.locator(`[data-session-id="${id}"]`);
  const rowAction = async (label: string) => {
    await row
      .getByRole('button', {
        name: `More session actions for ${name}, run ${runId.slice(0, 8)}`,
        exact: true,
      })
      .tap();
    const menu = page.getByRole('menu', {
      name: `Run ${runId.slice(0, 8)} · ${name}`,
      exact: true,
    });
    await expect(menu).toBeVisible();
    await menu.getByRole('menuitem', { name: label, exact: true }).tap();
  };
  await rowAction('Archive');
  await expect(row).toHaveCount(0);
  await manager.getByRole('button', { name: 'Archived', exact: true }).tap();
  await expect(row).toContainText(name);
  await row.scrollIntoViewIfNeeded();
  await capture('touch-codex-archived');
  await rowAction('Unarchive');
  await expect(row).toHaveCount(0);
  await manager.getByRole('button', { name: 'Unarchived', exact: true }).tap();
  await expect(row).toContainText(name);
  await rowAction('Host Terminal');
  const terminal = page.locator('.ns-window[data-app-id="terminal"]');
  await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  await expect(terminal.getByTestId('terminal-project-name')).toHaveText(
    projectPath.split('/').filter(Boolean).at(-1)!,
  );
  await expect(terminal.getByTestId('terminal-project-name')).toBeInViewport();
  await capture('touch-codex-project-terminal');
  await terminal.getByRole('button', { name: 'Details', exact: true }).tap();
  const hostDetails = page.getByRole('dialog', {
    name: 'Host Terminal Details',
    exact: true,
  });
  await expect(hostDetails.getByTestId('terminal-project-path')).toHaveText(
    projectPath,
  );
  await expect(
    hostDetails.getByTestId('terminal-project-path'),
  ).toBeInViewport();
  await capture('touch-codex-project-terminal-details');
  await hostDetails.getByRole('button', { name: 'Close', exact: true }).tap();
  await terminal.getByRole('button', { name: /^Close / }).tap();
  const confirmation = page.getByRole('dialog', { name: 'End host shell?' });
  await expect(
    confirmation.getByRole('button', { name: 'End Session', exact: true }),
  ).toBeInViewport();
  await confirmation
    .getByRole('button', { name: 'End Session', exact: true })
    .tap();
  await expect(terminal).toHaveCount(0);
  await openCodexManager(page, true);
  const recent = manager.getByRole('button', {
    name: `New session in ${projectPath}`,
    exact: true,
  });
  await recent.scrollIntoViewIfNeeded();
  await expect(recent).toBeInViewport();
  await capture('touch-codex-recent-projects');
  const created = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/host/codex/sessions') &&
      response.request().method() === 'POST',
  );
  await recent.tap();
  const response = await created;
  expect(response.ok()).toBeTruthy();
  const fresh = (await response.json()) as {
    session: { id: string; runId: string };
  };
  const freshWindow = codexWindow(page, fresh.session.id, fresh.session.runId);
  await expect(freshWindow.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  await endCodex(page, freshWindow, true);
  await openCodexManager(page, true);
  await row.getByRole('button', { name: 'Open', exact: true }).tap();
  await endCodex(page, resumed, true);
  await expect
    .poll(async () => (await session(request, id)).status)
    .toBe('ended');
  await expect(resumed.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'ended',
  );
  const history = await session(request, id, runId);
  expect(history.conversationId).toBeTruthy();
  const continueButton = resumed.getByRole('button', {
    name: 'Continue',
    exact: true,
  });
  await expect(continueButton).toBeEnabled();
  await expect(continueButton).toBeInViewport();
  await capture('touch-codex-ended-run');
  let continueRequests = 0;
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      request.url().endsWith(`/sessions/${id}/resume`)
    )
      continueRequests++;
  });
  await continueButton.tap();
  const continueDialog = page.getByRole('dialog', {
    name: 'Continue Conversation?',
  });
  await expect(continueDialog).toContainText(projectPath);
  await expect(continueDialog).toContainText(history.conversationId!);
  const confirmContinue = continueDialog.getByRole('button', {
    name: 'Continue',
    exact: true,
  });
  const cancelContinue = continueDialog.getByRole('button', {
    name: 'Cancel',
    exact: true,
  });
  await expect(confirmContinue).toBeInViewport();
  await expect(cancelContinue).toBeInViewport();
  await capture('touch-codex-continue-dialog');
  await cancelContinue.tap();
  expect(continueRequests).toBe(0);
  expect((await session(request, id)).runId).toBe(runId);
  await continueButton.tap();
  const continuation = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      response.url().endsWith(`/sessions/${id}/resume`),
  );
  await confirmContinue.tap();
  const continuationResponse = await continuation;
  expect(
    continuationResponse.ok(),
    await continuationResponse.text(),
  ).toBeTruthy();
  const { session: current } = await continuationResponse.json();
  expect(current.id).toBe(id);
  expect(current.runId).not.toBe(runId);
  const continuedWindow = codexWindow(page, id, current.runId);
  await expect(continuedWindow.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  const continuedState = await readState(continuedWindow, 'touch-continued');
  expect(continuedState.value).toBe(original.value);
  expect(continuedState.pid).not.toBe(original.pid);
  await capture('touch-codex-continued-run');
  await continuedWindow.getByRole('button', { name: /^Close / }).tap();
  await expect(continuedWindow).toHaveCount(0);
  await expect(resumed).toHaveClass(/is-active/);
  const openRunning = resumed.getByRole('button', {
    name: 'Open Running Session',
    exact: true,
  });
  await expect(openRunning).toBeInViewport();
  await capture('touch-codex-old-run');
  await openRunning.tap();
  await expect(continuedWindow.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  expect(await readState(continuedWindow, 'touch-running-reopened')).toEqual(
    continuedState,
  );
  expect(continueRequests).toBe(1);
  expect((await session(request, id, runId)).status).toBe('ended');
  await endCodex(page, continuedWindow, true);
});
