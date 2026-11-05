import { expect, test as base } from '@playwright/test';
import type { Page, TestInfo } from '@playwright/test';
import type { DraftRecord } from '../apps/dashboard/src/shared/drafts/storage';
import {
  createEntry,
  editorWindow,
  filesWindow,
  openDocument,
  persistedLayout,
  readContent,
  test as desktopTest,
} from './helpers';
import { monitorRuntimeErrors } from './runtime-errors';

type RuntimeMonitor = Awaited<ReturnType<typeof monitorRuntimeErrors>>;

async function verifyRuntime(
  runtime: RuntimeMonitor,
  info: TestInfo,
  index = 0,
) {
  try {
    expect(runtime.documents.size).toBeGreaterThan(0);
    if (runtime.pageErrors.length)
      await info.attach(`browser-page-errors-${index}`, {
        body: JSON.stringify(runtime.pageErrors, null, 2),
        contentType: 'application/json',
      });
    expect(
      runtime.errors,
      'Identity and startup failures must not throw uncaught JavaScript.',
    ).toEqual([]);
  } finally {
    runtime.dispose();
  }
}

const test = base.extend<{
  monitorPage: (page: Page) => Promise<void>;
}>({
  monitorPage: [
    async ({ page }, use, info) => {
      const monitors: RuntimeMonitor[] = [await monitorRuntimeErrors(page)];
      try {
        await use(async (other) => {
          monitors.push(await monitorRuntimeErrors(other));
        });
      } finally {
        for (const [index, monitor] of monitors.entries())
          await verifyRuntime(monitor, info, index);
      }
    },
    { auto: true },
  ],
});

function gate() {
  let release = () => {};
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { pending, release };
}

async function emptySameOriginPage(page: Page) {
  await page.route('**/identity-coordinator', (route) =>
    route.fulfill({
      contentType: 'text/html',
      body: '<!doctype html><title>Identity test coordinator</title>',
    }),
  );
  await page.goto('/identity-coordinator');
}

async function initializationRecord(page: Page) {
  return page.evaluate(
    () =>
      new Promise<{ id: string; state: string } | undefined>(
        (resolve, reject) => {
          const opening = indexedDB.open('axv-browser-identity', 1);
          opening.onerror = () => reject(opening.error);
          opening.onsuccess = () => {
            const db = opening.result;
            const transaction = db.transaction('initialization', 'readonly');
            const request = transaction
              .objectStore('initialization')
              .get('client');
            transaction.oncomplete = () => {
              db.close();
              resolve(
                request.result as { id: string; state: string } | undefined,
              );
            };
            transaction.onabort = () => {
              db.close();
              reject(transaction.error);
            };
          };
        },
      ),
  );
}

test('feature requests wait for accepted bootstrap and browser identity', async ({
  page,
}) => {
  const bootstrap = gate();
  const paths: string[] = [];
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname;
    if (path.startsWith('/api/')) paths.push(path);
  });
  await page.route('**/api/bootstrap?**', async (route) => {
    await bootstrap.pending;
    await route.continue();
  });
  try {
    await page.goto('/');
    await expect(page.getByLabel('AXV startup')).toBeVisible();
    await expect.poll(() => paths).toEqual(['/api/bootstrap']);
    await page.evaluate(
      () =>
        new Promise<void>((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
        ),
    );
    expect(paths).toEqual(['/api/bootstrap']);
    await expect(
      page.getByRole('complementary', { name: 'Application Dock' }),
    ).toHaveCount(0);
    expect(
      await page.evaluate(() => localStorage.getItem('nextstep-browser-id')),
    ).toBeTruthy();
    bootstrap.release();
    await expect(
      page.getByRole('complementary', { name: 'Application Dock' }),
    ).toBeVisible();
    await expect
      .poll(() => paths)
      .toEqual(
        expect.arrayContaining([
          '/api/bootstrap',
          '/api/host/codex/info',
          '/api/compute/environment',
          '/api/extensions',
        ]),
      );
  } finally {
    bootstrap.release();
  }
});

test('simultaneous initial pages coordinate one browser identity with real Web Locks', async ({
  page,
  context,
  monitorPage,
}) => {
  await emptySameOriginPage(page);
  await page.evaluate(
    () =>
      new Promise<void>((ready, reject) => {
        void navigator.locks
          .request('axv-browser-client-id', async () => {
            ready();
            await new Promise<void>((release) => {
              addEventListener('axv-release-client-lock', () => release(), {
                once: true,
              });
            });
          })
          .catch(reject);
      }),
  );
  const first = await context.newPage();
  const second = await context.newPage();
  await monitorPage(first);
  await monitorPage(second);
  const clientIds: (string | null)[] = [];
  for (const candidate of [first, second])
    candidate.on('request', (request) => {
      const url = new URL(request.url());
      if (url.pathname === '/api/bootstrap')
        clientIds.push(url.searchParams.get('clientId'));
    });
  try {
    await Promise.all([first.goto('/'), second.goto('/')]);
    await expect
      .poll(() =>
        page.evaluate(async () => {
          const pending = (await navigator.locks.query()).pending || [];
          return pending.filter((lock) => lock.name === 'axv-browser-client-id')
            .length;
        }),
      )
      .toBe(2);
    expect(clientIds).toEqual([]);
    expect(
      await page.evaluate(() => localStorage.getItem('nextstep-browser-id')),
    ).toBeNull();
    await page.evaluate(() =>
      dispatchEvent(new Event('axv-release-client-lock')),
    );
    for (const candidate of [first, second])
      await expect(
        candidate.getByRole('complementary', { name: 'Application Dock' }),
      ).toBeVisible();
    expect(clientIds).toHaveLength(2);
    expect(clientIds[0]).toBeTruthy();
    expect(clientIds[1]).toBe(clientIds[0]);
    for (const candidate of [first, second])
      expect(
        await candidate.evaluate(() =>
          localStorage.getItem('nextstep-browser-id'),
        ),
      ).toBe(clientIds[0]);
  } finally {
    await page.evaluate(() =>
      dispatchEvent(new Event('axv-release-client-lock')),
    );
    await first.close();
    await second.close();
  }
});

for (const operation of ['getItem', 'setItem'] as const) {
  test(`browser identity ${operation} failure prevents bootstrap and supports Retry`, async ({
    page,
  }) => {
    await page.addInitScript((method) => {
      let blocked = true;
      const original = Storage.prototype[method];
      addEventListener('axv-enable-identity-storage', () => {
        blocked = false;
      });
      if (method === 'getItem') {
        Storage.prototype.getItem = function (key: string) {
          if (blocked && this === localStorage && key === 'nextstep-browser-id')
            throw new DOMException('Fixture storage denied', 'SecurityError');
          return (original as Storage['getItem']).call(this, key);
        };
      } else {
        Storage.prototype.setItem = function (key: string, value: string) {
          if (blocked && this === localStorage && key === 'nextstep-browser-id')
            throw new DOMException(
              'Fixture storage full',
              'QuotaExceededError',
            );
          return (original as Storage['setItem']).call(this, key, value);
        };
      }
    }, operation);
    const apiPaths: string[] = [];
    page.on('request', (request) => {
      const path = new URL(request.url()).pathname;
      if (path.startsWith('/api/')) apiPaths.push(path);
    });
    await page.goto('/');
    const timeOrigin = await page.evaluate(() => performance.timeOrigin);
    await expect(page.getByRole('alert')).toContainText(
      operation === 'getItem'
        ? 'AXV cannot read this browser’s saved identity'
        : 'could not save this browser',
    );
    expect(apiPaths).toEqual([]);
    const pending =
      operation === 'setItem' ? await initializationRecord(page) : undefined;
    if (operation === 'setItem') {
      expect(pending?.state).toBe('pending');
      expect(pending?.id).toBeTruthy();
    }
    await expect(
      page.getByRole('complementary', { name: 'Application Dock' }),
    ).toHaveCount(0);
    await page.evaluate(() =>
      dispatchEvent(new Event('axv-enable-identity-storage')),
    );
    await page.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(
      page.getByRole('complementary', { name: 'Application Dock' }),
    ).toBeVisible();
    expect(apiPaths.filter((path) => path === '/api/bootstrap')).toHaveLength(
      1,
    );
    expect(await page.evaluate(() => performance.timeOrigin)).toBe(timeOrigin);
    expect(
      await page.evaluate(() => localStorage.getItem('nextstep-browser-id')),
    ).toBeTruthy();
    if (pending) {
      expect(
        await page.evaluate(() => localStorage.getItem('nextstep-browser-id')),
      ).toBe(pending.id);
      expect(await initializationRecord(page)).toEqual({
        id: pending.id,
        state: 'confirmed',
      });
    }
  });
}

for (const failure of ['unavailable', 'aborted'] as const) {
  test(`first identity waits for a committed election when IndexedDB is ${failure}`, async ({
    page,
  }) => {
    await page.addInitScript((failure) => {
      let blocked = true;
      addEventListener('axv-enable-identity-database', () => {
        blocked = false;
      });
      if (failure === 'unavailable') {
        const original = IDBFactory.prototype.open;
        IDBFactory.prototype.open = function (name: string, version?: number) {
          if (blocked && name === 'axv-browser-identity')
            throw new DOMException(
              'Identity database unavailable',
              'SecurityError',
            );
          return original.call(this, name, version);
        };
      } else {
        const original = IDBObjectStore.prototype.put;
        IDBObjectStore.prototype.put = function (
          value: unknown,
          key?: IDBValidKey,
        ) {
          const request = original.call(this, value, key);
          if (blocked && this.transaction.db.name === 'axv-browser-identity')
            this.transaction.abort();
          return request;
        };
      }
    }, failure);
    const bootstraps: string[] = [];
    page.on('request', (request) => {
      if (new URL(request.url()).pathname === '/api/bootstrap')
        bootstraps.push(request.url());
    });
    await page.goto('/');
    await expect(page.getByRole('alert')).toContainText(
      failure === 'unavailable'
        ? 'Browser storage is unavailable'
        : 'could not finish saving this browser',
    );
    expect(bootstraps).toEqual([]);
    expect(
      await page.evaluate(() => localStorage.getItem('nextstep-browser-id')),
    ).toBeNull();
    await page.evaluate(() =>
      dispatchEvent(new Event('axv-enable-identity-database')),
    );
    await page.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(
      page.getByRole('complementary', { name: 'Application Dock' }),
    ).toBeVisible();
    expect(bootstraps).toHaveLength(1);
  });
}

test('an existing browser identity needs no new IndexedDB record', async ({
  page,
}) => {
  await page.addInitScript(() => {
    localStorage.setItem('nextstep-browser-id', 'existing-browser-identity');
    const original = IDBFactory.prototype.open;
    IDBFactory.prototype.open = function (name: string, version?: number) {
      if (name === 'axv-browser-identity')
        throw new DOMException(
          'New identity storage must not be needed',
          'SecurityError',
        );
      return original.call(this, name, version);
    };
  });
  await page.goto('/');
  await expect(
    page.getByRole('complementary', { name: 'Application Dock' }),
  ).toBeVisible();
  expect(
    await page.evaluate(() => localStorage.getItem('nextstep-browser-id')),
  ).toBe('existing-browser-identity');
});

test('clearing only the browser identity does not recreate it from a confirmed record', async ({
  page,
  context,
  monitorPage,
}) => {
  await page.goto('/');
  await expect(
    page.getByRole('complementary', { name: 'Application Dock' }),
  ).toBeVisible();
  const confirmed = await initializationRecord(page);
  expect(confirmed?.state).toBe('confirmed');
  const other = await context.newPage();
  await monitorPage(other);
  try {
    await emptySameOriginPage(other);
    await other.evaluate(() => localStorage.removeItem('nextstep-browser-id'));
    const bootstraps: string[] = [];
    other.on('request', (request) => {
      if (new URL(request.url()).pathname === '/api/bootstrap')
        bootstraps.push(request.url());
    });
    await other.goto('/');
    await expect(other.getByRole('alert')).toContainText(
      'This browser’s saved identity changed or is missing',
    );
    expect(bootstraps).toEqual([]);
    expect(
      await other.evaluate(() => localStorage.getItem('nextstep-browser-id')),
    ).toBeNull();
    expect(await initializationRecord(other)).toEqual(confirmed);
    await expect(
      page.getByRole('complementary', { name: 'Application Dock' }),
    ).toBeVisible();
  } finally {
    await other.close();
  }
});

async function draftsFor(page: Page, entryId: string) {
  return page.evaluate(
    (id) =>
      new Promise<DraftRecord[]>((resolve, reject) => {
        const opening = indexedDB.open('nextstep-drafts', 1);
        opening.onerror = () => reject(opening.error);
        opening.onsuccess = () => {
          const db = opening.result;
          const transaction = db.transaction('drafts', 'readonly');
          const request = transaction.objectStore('drafts').getAll();
          transaction.oncomplete = () => {
            db.close();
            resolve(
              (request.result as DraftRecord[]).filter(
                (record) => record.entryId === id,
              ),
            );
          };
          transaction.onabort = () => {
            db.close();
            reject(transaction.error);
          };
        };
      }),
    entryId,
  );
}

desktopTest(
  'independent browser contexts recover separate unsaved drafts for the same document',
  async ({ page, request, browser }, info) => {
    const entry = await createEntry(
      request,
      'E2E Independent client drafts.txt',
    );
    await page.reload();
    const first = await openDocument(page, entry);
    const firstText = first.getByRole('textbox', { name: 'Document text' });
    const independent = await browser.newContext({
      baseURL: info.project.use.baseURL,
    });
    const other = await independent.newPage();
    const runtime = await monitorRuntimeErrors(other);
    try {
      await other.goto('/');
      await other
        .locator('[data-app-id="welcome"]')
        .getByRole('button', { name: 'Documents', exact: true })
        .click();
      await expect(filesWindow(other)).toBeVisible();
      const second = await openDocument(other, entry);
      const secondText = second.getByRole('textbox', { name: 'Document text' });
      const clients = await Promise.all(
        [page, other].map((candidate) =>
          candidate.evaluate(() => localStorage.getItem('nextstep-browser-id')),
        ),
      );
      expect(clients[0]).toBeTruthy();
      expect(clients[1]).toBeTruthy();
      expect(clients[1]).not.toBe(clients[0]);

      await firstText.fill('Unsaved text in the original browser container.');
      await expect(first).toContainText('Local recovery copy up to date');
      await secondText.fill('Independent unsaved text in the other container.');
      await expect(second).toContainText('Local recovery copy up to date');
      const originals = await Promise.all(
        [page, other].map(async (candidate, index) => {
          const records = await draftsFor(candidate, entry.id);
          expect(records).toHaveLength(1);
          expect(records[0]).toMatchObject({
            clientId: clients[index],
            entryId: entry.id,
            baseRevision: entry.revision,
          });
          await expect
            .poll(async () =>
              (await persistedLayout(candidate, request))?.windows.some(
                (window: { id: string }) => window.id === `notes:${entry.id}`,
              ),
            )
            .toBe(true);
          return records[0]!;
        }),
      );
      expect(originals[0]!.databaseId).toBe(originals[1]!.databaseId);
      expect(originals[0]!.draftId).not.toBe(originals[1]!.draftId);
      expect(originals[0]!.key).not.toBe(originals[1]!.key);
      await expect(first.getByTestId('save-status')).toHaveText(
        'Unsaved changes',
      );
      await expect(second.getByTestId('save-status')).toHaveText(
        'Unsaved changes',
      );

      page.once('dialog', (dialog) => dialog.accept());
      await page.reload();
      await editorWindow(page, entry.id)
        .getByRole('button', { name: 'Restore Draft', exact: true })
        .click();
      await expect(firstText).toHaveValue(originals[0]!.text);
      await expect(first.getByTestId('save-status')).toHaveText(
        'Recovered draft — not saved',
      );
      await expect(secondText).toHaveValue(originals[1]!.text);
      await expect(second.getByTestId('save-status')).toHaveText(
        'Unsaved changes',
      );
      await firstText.fill(
        'Newer typing stays in the original browser container.',
      );
      await expect(first).toContainText('Local recovery copy up to date');

      other.once('dialog', (dialog) => dialog.accept());
      await other.reload();
      await editorWindow(other, entry.id)
        .getByRole('button', { name: 'Restore Draft', exact: true })
        .click();
      await expect(secondText).toHaveValue(originals[1]!.text);
      await expect(firstText).toHaveValue(
        'Newer typing stays in the original browser container.',
      );
      await expect(first.getByTestId('save-status')).toHaveText(
        'Unsaved changes',
      );
      await expect(second.getByTestId('save-status')).toHaveText(
        'Recovered draft — not saved',
      );
      for (const [index, candidate] of [page, other].entries()) {
        const records = await draftsFor(candidate, entry.id);
        expect(records).toHaveLength(1);
        expect(records[0]).toMatchObject({
          key: originals[index]!.key,
          clientId: clients[index],
          databaseId: originals[index]!.databaseId,
          draftId: originals[index]!.draftId,
          baseRevision: entry.revision,
          text:
            index === 0
              ? 'Newer typing stays in the original browser container.'
              : originals[1]!.text,
        });
      }
      expect(await readContent(request, entry.id)).toBe('Initial text');
    } finally {
      await independent.close();
      await verifyRuntime(runtime, info);
    }
  },
);

for (const change of ['remove', 'replace'] as const) {
  desktopTest(
    `another tab can ${change} stored identity without remounting or moving an open draft`,
    async ({ page, request, context }, info) => {
      const entry = await createEntry(
        request,
        `E2E ${change} browser identity.txt`,
      );
      await page.reload();
      const editor = await openDocument(page, entry);
      const text = editor.getByRole('textbox', { name: 'Document text' });
      await text.fill('Original unsaved text remains in this editor.');
      await expect(editor).toContainText('Local recovery copy up to date');
      const original = (await draftsFor(page, entry.id))[0]!;
      expect(original.text).toBe(
        'Original unsaved text remains in this editor.',
      );
      const element = await text.elementHandle();
      expect(element).not.toBeNull();
      const sessionPointers = await page.evaluate(() =>
        Object.entries(sessionStorage).filter(([key]) =>
          key.startsWith('["nextstep-editor",'),
        ),
      );
      const bootstraps: string[] = [];
      page.on('request', (request) => {
        if (new URL(request.url()).pathname === '/api/bootstrap')
          bootstraps.push(request.url());
      });
      const other = await context.newPage();
      const runtime = await monitorRuntimeErrors(other);
      try {
        await emptySameOriginPage(other);
        await other.evaluate((action) => {
          if (action === 'remove')
            localStorage.removeItem('nextstep-browser-id');
          else
            localStorage.setItem('nextstep-browser-id', 'replacement-browser');
        }, change);
        await expect(page.getByRole('alert')).toContainText(
          'Browser storage changed or is unavailable',
        );
        expect(await element!.evaluate((node) => node.isConnected)).toBe(true);
        await expect(text).toHaveValue(original.text);
        await text.fill('Newer typing still belongs to the original identity.');
        await expect
          .poll(async () => (await draftsFor(page, entry.id))[0]?.text)
          .toBe('Newer typing still belongs to the original identity.');
        const retained = await draftsFor(page, entry.id);
        expect(retained).toHaveLength(1);
        expect(retained[0]).toMatchObject({
          key: original.key,
          draftId: original.draftId,
          clientId: original.clientId,
          databaseId: original.databaseId,
          baseRevision: original.baseRevision,
        });
        const savingLayout = page.waitForRequest(
          (request) =>
            new URL(request.url()).pathname === '/api/layout' &&
            request.method() === 'PUT',
        );
        await editor.getByRole('button', { name: /^Maximize / }).click();
        const layout = await savingLayout;
        expect(layout.postDataJSON().clientId).toBe(original.clientId);
        expect(await element!.evaluate((node) => node.isConnected)).toBe(true);
        expect(
          await page.evaluate(() =>
            Object.entries(sessionStorage).filter(([key]) =>
              key.startsWith('["nextstep-editor",'),
            ),
          ),
        ).toEqual(sessionPointers);
        expect(
          await page.evaluate(() =>
            localStorage.getItem('nextstep-browser-id'),
          ),
        ).toBe(change === 'remove' ? null : 'replacement-browser');
        expect(bootstraps).toEqual([]);
        expect(await readContent(request, entry.id)).toBe('Initial text');
      } finally {
        await other.close();
        await verifyRuntime(runtime, info);
      }
    },
  );
}
