import {
  actionMenu,
  createEntry,
  entries,
  expect,
  filesWindow,
  menuItem,
  persistedLayout,
  test,
} from './helpers';

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
    name: 'Unable to complete the operation',
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
  await inspector
    .getByRole('button', { name: 'More inspector actions', exact: true })
    .click();
  await expect(menuItem(page, 'Rename')).toBeEnabled();
  expect(
    (await request.post(`/api/entries/${selected.id}/trash`)).ok(),
  ).toBeTruthy();
  await page.evaluate(() => globalThis.dispatchEvent(new Event('focus')));
  await expect(inspector).toContainText('This item is in the Recycler.');
  await menuItem(page, 'Rename').click();
  const error = page.getByRole('dialog', {
    name: 'Unable to complete the operation',
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
