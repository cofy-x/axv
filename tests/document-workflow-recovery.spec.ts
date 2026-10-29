import { readFile } from 'node:fs/promises';
import type { APIRequestContext, Page } from '@playwright/test';
import {
  activate,
  actionMenu,
  createEntry,
  editorWindow,
  entries,
  expectedDownloadFilename,
  filesWindow,
  menuItem,
  nameDialog,
  openDocument,
  readContent,
} from './helpers';
import type { Entry } from './helpers';
import {
  endCodex,
  expect,
  projectPath,
  resumeCodex,
  session,
  startCodexInFolder,
  test,
} from './codex-helpers';
import {
  contextEndpoint,
  openDocumentTask,
  openReceiptHistory,
  openRunAttachments,
  runAttachmentPanel,
  selectAttachmentVersion,
} from './document-workflow-helpers';

const headers = { Origin: process.env.E2E_BASE_URL! };

interface Seed {
  entry: Entry;
  snapshot: { id: string; revision: number; name: string; sha256: string };
  project: { id: string; name: string; rootPath: string };
}

async function seed(request: APIRequestContext, name: string): Promise<Seed> {
  const entry = await createEntry(
    request,
    `E2E ${name}.md`,
    'file',
    'documents',
    '# Original snapshot\n\nRECOVERED_ORIGINAL',
  );
  const registered = await request.post('/api/projects', {
    headers,
    data: { rootPath: projectPath },
  });
  expect(registered.ok(), await registered.text()).toBeTruthy();
  const captured = await request.post(`/api/documents/${entry.id}/snapshots`, {
    headers,
    data: { revision: entry.revision },
  });
  expect(captured.ok(), await captured.text()).toBeTruthy();
  return {
    entry,
    project: (await registered.json()).project,
    snapshot: (await captured.json()).snapshot,
  };
}

async function openProject(page: Page) {
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await menuItem(page, 'Project Files').click();
  const launcher = page.locator('[data-window-id="project-files:projects"]');
  await launcher
    .getByLabel('Host project directory', { exact: true })
    .fill(projectPath);
  await launcher.getByRole('button', { name: 'Open', exact: true }).click();
  const browser = page.locator(
    '.ns-window.is-active[data-app-id="project-files"]:has(.project-browser)',
  );
  await expect(browser).toBeVisible();
  return browser;
}

test.beforeEach(async ({ isMobile }) => {
  test.skip(isMobile, 'Focused desktop recovery coverage.');
});

test('a permanently deleted document retains selectable snapshots and project export after refresh', async ({
  page,
  request,
}) => {
  const { entry, snapshot, project } = await seed(
    request,
    'Deleted document export',
  );
  await page.reload();
  const editor = await openDocument(page, entry);
  const text = editor.getByRole('textbox', { name: 'Document text' });
  const draft = '# Local deleted draft\n\nPRESERVE_THIS_DRAFT';
  await text.fill(draft);
  const panel = await openDocumentTask(page, editor, 'export');
  const versions = panel.getByRole('combobox', {
    name: 'Version',
    exact: true,
  });
  await expect(versions).toHaveValue('saved');
  expect(
    (await request.post(`/api/entries/${entry.id}/trash`)).ok(),
  ).toBeTruthy();
  expect((await request.delete(`/api/entries/${entry.id}`)).ok()).toBeTruthy();
  await panel.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(panel).toContainText(/source document.*unavailable/i);
  await expect(versions).toHaveValue('reviewed');
  await expect(
    panel.getByText('Newer saved version available.', { exact: false }),
  ).toHaveCount(0);
  await versions.selectOption(snapshot.id);
  await panel.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(versions).toBeEnabled();
  await expect(versions).toHaveValue(snapshot.id);
  await panel
    .getByRole('combobox', { name: 'Destination project', exact: true })
    .selectOption(project.id);
  const path = `E2E deleted-source-${entry.id}.md`;
  await panel.getByLabel('New file path', { exact: true }).fill(path);
  const exported = page.waitForResponse(
    (response) =>
      response.url().endsWith(`/api/projects/${project.id}/export`) &&
      response.request().method() === 'POST',
  );
  await panel
    .getByRole('group', { name: 'Saved version actions', exact: true })
    .getByRole('button', { name: 'Export', exact: true })
    .click();
  const result = await exported;
  expect(result.ok(), await result.text()).toBeTruthy();
  const file = await request.get(`/api/projects/${project.id}/file`, {
    params: { path },
  });
  expect((await file.json()).content).toBe(
    '# Original snapshot\n\nRECOVERED_ORIGINAL',
  );
  await activate(page, filesWindow(page));
  await actionMenu(
    page,
    filesWindow(page),
    'More file actions',
    'Refresh Folder',
  );
  await activate(page, editor);
  await expect(editor).toContainText('Your draft remains available');
  await expect(text).toHaveValue(draft);
  const saveCopy = editor.getByRole('button', {
    name: 'Save Copy',
    exact: true,
  });
  await expect(saveCopy).toBeInViewport();
  await saveCopy.click();
  const copyDialog = page.getByRole('dialog', {
    name: 'Save Copy',
    exact: true,
  });
  await copyDialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(copyDialog).toBeHidden();
  await expect(text).toHaveValue(draft);
  await expect(editor.getByTestId('save-status')).toHaveText('Unsaved changes');
  const copiesBefore = await entries(request);
  await actionMenu(page, editor, 'More document actions', 'Save Copy');
  const copyName = `E2E preserved-deleted-${entry.id}.md`;
  await nameDialog(page, copyName);
  const copy = (await entries(request)).find((item) => item.name === copyName);
  expect(copy).toBeDefined();
  expect(copy!.parentId).toBe('documents');
  expect(await readContent(request, copy!.id)).toBe(draft);
  expect((await entries(request)).length).toBe(copiesBefore.length + 1);
  await expect(text).toHaveValue(draft);
  const copyEditor = editorWindow(page, copy!.id);
  await expect(
    copyEditor.getByRole('textbox', { name: 'Document text' }),
  ).toHaveValue(draft);
  await copyEditor
    .getByRole('button', {
      name: 'More document actions',
      exact: true,
    })
    .click();
  const staleMenu = page.locator('.ns-menu-layer').getByRole('menu', {
    name: copyName,
    exact: true,
  });
  await expect(staleMenu).toBeVisible();
  const staleSaveCopy = await staleMenu
    .getByRole('menuitem', { name: 'Save Copy', exact: true })
    .elementHandle();
  expect(staleSaveCopy).not.toBeNull();
  // Removing the owner dismisses its content menu. A retained DOM reference
  // must not create a copy or redirect the old action to another editor.
  await copyEditor.locator('.ns-close').evaluate((button) => {
    (button as HTMLButtonElement).click();
  });
  await expect(copyEditor).toHaveCount(0);
  await expect(page.locator('.ns-menu-layer')).toHaveCount(0);
  await staleSaveCopy!.evaluate((button) =>
    (button as HTMLButtonElement).click(),
  );
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect((await entries(request)).length).toBe(copiesBefore.length + 1);
  await page.reload();
  await activate(page, panel);
  await expect(panel).toContainText(/source document.*unavailable/i);
  await panel
    .getByRole('combobox', { name: 'Version', exact: true })
    .selectOption(snapshot.id);
  await panel
    .locator('summary')
    .filter({ hasText: /^Preview$/ })
    .click();
  await expect(
    panel.getByRole('heading', { name: 'Original snapshot', exact: true }),
  ).toBeVisible();
});

test('an import with a lost response keeps its request identity through A to B to A and reopening', async ({
  page,
  request,
}) => {
  const { entry, snapshot, project } = await seed(
    request,
    'Import receipt recovery',
  );
  const names = {
    a: `E2E receipt-A-${entry.id}.md`,
    b: `E2E receipt-B-${entry.id}.md`,
  };
  for (const path of Object.values(names)) {
    const exported = await request.post(`/api/projects/${project.id}/export`, {
      headers,
      data: { requestId: `seed-${path}`, snapshotId: snapshot.id, path },
    });
    expect(exported.ok(), await exported.text()).toBeTruthy();
  }
  await page.reload();
  let browser = await openProject(page);
  const calls: { path: string; requestId: string }[] = [];
  let committedEntryId = '';
  let loseFirst = true;
  const endpoint = `/api/projects/${project.id}/import`;
  await page.route(`**${endpoint}`, async (route) => {
    const payload = route.request().postDataJSON() as {
      path: string;
      requestId: string;
    };
    calls.push(payload);
    if (payload.path === names.a && loseFirst) {
      loseFirst = false;
      const response = await route.fetch();
      expect(response.ok(), await response.text()).toBeTruthy();
      committedEntryId = (await response.json()).entry.id as string;
      await route.abort('failed');
    } else await route.continue();
  });
  await browser
    .locator('.project-directory')
    .getByRole('button', { name: names.a, exact: true })
    .click();
  await browser
    .getByRole('button', { name: 'Copy to Documents', exact: true })
    .click();
  await expect(browser.getByRole('alert')).toBeVisible();
  expect(committedEntryId).toBeTruthy();
  await browser
    .locator('.project-directory')
    .getByRole('button', { name: names.b, exact: true })
    .click();
  const copiedB = page.waitForResponse(
    (response) =>
      response.url().endsWith(endpoint) &&
      response.request().method() === 'POST',
  );
  await browser
    .getByRole('button', { name: 'Copy to Documents', exact: true })
    .click();
  const responseB = await copiedB;
  expect(responseB.ok(), await responseB.text()).toBeTruthy();
  const entryB = (await responseB.json()).entry as Entry;
  await expect(editorWindow(page, entryB.id).locator('textarea')).toHaveValue(
    '# Original snapshot\n\nRECOVERED_ORIGINAL',
  );
  // Closing the browser discards its hook instance. The reopened component must
  // recover A's unresolved request from this database's sessionStorage scope.
  const browserId = `project-files:${project.id}`;
  browser = page.locator(`[data-window-id="${browserId}"]`);
  await activate(page, browser);
  await browser.getByRole('button', { name: /^Close / }).click();
  browser = await openProject(page);
  await browser
    .locator('.project-directory')
    .getByRole('button', { name: names.a, exact: true })
    .click();
  const copiedA = page.waitForResponse(
    (response) =>
      response.url().endsWith(endpoint) &&
      response.request().method() === 'POST',
  );
  await browser
    .getByRole('button', { name: 'Copy to Documents', exact: true })
    .click();
  const responseA = await copiedA;
  expect(responseA.ok(), await responseA.text()).toBeTruthy();
  expect((await responseA.json()).entry.id).toBe(committedEntryId);
  expect(calls.map((call) => call.path)).toEqual([names.a, names.b, names.a]);
  expect(calls[2].requestId).toBe(calls[0].requestId);
  expect(calls[1].requestId).not.toBe(calls[0].requestId);
  const saved = await entries(request);
  expect(saved.filter((item) => item.name === names.a)).toHaveLength(1);
  expect(
    saved.filter((item) => item.name.startsWith(names.a.replace(/\.md$/, ''))),
  ).toHaveLength(1);
});

test('attachment replay feedback reflects accepted cancelled and unknown receipts', async ({
  page,
  request,
}) => {
  const { entry, snapshot } = await seed(request, 'Attachment replay status');
  await page.reload();
  const codex = await startCodexInFolder(page);
  const initialized = await session(request, codex.id, codex.runId);
  const endpoint = contextEndpoint(codex);
  const receipts = [
    ['accepted', 'Delivered'],
    ['cancelled', 'Cancelled'],
    ['unknown', 'Delivery unconfirmed'],
    ['accepted', 'Delivered'],
  ] as const;
  const requestIds: string[] = [];
  const returned = new Map<string, object>();
  await page.route(`**${endpoint}`, async (route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        json: { batches: [...returned.values()] },
      });
      return;
    }
    if (route.request().method() !== 'POST') {
      await route.continue();
      return;
    }
    const payload = route.request().postDataJSON() as { requestId: string };
    const status = receipts[requestIds.length][0];
    requestIds.push(payload.requestId);
    const batch = {
      id: `replayed-${payload.requestId}`,
      requestId: payload.requestId,
      sessionId: codex.id,
      runId: codex.runId,
      providerThreadId: initialized.conversationId,
      status,
      snapshots: [{ ...snapshot, entryId: entry.id }],
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    };
    returned.set(payload.requestId, batch);
    await route.fulfill({ status: 200, json: { batch } });
  });
  let panel = await openRunAttachments(page, codex);
  for (const [index, [status, label]] of receipts.entries()) {
    if (index > 0) {
      await panel.getByRole('button', { name: /^Close / }).click();
      panel = await openRunAttachments(page, codex);
    }
    await selectAttachmentVersion(panel, entry.id, snapshot.id);
    await panel.getByRole('button', { name: 'Attach', exact: true }).click();
    await expect(
      panel.getByRole('button', { name: 'Add document', exact: true }),
    ).toBeFocused();
    if (status === 'unknown') {
      const receipt = panel.getByRole('article').filter({ hasText: label });
      await expect(receipt.getByText(label, { exact: true })).toBeVisible();
      await expect(receipt).toContainText(
        /not.*(?:resent|sent again).*automatically/i,
      );
      await expect(
        panel.getByRole('button', { name: 'Cancel Attachment', exact: true }),
      ).toHaveCount(0);
      await panel.getByRole('button', { name: 'Refresh', exact: true }).click();
      await expect(receipt.getByText(label, { exact: true })).toBeVisible();
      expect(requestIds).toHaveLength(index + 1);
    } else {
      const history = await openReceiptHistory(panel);
      await expect(
        history.getByText(label, { exact: true }).first(),
      ).toBeVisible();
    }
  }
  expect(requestIds).toHaveLength(4);
  expect(requestIds[3]).toBe(requestIds[2]);
  await activate(page, codex.window);
  await endCodex(page, codex.window);
});

test('project download reports API failures inline and preserves the original file bytes', async ({
  page,
  request,
  browserName,
}) => {
  const { entry, snapshot, project } = await seed(
    request,
    'Project download recovery',
  );
  const path = `E2E download-${entry.id}.md`;
  const expectedFilename = expectedDownloadFilename(path, browserName);
  const exported = await request.post(`/api/projects/${project.id}/export`, {
    headers,
    data: {
      requestId: `download-seed-${entry.id}`,
      snapshotId: snapshot.id,
      path,
    },
  });
  expect(exported.ok(), await exported.text()).toBeTruthy();
  await page.reload();
  const browser = await openProject(page);
  await browser
    .locator('.project-directory')
    .getByRole('button', { name: path, exact: true })
    .click();
  await expect(
    browser.getByRole('heading', { name: 'Original snapshot' }),
  ).toBeVisible();
  const originalURL = page.url();
  let downloads = 0;
  page.on('download', () => downloads++);
  await page.route(
    `**/api/projects/${project.id}/download?*`,
    async (route) => {
      await route.fulfill({
        status: 409,
        json: {
          code: 'file_changed',
          error:
            'This file changed before the download. Refresh and try again.',
        },
      });
    },
    { times: 1 },
  );
  const more = browser.getByRole('button', {
    name: 'More project actions',
    exact: true,
  });
  await actionMenu(page, browser, 'More project actions', 'Download');
  await expect(browser.getByRole('alert')).toHaveText(
    'This file changed before the download. Refresh and try again.',
  );
  await expect(page).toHaveURL(originalURL);
  await expect(browser).toBeVisible();
  await expect(more).toBeEnabled();
  expect(downloads).toBe(0);
  await more.click();
  const downloadItem = page
    .locator('.ns-menu-layer')
    .getByRole('menuitem', { name: 'Download', exact: true });
  await expect(downloadItem).toBeEnabled();
  const downloaded = page.waitForEvent('download');
  await downloadItem.click();
  const download = await downloaded;
  expect(download.suggestedFilename()).toBe(expectedFilename);
  const saved = await download.path();
  expect(saved).not.toBeNull();
  expect(await readFile(saved!)).toEqual(
    Buffer.from('# Original snapshot\n\nRECOVERED_ORIGINAL'),
  );
  expect(downloads).toBe(1);
  await expect(page).toHaveURL(originalURL);
  await expect(browser.getByRole('alert')).toHaveCount(0);

  await page.route(`**/api/projects/${project.id}/file?*`, async (route) => {
    await route.fulfill({
      status: 415,
      json: {
        code: 'unsupported_file',
        error: 'This file cannot be previewed.',
      },
    });
  });
  await browser.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(browser.getByRole('alert')).toHaveText(
    'This file cannot be previewed.',
  );
  await expect(
    browser.getByRole('button', { name: 'Copy to Documents', exact: true }),
  ).toBeDisabled();
  const fallbackDownload = browser.getByRole('button', {
    name: 'Download',
    exact: true,
  });
  await expect(fallbackDownload).toBeInViewport();
  const fallbackDownloaded = page.waitForEvent('download');
  await fallbackDownload.click();
  const fallback = await fallbackDownloaded;
  expect(fallback.suggestedFilename()).toBe(expectedFilename);
  const fallbackPath = await fallback.path();
  expect(fallbackPath).not.toBeNull();
  expect(await readFile(fallbackPath!)).toEqual(
    Buffer.from('# Original snapshot\n\nRECOVERED_ORIGINAL'),
  );
  expect(downloads).toBe(2);
  await expect(browser.getByRole('alert')).toHaveText(
    'This file cannot be previewed.',
  );

  await page.evaluate(() => {
    const attempts: string[] = [];
    Reflect.set(globalThis, 'closedProjectDownloadRequests', attempts);
    const fetch = globalThis.fetch;
    globalThis.fetch = (...args) => {
      const input = args[0];
      const url = input instanceof Request ? input.url : String(input);
      if (url.includes('/download?')) attempts.push(url);
      return fetch(...args);
    };
  });
  await more.click();
  await expect(downloadItem).toBeVisible();
  // Retain the menu while closing its owner to exercise the stale action.
  await browser.locator('.ns-close').evaluate((button) => {
    (button as HTMLButtonElement).click();
  });
  await expect(browser).toHaveCount(0);
  await downloadItem.click();
  await expect(page.locator('.ns-menu-layer')).toHaveCount(0);
  expect(
    await page.evaluate(() =>
      Reflect.get(globalThis, 'closedProjectDownloadRequests'),
    ),
  ).toEqual([]);
  expect(downloads).toBe(2);
});

test('a fixed-run attachment panel never moves pending context to a continued run', async ({
  page,
  request,
}) => {
  const { entry, snapshot } = await seed(request, 'Exact run continuation');
  await page.reload();
  const codex = await startCodexInFolder(page);
  const original = await session(request, codex.id, codex.runId);
  const endpoint = contextEndpoint(codex);
  const queued = await request.post(endpoint, {
    headers,
    data: { requestId: `original-run-${entry.id}`, snapshotIds: [snapshot.id] },
  });
  expect(queued.ok(), await queued.text()).toBeTruthy();
  const panel = await openRunAttachments(page, codex);
  await expect(
    panel.getByRole('button', { name: 'Cancel Attachment', exact: true }),
  ).toBeVisible();
  await selectAttachmentVersion(panel, entry.id, snapshot.id);
  const sent: string[] = [];
  page.on('request', (request) => {
    if (request.method() === 'POST' && request.url().endsWith('/context'))
      sent.push(request.url());
  });
  const ended = await request.post(
    `/api/host/codex/sessions/${codex.id}/runs/${codex.runId}/end`,
    { headers },
  );
  expect(ended.ok(), await ended.text()).toBeTruthy();
  await expect
    .poll(async () => (await session(request, codex.id, codex.runId)).status)
    .toBe('ended');
  const continued = await request.post(
    `/api/host/codex/sessions/${codex.id}/resume`,
    {
      headers,
      data: {
        requestId: `continued-run-${entry.id}`,
        expectedRunId: codex.runId,
      },
    },
  );
  expect(continued.ok(), await continued.text()).toBeTruthy();
  const next = (await continued.json()).session as {
    id: string;
    runId: string;
  };
  expect(next.id).toBe(codex.id);
  expect(next.runId).not.toBe(codex.runId);
  await expect
    .poll(async () => (await session(request, next.id, next.runId)).status)
    .toBe('running');
  expect((await session(request, next.id, next.runId)).conversationId).toBe(
    original.conversationId,
  );
  await panel.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(runAttachmentPanel(page, codex)).toHaveClass(/is-active/);
  await expect(panel.getByText('Not delivered', { exact: true })).toBeVisible();
  await expect(
    panel.getByRole('combobox', { name: 'Conversation', exact: true }),
  ).toHaveCount(0);
  await expect(
    panel.getByRole('button', { name: 'Attach', exact: true }),
  ).toBeDisabled();
  expect(sent).toEqual([]);
  const nextWindow = await resumeCodex(page, next.id, next.runId);
  await expect(nextWindow).toHaveClass(/is-active/);
  await nextWindow
    .getByRole('button', { name: 'Attachments', exact: true })
    .click();
  const nextPanel = runAttachmentPanel(page, next);
  await expect(nextPanel).toHaveClass(/is-active/);
  await expect(nextPanel).toContainText('No documents attached.');
  const context = await request.get(contextEndpoint(next));
  expect(context.ok()).toBeTruthy();
  expect((await context.json()).batches).toEqual([]);
  await nextPanel
    .getByRole('button', { name: 'Open Codex', exact: true })
    .click();
  await expect(nextWindow).toHaveClass(/is-active/);
  await endCodex(page, nextWindow);
});

test('an unavailable Codex list does not block saved versions or project export controls', async ({
  page,
  request,
}) => {
  const { entry, snapshot, project } = await seed(
    request,
    'Independent document actions',
  );
  await page.route('**/api/host/codex/sessions', async (route) => {
    await route.fulfill({
      status: 503,
      json: { code: 'server_stopping', error: 'Agent service is unavailable.' },
    });
  });
  await page.reload();
  const editor = await openDocument(page, entry);
  const panel = await openDocumentTask(page, editor, 'export');
  await panel
    .getByRole('combobox', { name: 'Version', exact: true })
    .selectOption(snapshot.id);
  await panel
    .locator('summary')
    .filter({ hasText: /^Preview$/ })
    .click();
  await expect(
    panel.getByRole('heading', { name: 'Original snapshot', exact: true }),
  ).toBeVisible();
  await panel
    .getByRole('combobox', { name: 'Destination project', exact: true })
    .selectOption(project.id);
  const exportPath = `review-${entry.id}.md`;
  await panel.getByLabel('New file path', { exact: true }).fill(exportPath);
  const tasks = panel.getByRole('tablist', {
    name: 'Document actions',
    exact: true,
  });
  await tasks.getByRole('tab', { name: 'Details', exact: true }).click();
  await expect(
    panel.getByRole('button', { name: 'Keep version', exact: true }),
  ).toBeEnabled();
  const exportTab = tasks.getByRole('tab', { name: 'Export', exact: true });
  await exportTab.click();
  const endpoint = `/api/projects/${project.id}/export`;
  let releaseExport!: () => void;
  const gate = new Promise<void>((resolve) => {
    releaseExport = resolve;
  });
  let exportStarted!: () => void;
  const started = new Promise<void>((resolve) => {
    exportStarted = resolve;
  });
  await page.route(`**${endpoint}`, async (route) => {
    if (route.request().method() === 'POST') {
      exportStarted();
      await gate;
    }
    await route.continue();
  });
  const exported = page.waitForResponse(
    (response) =>
      response.url().endsWith(endpoint) &&
      response.request().method() === 'POST',
  );
  try {
    await panel
      .getByRole('group', { name: 'Saved version actions', exact: true })
      .getByRole('button', { name: 'Export', exact: true })
      .click();
    await started;
    for (const label of ['Attach', 'Export', 'Details'])
      await expect(
        tasks.getByRole('tab', { name: label, exact: true }),
      ).toBeDisabled();
    await expect(exportTab).toHaveAttribute('aria-selected', 'true');
    await expect(
      panel.getByRole('combobox', { name: 'Version', exact: true }),
    ).toBeDisabled();
    await expect(
      panel.getByRole('combobox', { name: 'Destination project', exact: true }),
    ).toBeDisabled();
    await expect(
      panel.getByLabel('New file path', { exact: true }),
    ).toBeDisabled();
    releaseExport();
    const result = await exported;
    expect(result.ok(), await result.text()).toBeTruthy();
    const file = await request.get(`/api/projects/${project.id}/file`, {
      params: { path: exportPath },
    });
    expect(file.ok()).toBeTruthy();
    expect((await file.json()).content).toBe(
      '# Original snapshot\n\nRECOVERED_ORIGINAL',
    );
  } finally {
    releaseExport();
  }
});

test('a changed saved revision requires explicit review before attaching to the same run', async ({
  page,
  request,
}) => {
  const { entry } = await seed(request, 'Reviewed attachment revision');
  await page.reload();
  const codex = await startCodexInFolder(page);
  const panel = await openRunAttachments(page, codex);
  const versions = await selectAttachmentVersion(panel, entry.id);
  await expect(versions.locator('option:checked')).toHaveText(
    `Saved revision ${entry.revision}`,
  );
  const updatedContent = '# Externally saved version\n\nEXTERNAL_SAVED_V2';
  const updated = await request.patch(`/api/entries/${entry.id}`, {
    headers,
    data: { revision: entry.revision, content: updatedContent },
  });
  expect(updated.ok(), await updated.text()).toBeTruthy();
  const nextRevision = (await updated.json()).entry.revision as number;
  expect(nextRevision).toBe(entry.revision + 1);
  const snapshotEndpoint = `/api/documents/${entry.id}/snapshots`;
  const endpoint = contextEndpoint(codex);
  const capturedRevisions: number[] = [];
  let releaseCapture!: () => void;
  const captureGate = new Promise<void>((resolve) => {
    releaseCapture = resolve;
  });
  let captureStarted!: () => void;
  const capturing = new Promise<void>((resolve) => {
    captureStarted = resolve;
  });
  let releaseAttach!: () => void;
  const attachGate = new Promise<void>((resolve) => {
    releaseAttach = resolve;
  });
  let attachStarted!: () => void;
  const attaching = new Promise<void>((resolve) => {
    attachStarted = resolve;
  });
  let contextPosts = 0;
  await page.route(`**${snapshotEndpoint}`, async (route) => {
    if (route.request().method() === 'POST') {
      capturedRevisions.push(
        (route.request().postDataJSON() as { revision: number }).revision,
      );
      if (capturedRevisions.length === 1) {
        captureStarted();
        await captureGate;
      }
    }
    await route.continue();
  });
  await page.route(`**${endpoint}`, async (route) => {
    if (route.request().method() === 'POST') {
      contextPosts++;
      attachStarted();
      await attachGate;
    }
    await route.continue();
  });
  const expectBusy = async () => {
    await expect(
      panel.getByRole('combobox', { name: 'Document', exact: true }),
    ).toBeDisabled();
    await expect(
      panel.getByRole('combobox', { name: 'Version', exact: true }),
    ).toBeDisabled();
    await expect(
      panel.getByRole('button', { name: 'Cancel', exact: true }),
    ).toBeDisabled();
  };
  try {
    const conflict = page.waitForResponse(
      (response) =>
        response.url().endsWith(snapshotEndpoint) &&
        response.request().method() === 'POST',
    );
    await panel.getByRole('button', { name: 'Attach', exact: true }).click();
    await capturing;
    await expectBusy();
    releaseCapture();
    const rejected = await conflict;
    expect(rejected.status()).toBe(409);
    expect((await rejected.json()).code).toBe('revision_conflict');
    await expect(panel.getByRole('alert')).toBeVisible();
    await expect(
      panel.getByRole('button', { name: 'Attach', exact: true }),
    ).toBeEnabled();
    await expect(versions.locator('option:checked')).toHaveText(
      `Saved revision ${entry.revision}`,
    );
    expect(contextPosts).toBe(0);
    await panel.getByRole('button', { name: 'Refresh', exact: true }).click();
    await expect(versions).toHaveValue('reviewed');
    await expect(versions.locator('option:checked')).toHaveText(
      `Reviewed saved revision ${entry.revision}`,
    );
    await expect(versions.locator('option[value="saved"]')).toHaveText(
      `Saved revision ${nextRevision}`,
    );
    await panel
      .locator('summary')
      .filter({ hasText: /^Preview$/ })
      .click();
    await expect(
      panel.getByRole('heading', { name: 'Original snapshot', exact: true }),
    ).toBeVisible();
    await expect(panel).not.toContainText('EXTERNAL_SAVED_V2');
    await versions.selectOption('saved');
    await expect(
      panel.getByRole('heading', {
        name: 'Externally saved version',
        exact: true,
      }),
    ).toBeVisible();
    const accepted = page.waitForResponse(
      (response) =>
        response.url().endsWith(endpoint) &&
        response.request().method() === 'POST',
    );
    await panel.getByRole('button', { name: 'Attach', exact: true }).click();
    await attaching;
    await expectBusy();
    releaseAttach();
    const queued = await accepted;
    expect(queued.ok(), await queued.text()).toBeTruthy();
    const batch = (await queued.json()).batch as {
      runId: string;
      snapshots: { id: string; revision: number }[];
    };
    expect(batch.runId).toBe(codex.runId);
    expect(batch.snapshots).toHaveLength(1);
    expect(batch.snapshots[0].revision).toBe(nextRevision);
    const savedSnapshot = await request.get(
      `/api/document-snapshots/${batch.snapshots[0].id}`,
    );
    expect(savedSnapshot.ok()).toBeTruthy();
    expect((await savedSnapshot.json()).snapshot.content).toBe(updatedContent);
    expect(capturedRevisions).toEqual([entry.revision, nextRevision]);
    expect(contextPosts).toBe(1);
    await expect(
      panel.getByRole('button', { name: 'Add document', exact: true }),
    ).toBeFocused();
  } finally {
    releaseCapture();
    releaseAttach();
  }
  await activate(page, codex.window);
  await endCodex(page, codex.window);
});

test('an older delivery poll cannot replace a newer confirmed receipt', async ({
  page,
  request,
}) => {
  const { entry, snapshot } = await seed(request, 'Delivery poll ordering');
  await page.reload();
  const codex = await startCodexInFolder(page);
  const initialized = await session(request, codex.id, codex.runId);
  const endpoint = `/api/host/codex/sessions/${codex.id}/runs/${codex.runId}/context`;
  const queued = await request.post(endpoint, {
    headers,
    data: { requestId: `poll-order-${entry.id}`, snapshotIds: [snapshot.id] },
  });
  expect(queued.ok(), await queued.text()).toBeTruthy();
  const batch = (await queued.json()).batch;
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  let first = true;
  let staleStarted!: () => void;
  const started = new Promise<void>((resolve) => {
    staleStarted = resolve;
  });
  let staleFinished!: () => void;
  const finished = new Promise<void>((resolve) => {
    staleFinished = resolve;
  });
  await page.route(`**${endpoint}`, async (route) => {
    if (route.request().method() !== 'GET') {
      await route.continue();
      return;
    }
    if (first) {
      first = false;
      staleStarted();
      await gate;
      try {
        await route
          .fulfill({ status: 200, json: { batches: [batch] } })
          .catch((error: unknown) => {
            if (!route.request().failure()) throw error;
          });
      } finally {
        staleFinished();
      }
      return;
    }
    await route.fulfill({
      status: 200,
      json: {
        batches: [
          {
            ...batch,
            status: 'accepted',
            providerThreadId: initialized.conversationId,
            providerTurnId: 'confirmed-turn',
          },
        ],
      },
    });
  });
  try {
    await codex.window
      .getByRole('button', { name: 'Attachments', exact: true })
      .click();
    const panel = runAttachmentPanel(page, codex);
    await started;
    await panel.getByRole('button', { name: 'Refresh', exact: true }).click();
    await openReceiptHistory(panel);
    await expect(panel.getByText('Delivered', { exact: true })).toBeVisible();
    release();
    await finished;
    await page.evaluate(
      () =>
        new Promise<void>((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
        ),
    );
    await expect(panel.getByText('Delivered', { exact: true })).toBeVisible({
      timeout: 500,
    });
    await expect(
      panel.getByRole('button', { name: 'Cancel Attachment', exact: true }),
    ).toHaveCount(0);
  } finally {
    release();
  }
  await activate(page, codex.window);
  await endCodex(page, codex.window);
});
