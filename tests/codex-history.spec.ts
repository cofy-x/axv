import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { activate } from './helpers';
import {
  codexWindow,
  endCodex,
  expect,
  openCodexManager,
  readState,
  runCodex,
  session,
  sessions,
  startCodexInFolder,
  test,
} from './codex-helpers';

test('continuing an exited conversation creates a new run and keeps old windows bound to the ended run', async ({
  page,
  request,
}) => {
  const first = await startCodexInFolder(page);
  await runCodex(first.window, 'state saved-conversation');
  const original = await readState(first.window, 'before-exit');
  const identity = await session(request, first.id, first.runId);
  expect(identity.conversationId).toBeTruthy();
  const count = (await sessions(request)).length;
  await runCodex(first.window, 'exit');
  await expect(first.window.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'ended',
  );
  await expect(
    first.window.getByRole('button', { name: 'Reconnect', exact: true }),
  ).toHaveCount(0);
  const continueButton = first.window.getByRole('button', {
    name: 'Continue',
    exact: true,
  });
  await expect(continueButton).toBeEnabled();
  let resumes = 0;
  page.on('request', (request) => {
    if (request.url().endsWith(`/sessions/${first.id}/resume`)) resumes++;
  });
  await continueButton.click();
  const dialog = page.getByRole('dialog', { name: 'Continue Conversation?' });
  await expect(dialog).toContainText(identity.conversationId!);
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  expect(resumes).toBe(0);
  expect((await session(request, first.id)).runId).toBe(first.runId);
  await continueButton.click();
  const resumed = page.waitForResponse((response) =>
    response.url().endsWith(`/sessions/${first.id}/resume`),
  );
  await dialog.getByRole('button', { name: 'Continue', exact: true }).click();
  const response = await resumed;
  expect(response.ok(), await response.text()).toBeTruthy();
  const current = (await response.json()).session;
  expect(current.id).toBe(first.id);
  expect(current.runId).not.toBe(first.runId);
  expect(current.conversationId).toBe(identity.conversationId);
  const restored = codexWindow(page, first.id, current.runId);
  await expect(restored.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  const recovered = await readState(restored, 'after-continue');
  expect(recovered.value).toBe(original.value);
  expect(recovered.pid).not.toBe(original.pid);
  expect((await sessions(request)).length).toBe(count);
  const lateEnd = await request.post(
    `/api/host/codex/sessions/${first.id}/runs/${first.runId}/end`,
    { headers: { Origin: process.env.E2E_BASE_URL! } },
  );
  expect(lateEnd.ok()).toBeTruthy();
  expect((await lateEnd.json()).session.runId).toBe(first.runId);
  expect((await session(request, first.id)).runId).toBe(current.runId);
  expect((await session(request, first.id)).status).toBe('running');
  await page.reload();
  const oldWindow = codexWindow(page, first.id, first.runId);
  await activate(page, oldWindow);
  await expect(oldWindow.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'ended',
  );
  await expect(
    oldWindow.getByRole('button', {
      name: 'Open Running Session',
      exact: true,
    }),
  ).toBeEnabled();
  await oldWindow
    .getByRole('button', { name: 'Open Running Session', exact: true })
    .click();
  await expect(restored).toHaveClass(/is-active/);
  expect(await readState(restored, 'old-window-isolated')).toEqual(recovered);
  expect(resumes).toBe(1);
  await endCodex(page, restored);
});

test('a lost tmux runtime stays lost through reads and reload until explicitly continued', async ({
  page,
  request,
}) => {
  const first = await startCodexInFolder(page);
  await runCodex(first.window, 'state after-runtime-loss');
  const original = await readState(first.window, 'before-loss');
  const saved = await session(request, first.id, first.runId);
  const ownedSocket = process.env.E2E_CODEX_TMUX_SOCKET;
  expect(ownedSocket).toBeTruthy();
  expect(saved.tmuxSocket.endsWith(`/${ownedSocket}`)).toBeTruthy();
  await promisify(execFile)('tmux', [
    '-L',
    ownedSocket!,
    'kill-session',
    '-t',
    `=${saved.tmuxSession}`,
  ]);
  await expect
    .poll(async () => (await session(request, first.id, first.runId)).status)
    .toBe('lost');
  await page.reload();
  const manager = await openCodexManager(page);
  const row = manager.locator(`[data-session-id="${first.id}"]`);
  await expect(row).toContainText('lost');
  expect((await session(request, first.id)).runId).toBe(first.runId);
  await row.getByRole('button', { name: 'Continue', exact: true }).click();
  const resumed = page.waitForResponse((response) =>
    response.url().endsWith(`/sessions/${first.id}/resume`),
  );
  await page
    .getByRole('dialog', { name: 'Continue Conversation?' })
    .getByRole('button', { name: 'Continue', exact: true })
    .click();
  const response = await resumed;
  expect(response.ok(), await response.text()).toBeTruthy();
  const current = (await response.json()).session;
  expect(current.runId).not.toBe(first.runId);
  expect(current.conversationId).toBe(saved.conversationId);
  const restored = codexWindow(page, first.id, current.runId);
  await expect(restored.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  const recovered = await readState(restored, 'after-loss');
  expect(recovered.value).toBe(original.value);
  expect(recovered.pid).not.toBe(original.pid);
  expect((await session(request, first.id, first.runId)).status).toBe('lost');
  await endCodex(page, restored);
});

test('missing provider history reports an error without silently starting a new conversation', async ({
  page,
  request,
}) => {
  const first = await startCodexInFolder(page);
  const saved = await session(request, first.id, first.runId);
  await runCodex(first.window, 'forget-history');
  await expect(first.window.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'ended',
  );
  await first.window
    .getByRole('button', { name: 'Continue', exact: true })
    .click();
  await page
    .getByRole('dialog', { name: 'Continue Conversation?' })
    .getByRole('button', { name: 'Continue', exact: true })
    .click();
  await expect(first.window.getByRole('alert')).toContainText(/history/i);
  const current = await session(request, first.id);
  expect(current.conversationId).toBe(saved.conversationId);
  expect(['ended', 'failed']).toContain(current.status);
  expect(
    (await sessions(request)).filter((row) =>
      ['starting', 'running', 'stopping'].includes(row.status),
    ),
  ).toHaveLength(0);
});
