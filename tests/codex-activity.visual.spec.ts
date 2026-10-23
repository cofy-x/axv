import type { Locator } from '@playwright/test';
import {
  choose,
  codexOutput,
  dismissCodexAlerts,
  expect,
  openCodexManager,
  runCodex,
  session,
  sessions,
  startCodex,
  test,
} from './codex-helpers';

test('Codex activity and review controls fit manager and terminal windows', async ({
  page,
  request,
}, info) => {
  const touch = Boolean(info.project.use.hasTouch);
  // The shared server retains earlier conversations. Failure alerts can be
  // dismissed; successful turns never create an acknowledgment requirement.
  await dismissCodexAlerts(request);
  for (const previous of await sessions(request)) {
    if (previous.archivedAt) continue;
    const archived = await request.patch(
      `/api/host/codex/sessions/${previous.id}`,
      {
        headers: { Origin: process.env.E2E_BASE_URL! },
        data: { revision: previous.metadataRevision, archived: true },
      },
    );
    expect(archived.ok(), await archived.text()).toBeTruthy();
  }

  const examples = [];
  for (const [title, activity, state] of [
    ['Review project updates', 'complete', 'completed'],
    ['Build the preview service', 'approval', 'waiting_approval'],
  ]) {
    const current = await startCodex(page, touch);
    const metadata = await session(request, current.id, current.runId);
    const renamed = await request.patch(
      `/api/host/codex/sessions/${current.id}`,
      {
        headers: { Origin: process.env.E2E_BASE_URL! },
        data: { revision: metadata.metadataRevision, title },
      },
    );
    expect(renamed.ok(), await renamed.text()).toBeTruthy();
    await runCodex(current.window, `message ${title}`);
    await runCodex(current.window, `activity ${activity}`);
    await expect(codexOutput(current.window)).toContainText(
      `ACTIVITY ${activity}`,
    );
    await expect
      .poll(
        async () =>
          (await session(request, current.id, current.runId)).activity?.state,
      )
      .toBe(state);
    examples.push({
      ...current,
      state,
      title,
      projectPath: metadata.projectPath,
    });
  }

  const capture = async (name: string) => {
    const path = info.outputPath(`${name}.png`);
    await page.screenshot({ path, fullPage: true });
    await info.attach(name, {
      path,
      contentType: 'image/png',
    });
  };
  const fitsWindow = async (target: Locator) => {
    await expect
      .poll(() =>
        target.evaluate((element) => {
          const frame = element.closest('.ns-window')!.getBoundingClientRect();
          return frame.left >= 0 && frame.right <= window.innerWidth;
        }),
      )
      .toBe(true);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBeLessThanOrEqual(page.viewportSize()!.width);
  };

  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 1024, height: 768 },
    { width: 390, height: 844 },
  ]) {
    await page.setViewportSize(viewport);
    const manager = await openCodexManager(page, touch);
    await choose(
      manager.getByRole('button', { name: 'Unarchived', exact: true }),
      touch,
    );
    const attention = manager.getByTestId('codex-action-needed');
    await expect(attention).toHaveAccessibleName('Action needed (1)');
    await expect(attention).toContainText('Action needed ·');
    await expect(attention.getByText('1', { exact: true })).toBeVisible();
    await expect(attention).toHaveAttribute('title', 'Action needed (1)');
    await expect(attention).toBeInViewport();
    await expect(manager.getByTestId('codex-activity-summary')).toHaveCount(0);
    await expect(
      manager.getByRole('region', { name: 'Recent projects' }),
    ).toHaveCount(0);
    await expect(manager.getByTestId('codex-default-project')).toBeHidden();
    for (const current of examples) {
      const row = manager.locator(
        `[data-session-id="${current.id}"][data-run-id="${current.runId}"]`,
      );
      await expect(row.locator('strong')).toHaveText(current.title);
      await expect(row.getByTestId('codex-run-id')).toHaveCount(0);
      await expect(row.getByTestId('codex-session-project')).toHaveText(
        current.projectPath.split('/').filter(Boolean).at(-1)!,
      );
      await expect(row.getByTestId('codex-session-project')).toHaveAttribute(
        'title',
        current.projectPath,
      );
      await expect(row).not.toContainText(current.projectPath);
      await expect(row).not.toContainText('Run running');
      await expect(
        row.getByRole('button', { name: 'Details', exact: true }),
      ).toHaveCount(0);
      await expect(
        row.getByRole('button', { name: 'Dismiss alert', exact: true }),
      ).toHaveCount(0);
      await expect(row.getByTestId('codex-activity')).toHaveAttribute(
        'data-attention-kind',
        current.state === 'completed' ? 'none' : 'warning',
      );
    }
    await fitsWindow(manager);
    await capture(`${viewport.width}x${viewport.height}-activity-manager`);
    await choose(attention, touch);
    await fitsWindow(manager);
    const review = manager.getByRole('button', {
      name: 'Dismiss alert',
      exact: true,
    });
    await expect(review).toHaveCount(0);
    await expect(
      manager.locator(`[data-session-id="${examples[0].id}"]`),
    ).toHaveCount(0);
    await capture(
      `${viewport.width}x${viewport.height}-activity-manager-attention`,
    );

    for (const current of examples) {
      const manager = await openCodexManager(page, touch);
      await choose(
        manager.getByRole('button', { name: 'Unarchived', exact: true }),
        touch,
      );
      const row = manager.locator(
        `[data-session-id="${current.id}"][data-run-id="${current.runId}"]`,
      );
      await choose(
        row.getByRole('button', { name: 'Open', exact: true }),
        touch,
      );
      await expect(current.window).toHaveClass(/is-active/);
      await expect(
        current.window.getByTestId('codex-activity'),
      ).toHaveAttribute('data-activity-state', current.state);
      const review = current.window.getByRole('button', {
        name: 'Dismiss alert',
        exact: true,
      });
      await expect(review).toHaveCount(0);
      await expect(
        current.window.getByLabel('Codex terminal input'),
      ).toBeEditable();
      await expect(current.window.getByTestId('codex-screen')).toBeInViewport();
      await expect
        .poll(async () => {
          const host = (await current.window
            .getByTestId('codex-screen')
            .boundingBox())!;
          const cells = (await current.window
            .locator('.xterm-screen')
            .boundingBox())!;
          return Math.max(cells.width - host.width, cells.height - host.height);
        })
        .toBeLessThanOrEqual(1);
      await fitsWindow(current.window);
      await capture(
        `${viewport.width}x${viewport.height}-activity-${current.state}`,
      );
    }
  }

  // Failure controls have their own scenes so the primary comparison keeps
  // the original two named sessions and successful-completion scenario.
  const failed = await startCodex(page, touch);
  const metadata = await session(request, failed.id, failed.runId);
  const renamed = await request.patch(`/api/host/codex/sessions/${failed.id}`, {
    headers: { Origin: process.env.E2E_BASE_URL! },
    data: {
      revision: metadata.metadataRevision,
      title: 'Inspect a failed turn',
    },
  });
  expect(renamed.ok(), await renamed.text()).toBeTruthy();
  await runCodex(failed.window, 'message Inspect a failed turn');
  await runCodex(failed.window, 'activity fail');
  await expect(failed.window.getByTestId('codex-activity')).toHaveAttribute(
    'data-activity-state',
    'failed',
  );
  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 390, height: 844 },
  ]) {
    await page.setViewportSize(viewport);
    const manager = await openCodexManager(page, touch);
    await choose(
      manager.getByRole('button', { name: 'Unarchived', exact: true }),
      touch,
    );
    await choose(manager.getByTestId('codex-action-needed'), touch);
    const row = manager.locator(
      `[data-session-id="${failed.id}"][data-run-id="${failed.runId}"]`,
    );
    await expect(
      manager.getByTestId('codex-action-needed'),
    ).toHaveAccessibleName('Action needed (2)');
    await expect(
      row.getByRole('button', { name: 'Dismiss alert', exact: true }),
    ).toBeInViewport();
    await fitsWindow(manager);
    await capture(
      `${viewport.width}x${viewport.height}-activity-manager-failed`,
    );
    await choose(row.getByRole('button', { name: 'Open', exact: true }), touch);
    await expect(
      failed.window.getByRole('button', { name: 'Dismiss alert', exact: true }),
    ).toBeInViewport();
    await fitsWindow(failed.window);
    await capture(`${viewport.width}x${viewport.height}-activity-failed`);
  }
  await choose(
    failed.window.getByRole('button', { name: 'Dismiss alert', exact: true }),
    touch,
  );
  await expect(failed.window.getByTestId('codex-activity')).toHaveAttribute(
    'data-needs-attention',
    'false',
  );
});
