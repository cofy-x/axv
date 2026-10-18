import type { Locator, Page } from '@playwright/test';
import { activate } from './helpers';
import { choose, expect } from './codex-helpers';

export type DocumentTask = 'attach' | 'export' | 'details';

const taskLabels: Record<DocumentTask, string> = {
  attach: 'Attach',
  export: 'Export',
  details: 'Details',
};

export async function openDocumentTask(
  page: Page,
  editor: Locator,
  task: DocumentTask,
  touch = false,
) {
  await activate(page, editor);
  await choose(
    editor.getByRole('button', {
      name: 'More document actions',
      exact: true,
    }),
    touch,
  );
  await choose(
    page
      .locator('.ns-menu-layer')
      .getByRole('menuitem', { name: taskLabels[task], exact: true }),
    touch,
  );
  const activePanel = page.locator(
    '.ns-window.is-active[data-app-id="document-workflow"]',
  );
  await expect(activePanel).toBeVisible();
  const windowId = await activePanel.getAttribute('data-window-id');
  expect(windowId).toBeTruthy();
  const panel = page.locator(`[data-window-id="${windowId}"]`);
  await expect(panel).toBeVisible();
  await expect(panel).toHaveClass(/is-active/);
  return panel;
}

export interface AttachmentRun {
  id: string;
  runId: string;
  window: Locator;
}

export const contextEndpoint = ({
  id,
  runId,
}: Pick<AttachmentRun, 'id' | 'runId'>) =>
  `/api/host/codex/sessions/${id}/runs/${runId}/context`;

export const runAttachmentPanel = (
  page: Page,
  { id, runId }: Pick<AttachmentRun, 'id' | 'runId'>,
) => page.locator(`[data-window-id="conversation-context:${id}:${runId}"]`);

export async function openRunAttachments(
  page: Page,
  run: AttachmentRun,
  touch = false,
) {
  await activate(page, run.window);
  await choose(
    run.window.getByRole('button', { name: 'Attachments', exact: true }),
    touch,
  );
  const panel = runAttachmentPanel(page, run);
  await expect(panel).toBeVisible();
  await expect(panel).toHaveClass(/is-active/);
  return panel;
}

export async function selectAttachmentVersion(
  panel: Locator,
  entryId: string,
  version = 'saved',
  touch = false,
) {
  await choose(
    panel.getByRole('button', { name: 'Add document', exact: true }),
    touch,
  );
  await panel
    .getByRole('combobox', { name: 'Document', exact: true })
    .selectOption(entryId);
  const versions = panel.getByRole('combobox', {
    name: 'Version',
    exact: true,
  });
  await expect(versions).toHaveValue('saved');
  await versions.selectOption(version);
  return versions;
}

export async function openReceiptHistory(panel: Locator, touch = false) {
  const summary = panel.locator('summary').filter({ hasText: /^History/ });
  await expect(summary).toBeVisible();
  if (
    !(await summary.evaluate(
      (element) => (element.parentElement as HTMLDetailsElement).open,
    ))
  )
    await choose(summary, touch);

  return summary.locator('..');
}
