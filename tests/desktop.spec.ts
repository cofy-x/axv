import { readFile } from 'node:fs/promises';
import {
  actionMenu,
  activate,
  bounds,
  createEntry,
  dockEntry,
  editorWindow,
  entries,
  expect,
  filesWindow,
  launch,
  menuItem,
  nameDialog,
  newFileItem,
  openDocument,
  persistedLayout,
  readContent,
  switchWorkspace,
  test,
  windowMenu,
} from './helpers';

test('windows drag, resize, shade, miniaturize and retain desktop geometry', async ({
  page,
  request,
}) => {
  const files = filesWindow(page);
  const initial = await bounds(files);
  const title = await bounds(files.locator('.ns-window-title'));
  await page.mouse.move(title.x + title.width / 2, title.y + title.height / 2);
  await page.mouse.down();
  await page.mouse.move(
    title.x + title.width / 2 + 72,
    title.y + title.height / 2 + 48,
    { steps: 8 },
  );
  await page.mouse.up();
  const moved = await bounds(files);
  expect(moved.x).toBeCloseTo(initial.x + 72, 0);
  expect(moved.y).toBeCloseTo(initial.y + 48, 0);
  const corner = await bounds(files.locator('[data-resize="se"]'));
  await page.mouse.move(
    corner.x + corner.width / 2,
    corner.y + corner.height / 2,
  );
  await page.mouse.down();
  await page.mouse.move(
    corner.x + corner.width / 2 + 60,
    corner.y + corner.height / 2 + 40,
    { steps: 8 },
  );
  await page.mouse.up();
  const resized = await bounds(files);
  expect(resized.width).toBeCloseTo(moved.width + 60, 0);
  expect(resized.height).toBeCloseTo(moved.height + 40, 0);
  await files.getByRole('button', { name: /^Shade / }).click();
  await expect(files).toHaveClass(/is-shaded/);
  await expect(
    files.getByRole('button', { name: 'New', exact: true }),
  ).toBeHidden();
  await files.getByRole('button', { name: /^Unshade / }).click();
  await expect.poll(() => bounds(files)).toEqual(resized);
  await files.getByRole('button', { name: /^Maximize / }).click();
  expect((await bounds(files)).width).toBeGreaterThan(resized.width);
  await files.getByRole('button', { name: /^Restore / }).click();
  await expect.poll(() => bounds(files)).toEqual(resized);
  await files.getByRole('button', { name: /^Miniaturize / }).click();
  await expect(files).toBeHidden();
  await expect(page.locator('.ns-miniwindow-shelf')).toBeVisible();
  await activate(page, files);
  await expect.poll(() => bounds(files)).toEqual(resized);
  await expect
    .poll(
      async () =>
        (await persistedLayout(page, request))?.windows.find(
          (win: { id: string }) => win.id === 'files:documents',
        )?.width,
    )
    .toBe(resized.width);
  await page.reload();
  await expect.poll(() => bounds(filesWindow(page))).toEqual(resized);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    filesWindow(page).getByRole('button', { name: /^Close / }),
  ).toBeInViewport();
  await page.setViewportSize({ width: 1440, height: 900 });
  await expect.poll(() => bounds(filesWindow(page))).toEqual(resized);
});

test('Dock pinning, order, clock, running state and document selection persist', async ({
  page,
  request,
}) => {
  const a = await createEntry(request, 'E2E Dock A.txt');
  const b = await createEntry(request, 'E2E Dock B.txt');
  await page.reload();
  const first = await openDocument(page, a);
  const second = await openDocument(page, b);
  await expect(
    dockEntry(page, 'notes').locator('.ns-dock-tile'),
  ).toHaveAttribute('aria-label', /2 running windows/);
  await launch(page, 'notes');
  await menuItem(page, a.name).click();
  await expect(first).toHaveClass(/is-active/);
  await expect(second).toHaveCount(1);
  const gallery = dockEntry(page, 'gallery');
  await gallery.locator('.ns-dock-options').click();
  await menuItem(page, 'Keep in Dock').click();
  await expect(gallery).toHaveCount(0);
  await page
    .getByRole('button', { name: 'Add application to Dock', exact: true })
    .click();
  await menuItem(page, 'Component Gallery').click();
  await expect(gallery).toBeVisible();
  const order = () =>
    page
      .locator('.ns-dock [data-dock-app]')
      .evaluateAll((items) =>
        items.map((item) => (item as HTMLElement).dataset.dockApp),
      );
  const before = await order();
  await gallery.locator('.ns-dock-options').click();
  await menuItem(page, 'Move Earlier').click();
  let changed = await order();
  expect(changed.indexOf('gallery')).toBe(before.indexOf('gallery') - 1);
  const dragged = await bounds(gallery.locator('.ns-dock-tile'));
  const destination = await bounds(
    dockEntry(page, changed[0]!).locator('.ns-dock-tile'),
  );
  await page.mouse.move(
    dragged.x + dragged.width / 2,
    dragged.y + dragged.height / 2,
  );
  await page.mouse.down();
  await page.mouse.move(
    destination.x + destination.width / 2,
    destination.y + destination.height / 2,
    { steps: 12 },
  );
  await page.mouse.up();
  await expect.poll(async () => (await order())[0]).toBe('gallery');
  changed = await order();
  await expect
    .poll(async () =>
      (await persistedLayout(page, request))?.dock
        .filter((item: { pinned: boolean }) => item.pinned)
        .map((item: { appId: string }) => item.appId),
    )
    .toEqual(changed);
  await page.reload();
  await expect.poll(order).toEqual(changed);
  await page
    .getByRole('button', { name: 'Clock and date', exact: true })
    .click();
  await expect(
    page.getByRole('dialog', { name: 'Clock', exact: true }),
  ).toContainText(String(new Date().getFullYear()));
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'OK', exact: true })
    .click();
});

test('workspaces and Clip retain mounted drafts, focus, sticky windows and renamed layout', async ({
  page,
  request,
}) => {
  const entry = await createEntry(request, 'E2E Workspace draft.txt');
  await page.reload();
  const note = await openDocument(page, entry);
  const text = note.locator('textarea[aria-label="Document text"]');
  await text.fill('Draft across workspaces\n'.repeat(30));
  await text.evaluate((element) => {
    element.dataset.mountProbe = 'same-editor';
    element.scrollTop = 100;
  });
  const scrollTop = () => text.evaluate((element) => element.scrollTop);
  await expect.poll(scrollTop).toBe(100);
  await note.getByRole('button', { name: /^Shade / }).click();
  await expect(text).toBeHidden();
  await note.getByRole('button', { name: /^Unshade / }).click();
  await expect.poll(scrollTop).toBe(100);
  await note.getByRole('button', { name: /^Miniaturize / }).click();
  await expect(note).toBeHidden();
  await activate(page, note);
  await expect.poll(scrollTop).toBe(100);
  await windowMenu(page, note);
  await menuItem(page, 'Add to Clip').click();
  await expect(
    page.getByRole('button', { name: `Clip: ${entry.name}`, exact: true }),
  ).toBeVisible();
  await switchWorkspace(page, 2);
  await expect(note).toBeHidden();
  await expect(note).toHaveCount(1);
  expect(
    await text.evaluate((element) => element.contains(document.activeElement)),
  ).toBe(false);
  await expect(
    page.getByRole('button', { name: `Clip: ${entry.name}`, exact: true }),
  ).toHaveCount(0);
  await switchWorkspace(page, 1);
  await expect(text).toHaveAttribute('data-mount-probe', 'same-editor');
  await expect(text).toHaveValue('Draft across workspaces\n'.repeat(30));
  await expect.poll(scrollTop).toBe(100);
  await windowMenu(page, note);
  await menuItem(page, 'Move to Workspace').hover();
  await menuItem(page, 'Development').click();
  await expect(note).toBeHidden();
  await switchWorkspace(page, 2);
  await expect(note).toBeVisible();
  await windowMenu(page, note);
  await menuItem(page, 'All Workspaces').click();
  await switchWorkspace(page, 3);
  await expect(note).toBeVisible();
  await page.getByRole('button', { name: /^Workspace 3:/ }).click();
  await menuItem(page, 'Rename Workspace').click();
  const dialog = page.getByRole('dialog', {
    name: 'Rename Workspace',
    exact: true,
  });
  await dialog
    .getByRole('textbox', { name: 'Workspace name', exact: true })
    .fill('E2E Writing');
  await dialog.getByRole('button', { name: 'OK', exact: true }).click();
  await expect(
    page.getByRole('button', { name: 'Workspace 3: E2E Writing', exact: true }),
  ).toBeVisible();
  await expect
    .poll(
      async () => (await persistedLayout(page, request))?.workspaces[2].name,
    )
    .toBe('E2E Writing');
  expect(await readContent(request, entry.id)).toBe('Initial text');
});

test('columns, Inspector and file import/edit/move/recycle/download share one filesystem', async ({
  page,
  request,
}) => {
  const files = filesWindow(page);
  await newFileItem(page, files, 'New Folder');
  await nameDialog(page, 'E2E Projects');
  await files
    .getByRole('option', { name: 'E2E Projects', exact: true })
    .click();
  await expect(
    files.getByRole('listbox', { name: 'E2E Projects column', exact: true }),
  ).toBeVisible();
  await files.getByLabel('Import files', { exact: true }).setInputFiles({
    name: 'E2E Imported.txt',
    mimeType: 'text/plain',
    buffer: Buffer.from('Imported bytes.\n'),
  });
  const imported = files.getByRole('option', {
    name: 'E2E Imported.txt',
    exact: true,
  });
  await expect(imported).toBeVisible();
  await imported.click();
  await files.getByRole('button', { name: 'Inspect', exact: true }).click();
  const inspector = page.locator('[data-app-id="inspector"]');
  await expect(inspector).toContainText('E2E Imported.txt');
  await inspector.getByRole('button', { name: 'Pin', exact: true }).click();
  await activate(page, files);
  await files.getByRole('button', { name: 'Open', exact: true }).click();
  const importedEntry = (await entries(request)).find(
    (entry) => entry.name === 'E2E Imported.txt',
  )!;
  const note = editorWindow(page, importedEntry.id);
  await note
    .getByRole('textbox', { name: 'Document text' })
    .fill('Edited through the shared filesystem.\n第二行。');
  await note
    .getByRole('textbox', { name: 'Document text' })
    .press('ControlOrMeta+s');
  await expect(note.getByTestId('save-status')).toHaveText('Saved');
  await activate(page, files);
  await actionMenu(page, files, 'More file actions', 'Rename');
  await nameDialog(page, 'E2E Final.txt');
  await expect(note).toHaveAttribute('aria-label', 'E2E Final.txt');
  await expect(inspector).toContainText('E2E Final.txt');
  await files
    .getByRole('option', { name: 'E2E Final.txt', exact: true })
    .click();
  await activate(page, inspector);
  await actionMenu(page, inspector, 'More inspector actions', 'Move');
  const move = page.getByRole('dialog');
  await move.getByRole('combobox').selectOption('documents');
  await move.getByRole('button', { name: 'Move', exact: true }).click();
  await expect
    .poll(
      async () =>
        (await entries(request)).find((entry) => entry.id === importedEntry.id)
          ?.parentId,
    )
    .toBe('documents');
  await expect(inspector.getByTestId('inspector-path')).not.toContainText(
    'E2E Projects',
  );
  await activate(page, files);
  await files.getByRole('button', { name: 'Documents', exact: true }).click();
  await files
    .getByRole('option', { name: 'E2E Final.txt', exact: true })
    .click();
  await actionMenu(page, files, 'More file actions', 'Move to Recycler');
  await expect(inspector).toContainText(/Recycler|deleted|unavailable/);
  await launch(page, 'trash');
  const recycler = page.locator('[data-app-id="trash"]');
  await recycler.getByRole('option', { name: /E2E Final\.txt/ }).click();
  await recycler.getByRole('button', { name: 'Restore', exact: true }).click();
  await activate(page, files);
  await files
    .getByRole('option', { name: 'E2E Final.txt', exact: true })
    .click();
  const downloadPromise = page.waitForEvent('download');
  await activate(page, inspector);
  await actionMenu(page, inspector, 'More inspector actions', 'Download');
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe('E2E Final.txt');
  expect((await readFile((await download.path())!)).toString()).toBe(
    'Edited through the shared filesystem.\n第二行。',
  );
  await expect(inspector).toContainText('E2E Final.txt');
});

test('revision conflicts preserve local text and save a separate copy', async ({
  page,
  request,
}) => {
  const entry = await createEntry(request, 'E2E Conflict.txt');
  await page.reload();
  const note = await openDocument(page, entry);
  const editor = note.getByRole('textbox', { name: 'Document text' });
  await expect(editor).toHaveValue('Initial text');
  expect(
    (
      await request.patch(`/api/entries/${entry.id}`, {
        data: { revision: entry.revision, content: 'Remote saved text' },
      })
    ).ok(),
  ).toBeTruthy();
  await editor.fill('My local draft stays intact');
  await editor.press('ControlOrMeta+s');
  await expect(note.getByTestId('save-status')).toHaveText(
    'Another window changed this document',
  );
  await expect(editor).toHaveValue('My local draft stays intact');
  await note
    .getByRole('button', { name: 'More document actions', exact: true })
    .click();
  await menuItem(page, 'Save Copy').click();
  await nameDialog(page, 'E2E Preserved.txt');
  const copy = (await entries(request)).find(
    (item) => item.name === 'E2E Preserved.txt',
  )!;
  await expect(
    editorWindow(page, copy.id).getByRole('textbox', { name: 'Document text' }),
  ).toHaveValue('My local draft stays intact');
  expect(await readContent(request, entry.id)).toBe('Remote saved text');
  await activate(page, note);
  await note
    .getByRole('button', { name: 'More document actions', exact: true })
    .click();
  await menuItem(page, 'Reload').click();
  await page
    .getByRole('dialog', { name: 'Load the saved document?', exact: true })
    .getByRole('button', { name: 'Load Saved', exact: true })
    .click();
  await expect(editor).toHaveValue('Remote saved text');
  await expect(note.getByTestId('save-status')).toHaveText('Saved');
});

test('failed saves, cancel/discard protection and local recovery preserve drafts', async ({
  page,
  request,
}) => {
  const entry = await createEntry(request, 'E2E Recovery.txt');
  await page.reload();
  const note = await openDocument(page, entry);
  const editor = note.getByRole('textbox', { name: 'Document text' });
  const endpoint = `**/api/entries/${entry.id}`;
  await page.route(endpoint, (route) =>
    route.request().method() === 'PATCH'
      ? route.abort('failed')
      : route.continue(),
  );
  await editor.fill('This draft survives a network failure and reload.');
  await editor.press('ControlOrMeta+s');
  await expect(note.getByTestId('save-status')).toHaveText(
    'Not saved — your draft is still here',
  );
  await expect(note).toContainText('Local recovery copy up to date');
  await note.getByRole('button', { name: /^Close / }).click();
  const dialog = page.getByRole('dialog', {
    name: 'Save changes?',
    exact: true,
  });
  await expect(
    dialog.getByRole('button', { name: 'Save', exact: true }),
  ).toBeFocused();
  await dialog.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(note.getByTestId('save-status')).toHaveText(
    'Not saved — your draft is still here',
  );
  await note.getByRole('button', { name: /^Close / }).click();
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(editor).toHaveValue(
    'This draft survives a network failure and reload.',
  );
  page.once('dialog', (event) => event.accept());
  await page.reload();
  await expect(editorWindow(page, entry.id)).toContainText(
    'A local recovery copy is available.',
  );
  await expect(
    editorWindow(page, entry.id).getByRole('textbox', {
      name: 'Document text',
    }),
  ).toBeDisabled();
  await editorWindow(page, entry.id)
    .getByRole('button', { name: 'Restore Draft', exact: true })
    .click();
  await expect(
    editorWindow(page, entry.id).getByRole('textbox', {
      name: 'Document text',
    }),
  ).toHaveValue('This draft survives a network failure and reload.');
  await expect(editorWindow(page, entry.id)).toContainText(
    'Recovered a local draft',
  );
  expect(await readContent(request, entry.id)).toBe('Initial text');
  await page.unroute(endpoint);
  await editorWindow(page, entry.id)
    .getByRole('textbox', { name: 'Document text' })
    .press('ControlOrMeta+s');
  await expect(
    editorWindow(page, entry.id).getByTestId('save-status'),
  ).toHaveText('Saved');
  await editorWindow(page, entry.id)
    .getByRole('textbox', { name: 'Document text' })
    .fill('Discard only this later draft');
  await editorWindow(page, entry.id)
    .getByRole('button', { name: /^Close / })
    .click();
  await page
    .getByRole('dialog', { name: 'Save changes?', exact: true })
    .getByRole('button', { name: 'Discard', exact: true })
    .click();
  await expect(editorWindow(page, entry.id)).toHaveCount(0);
  const reopened = await openDocument(page, entry);
  await expect(
    reopened.getByRole('textbox', { name: 'Document text' }),
  ).toHaveValue('This draft survives a network failure and reload.');
});

test('keyboard menus skip disabled items and modal dialogs restore focus', async ({
  page,
}) => {
  await launch(page, 'gallery');
  const gallery = page.locator('[data-app-id="gallery"]');
  await gallery
    .getByRole('tab', { name: 'Dialogs & Menus', exact: true })
    .click();
  const opener = gallery.getByRole('button', {
    name: 'Show Input Dialog',
    exact: true,
  });
  await opener.click();
  const dialog = page.getByRole('dialog', {
    name: 'Name Your Document',
    exact: true,
  });
  const name = dialog.getByRole('textbox', { name: 'Name', exact: true });
  await expect(name).toBeFocused();
  await page.keyboard.press('Shift+Tab');
  await expect(
    dialog.getByRole('button', { name: 'Save', exact: true }),
  ).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(name).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(opener).toBeFocused();
  await opener.click();
  await name.fill('E2E Keyboard.txt');
  await page.keyboard.press('Enter');
  await expect(gallery).toContainText('Selected: E2E Keyboard.txt');
  const menu = gallery.getByRole('button', { name: 'Show Menu', exact: true });
  await menu.click();
  await expect(menuItem(page, 'Unavailable')).toHaveAttribute(
    'aria-disabled',
    'true',
  );
  await expect(menuItem(page, 'Selected option')).toHaveAttribute(
    'aria-checked',
    'true',
  );
  await page.keyboard.press('End');
  await page.keyboard.press('ArrowRight');
  await expect(page.getByRole('menu')).toHaveCount(2);
  await page.keyboard.press('Enter');
  await expect(gallery).toContainText('Nested command selected.');
  await expect(page.getByRole('menu')).toHaveCount(0);
  await expect(menu).toBeFocused();
});

test('preferences persist and browser clients keep independent layouts', async ({
  page,
  request,
  browser,
}) => {
  await launch(page, 'appearance');
  const preferences = page.locator('[data-app-id="appearance"]');
  await preferences
    .getByRole('button', { name: 'Dither', exact: true })
    .click();
  await expect(preferences).toContainText('Saved.');
  await switchWorkspace(page, 4);
  await expect
    .poll(
      async () => (await persistedLayout(page, request))?.currentWorkspaceId,
    )
    .toBe('workspace-4');
  const independent = await browser.newContext({
    baseURL: test.info().project.use.baseURL,
  });
  try {
    const other = await independent.newPage();
    await other.goto('/');
    await expect(
      other.getByRole('button', { name: /^Workspace 1:/ }),
    ).toBeVisible();
    await expect(other.locator('.ns-desktop')).toHaveClass(/dither/);
  } finally {
    await independent.close();
  }
  await page.reload();
  await expect(
    page.getByRole('button', { name: /^Workspace 4:/ }),
  ).toBeVisible();
  await expect(page.locator('.ns-desktop')).toHaveClass(/dither/);
});

test('a pinned application menu saves its owner after focus changes and closes with it', async ({
  page,
  request,
}) => {
  const a = await createEntry(request, 'E2E Owner A.txt');
  const b = await createEntry(request, 'E2E Owner B.txt');
  await page.reload();
  const first = await openDocument(page, a);
  await first
    .getByRole('textbox', { name: 'Document text' })
    .fill('Saved through the pinned menu for A.');
  await page
    .getByRole('navigation', { name: 'Application menus' })
    .getByRole('button', { name: 'File', exact: true })
    .click();
  await page
    .getByRole('button', { name: 'Pin File menu', exact: true })
    .click();
  const pinned = page.locator(
    `.ns-pinned-menu[data-owner-window-id="notes:${a.id}"]`,
  );
  await expect(pinned).toBeVisible();
  const second = await openDocument(page, b);
  await second
    .getByRole('textbox', { name: 'Document text' })
    .fill('B stays an unsaved draft.');
  await pinned.getByRole('menuitem', { name: 'Save', exact: true }).click();
  await expect(first.getByTestId('save-status')).toHaveText('Saved');
  await expect(second.getByTestId('save-status')).toHaveText('Unsaved changes');
  expect(await readContent(request, a.id)).toBe(
    'Saved through the pinned menu for A.',
  );
  expect(await readContent(request, b.id)).toBe('Initial text');
  await pinned
    .getByRole('menuitem', { name: 'Close Window', exact: true })
    .click();
  await expect(first).toHaveCount(0);
  await expect(pinned).toHaveCount(0);
  await expect(
    second.getByRole('textbox', { name: 'Document text' }),
  ).toHaveValue('B stays an unsaved draft.');
});

test('image previews and file-size errors use real imported data', async ({
  page,
}) => {
  const files = filesWindow(page);
  const png = Buffer.from(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a0V8AAAAASUVORK5CYII=',
    'base64',
  );
  await files.getByLabel('Import files', { exact: true }).setInputFiles({
    name: 'E2E Pixel.png',
    mimeType: 'image/png',
    buffer: png,
  });
  await files
    .getByRole('option', { name: 'E2E Pixel.png', exact: true })
    .click();
  await files.getByRole('button', { name: 'Inspect', exact: true }).click();
  const inspector = page.locator('[data-app-id="inspector"]');
  const preview = inspector.getByRole('img', {
    name: 'Preview of E2E Pixel.png',
    exact: true,
  });
  await expect(preview).toBeVisible();
  await expect
    .poll(() =>
      preview.evaluate((image) => (image as HTMLImageElement).naturalWidth),
    )
    .toBe(1);
  await activate(page, files);
  await files.getByLabel('Import files', { exact: true }).setInputFiles({
    name: 'E2E Too Big.bin',
    mimeType: 'application/octet-stream',
    buffer: Buffer.alloc(10 * 1024 * 1024 + 1),
  });
  const dialog = page.getByRole('dialog', {
    name: 'Unable to complete the operation',
    exact: true,
  });
  await expect(dialog).toContainText('10 MiB');
  await dialog.getByRole('button', { name: 'OK', exact: true }).click();
  await expect(
    files.getByRole('option', { name: 'E2E Too Big.bin', exact: true }),
  ).toHaveCount(0);
});

test('future layout recovery resets only desktop state after confirmation', async ({
  page,
  request,
}) => {
  const entry = await createEntry(
    request,
    'E2E Keep my data.txt',
    'file',
    'documents',
    'Files must survive layout recovery.',
  );
  await expect
    .poll(async () => (await persistedLayout(page, request))?.version)
    .toBe(2);
  const clientId = await page.evaluate(() =>
    localStorage.getItem('nextstep-browser-id'),
  );
  const response = await request.put('/api/layout', {
    data: { clientId, layout: { version: 999, windows: [] } },
  });
  expect(response.ok()).toBeTruthy();
  await page.reload();
  await expect(page.getByRole('alert')).toContainText('newer version');
  await page.getByRole('button', { name: 'Reset Layout', exact: true }).click();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  expect((await persistedLayout(page, request)).version).toBe(999);
  await page.getByRole('button', { name: 'Reset Layout', exact: true }).click();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Reset Layout', exact: true })
    .click();
  await expect(
    page.getByRole('button', { name: 'Reset Layout', exact: true }),
  ).toHaveCount(0);
  await expect
    .poll(async () => (await persistedLayout(page, request))?.version)
    .toBe(2);
  expect(await readContent(request, entry.id)).toBe(
    'Files must survive layout recovery.',
  );
});

for (const duplicate of [false, true]) {
  test(`${duplicate ? 'duplicated tabs' : 'two tabs'} recover their own unsaved drafts for the same document`, async ({
    page,
    request,
  }) => {
    const entry = await createEntry(request, 'E2E Separate tab drafts.txt');
    await page.reload();
    const first = await openDocument(page, entry);
    await expect
      .poll(async () =>
        (await persistedLayout(page, request))?.windows.some(
          (win: { id: string }) => win.id === `notes:${entry.id}`,
        ),
      )
      .toBe(true);
    const other = duplicate
      ? await (async () => {
          const popup = page.waitForEvent('popup');
          // A same-origin popup inherits the opener's sessionStorage, matching
          // the draft-identity collision caused by duplicating a browser tab.
          await page.evaluate(() => window.open('/', '_blank'));
          return popup;
        })()
      : await page.context().newPage();
    const otherErrors: string[] = [];
    other.on('pageerror', (error) => otherErrors.push(error.message));
    try {
      if (!duplicate) await other.goto('/');
      const second = editorWindow(other, entry.id);
      await expect(
        second.getByRole('textbox', { name: 'Document text' }),
      ).toBeEnabled();
      await page.bringToFront();
      await first
        .getByRole('textbox', { name: 'Document text' })
        .fill('Unsaved text belonging only to the first tab.');
      await expect(
        first.getByRole('textbox', { name: 'Document text' }),
      ).toHaveValue('Unsaved text belonging only to the first tab.');
      await other.bringToFront();
      await second
        .getByRole('textbox', { name: 'Document text' })
        .fill('A different unsaved draft in the second tab.');
      await expect(
        second.getByRole('textbox', { name: 'Document text' }),
      ).toHaveValue('A different unsaved draft in the second tab.');
      await expect(first).toContainText('Local recovery copy up to date');
      await expect(second).toContainText('Local recovery copy up to date');
      page.once('dialog', (dialog) => dialog.accept());
      await page.reload();
      await editorWindow(page, entry.id)
        .getByRole('button', { name: 'Restore Draft', exact: true })
        .click();
      await expect(
        editorWindow(page, entry.id).getByRole('textbox', {
          name: 'Document text',
        }),
      ).toHaveValue('Unsaved text belonging only to the first tab.');
      other.once('dialog', (dialog) => dialog.accept());
      await other.reload();
      await editorWindow(other, entry.id)
        .getByRole('button', { name: 'Restore Draft', exact: true })
        .click();
      await expect(
        editorWindow(other, entry.id).getByRole('textbox', {
          name: 'Document text',
        }),
      ).toHaveValue('A different unsaved draft in the second tab.');
      expect(await readContent(request, entry.id)).toBe('Initial text');
      expect(otherErrors).toEqual([]);
    } finally {
      await other.close();
    }
  });
}
