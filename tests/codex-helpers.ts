import type { APIRequestContext, Locator, Page } from '@playwright/test';
import { activate, expect, menuItem, test as desktopTest } from './helpers';

export interface CodexSession {
  id: string;
  runId: string;
  conversationId?: string;
  continuation: {
    state: 'available' | 'active' | 'unavailable';
    reason?: string;
    activeRunId?: string;
  };
  title: string;
  archivedAt?: string;
  metadataRevision: number;
  projectPath: string;
  status: string;
  connections: number;
  tmuxSession: string;
  tmuxSocket: string;
  attachCommand: string;
  activity?: {
    observation: { instanceId: string; revision: number };
    state: string;
    sequence?: number;
    eventId?: string;
    turnId?: string;
    updatedAt?: string;
    needsAttention: boolean;
    reason?: string;
  };
}

export const projectPath = process.env.E2E_CODEX_PROJECT || '';
export const defaultProjectPath = process.env.E2E_CODEX_DEFAULT_PROJECT || '';
export const configPath = process.env.E2E_CODEX_CONFIG || '';
export const networkDigest = process.env.E2E_CODEX_NETWORK_DIGEST || '';

export async function sessions(
  request: APIRequestContext,
): Promise<CodexSession[]> {
  const response = await request.get('/api/host/codex/sessions');
  expect(response.ok()).toBeTruthy();
  return (await response.json()).sessions;
}

export async function dismissCodexAlerts(request: APIRequestContext) {
  for (const current of await sessions(request)) {
    if (
      !current.activity?.needsAttention ||
      !current.activity.eventId ||
      !['failed', 'interrupted'].includes(current.activity.state)
    )
      continue;
    const response = await request.post(
      `/api/host/codex/sessions/${current.id}/runs/${current.runId}/activity/review`,
      {
        headers: { Origin: process.env.E2E_BASE_URL! },
        data: { eventId: current.activity.eventId },
      },
    );
    expect(response.ok(), await response.text()).toBeTruthy();
  }
}

export async function session(
  request: APIRequestContext,
  id: string,
  runId?: string,
): Promise<CodexSession> {
  const response = await request.get(
    `/api/host/codex/sessions/${id}${runId ? `/runs/${runId}` : ''}`,
  );
  expect(response.ok()).toBeTruthy();
  return (await response.json()).session;
}

export const test = desktopTest.extend<{ codexReady: void }>({
  codexReady: [
    async ({ request }, use) => {
      test.skip(!projectPath, 'Codex requires macOS or Linux and tmux.');
      const endSessions = async () => {
        for (const current of await sessions(request)) {
          if (!['starting', 'running', 'stopping'].includes(current.status))
            continue;
          const response = await request.post(
            `/api/host/codex/sessions/${current.id}/runs/${current.runId}/end`,
            { headers: { Origin: process.env.E2E_BASE_URL! } },
          );
          expect(response.ok(), await response.text()).toBeTruthy();
        }
      };
      await endSessions();
      try {
        await use();
      } finally {
        await endSessions();
      }
    },
    { auto: true },
  ],
});

export async function choose(target: Locator, touch = false) {
  if (touch) await target.tap();
  else await target.click();
}

export async function codexRowMenu(page: Page, row: Locator, touch = false) {
  const runId = await row.getAttribute('data-run-id');
  expect(runId).toBeTruthy();
  await choose(
    row.getByRole('button', { name: /^More session actions for / }),
    touch,
  );
  const menu = page.locator('.ns-menu-layer').getByRole('menu', {
    name: new RegExp(`^Run ${runId!.slice(0, 8)} · `),
  });
  await expect(menu).toBeVisible();
  return menu;
}

export async function openCodexManager(page: Page, touch = false) {
  // The application menu remains available independently of optional desktop
  // shortcuts or Dock pins, including before the host CLI is installed.
  await choose(
    page.getByRole('button', { name: 'Workspace menu', exact: true }),
    touch,
  );
  await choose(menuItem(page, 'Applications'), touch);
  await choose(
    page
      .getByRole('menu', { name: 'Applications', exact: true })
      .getByRole('menuitem', { name: 'Codex', exact: true }),
    touch,
  );
  const sessionLauncher = page
    .locator('.ns-window.is-active[data-app-id="codex"]')
    .getByRole('button', { name: 'Sessions', exact: true });
  if (await sessionLauncher.count()) await choose(sessionLauncher, touch);
  const manager = page.getByTestId('codex-manager');
  await expect(manager).toBeVisible();
  return manager;
}

export async function resumeCodex(page: Page, id: string, runId: string) {
  const existing = codexWindow(page, id, runId);
  if (await existing.count()) {
    await activate(page, existing);
    return existing;
  }
  const manager = await openCodexManager(page);
  await manager
    .locator(`[data-session-id="${id}"][data-run-id="${runId}"]`)
    .getByRole('button', { name: /^(Open|View)$/ })
    .click();
  const window = codexWindow(page, id, runId);
  await expect(window).toHaveClass(/is-active/);
  return window;
}

export async function openCodexEnvironment(page: Page, touch = false) {
  const manager = await openCodexManager(page, touch);
  await choose(
    manager
      .locator('.codex-manager-tools')
      .getByRole('button', { name: 'More Codex manager actions', exact: true }),
    touch,
  );
  await choose(
    page
      .locator('.ns-menu-layer')
      .getByRole('menuitem', { name: 'Environment', exact: true }),
    touch,
  );
  const panel = page.locator('.ns-window[data-app-id="codex-environment"]');
  await expect(panel).toBeVisible();
  await expect(panel).toHaveClass(/is-panel/);
  return panel;
}

export function codexWindow(page: Page, id: string, runId: string) {
  return page.locator(
    `.ns-window:has([data-testid="codex-session"][data-session-id="${id}"][data-run-id="${runId}"])`,
  );
}

export async function openCodexFolderPicker(page: Page, touch = false) {
  const manager = await openCodexManager(page, touch);
  const toggle = manager.getByRole('button', {
    name: 'New in Folder',
    exact: true,
  });
  if ((await toggle.getAttribute('aria-expanded')) !== 'true')
    await choose(toggle, touch);
  await expect(
    manager.getByRole('button', { name: 'Go', exact: true }),
  ).toBeEnabled();
  return manager;
}

export async function selectCodexProject(page: Page, touch = false) {
  const manager = await openCodexFolderPicker(page, touch);
  await manager
    .getByLabel('Project directory', { exact: true })
    .fill(projectPath);
  await choose(manager.getByRole('button', { name: 'Go', exact: true }), touch);
  await expect(
    manager.getByRole('button', { name: 'Open folder nested', exact: true }),
  ).toBeVisible();
  return manager;
}

export async function startCodex(page: Page, touch = false) {
  const manager = await openCodexManager(page, touch);
  return createCodex(page, manager, 'New Session', touch);
}

export async function startCodexInFolder(page: Page, touch = false) {
  const manager = await selectCodexProject(page, touch);
  return createCodex(page, manager, 'Start Here', touch);
}

async function createCodex(
  page: Page,
  manager: Locator,
  label: string,
  touch: boolean,
) {
  const created = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/host/codex/sessions') &&
      response.request().method() === 'POST',
  );
  await choose(
    manager.getByRole('button', { name: label, exact: true }),
    touch,
  );
  const response = await created;
  expect(response.ok()).toBeTruthy();
  const { session: current } = (await response.json()) as {
    session: CodexSession;
  };
  const window = codexWindow(page, current.id, current.runId);
  await expect(window.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  await expect
    .poll(async () => {
      const initialized = await session(
        page.request,
        current.id,
        current.runId,
      );
      return initialized.status === 'running' && !!initialized.conversationId;
    })
    .toBe(true);
  await expect(codexOutput(window)).toContainText('codex-test>');
  await expect(window.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  await expect(window.getByLabel('Codex terminal input')).toBeEditable();
  return { window, id: current.id, runId: current.runId };
}

export async function runCodex(window: Locator, command: string) {
  const input = window.getByLabel('Codex terminal input', { exact: true });
  await expect(input).toBeEditable();
  await input.focus();
  await input.pressSequentially(command);
  await input.press('Enter');
}

export function codexOutput(window: Locator) {
  return window.locator('.xterm-accessibility-tree');
}

export async function readState(window: Locator, tag: string) {
  await runCodex(window, `inspect ${tag}`);
  // A following prompt proves the value is complete. textContent joins xterm
  // rows so a narrow viewport cannot split the value being asserted.
  const pattern = new RegExp(
    `STATE ${tag} pid=(\\d+) value=([\\s\\S]*?)codex-test>`,
  );
  await expect(codexOutput(window)).toContainText(pattern);
  const match = (await codexOutput(window).textContent())!.match(pattern)!;
  return { pid: Number(match[1]), value: match[2].trim() };
}

export async function codexSessionAction(
  page: Page,
  window: Locator,
  name: string,
  touch = false,
) {
  await choose(
    window.getByRole('button', { name: 'More Codex actions', exact: true }),
    touch,
  );
  await choose(
    page.locator('.ns-menu-layer').getByRole('menuitem', { name, exact: true }),
    touch,
  );
}

export async function requestEndCodex(
  page: Page,
  window: Locator,
  touch = false,
) {
  await codexSessionAction(page, window, 'End Session', touch);
  const dialog = page.getByRole('dialog', { name: 'End Codex session?' });
  await expect(
    dialog.getByRole('button', { name: 'Cancel', exact: true }),
  ).toBeFocused();
  return dialog;
}

export async function endCodex(page: Page, window: Locator, touch = false) {
  const runId = await window
    .getByTestId('codex-session')
    .getAttribute('data-run-id');
  expect(runId).toBeTruthy();
  const dialog = await requestEndCodex(page, window, touch);
  await choose(
    dialog.getByRole('button', { name: 'End Session', exact: true }),
    touch,
  );
  await expect(dialog).toBeHidden();
  await expect(window.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'ended',
  );
  // The frame title follows the session state in a separate effect. Wait for
  // this exact run before another helper snapshots the Dock window chooser.
  await expect
    .poll(async () =>
      (await window.getAttribute('aria-label'))?.endsWith(
        ` · Ended (${runId!.slice(0, 8)})`,
      ),
    )
    .toBe(true);
}

export { expect };
