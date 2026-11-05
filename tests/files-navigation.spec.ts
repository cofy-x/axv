import {
  actionMenu,
  bounds,
  createEntry,
  entries,
  expect,
  filesWindow,
  menuItem,
  persistedLayout,
  test,
} from './helpers';

test('narrow Files shows one directory and preserves desktop column preferences', async ({
  page,
  request,
}) => {
  const parent = await createEntry(request, 'E2E Narrow Parent', 'folder');
  const child = await createEntry(
    request,
    'E2E Narrow Child',
    'folder',
    parent.id,
  );
  const sibling = await createEntry(
    request,
    'E2E Narrow Sibling',
    'folder',
    parent.id,
  );
  const file = await createEntry(request, 'E2E Narrow.txt', 'file', child.id);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.reload();
  const files = filesWindow(page);
  await files.getByRole('option', { name: parent.name, exact: true }).click();
  const separator = files.getByRole('separator', {
    name: 'Resize Documents column',
  });
  await separator.focus();
  await separator.press('ArrowRight');
  await expect(separator).toHaveAttribute('aria-valuenow', '230');
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(files.getByRole('listbox')).toHaveCount(1);
  await expect(files.getByLabel('Folder view')).toHaveCount(0);
  await expect(files.getByLabel('Sort files')).toHaveCount(0);
  await expect(files.locator('.miller-column-heading:visible')).toHaveCount(0);
  const current = files.getByRole('listbox', { name: `${parent.name} column` });
  const browser = await bounds(files.locator('.miller-browser'));
  expect((await bounds(current)).width).toBeGreaterThanOrEqual(
    browser.width - 2,
  );
  await current.focus();
  await current.press('Home');
  await expect(current).toBeFocused();
  await expect(
    current.getByRole('option', { name: child.name, exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await current.press('ArrowDown');
  await expect(
    current.getByRole('option', { name: sibling.name, exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await current.press('ArrowUp');
  await current.press('Enter');
  await expect(
    files.getByRole('listbox', { name: `${child.name} column` }),
  ).toBeFocused();
  await files.getByRole('option', { name: file.name, exact: true }).click();
  await files.getByRole('button', { name: 'Up', exact: true }).click();
  await expect(
    current.getByRole('option', { name: child.name, exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await page.setViewportSize({ width: 1440, height: 900 });
  await expect(
    files.getByRole('listbox', { name: 'Workspace column', exact: true }),
  ).toBeVisible();
  await expect(separator).toHaveAttribute('aria-valuenow', '230');
  await expect(files.getByLabel('Folder view')).toHaveValue('columns');
  await expect
    .poll(
      async () =>
        (await persistedLayout(page, request))?.windows.find(
          (win: { id: string }) => win.id === 'files:documents',
        )?.viewState.columnWidths,
    )
    .toMatchObject({ documents: 230 });
});

test('narrow Files sorts from its view menu and retains the wide view', async ({
  page,
  request,
}) => {
  const parent = await createEntry(request, 'E2E Narrow Sort', 'folder');
  const larger = await createEntry(
    request,
    'E2E Larger.txt',
    'file',
    parent.id,
    'Larger document content',
  );
  const smaller = await createEntry(
    request,
    'E2E Smaller.txt',
    'file',
    parent.id,
    's',
  );
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.reload();
  const files = filesWindow(page);
  await files.getByRole('option', { name: parent.name, exact: true }).click();
  await files.getByLabel('Folder view').selectOption('list');
  const list = files.getByRole('listbox', { name: `${parent.name} files` });
  await expect(list.getByRole('option').first()).toHaveAttribute(
    'aria-label',
    larger.name,
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(files.getByLabel('Folder view')).toHaveCount(0);
  await expect(files.locator('.files-sort')).toHaveCount(0);
  await expect(files.getByRole('status')).toContainText('2 items');
  const options = files.getByRole('button', { name: 'File view options' });
  await options.focus();
  await options.press('ArrowDown');
  await expect(menuItem(page, 'Sort by Name')).toHaveAttribute(
    'aria-checked',
    'true',
  );
  await page.locator('.ns-menu-layer').getByRole('menu').press('Escape');
  await expect(options).toBeFocused();
  await options.click();
  await menuItem(page, 'Sort by Size').click();
  await expect(list.getByRole('option').first()).toHaveAttribute(
    'aria-label',
    smaller.name,
  );
  await options.click();
  await menuItem(page, 'Descending').click();
  await expect(list.getByRole('option').first()).toHaveAttribute(
    'aria-label',
    larger.name,
  );
  await options.click();
  await expect(menuItem(page, 'Sort by Size')).toHaveAttribute(
    'aria-checked',
    'true',
  );
  await expect(menuItem(page, 'Descending')).toHaveAttribute(
    'aria-checked',
    'true',
  );
  await page.locator('.ns-menu-layer').getByRole('menu').press('Escape');
  await expect(options).toBeFocused();
  await page.setViewportSize({ width: 1440, height: 900 });
  await expect(options).toHaveCount(0);
  await expect(files.getByLabel('Folder view')).toHaveValue('list');
  await expect(files.getByLabel('Sort files', { exact: true })).toHaveValue(
    'size',
  );
  await expect(
    files.getByRole('button', { name: 'Sort ascending', exact: true }),
  ).toBeVisible();
  await expect
    .poll(
      async () =>
        (await persistedLayout(page, request))?.windows.find(
          (win: { id: string }) => win.id === 'files:documents',
        )?.viewState,
    )
    .toMatchObject({ view: 'list', sort: 'size', descending: true });
});

for (const width of [1440, 390]) {
  test(`metadata refresh preserves new navigation and repairs deleted ancestors at ${width}px`, async ({
    page,
    request,
  }) => {
    const parent = await createEntry(request, 'E2E Parent', 'folder');
    const child = await createEntry(request, 'E2E Child', 'folder', parent.id);
    const destination = await createEntry(request, 'E2E Destination', 'folder');
    await page.setViewportSize({ width, height: 900 });
    await page.reload();
    const files = filesWindow(page);
    await files.getByRole('option', { name: parent.name, exact: true }).click();
    const childEntry = files.getByRole('option', {
      name: child.name,
      exact: true,
    });
    await expect(childEntry).toBeVisible();
    expect(
      (
        await request.patch(`/api/entries/${parent.id}`, {
          data: { parentId: destination.id, revision: parent.revision },
        })
      ).ok(),
    ).toBeTruthy();

    let markFetched = () => {};
    let releaseResponse = () => {};
    const fetched = new Promise<void>((resolve) => {
      markFetched = resolve;
    });
    const released = new Promise<void>((resolve) => {
      releaseResponse = resolve;
    });
    await page.route(
      '**/api/entries',
      async (route) => {
        const response = await route.fetch();
        markFetched();
        await released;
        await route.fulfill({ response });
      },
      { times: 1 },
    );
    try {
      await page.evaluate(() => globalThis.dispatchEvent(new Event('focus')));
      await fetched;
      // Navigate while the metadata response is still pending, then reconcile
      // the moved ancestor without replacing the user's more recent location.
      await childEntry.click();
    } finally {
      releaseResponse();
    }
    const path = files.getByRole('navigation', { name: 'Folder path' });
    await expect(
      path.getByRole('button', { name: destination.name }),
    ).toBeVisible();
    await expect(
      files.getByRole('listbox', { name: `${child.name} column`, exact: true }),
    ).toBeVisible();
    await expect
      .poll(
        async () =>
          (await persistedLayout(page, request))?.windows.find(
            (win: { id: string }) => win.id === 'files:documents',
          )?.viewState,
      )
      .toMatchObject({
        folderId: child.id,
        pathIds: ['root', 'documents', destination.id, parent.id, child.id],
      });

    expect(
      (await request.post(`/api/entries/${parent.id}/trash`)).ok(),
    ).toBeTruthy();
    await page.evaluate(() => globalThis.dispatchEvent(new Event('focus')));
    await expect(path.getByRole('button', { name: child.name })).toHaveCount(0);
    await expect(
      files.getByRole('listbox', {
        name: `${destination.name} column`,
        exact: true,
      }),
    ).toBeVisible();
    await expect
      .poll(
        async () =>
          (await persistedLayout(page, request))?.windows.find(
            (win: { id: string }) => win.id === 'files:documents',
          )?.viewState,
      )
      .toMatchObject({
        folderId: destination.id,
        pathIds: ['root', 'documents', destination.id],
      });
    await page.reload();
    await expect(
      files.getByRole('listbox', {
        name: `${destination.name} column`,
        exact: true,
      }),
    ).toBeVisible();
  });
}

test('file toolbar menus retain their selection and destination after metadata changes', async ({
  page,
  request,
}) => {
  const selected = await createEntry(request, 'E2E Selected file.txt');
  const other = await createEntry(request, 'E2E Other file.txt');
  const folder = await createEntry(request, 'E2E Menu destination', 'folder');
  await page.reload();
  const files = filesWindow(page);
  await files.getByRole('option', { name: selected.name, exact: true }).click();
  const more = files.getByRole('button', {
    name: 'More file actions',
    exact: true,
  });
  await more.focus();
  await more.press('Enter');
  await expect(menuItem(page, 'Rename')).toBeEnabled();
  await expect(menuItem(page, 'Recycle')).toBeEnabled();
  await expect(menuItem(page, 'Paste Here')).toBeDisabled();
  await expect(menuItem(page, 'Refresh Folder')).toBeEnabled();
  await page.locator('.ns-menu-layer').getByRole('menu').press('Escape');
  await expect(more).toBeFocused();
  await more.click();
  expect(
    (await request.post(`/api/entries/${selected.id}/trash`)).ok(),
  ).toBeTruthy();
  await page.evaluate(() => globalThis.dispatchEvent(new Event('focus')));
  await expect(
    files.getByRole('option', { name: selected.name, exact: true }),
  ).toHaveCount(0);
  const patches: string[] = [];
  page.on('request', (request) => {
    if (request.method() === 'PATCH' && request.url().includes('/api/entries/'))
      patches.push(request.url());
  });
  await menuItem(page, 'Rename').click();
  const error = page.getByRole('dialog', {
    name: 'Action failed',
    exact: true,
  });
  await expect(error).toContainText(/selection changed|no longer available/i);
  await expect(
    page.getByRole('dialog', { name: 'Rename', exact: true }),
  ).toHaveCount(0);
  expect(patches).toEqual([]);
  await error.getByRole('button', { name: 'OK', exact: true }).click();
  await files.getByRole('option', { name: other.name, exact: true }).click();
  await actionMenu(page, files, 'More file actions', 'Rename');
  const rename = page.getByRole('dialog', { name: 'Rename', exact: true });
  await expect(rename.getByRole('textbox')).toHaveValue(other.name);
  await rename.getByRole('button', { name: 'Cancel', exact: true }).click();
  expect(
    (await entries(request)).find((entry) => entry.id === other.id)?.name,
  ).toBe(other.name);

  await files.getByRole('option', { name: folder.name, exact: true }).click();
  await files.getByRole('button', { name: 'New', exact: true }).click();
  expect(
    (await request.post(`/api/entries/${folder.id}/trash`)).ok(),
  ).toBeTruthy();
  await page.evaluate(() => globalThis.dispatchEvent(new Event('focus')));
  await expect(
    files.getByRole('navigation', { name: 'Folder path' }).getByRole('button', {
      name: folder.name,
      exact: false,
    }),
  ).toHaveCount(0);
  await menuItem(page, 'New Folder').click();
  await expect(error).toContainText('This folder changed.');
  await expect(
    page.getByRole('dialog', { name: 'New Folder', exact: true }),
  ).toHaveCount(0);
});

test('Inspector actions reject a target recycled while its menu is open', async ({
  page,
  request,
}) => {
  const selected = await createEntry(request, 'E2E Inspector target.txt');
  await page.reload();
  const files = filesWindow(page);
  await files.getByRole('option', { name: selected.name, exact: true }).click();
  await files.getByRole('button', { name: 'Inspect', exact: true }).click();
  const inspector = page.locator('[data-app-id="inspector"]');
  await expect(inspector).toContainText('Follows selection');
  await expect(
    inspector.getByRole('button', { name: 'Open', exact: true }),
  ).toBeInViewport();
  const revision = inspector.getByTestId('inspector-revision');
  await expect(revision).toBeHidden();
  const details = inspector.locator('summary').filter({ hasText: /^Details$/ });
  await details.click();
  await expect(revision).toHaveText(String(selected.revision));
  await details.click();
  await expect(revision).toBeHidden();
  await inspector
    .getByRole('button', { name: 'More inspector actions', exact: true })
    .click();
  await expect(menuItem(page, 'Rename')).toBeEnabled();
  expect(
    (await request.post(`/api/entries/${selected.id}/trash`)).ok(),
  ).toBeTruthy();
  await page.evaluate(() => globalThis.dispatchEvent(new Event('focus')));
  await expect(inspector).toContainText('This item is in the Recycler.');
  await expect(
    inspector.getByRole('button', { name: 'Open', exact: true }),
  ).toBeDisabled();
  await menuItem(page, 'Rename').click();
  const error = page.getByRole('dialog', {
    name: 'Action failed',
    exact: true,
  });
  await expect(error).toContainText(
    'This action is no longer available for this item.',
  );
  await expect(
    page.getByRole('dialog', { name: 'Rename', exact: true }),
  ).toHaveCount(0);
  await error.getByRole('button', { name: 'OK', exact: true }).click();
  await inspector.getByRole('button', { name: 'Restore', exact: true }).click();
  await expect(inspector).not.toContainText('This item is in the Recycler.');
  expect(
    (await entries(request)).find((entry) => entry.id === selected.id)?.name,
  ).toBe(selected.name);
});
