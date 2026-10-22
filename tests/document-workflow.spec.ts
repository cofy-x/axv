import { randomUUID } from 'node:crypto';
import { readFile, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { stripVTControlCharacters } from 'node:util';
import type { Page } from '@playwright/test';
import {
  activate,
  actionMenu,
  createEntry,
  editorWindow,
  menuItem,
  openDocument,
  readContent,
} from './helpers';
import {
  choose,
  endCodex,
  expect,
  projectPath,
  runCodex,
  startCodexInFolder,
  test,
} from './codex-helpers';
import {
  contextEndpoint,
  openDocumentTask,
  openReceiptHistory,
  openRunAttachments,
  selectAttachmentVersion,
} from './document-workflow-helpers';
import {
  codeEditor,
  copyEditorText,
  expectEditorText,
} from './code-editor-helpers';

interface ProjectIdentity {
  id: string;
  rootPath: string;
  name: string;
}

interface Snapshot {
  id: string;
  content: string;
  revision: number;
  sha256: string;
}

interface Batch {
  id: string;
  runId: string;
  status: string;
  providerTurnId?: string;
  snapshots: { id: string; revision: number }[];
}

async function openProjectLauncher(page: Page, touch: boolean) {
  await choose(
    page.getByRole('button', { name: 'Workspace menu', exact: true }),
    touch,
  );
  await choose(menuItem(page, 'Applications'), touch);
  await choose(menuItem(page, 'Project Files'), touch);
  const launcher = page.locator(
    '.ns-window[data-window-id="project-files:projects"]',
  );
  await expect(launcher).toBeVisible();
  return launcher;
}

test.setTimeout(120_000);

test('host source highlighting remains read-only and treats HTML as literal text', async ({
  page,
  isMobile,
}) => {
  const suffix = randomUUID();
  const samples = [
    {
      name: `readonly-${suffix}.ts`,
      content: [
        '// Host source is displayed without running it.',
        'export const markup = \'<script>globalThis.axvHostSourceExecuted = true</script><img src="/axv-host-source-probe" onerror="globalThis.axvHostSourceExecuted = true">\';',
        'export const enabled: boolean = true;',
        '',
      ].join('\n'),
      highlighted: true,
    },
    {
      name: `readonly-${suffix}.axvtext`,
      content:
        '<script>globalThis.axvHostSourceExecuted = true</script>\nUnknown extension: keep this source as plain text.\n',
      highlighted: false,
    },
  ];
  const paths = samples.map((sample) => join(projectPath, sample.name));
  const createdPaths: string[] = [];
  const probeRequests: string[] = [];
  page.on('request', (request) => {
    if (new URL(request.url()).pathname === '/axv-host-source-probe')
      probeRequests.push(request.url());
  });
  try {
    for (const [index, sample] of samples.entries()) {
      await writeFile(paths[index], sample.content, { flag: 'wx' });
      createdPaths.push(paths[index]);
    }
    const launcher = await openProjectLauncher(page, isMobile);
    await launcher
      .getByLabel('Host project directory', { exact: true })
      .fill(projectPath);
    const registered = page.waitForResponse(
      (response) =>
        response.url().endsWith('/api/projects') &&
        response.request().method() === 'POST',
    );
    await choose(
      launcher.getByRole('button', { name: 'Open', exact: true }),
      isMobile,
    );
    const registration = await registered;
    expect(registration.ok(), await registration.text()).toBeTruthy();
    const project = (await registration.json()).project as ProjectIdentity;
    const browser = page.locator(
      `[data-window-id="project-files:${project.id}"]`,
    );
    for (const [index, sample] of samples.entries()) {
      if (index && isMobile)
        await choose(
          browser.getByRole('button', { name: 'Back', exact: true }),
          true,
        );
      await choose(
        browser.getByRole('button', { name: sample.name, exact: true }),
        isMobile,
      );
      const source = codeEditor(browser, 'Project file source');
      await expect(source).toBeVisible();
      await expect(source).toHaveAttribute('aria-readonly', 'true');
      await expectEditorText(source, sample.content);
      expect(await copyEditorText(source)).toBe(sample.content);
      if (sample.highlighted)
        await expect
          .poll(() =>
            source
              .locator('span')
              .evaluateAll(
                (spans) =>
                  new Set(
                    spans
                      .filter((span) => span.textContent)
                      .map((span) => getComputedStyle(span).color),
                  ).size,
              ),
          )
          .toBeGreaterThan(1);
      await source.press('ControlOrMeta+End');
      await source.pressSequentially('READ_ONLY_PROBE');
      await expectEditorText(source, sample.content);
      await expect(
        browser
          .getByRole('region', { name: 'Project file preview' })
          .locator('script, img'),
      ).toHaveCount(0);
      expect(
        await page.evaluate(() =>
          Reflect.get(globalThis, 'axvHostSourceExecuted'),
        ),
      ).toBeUndefined();
      expect(await readFile(paths[index])).toEqual(Buffer.from(sample.content));
    }
    expect(probeRequests).toEqual([]);
  } finally {
    await Promise.all(createdPaths.map((path) => rm(path, { force: true })));
  }
});

for (const width of [1440, 390]) {
  test(`saved Markdown follows the host project and exact Codex run at ${width}px`, async ({
    page,
    request,
    isMobile,
  }, info) => {
    test.skip(
      isMobile && width !== 390,
      'Touch projects cover the narrow flow.',
    );
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
    const captureScene = async (name: string) => {
      const path = info.outputPath(`${width}-${name}.png`);
      await page.screenshot({ path, fullPage: true });
      await info.attach(name, { path, contentType: 'image/png' });
    };
    const source = [
      '# Plan V1',
      '',
      'IMMUTABLE_SNAPSHOT_V1',
      '',
      '| Task | State |',
      '| --- | --- |',
      '| Review | Ready |',
      '',
      '[Docs](https://example.com/guide)',
      '[Relative](./guide.md)',
      '![Private image](https://example.invalid/markdown-preview-probe.png)',
      '',
      '<script>globalThis.axvPreviewExecuted = true</script>',
      '',
    ].join('\n');
    const suffix = width === 1440 ? 'markdown' : 'md';
    const entry = await createEntry(
      request,
      `E2E Document workflow ${width}.${suffix}`,
      'file',
      'documents',
      source,
    );
    await page.reload();
    const editor = await openDocument(page, entry);
    const text = editor.locator('textarea[aria-label="Document text"]');
    await text.press('ControlOrMeta+End');
    await text.pressSequentially('UNSAVED_DRAFT');
    const draft = await text.inputValue();
    expect(draft).toContain('UNSAVED_DRAFT');
    const more = editor.getByRole('button', {
      name: 'More document actions',
      exact: true,
    });
    await expect(more).toBeInViewport();
    await more.focus();
    await more.press('Enter');
    await expect(
      page.locator('.ns-menu-layer').getByRole('menu', {
        name: entry.name,
        exact: true,
      }),
    ).toBeVisible();
    for (const label of ['Attach', 'Export', 'Details', 'Save Copy', 'Reload'])
      await expect(
        page
          .locator('.ns-menu-layer')
          .getByRole('menuitem', { name: label, exact: true }),
      ).toBeInViewport();
    await captureScene('editor-actions-menu');
    await page.keyboard.press('Escape');
    await expect(more).toBeFocused();
    await expect(text).toHaveValue(draft);
    await expect(editor.getByTestId('save-status')).toHaveText(
      'Unsaved changes',
    );
    expect(await readContent(request, entry.id)).toBe(source);
    await text.evaluate((element) => {
      const input = element as HTMLTextAreaElement;
      input.dataset.mountProbe = 'retained-markdown-editor';
      input.setSelectionRange(2, 7);
    });
    const imageRequests: string[] = [];
    page.on('request', (request) => {
      if (request.url().includes('markdown-preview-probe'))
        imageRequests.push(request.url());
    });
    const views = editor.getByRole('group', {
      name: 'Document view',
      exact: true,
    });
    const edit = views.getByRole('button', { name: 'Edit', exact: true });
    const previewButton = views.getByRole('button', {
      name: 'Preview',
      exact: true,
    });
    await expect(edit).toHaveAttribute('aria-pressed', 'true');
    await expect(previewButton).toHaveAttribute('aria-pressed', 'false');
    if (isMobile) await choose(previewButton, true);
    else {
      await previewButton.focus();
      await previewButton.press('Enter');
    }
    await expect(previewButton).toHaveAttribute('aria-pressed', 'true');
    await expect(edit).toHaveAttribute('aria-pressed', 'false');
    const preview = editor.getByRole('region', { name: 'Markdown preview' });
    await expect(
      preview.getByRole('heading', { name: 'Plan V1' }),
    ).toBeVisible();
    await expect(preview.getByRole('table')).toBeVisible();
    await expect(preview.getByRole('link', { name: 'Docs' })).toHaveAttribute(
      'rel',
      'noopener noreferrer',
    );
    await expect(preview.getByRole('link', { name: 'Relative' })).toHaveCount(
      0,
    );
    await expect(preview.locator('img, script')).toHaveCount(0);
    await expect(preview).toContainText('images are not loaded');
    expect(
      await page.evaluate(() => Reflect.get(globalThis, 'axvPreviewExecuted')),
    ).toBeUndefined();
    expect(imageRequests).toEqual([]);
    await captureScene('markdown-preview');
    await expect(text).toBeHidden();
    await expect(text).toHaveAttribute('inert', '');
    await text.evaluate((element) => element.focus());
    await expect(text).not.toBeFocused();
    if (isMobile) await choose(edit, true);
    else {
      await edit.focus();
      await edit.press('Space');
    }
    await expect(edit).toHaveAttribute('aria-pressed', 'true');
    await expect(previewButton).toHaveAttribute('aria-pressed', 'false');
    await expect(text).toBeFocused();
    await expect(text).toHaveAttribute(
      'data-mount-probe',
      'retained-markdown-editor',
    );
    expect(
      await text.evaluate((element) => {
        const input = element as HTMLTextAreaElement;
        return [input.selectionStart, input.selectionEnd];
      }),
    ).toEqual([2, 7]);
    await expect(text).toHaveValue(draft);
    await text.press('ControlOrMeta+z');
    await expect(text).not.toHaveValue(draft);
    await text.press('ControlOrMeta+Shift+z');
    await expect(text).toHaveValue(draft);
    expect(await readContent(request, entry.id)).toBe(source);

    const launcher = await openProjectLauncher(page, isMobile);
    await choose(
      launcher.getByRole('button', { name: 'Choose Folder', exact: true }),
      isMobile,
    );
    const folders = launcher.getByRole('region', {
      name: 'Choose a host folder',
      exact: true,
    });
    await folders.getByLabel('Folder path', { exact: true }).fill(projectPath);
    await choose(
      folders.getByRole('button', { name: 'Go', exact: true }),
      isMobile,
    );
    await expect(
      folders.getByRole('button', { name: 'Open folder nested', exact: true }),
    ).toBeVisible();
    await captureScene('host-folder-chooser');
    await choose(
      folders.getByRole('button', { name: 'Use Folder', exact: true }),
      isMobile,
    );
    await expect(
      launcher.getByLabel('Host project directory', { exact: true }),
    ).toHaveValue(projectPath);
    const registered = page.waitForResponse(
      (response) =>
        response.url().endsWith('/api/projects') &&
        response.request().method() === 'POST',
    );
    await choose(
      launcher.getByRole('button', { name: 'Open', exact: true }),
      isMobile,
    );
    const registration = await registered;
    expect(registration.ok(), await registration.text()).toBeTruthy();
    const project = (await registration.json()).project as ProjectIdentity;
    expect(project.rootPath).toBe(projectPath);
    const projectBrowser = page.locator(
      `[data-window-id="project-files:${project.id}"]`,
    );
    await expect(projectBrowser).toBeVisible();

    let terminalFrames = '';
    page.on('websocket', (socket) => {
      if (!socket.url().includes('/api/host/codex/sessions/')) return;
      socket.on('framereceived', ({ payload }) => {
        terminalFrames +=
          typeof payload === 'string' ? payload : payload.toString('utf8');
      });
    });
    const codex = await startCodexInFolder(page, isMobile);
    const panel = await openDocumentTask(page, editor, 'details', isMobile);
    await expect(panel).toHaveAttribute(
      'data-window-id',
      `document-workflow:${entry.id}`,
    );
    await choose(
      panel.locator('summary').filter({ hasText: /^Related work/ }),
      isMobile,
    );
    await panel
      .getByRole('combobox', { name: 'Link to', exact: true })
      .selectOption(`project:${project.id}`);
    await choose(
      panel.getByRole('button', { name: 'Add Link', exact: true }),
      isMobile,
    );
    await expect(
      panel.getByRole('region', { name: 'Document associations' }),
    ).toContainText(project.name);
    await panel
      .getByRole('combobox', { name: 'Link to', exact: true })
      .selectOption(`conversation:${codex.id}`);
    await choose(
      panel.getByRole('button', { name: 'Add Link', exact: true }),
      isMobile,
    );
    const captured = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/documents/${entry.id}/snapshots`) &&
        response.request().method() === 'POST',
    );
    await choose(
      panel.getByRole('button', { name: 'Keep version', exact: true }),
      isMobile,
    );
    const capture = await captured;
    expect(capture.ok(), await capture.text()).toBeTruthy();
    const snapshot = (await capture.json()).snapshot as Snapshot;
    expect(snapshot.content).toBe(source);
    expect(snapshot.revision).toBe(entry.revision);
    expect(snapshot.sha256).toMatch(/^[a-f0-9]{64}$/);
    await expect(text).toHaveValue(draft);
    await captureScene('saved-document');
    const links = await request.get(`/api/documents/${entry.id}/links`);
    expect((await links.json()).links).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ kind: 'project', projectId: project.id }),
        expect.objectContaining({
          kind: 'conversation',
          conversationId: codex.id,
        }),
      ]),
    );

    await activate(page, editor);
    const savedV2 = '# Current document\n\nMUTABLE_SOURCE_V2';
    await text.fill(savedV2);
    await choose(
      editor.getByRole('button', { name: 'Save', exact: true }),
      isMobile,
    );
    await expect(editor.getByTestId('save-status')).toHaveText('Saved');
    await text.press('ControlOrMeta+End');
    await text.pressSequentially('\nUNSAVED_DRAFT_V3');
    const dirtyV3 = await text.inputValue();
    await expect(editor.getByTestId('save-status')).toHaveText(
      'Unsaved changes',
    );
    expect(await readContent(request, entry.id)).toBe(savedV2);
    await openDocumentTask(page, editor, 'details', isMobile);
    await panel
      .getByRole('combobox', { name: 'Version', exact: true })
      .selectOption(snapshot.id);
    await choose(
      panel.locator('summary').filter({ hasText: /^Preview$/ }),
      isMobile,
    );
    await expect(
      panel.getByRole('heading', { name: 'Plan V1', exact: true }),
    ).toBeVisible();
    await expect(panel).not.toContainText('MUTABLE_SOURCE_V2');
    await captureScene('snapshot-preview');

    const tasks = panel.getByRole('tablist', {
      name: 'Document actions',
      exact: true,
    });
    const detailsTab = tasks.getByRole('tab', { name: 'Details', exact: true });
    const exportTab = tasks.getByRole('tab', { name: 'Export', exact: true });
    const attachTab = tasks.getByRole('tab', { name: 'Attach', exact: true });
    await expect(detailsTab).toHaveAttribute('aria-selected', 'true');
    await detailsTab.focus();
    await detailsTab.press('ArrowLeft');
    await expect(exportTab).toBeFocused();
    await expect(exportTab).toHaveAttribute('aria-selected', 'true');
    await expect(
      panel.getByRole('tabpanel', { name: 'Export', exact: true }),
    ).toBeVisible();
    await expect(panel).not.toContainText('Kept revision');
    await expect(
      panel.getByRole('combobox', { name: 'Version', exact: true }),
    ).toHaveValue(snapshot.id);
    await panel
      .getByRole('combobox', { name: 'Destination project', exact: true })
      .selectOption(project.id);
    const filename = `E2E Export ${width} ${entry.id.slice(0, 8)}.md`;
    const exportPath = `nested/${filename}`;
    await panel.getByLabel('New file path', { exact: true }).fill(exportPath);
    await exportTab.focus();
    await exportTab.press('ArrowLeft');
    await expect(attachTab).toBeFocused();
    await expect(attachTab).toHaveAttribute('aria-selected', 'true');
    await panel
      .getByRole('combobox', { name: 'Conversation', exact: true })
      .selectOption(JSON.stringify([codex.id, codex.runId]));
    await attachTab.focus();
    await attachTab.press('End');
    await expect(detailsTab).toBeFocused();
    await expect(detailsTab).toHaveAttribute('aria-selected', 'true');
    await detailsTab.press('ArrowLeft');
    await expect(exportTab).toBeFocused();
    await expect(exportTab).toHaveAttribute('aria-selected', 'true');
    await expect(
      panel.getByRole('combobox', { name: 'Version', exact: true }),
    ).toHaveValue(snapshot.id);
    await expect(
      panel.getByRole('combobox', { name: 'Destination project', exact: true }),
    ).toHaveValue(project.id);
    await expect(
      panel.getByLabel('New file path', { exact: true }),
    ).toHaveValue(exportPath);
    await expect(
      panel.getByRole('heading', { name: 'Plan V1', exact: true }),
    ).toBeVisible();
    const exported = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/projects/${project.id}/export`) &&
        response.request().method() === 'POST',
    );
    await choose(
      panel
        .getByRole('group', { name: 'Saved version actions', exact: true })
        .getByRole('button', { name: 'Export', exact: true }),
      isMobile,
    );
    const exportResult = await exported;
    expect(exportResult.ok(), await exportResult.text()).toBeTruthy();
    await expect(projectBrowser).toHaveClass(/is-active/);
    const filePreview = projectBrowser.getByRole('region', {
      name: 'Project file preview',
    });
    await expect(
      filePreview.getByRole('heading', { name: 'Plan V1' }),
    ).toBeVisible();
    await expect(filePreview).not.toContainText('MUTABLE_SOURCE_V2');
    const selectedFile = projectBrowser
      .locator('.project-directory')
      .getByRole('button', {
        name: filename,
        exact: true,
        includeHidden: true,
      });
    if (width === 390) {
      const hiddenDestination = panel.locator(
        `select:has(option[value="${project.id}"])`,
      );
      await expect(panel).toBeHidden();
      await expect(hiddenDestination).toBeHidden();
      await hiddenDestination.evaluate((element) => element.focus());
      await expect(hiddenDestination).not.toBeFocused();
      await expect(projectBrowser.locator('.project-directory')).toBeHidden();
      await expect(filePreview).toBeFocused();
      await selectedFile.evaluate((element) => element.focus());
      await expect(selectedFile).not.toBeFocused();
      await choose(
        projectBrowser.getByRole('button', {
          name: 'Back',
          exact: true,
        }),
        isMobile,
      );
      await expect(projectBrowser.locator('.project-directory')).toBeVisible();
      await expect(selectedFile).toHaveAttribute('aria-current', 'true');
      await expect(selectedFile).toBeFocused();
      await selectedFile.press('Enter');
      await expect(filePreview).toBeFocused();
      await expect(projectBrowser.locator('.project-directory')).toBeHidden();
    } else {
      await expect(selectedFile).toHaveAttribute('aria-current', 'true');
      await expect(
        projectBrowser.getByRole('button', {
          name: 'Back',
          exact: true,
        }),
      ).toBeHidden();
    }
    await captureScene('project-files');
    const projectMore = projectBrowser.getByRole('button', {
      name: 'More project actions',
      exact: true,
    });
    await expect(projectMore).toBeInViewport();
    await projectMore.focus();
    await projectMore.press('Enter');
    for (const label of ['Download', 'Projects'])
      await expect(
        page
          .locator('.ns-menu-layer')
          .getByRole('menuitem', { name: label, exact: true }),
      ).toBeInViewport();
    await captureScene('project-actions-menu');
    await page.keyboard.press('Escape');
    await expect(projectMore).toBeFocused();
    await expect(selectedFile).toHaveAttribute('aria-current', 'true');
    await actionMenu(
      page,
      projectBrowser,
      'More project actions',
      'Projects',
      isMobile,
    );
    await expect(launcher).toHaveClass(/is-active/);
    await activate(page, projectBrowser);
    await expect(filePreview).toContainText('IMMUTABLE_SNAPSHOT_V1');
    const copied = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/projects/${project.id}/import`) &&
        response.request().method() === 'POST',
    );
    await choose(
      projectBrowser.getByRole('button', {
        name: 'Copy to Documents',
        exact: true,
      }),
      isMobile,
    );
    const copyResult = await copied;
    expect(copyResult.ok(), await copyResult.text()).toBeTruthy();
    const imported = (await copyResult.json()).entry as { id: string };
    await expect(
      editorWindow(page, imported.id).locator('textarea'),
    ).toHaveValue(source);
    await openDocumentTask(page, editor, 'export', isMobile);
    await panel
      .getByRole('combobox', { name: 'Version', exact: true })
      .selectOption(snapshot.id);
    await panel
      .getByRole('combobox', { name: 'Destination project', exact: true })
      .selectOption(project.id);
    await panel.getByLabel('New file path', { exact: true }).fill(exportPath);
    const repeated = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/projects/${project.id}/export`) &&
        response.request().method() === 'POST',
    );
    await choose(
      panel
        .getByRole('group', { name: 'Saved version actions', exact: true })
        .getByRole('button', { name: 'Export', exact: true }),
      isMobile,
    );
    const conflict = await repeated;
    expect(conflict.status()).toBe(409);
    await expect(panel.getByRole('alert')).toContainText('was not overwritten');
    const retained = await request.get(`/api/projects/${project.id}/file`, {
      params: { path: exportPath },
    });
    expect(retained.ok()).toBeTruthy();
    expect((await retained.json()).content).toBe(source);

    const endpoint = contextEndpoint(codex);
    await openDocumentTask(page, editor, 'attach', isMobile);
    await choose(
      panel.getByRole('button', { name: 'Refresh', exact: true }),
      isMobile,
    );
    await expect(
      panel
        .getByRole('combobox', { name: 'Version', exact: true })
        .locator('option[value="saved"]'),
    ).toHaveText(`Saved revision ${entry.revision + 1}`);
    await panel
      .getByRole('combobox', { name: 'Version', exact: true })
      .selectOption('saved');
    await expect(
      panel.getByRole('combobox', { name: 'Conversation', exact: true }),
    ).toHaveValue(JSON.stringify([codex.id, codex.runId]));
    const queuedFromEditor = page.waitForResponse(
      (response) =>
        response.url().endsWith(endpoint) &&
        response.request().method() === 'POST',
    );
    await choose(
      panel
        .getByRole('group', { name: 'Saved version actions', exact: true })
        .getByRole('button', { name: 'Attach', exact: true }),
      isMobile,
    );
    const editorAttachment = await queuedFromEditor;
    expect(editorAttachment.ok(), await editorAttachment.text()).toBeTruthy();
    const cancelled = (await editorAttachment.json()).batch as Batch;
    expect(cancelled.runId).toBe(codex.runId);
    expect(cancelled.snapshots[0].revision).toBe(entry.revision + 1);
    const contextPanel = await openRunAttachments(page, codex, isMobile);
    const addDocument = contextPanel.getByRole('button', {
      name: 'Add document',
      exact: true,
    });
    await expect(
      contextPanel.getByRole('heading', { name: 'Documents', exact: true }),
    ).toBeVisible();
    await expect(addDocument).toHaveText('Add');
    for (const action of [
      addDocument,
      contextPanel.getByRole('button', { name: 'Open Codex', exact: true }),
      contextPanel.getByRole('button', { name: 'Refresh', exact: true }),
    ])
      await expect(action).toBeInViewport();
    expect(
      await contextPanel
        .locator('.document-workflow')
        .evaluate((content) => content.scrollWidth <= content.clientWidth),
    ).toBe(true);
    if (isMobile) await choose(addDocument, true);
    else {
      await addDocument.focus();
      await addDocument.press('Enter');
    }
    const documentPicker = contextPanel.getByRole('combobox', {
      name: 'Document',
      exact: true,
    });
    await expect(documentPicker).toBeVisible();
    if (!isMobile) await expect(documentPicker).toBeFocused();
    const cancelSelection = contextPanel
      .getByRole('region', { name: 'Add document', exact: true })
      .getByRole('button', { name: 'Cancel', exact: true });
    if (isMobile) await choose(cancelSelection, true);
    else {
      await cancelSelection.focus();
      await cancelSelection.press('Enter');
    }
    await expect(documentPicker).toBeHidden();
    await expect(addDocument).toBeFocused();
    await expect(
      contextPanel.getByRole('combobox', {
        name: 'Conversation',
        exact: true,
      }),
    ).toHaveCount(0);
    let capturedPicker = false;
    const attach = async (version = 'saved') => {
      const versions = await selectAttachmentVersion(
        contextPanel,
        entry.id,
        version,
        isMobile,
      );
      if (version === 'saved')
        await expect(versions.locator('option:checked')).toHaveText(
          `Saved revision ${entry.revision + 1}`,
        );
      if (!capturedPicker) {
        await choose(
          contextPanel.locator('summary').filter({ hasText: /^Preview$/ }),
          isMobile,
        );
        await expect(
          contextPanel.getByRole('heading', {
            name: 'Current document',
            exact: true,
          }),
        ).toBeVisible();
        await expect(contextPanel).toContainText('MUTABLE_SOURCE_V2');
        await expect(contextPanel).not.toContainText('UNSAVED_DRAFT_V3');
        await captureScene('attachment-picker');
        capturedPicker = true;
      }
      const attached = page.waitForResponse(
        (response) =>
          response.url().endsWith(endpoint) &&
          response.request().method() === 'POST',
      );
      await choose(
        contextPanel.getByRole('button', {
          name: 'Attach',
          exact: true,
        }),
        isMobile,
      );
      const response = await attached;
      expect(response.ok(), await response.text()).toBeTruthy();
      const batch = (await response.json()).batch as Batch;
      expect(batch.runId).toBe(codex.runId);
      expect(batch.status).toBe('pending');
      expect(batch.snapshots).toHaveLength(1);
      expect(batch.snapshots[0].revision).toBe(
        version === 'saved' ? entry.revision + 1 : snapshot.revision,
      );
      if (version !== 'saved') expect(batch.snapshots[0].id).toBe(snapshot.id);
      const capturedSource = await request.get(
        `/api/document-snapshots/${batch.snapshots[0].id}`,
      );
      expect(capturedSource.ok()).toBeTruthy();
      expect((await capturedSource.json()).snapshot.content).toBe(
        version === 'saved' ? savedV2 : source,
      );
      await expect(contextPanel).toHaveClass(/is-active/);
      await expect(
        contextPanel.getByRole('combobox', { name: 'Document', exact: true }),
      ).toBeHidden();
      await expect(
        contextPanel.getByRole('button', {
          name: 'Add document',
          exact: true,
        }),
      ).toBeFocused();
      await expect(text).toHaveValue(dirtyV3);
      await expect(editor.getByTestId('save-status')).toHaveText(
        'Unsaved changes',
      );
      return batch;
    };
    await choose(
      contextPanel.getByRole('button', {
        name: 'Cancel Attachment',
        exact: true,
      }),
      isMobile,
    );
    await openReceiptHistory(contextPanel, isMobile);
    await expect(
      contextPanel.getByText('Cancelled', { exact: true }),
    ).toBeVisible();
    const pending = await attach();
    expect(pending.id).not.toBe(cancelled.id);
    await choose(
      contextPanel.getByRole('button', {
        name: 'Open Codex',
        exact: true,
      }),
      isMobile,
    );
    await expect(codex.window).toHaveClass(/is-active/);
    terminalFrames = '';
    await runCodex(codex.window, 'message REVIEW_ATTACHED_SAVED_REVISION');
    await expect
      .poll(async () => {
        const response = await request.get(endpoint);
        expect(response.ok()).toBeTruthy();
        const batches = (await response.json()).batches as Batch[];
        return batches.find((batch) => batch.id === pending.id)?.status;
      })
      .toBe('accepted');
    const contextResponse = await request.get(endpoint);
    const batches = (await contextResponse.json()).batches as Batch[];
    expect(batches.find((batch) => batch.id === cancelled.id)?.status).toBe(
      'cancelled',
    );
    expect(
      batches.find((batch) => batch.id === pending.id)?.providerTurnId,
    ).toBeTruthy();
    await expect
      .poll(() => stripVTControlCharacters(terminalFrames).replace(/\s+/g, ''))
      .toContain('MUTABLE_SOURCE_V2');
    const delivered = stripVTControlCharacters(terminalFrames);
    expect(delivered).toContain('TURN_INPUT');
    expect(delivered).not.toContain('IMMUTABLE_SNAPSHOT_V1');
    expect(delivered).not.toContain('UNSAVED_DRAFT');
    await openRunAttachments(page, codex, isMobile);
    await openReceiptHistory(contextPanel, isMobile);
    await expect(
      contextPanel.getByText('Delivered', { exact: true }),
    ).toBeVisible();
    await captureScene('delivered');

    const steering = await attach(snapshot.id);
    await choose(
      contextPanel.getByRole('button', {
        name: 'Open Codex',
        exact: true,
      }),
      isMobile,
    );
    terminalFrames = '';
    await runCodex(codex.window, 'steer REVIEW_HISTORICAL_SNAPSHOT');
    await expect
      .poll(async () => {
        const response = await request.get(endpoint);
        expect(response.ok()).toBeTruthy();
        const deliveries = (await response.json()).batches as Batch[];
        return deliveries.find((batch) => batch.id === steering.id);
      })
      .toEqual(
        expect.objectContaining({
          status: 'accepted',
          providerTurnId: batches.find((batch) => batch.id === pending.id)
            ?.providerTurnId,
        }),
      );
    await expect
      .poll(() => stripVTControlCharacters(terminalFrames).replace(/\s+/g, ''))
      .toContain('IMMUTABLE_SNAPSHOT_V1');
    const steered = stripVTControlCharacters(terminalFrames);
    expect(steered).toContain('TURN_INPUT');
    expect(steered).not.toContain('MUTABLE_SOURCE_V2');
    expect(steered).not.toContain('UNSAVED_DRAFT');
    await openRunAttachments(page, codex, isMobile);
    await openReceiptHistory(contextPanel, isMobile);
    await expect(
      contextPanel.getByText('Delivered', { exact: true }),
    ).toHaveCount(2);

    expect(
      (await request.post(`/api/entries/${entry.id}/trash`)).ok(),
    ).toBeTruthy();
    expect(
      (await request.delete(`/api/entries/${entry.id}`)).ok(),
    ).toBeTruthy();
    await choose(
      contextPanel.getByRole('button', {
        name: `${entry.name} · revision ${snapshot.revision}`,
        exact: true,
      }),
      isMobile,
    );
    const snapshotWindow = page.locator(
      `[data-window-id="document-snapshot:${snapshot.id}"]`,
    );
    await expect(
      snapshotWindow.getByRole('heading', { name: 'Plan V1' }),
    ).toBeVisible();
    await page.reload();
    await expect(
      snapshotWindow.getByRole('heading', { name: 'Plan V1' }),
    ).toBeVisible();
    const retainedSnapshot = await request.get(
      `/api/document-snapshots/${snapshot.id}`,
    );
    expect(retainedSnapshot.ok()).toBeTruthy();
    expect((await retainedSnapshot.json()).snapshot.content).toBe(source);
    await captureScene('deleted-source-snapshot');
    await choose(
      snapshotWindow.getByRole('button', {
        name: 'Document Details',
        exact: true,
      }),
      isMobile,
    );
    await expect(panel).toHaveClass(/is-active/);
    await expect(panel).toHaveAttribute(
      'data-window-id',
      `document-workflow:${entry.id}`,
    );
    await activate(page, snapshotWindow);
    await choose(
      snapshotWindow.getByRole('button', { name: /^Close / }),
      isMobile,
    );
    await activate(page, codex.window);
    await endCodex(page, codex.window, isMobile);
  });
}
