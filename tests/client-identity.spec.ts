import { expect, test as base } from '@playwright/test';
import type { Page, TestInfo } from '@playwright/test';
import { launch, persistedLayout, test as desktopTest } from './helpers';
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

async function projectFolder(page: Page) {
  await launch(page, 'project-files');
  return page.getByRole('textbox', { name: 'Host project directory' });
}

desktopTest(
  'independent browser contexts restore separate desktop layouts',
  async ({ page, request, browser }, info) => {
    const independent = await browser.newContext({
      baseURL: info.project.use.baseURL,
    });
    const other = await independent.newPage();
    const runtime = await monitorRuntimeErrors(other);
    try {
      await other.goto('/');
      await expect(other.locator('[data-app-id="welcome"]')).toBeVisible();
      const clients = await Promise.all(
        [page, other].map((candidate) =>
          candidate.evaluate(() => localStorage.getItem('nextstep-browser-id')),
        ),
      );
      expect(clients[0]).toBeTruthy();
      expect(clients[1]).toBeTruthy();
      expect(clients[1]).not.toBe(clients[0]);

      await launch(page, 'project-files');
      await launch(other, 'appearance');
      await expect
        .poll(async () => {
          const [left, right] = await Promise.all(
            [page, other].map((candidate) =>
              persistedLayout(candidate, request),
            ),
          );
          return [left, right].map((layout) =>
            layout?.windows.map((window: { appId: string }) => window.appId),
          );
        })
        .toEqual([
          ['welcome', 'project-files'],
          ['welcome', 'appearance'],
        ]);
      await Promise.all([page.reload(), other.reload()]);
      await expect(page.locator('[data-app-id="project-files"]')).toBeVisible();
      await expect(page.locator('[data-app-id="appearance"]')).toHaveCount(0);
      await expect(other.locator('[data-app-id="appearance"]')).toBeVisible();
      await expect(other.locator('[data-app-id="project-files"]')).toHaveCount(
        0,
      );
    } finally {
      await independent.close();
      await verifyRuntime(runtime, info);
    }
  },
);

for (const change of ['remove', 'replace'] as const) {
  desktopTest(
    `another tab can ${change} stored identity while the original work and layout stay bound`,
    async ({ page, context }, info) => {
      const folder = await projectFolder(page);
      const originalPath = '/example/original-folder';
      await folder.fill(originalPath);
      const browserId = await page.evaluate(() =>
        localStorage.getItem('nextstep-browser-id'),
      );
      expect(browserId).toBeTruthy();
      const element = await folder.elementHandle();
      expect(element).not.toBeNull();
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
        await expect(folder).toHaveValue(originalPath);
        const newPath = '/example/newer-folder';
        await folder.fill(newPath);
        const savingLayout = page.waitForRequest(
          (request) =>
            new URL(request.url()).pathname === '/api/layout' &&
            request.method() === 'PUT' &&
            request
              .postDataJSON()
              .layout.windows.some(
                (window: { appId: string; zoomed: boolean }) =>
                  window.appId === 'project-files' && window.zoomed,
              ),
        );
        await page
          .locator('[data-app-id="project-files"]')
          .getByRole('button', { name: /^Maximize / })
          .click();
        const layout = await savingLayout;
        expect(layout.postDataJSON().clientId).toBe(browserId);
        expect(await element!.evaluate((node) => node.isConnected)).toBe(true);
        await expect(folder).toHaveValue(newPath);
        expect(
          await page.evaluate(() =>
            localStorage.getItem('nextstep-browser-id'),
          ),
        ).toBe(change === 'remove' ? null : 'replacement-browser');
        expect(bootstraps).toEqual([]);
      } finally {
        await other.close();
        await verifyRuntime(runtime, info);
      }
    },
  );
}
