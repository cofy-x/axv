import { windowAction } from './helpers';
import type { Route } from '@playwright/test';
import {
  choose,
  codexOutput,
  codexRowMenu,
  codexSessionAction,
  codexWindow,
  dismissCodexAlerts,
  endCodex,
  expect,
  openCodexManager,
  runCodex,
  session,
  startCodex,
  test,
} from './codex-helpers';

test('Codex activity keeps successful turns quiet and dismisses only exact failure alerts', async ({
  page,
  request,
}) => {
  const current = await startCodex(page);
  const read = () => session(request, current.id, current.runId);
  const activity = async (state: string) => {
    await runCodex(current.window, `activity ${state}`);
    await expect(codexOutput(current.window)).toContainText(
      `ACTIVITY ${state}`,
    );
  };
  const review = (eventId: string) =>
    request.post(
      `/api/host/codex/sessions/${current.id}/runs/${current.runId}/activity/review`,
      {
        headers: { Origin: process.env.E2E_BASE_URL! },
        data: { eventId },
      },
    );

  await runCodex(current.window, 'message Inspect the project');
  await expect.poll(async () => (await read()).activity?.state).toBe('working');
  const firstTurn = (await read()).activity!.turnId;
  const workingManager = await openCodexManager(page);
  await expect(workingManager.getByTestId('codex-activity-summary')).toHaveText(
    '1 working',
  );
  await expect(workingManager.getByTestId('codex-action-needed')).toHaveCount(
    0,
  );
  await expect(
    workingManager
      .getByRole('group', { name: 'Session list', exact: true })
      .getByRole('button'),
  ).toHaveCount(2);
  await workingManager
    .locator(
      `[data-session-id="${current.id}"][data-run-id="${current.runId}"]`,
    )
    .getByRole('button', { name: 'Open', exact: true })
    .click();
  await activity('approval');
  await expect
    .poll(async () => (await read()).activity?.state)
    .toBe('waiting_approval');
  const approval = (await read()).activity!;
  expect(approval.needsAttention).toBe(true);
  await expect(
    current.window.getByRole('button', { name: 'Dismiss alert', exact: true }),
  ).toHaveCount(0);
  const reviewedApproval = await review(approval.eventId!);
  expect(reviewedApproval.status()).toBe(409);
  expect((await reviewedApproval.json()).code).toBe('activity_pending');
  expect((await read()).activity?.needsAttention).toBe(true);
  await activity('resolve');
  await expect.poll(async () => (await read()).activity?.state).toBe('working');
  await activity('input');
  await expect
    .poll(async () => (await read()).activity?.state)
    .toBe('waiting_input');
  expect((await read()).activity?.needsAttention).toBe(true);
  await activity('resolve');
  await activity('complete');
  await expect
    .poll(async () => (await read()).activity?.state)
    .toBe('completed');
  const completed = (await read()).activity!;
  await expect(current.window.getByTestId('codex-activity')).toHaveAttribute(
    'data-attention-kind',
    'none',
  );
  expect(completed.turnId).toBe(firstTurn);
  expect(completed.needsAttention).toBe(false);
  const stale = await review(approval.eventId!);
  expect(stale.status()).toBe(409);
  expect((await stale.json()).code).toBe('activity_changed');
  expect((await read()).activity?.needsAttention).toBe(false);
  const successful = await review(completed.eventId!);
  expect(successful.status()).toBe(409);
  expect((await successful.json()).code).toBe('activity_not_actionable');
  await expect(
    current.window.getByRole('button', { name: 'Dismiss alert', exact: true }),
  ).toHaveCount(0);
  await expect(workingManager.getByTestId('codex-action-needed')).toHaveCount(
    0,
  );
  expect((await read()).activity?.needsAttention).toBe(false);
  await page.reload();
  await expect(current.window.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  expect((await read()).activity?.needsAttention).toBe(false);

  await runCodex(current.window, 'message Continue the review');
  await expect.poll(async () => (await read()).activity?.state).toBe('working');
  expect((await read()).activity?.turnId).not.toBe(firstTurn);
  await activity('fail');
  await expect.poll(async () => (await read()).activity?.state).toBe('failed');
  expect((await read()).status).toBe('running');
  await current.window.getByRole('button', { name: /^Close / }).click();
  await expect(codexWindow(page, current.id, current.runId)).toHaveCount(0);
  const manager = await openCodexManager(page);
  const row = manager.locator(
    `[data-session-id="${current.id}"][data-run-id="${current.runId}"]`,
  );
  await expect(row.getByTestId('codex-activity')).toHaveAttribute(
    'data-activity-state',
    'failed',
  );
  await expect(row).toContainText('Failed');
  await expect(row.getByTestId('codex-activity')).toHaveAttribute(
    'data-attention-kind',
    'warning',
  );
  await expect(
    row.getByRole('button', { name: 'Dismiss alert', exact: true }),
  ).toHaveCount(0);
  const failedMenu = await codexRowMenu(page, row);
  await failedMenu
    .getByRole('menuitem', { name: 'Dismiss alert', exact: true })
    .click();
  await expect(row.getByTestId('codex-activity')).toHaveAttribute(
    'data-needs-attention',
    'false',
  );
});

test('late dismissals preserve newer interrupted activity through unavailable observations', async ({
  page,
  request,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  const current = await startCodex(page, touch);
  const read = () => session(request, current.id, current.runId);
  const badge = current.window.getByTestId('codex-activity');
  const activity = async (state: string, expected = state) => {
    await runCodex(current.window, `activity ${state}`);
    await expect
      .poll(async () => (await read()).activity?.state)
      .toBe(expected);
    await expect(badge).toHaveAttribute('data-activity-state', expected);
  };
  await runCodex(current.window, 'message Finish the first turn');
  await activity('fail', 'failed');
  const failed = (await read()).activity!;
  const reviewURL = `**/api/host/codex/sessions/${current.id}/runs/${current.runId}/activity/review`;
  let release!: () => void;
  const released = new Promise<void>((resolve) => {
    release = resolve;
  });
  let held = false;
  await page.route(reviewURL, async (route) => {
    const response = await route.fetch();
    expect(response.ok(), await response.text()).toBeTruthy();
    expect(route.request().postDataJSON()).toEqual({
      eventId: failed.eventId,
    });
    held = true;
    await released;
    await route.fulfill({ response });
  });
  const review = current.window.getByRole('button', {
    name: 'Dismiss alert',
    exact: true,
  });
  try {
    await choose(review, touch);
    await expect.poll(() => held).toBe(true);
    await runCodex(current.window, 'message Observe a newer turn');
    await expect
      .poll(async () => (await read()).activity?.state)
      .toBe('working');
    await activity('unknown');
    await expect(badge).toContainText('Activity unavailable');
    await expect(badge).toHaveAttribute('data-needs-attention', 'false');
    await activity('working');
    await activity('interrupt', 'interrupted');
    expect((await read()).status).toBe('running');
    const interrupted = (await read()).activity!;
    expect(interrupted.turnId).not.toBe(failed.turnId);
    release();
    await expect(review).toBeEnabled();
    await expect(badge).toHaveAttribute('data-activity-state', 'interrupted');
    await expect(badge).toHaveAttribute('data-needs-attention', 'true');
    expect((await read()).activity?.eventId).toBe(interrupted.eventId);
  } finally {
    release();
    await page.unroute(reviewURL);
  }
  await review.focus();
  await page.keyboard.press('Enter');
  await expect(badge).toHaveAttribute('data-needs-attention', 'false');
  expect((await read()).activity?.needsAttention).toBe(false);
  await expect(current.window.getByLabel('Codex terminal input')).toBeFocused();
});

test('continuation replaces the old run activity in the session manager', async ({
  page,
  request,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  const current = await startCodex(page, touch);
  await runCodex(current.window, 'message Fail before continuation');
  await runCodex(current.window, 'activity fail');
  await expect
    .poll(
      async () =>
        (await session(request, current.id, current.runId)).activity?.state,
    )
    .toBe('failed');
  await endCodex(page, current.window, touch);
  const manager = await openCodexManager(page, touch);
  const originalRow = manager.locator(
    `[data-session-id="${current.id}"][data-run-id="${current.runId}"]`,
  );
  await expect(originalRow.getByTestId('codex-activity')).toHaveAttribute(
    'data-activity-state',
    'failed',
  );
  // The terminal's final-status read can finish before the manager refreshes.
  // Its context menu deliberately keeps the capabilities captured on opening.
  await expect(originalRow).toContainText('Run ended');
  // Unreviewed work opens its original run. Continuation remains an explicit
  // action in that row's menu and must never reuse the predecessor's activity.
  await choose(
    originalRow.getByRole('button', { name: /^More session actions/ }),
    touch,
  );
  const continueAction = page.locator('.ns-menu-layer').getByRole('menuitem', {
    name: 'Continue',
    exact: true,
  });
  await expect(continueAction).toBeEnabled();
  await choose(continueAction, touch);
  const confirmation = page.getByRole('dialog', {
    name: 'Continue Conversation?',
  });
  const resumed = page.waitForResponse(
    (response) =>
      response
        .url()
        .endsWith(`/api/host/codex/sessions/${current.id}/resume`) &&
      response.request().method() === 'POST',
  );
  await choose(
    confirmation.getByRole('button', { name: 'Continue', exact: true }),
    touch,
  );
  const response = await resumed;
  expect(response.ok(), await response.text()).toBeTruthy();
  const next = (await response.json()).session;
  expect(next.runId).not.toBe(current.runId);
  await expect(
    codexWindow(page, current.id, next.runId).getByTestId('codex-status'),
  ).toHaveAttribute('data-state', 'connected');
  await openCodexManager(page, touch);
  const latestRow = manager.locator(
    `[data-session-id="${current.id}"][data-run-id="${next.runId}"]`,
  );
  await expect(originalRow).toHaveCount(0);
  await expect(latestRow.getByTestId('codex-activity')).toHaveAttribute(
    'data-activity-state',
    'idle',
  );
  await expect(latestRow.getByTestId('codex-activity')).toHaveAttribute(
    'data-needs-attention',
    'false',
  );
  expect(
    (await session(request, current.id, current.runId)).activity?.state,
  ).toBe('failed');
  expect(
    (await session(request, current.id, current.runId)).activity
      ?.needsAttention,
  ).toBe(true);
});

test('a dismissed failure stays dismissed after an unavailable read recovers', async ({
  page,
  request,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  const current = await startCodex(page, touch);
  await runCodex(current.window, 'message Fail before observation recovery');
  await runCodex(current.window, 'activity fail');
  const badge = current.window.getByTestId('codex-activity');
  await expect(badge).toHaveAttribute('data-activity-state', 'failed');
  const review = current.window.getByRole('button', {
    name: 'Dismiss alert',
    exact: true,
  });
  await choose(review, touch);
  await expect(badge).toHaveAttribute('data-needs-attention', 'false');
  const failed = (await session(request, current.id, current.runId)).activity!;
  const listURL = '**/api/host/codex/sessions';
  const runURL = `**/api/host/codex/sessions/${current.id}/runs/${current.runId}`;
  const unavailable = async (route: Route) => {
    const response = await route.fetch();
    const body = await response.json();
    for (const record of body.sessions || [body.session]) {
      if (record.id !== current.id || record.runId !== current.runId) continue;
      // Preserve the server's ordering stamp while simulating an unavailable
      // activity file. This exercises the client boundary, not provider events.
      record.activity = {
        state: 'unknown',
        needsAttention: false,
        reason: 'Fixture observation unavailable.',
        observation: record.activity.observation,
      };
    }
    await route.fulfill({ response, json: body });
  };
  await page.route(listURL, unavailable);
  await page.route(runURL, unavailable);
  try {
    await page.evaluate(() => window.dispatchEvent(new Event('focus')));
    await expect(badge).toHaveAttribute('data-activity-state', 'unknown');
    await expect(current.window).toContainText(
      'Fixture observation unavailable.',
    );
    await expect(review).toHaveCount(0);
  } finally {
    await page.unrouteAll({ behavior: 'wait' });
  }
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await expect(badge).toHaveAttribute('data-activity-state', 'failed');
  await expect(badge).toHaveAttribute('data-needs-attention', 'false');
  await expect(current.window).not.toContainText(
    'Fixture observation unavailable.',
  );
  await expect(review).toHaveCount(0);
  expect(
    (await session(request, current.id, current.runId)).activity?.eventId,
  ).toBe(failed.eventId);
});

test('manager distinguishes loading, failed activity reads and successful recovery', async ({
  page,
  request,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  await dismissCodexAlerts(request);
  const listURL = '**/api/host/codex/sessions';
  let release!: () => void;
  const released = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(listURL, async (route) => {
    await released;
    await route.fulfill({
      status: 503,
      json: {
        code: 'fixture_unavailable',
        error: 'Fixture activity refresh unavailable.',
      },
    });
  });
  const manager = await openCodexManager(page, touch);
  const summary = manager.getByTestId('codex-activity-summary');
  try {
    await expect(summary).toHaveText('Loading session activity…');
    await expect(manager.getByTestId('codex-action-needed')).toHaveCount(0);
    await expect(manager).not.toContainText('No unarchived sessions.');
    release();
    await expect(manager.getByRole('alert')).toContainText(
      'Fixture activity refresh unavailable.',
    );
    await expect(summary).toContainText('Session activity unavailable');
    await expect(summary).not.toContainText('0 working');
  } finally {
    release();
    await page.unroute(listURL);
  }
  await choose(
    manager.getByRole('button', { name: 'Refresh', exact: true }),
    touch,
  );
  await expect(manager.getByTestId('codex-action-needed')).toHaveCount(0);
  await expect(summary).toHaveCount(0);
  await expect(manager).not.toContainText('0 working');
  await expect(manager.getByRole('alert')).toHaveCount(0);
});

test('a sole terminal retains retired service observations while shaded', async ({
  page,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  const current = await startCodex(page, touch);
  await runCodex(current.window, 'message Retain review ordering while hidden');
  await runCodex(current.window, 'activity fail');
  const badge = current.window.getByTestId('codex-activity');
  await expect(badge).toHaveAttribute('data-activity-state', 'failed');
  // Narrow layouts hide inactive windows, so bring the manager forward before
  // closing it and leaving the retained terminal as the sole subscriber.
  await openCodexManager(page, touch);
  const manager = page.locator('.ns-window:has([data-testid="codex-manager"])');
  await choose(manager.getByRole('button', { name: /^Close / }), touch);
  await expect(manager).toHaveCount(0);

  const runURL = `**/api/host/codex/sessions/${current.id}/runs/${current.runId}`;
  const reviewURL = `${runURL}/activity/review`;
  let releaseReview!: () => void;
  const reviewReleased = new Promise<void>((resolve) => {
    releaseReview = resolve;
  });
  let releaseReads!: () => void;
  const readsReleased = new Promise<void>((resolve) => {
    releaseReads = resolve;
  });
  let reviewHeld = false;
  let revision = 0;
  let pauseReads = false;
  const diagnostic = 'Fixture review record unavailable after restart.';
  await page.route(reviewURL, async (route) => {
    const response = await route.fetch();
    expect(response.ok(), await response.text()).toBeTruthy();
    reviewHeld = true;
    await reviewReleased;
    await route.fulfill({ response });
  });
  try {
    await choose(
      current.window.getByRole('button', {
        name: 'Dismiss alert',
        exact: true,
      }),
      touch,
    );
    await expect.poll(() => reviewHeld).toBe(true);
    await page.route(runURL, async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      // A restarted service can authoritatively report damaged review storage
      // for the same provider event; the old service's saved review is stale.
      body.session.activity = {
        ...body.session.activity,
        observation: {
          instanceId: 'fixture-restarted-service',
          revision: ++revision,
        },
        needsAttention: true,
        reason: `${diagnostic} Observation ${revision}.`,
      };
      if (pauseReads) await readsReleased;
      await route.fulfill({ response, json: body });
    });
    await page.evaluate(() => window.dispatchEvent(new Event('focus')));
    await expect(current.window).toContainText(diagnostic);
    await expect(badge).toHaveAttribute('data-attention-kind', 'warning');
    await windowAction(page, current.window, 'Shade', touch);
    await expect(current.window).toHaveClass(/is-shaded/);
    await expect(
      current.window.getByLabel('Codex terminal input'),
    ).not.toBeFocused();
    const beforeUnshade = revision;
    await windowAction(page, current.window, 'Unshade', touch);
    await expect(current.window).not.toHaveClass(/is-shaded/);
    await expect.poll(() => revision).toBeGreaterThan(beforeUnshade);
    await expect(current.window).toContainText(`Observation ${revision}.`);
    // Prevent another poll from concealing a brief acceptance of the retired
    // response. The dismissal response itself provides the synchronization.
    pauseReads = true;
    releaseReview();
    await expect(
      current.window.getByRole('button', { name: 'Dismissing…', exact: true }),
    ).toHaveCount(0);
    await expect(badge).toHaveAttribute('data-needs-attention', 'true');
    await expect(current.window).toContainText(diagnostic);
    await expect(
      current.window.getByRole('button', {
        name: 'Dismiss alert',
        exact: true,
      }),
    ).toBeEnabled();
  } finally {
    releaseReview();
    releaseReads();
    await page.unroute(reviewURL);
    await page.unroute(runURL);
  }
});

test('action needed appears only for unresolved work, finds archived runs and restores keyboard focus', async ({
  page,
  request,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  await dismissCodexAlerts(request);
  const manager = await openCodexManager(page, touch);
  const filters = manager.getByRole('group', {
    name: 'Session list',
    exact: true,
  });
  const action = manager.getByTestId('codex-action-needed');
  await expect(filters.getByRole('button')).toHaveCount(2);
  await expect(action).toHaveCount(0);

  const pending = await startCodex(page, touch);
  await runCodex(pending.window, 'message Ask for the next step');
  await runCodex(pending.window, 'activity input');
  await expect
    .poll(
      async () =>
        (await session(request, pending.id, pending.runId)).activity?.state,
    )
    .toBe('waiting_input');
  await codexSessionAction(page, pending.window, 'Archive', touch);
  await choose(pending.window.getByRole('button', { name: /^Close / }), touch);
  await openCodexManager(page, touch);
  await expect(action).toHaveAccessibleName('Action needed (1)');
  await expect(filters.getByTestId('codex-action-needed')).toHaveCount(0);
  await action.focus();
  await page.keyboard.press('Enter');
  await expect(action).toHaveAttribute('aria-pressed', 'true');
  const pendingRow = manager.locator(
    `[data-session-id="${pending.id}"][data-run-id="${pending.runId}"]`,
  );
  await expect(pendingRow).toContainText('Waiting for input');
  await expect(pendingRow.getByTestId('codex-session-project')).toContainText(
    'Archived',
  );
  await expect(
    pendingRow.getByRole('button', { name: 'Dismiss alert', exact: true }),
  ).toHaveCount(0);
  const pendingMenu = await codexRowMenu(page, pendingRow, touch);
  await expect(
    pendingMenu.getByRole('menuitem', { name: 'Dismiss alert', exact: true }),
  ).toHaveCount(0);
  await page.keyboard.press('Escape');
  await choose(
    pendingRow.getByRole('button', { name: 'Open', exact: true }),
    touch,
  );
  await expect(pending.window).toHaveClass(/is-active/);
  await expect(pending.window.getByTestId('codex-activity')).toHaveAttribute(
    'data-activity-state',
    'waiting_input',
  );
  await runCodex(pending.window, 'activity resolve');
  await runCodex(pending.window, 'activity complete');
  await expect(pending.window.getByTestId('codex-activity')).toHaveAttribute(
    'data-activity-state',
    'completed',
  );
  await expect(pending.window.getByLabel('Codex terminal input')).toBeFocused();
  await expect(
    pending.window.getByRole('button', { name: 'Dismiss alert', exact: true }),
  ).toHaveCount(0);
  await choose(pending.window.getByRole('button', { name: /^Close / }), touch);
  await openCodexManager(page, touch);
  await expect(action).toHaveCount(0);
  await expect(
    filters.getByRole('button', { name: 'Unarchived', exact: true }),
  ).toHaveAttribute('aria-pressed', 'true');
  await expect(pendingRow).toHaveCount(0);
  expect(
    (await session(request, pending.id, pending.runId)).activity
      ?.needsAttention,
  ).toBe(false);

  const failed = await startCodex(page, touch);
  await runCodex(failed.window, 'message Exercise an archived failure alert');
  await runCodex(failed.window, 'activity fail');
  await expect
    .poll(
      async () =>
        (await session(request, failed.id, failed.runId)).activity?.state,
    )
    .toBe('failed');
  await codexSessionAction(page, failed.window, 'Archive', touch);
  await choose(failed.window.getByRole('button', { name: /^Close / }), touch);
  const interrupted = await startCodex(page, touch);
  await runCodex(
    interrupted.window,
    'message Exercise the next alert focus target',
  );
  await runCodex(interrupted.window, 'activity interrupt');
  await expect
    .poll(
      async () =>
        (await session(request, interrupted.id, interrupted.runId)).activity
          ?.state,
    )
    .toBe('interrupted');
  await codexSessionAction(page, interrupted.window, 'Archive', touch);
  await choose(
    interrupted.window.getByRole('button', { name: /^Close / }),
    touch,
  );
  await openCodexManager(page, touch);
  const archived = filters.getByRole('button', {
    name: 'Archived',
    exact: true,
  });
  await choose(archived, touch);
  await expect(action).toHaveAccessibleName('Action needed (2)');
  await action.focus();
  await page.keyboard.press('Enter');
  const failedRow = manager.locator(
    `[data-session-id="${failed.id}"][data-run-id="${failed.runId}"]`,
  );
  await expect(failedRow).toContainText('Failed');
  await expect(pendingRow).toHaveCount(0);
  const interruptedRow = manager.locator(
    `[data-session-id="${interrupted.id}"][data-run-id="${interrupted.runId}"]`,
  );
  await interruptedRow
    .getByRole('button', { name: 'Dismiss alert', exact: true })
    .focus();
  await page.keyboard.press('Enter');
  await expect(interruptedRow).toHaveCount(0);
  await expect(action).toHaveAccessibleName('Action needed (1)');
  await expect(
    failedRow.locator('[data-session-primary-action]'),
  ).toBeFocused();
  expect(
    (await session(request, failed.id, failed.runId)).activity?.needsAttention,
  ).toBe(true);
  expect(
    (await session(request, interrupted.id, interrupted.runId)).activity
      ?.needsAttention,
  ).toBe(false);
  const failedMenu = await codexRowMenu(page, failedRow, touch);
  await expect(
    failedMenu.getByRole('menuitem', { name: 'Dismiss alert', exact: true }),
  ).toBeEnabled();
  await choose(
    failedMenu.getByRole('menuitem', { name: 'Details', exact: true }),
    touch,
  );
  const details = page.getByRole('dialog', { name: 'Codex Session Details' });
  await expect(details).toContainText(failed.runId);
  await expect(details).not.toContainText(pending.runId);
  await expect(
    details.getByText('Last activity', { exact: true }),
  ).toBeVisible();
  await choose(
    details.getByRole('button', { name: 'Close', exact: true }),
    touch,
  );
  await expect(
    failedRow.getByRole('button', { name: /^More session actions for / }),
  ).toBeFocused();
  const dismiss = failedRow.getByRole('button', {
    name: 'Dismiss alert',
    exact: true,
  });
  await dismiss.focus();
  await page.keyboard.press('Enter');
  await expect(action).toHaveCount(0);
  await expect(archived).toHaveAttribute('aria-pressed', 'true');
  await expect(archived).toBeFocused();
  await expect(failedRow.getByTestId('codex-activity')).toHaveAttribute(
    'data-needs-attention',
    'false',
  );
  await expect(pendingRow).toContainText('Completed');
  const completedMenu = await codexRowMenu(page, pendingRow, touch);
  await expect(
    completedMenu.getByRole('menuitem', { name: 'Dismiss alert', exact: true }),
  ).toHaveCount(0);
  await page.keyboard.press('Escape');
});
