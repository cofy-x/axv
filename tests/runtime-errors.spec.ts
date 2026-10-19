import { createServer } from 'node:http';
import type { ServerResponse } from 'node:http';
import type { AddressInfo } from 'node:net';
import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import {
  installRuntimeErrorObservers,
  monitorRuntimeErrors,
} from './runtime-errors';

const base = '/__runtime-errors';

async function serveHTML(page: Page, path: string, body: string) {
  await page.route(`**${base}/${path}`, (route) =>
    route.fulfill({ contentType: 'text/html', body }),
  );
}

test('runtime guard captures initial and asynchronous exceptions and all rejection reasons across reloads', async ({
  page,
}) => {
  const monitor = await monitorRuntimeErrors(page);
  await serveHTML(
    page,
    'throws',
    `<!doctype html>
    <script>throw new Error('INITIAL_RUNTIME_ERROR')</script>
    <script>
      setTimeout(() => { throw new Error('TIMER_RUNTIME_ERROR'); }, 0);
      setTimeout(() => { throw new Error('Fetch API cannot load http://127.0.0.1/api/compute/environment due to access control checks.'); }, 0);
      Promise.reject(new Error('ERROR_REJECTION'));
      Promise.reject('STRING_REJECTION');
    </script>`,
  );
  await page.goto(`${base}/throws`);
  await expect.poll(() => monitor.errors.length).toBe(5);
  expect(monitor.errors).toEqual(
    expect.arrayContaining([
      expect.objectContaining({
        kind: 'error',
        message: expect.stringContaining('INITIAL_RUNTIME_ERROR'),
      }),
      expect.objectContaining({
        kind: 'error',
        message: expect.stringContaining('TIMER_RUNTIME_ERROR'),
      }),
      expect.objectContaining({
        kind: 'error',
        message: expect.stringContaining('due to access control checks.'),
      }),
      expect.objectContaining({
        kind: 'unhandledrejection',
        message: 'ERROR_REJECTION',
      }),
      expect.objectContaining({
        kind: 'unhandledrejection',
        message: 'STRING_REJECTION',
      }),
    ]),
  );
  const firstDocument = monitor.errors[0].documentId;
  expect(
    monitor.errors.every((error) => error.documentId === firstDocument),
  ).toBeTruthy();
  await page.reload();
  await expect.poll(() => monitor.errors.length).toBe(10);
  expect(new Set(monitor.errors.map((error) => error.documentId)).size).toBe(2);
  expect(
    [...monitor.documents.values()].filter((url) =>
      url.endsWith(`${base}/throws`),
    ),
  ).toHaveLength(2);
  expect(monitor.pageErrors.length).toBeGreaterThan(0);
  monitor.dispose();
});

test('runtime guard observes non-secure cross-origin child documents without crypto.randomUUID', async ({
  page,
}) => {
  const monitor = await monitorRuntimeErrors(page);
  await serveHTML(
    page,
    'frame',
    `<!doctype html><script>throw new Error('FRAME_RUNTIME_ERROR')</script>`,
  );
  await serveHTML(
    page,
    'parent',
    `<!doctype html><iframe src="http://runtime-frame.invalid${base}/frame"></iframe>`,
  );
  await page.goto(`${base}/parent`);
  await expect.poll(() => monitor.errors.length).toBe(1);
  expect(monitor.errors[0]).toMatchObject({
    kind: 'error',
    message: expect.stringContaining('FRAME_RUNTIME_ERROR'),
    url: expect.stringContaining(`${base}/frame`),
  });
  expect([...monitor.documents.values()]).toEqual(
    expect.arrayContaining([
      expect.stringContaining(`${base}/parent`),
      expect.stringContaining(`${base}/frame`),
    ]),
  );
  const frame = page.frame({
    url: `http://runtime-frame.invalid${base}/frame`,
  });
  expect(frame).not.toBeNull();
  expect(await frame!.evaluate(() => isSecureContext)).toBe(false);
  expect(await frame!.evaluate(() => typeof crypto.randomUUID)).toBe(
    'undefined',
  );
  monitor.dispose();
});

test('runtime guard reports installation failure even after a document was ready', async ({
  page,
}) => {
  const monitor = await monitorRuntimeErrors(page);
  await serveHTML(
    page,
    'setup-failure',
    '<!doctype html><title>Setup failure fixture</title>',
  );
  await page.goto(`${base}/setup-failure`);
  expect(monitor.documents.size).toBeGreaterThan(0);
  await page.evaluate(() => {
    const original = globalThis.addEventListener;
    Reflect.set(globalThis, '__restoreRuntimeErrorListener', () => {
      globalThis.addEventListener = original;
    });
    globalThis.addEventListener = () => {
      throw new Error('MONITOR_SETUP_FAILURE');
    };
  });
  try {
    await page.evaluate(installRuntimeErrorObservers, monitor.marker);
  } finally {
    await page.evaluate(() => {
      const restore = Reflect.get(
        globalThis,
        '__restoreRuntimeErrorListener',
      ) as () => void;
      restore();
      Reflect.deleteProperty(globalThis, '__restoreRuntimeErrorListener');
    });
  }
  await expect.poll(() => monitor.errors.length).toBe(1);
  expect(monitor.errors[0]).toMatchObject({
    kind: 'monitor',
    message: 'MONITOR_SETUP_FAILURE',
  });
  monitor.dispose();
});

test('runtime guard does not mistake handled rejections or failed resources for uncaught JavaScript', async ({
  page,
}) => {
  const monitor = await monitorRuntimeErrors(page);
  await page.route(`**${base}/missing-image`, (route) =>
    route.fulfill({ status: 404, body: '' }),
  );
  await page.route(`**${base}/handled-fetch`, (route) => route.abort());
  await serveHTML(
    page,
    'handled',
    `<!doctype html>
    <img src="${base}/missing-image">
    <script>
      globalThis.handled = false;
      Promise.all([
        Promise.reject(new Error('HANDLED_ERROR')).catch(() => {}),
        Promise.reject('HANDLED_STRING').catch(() => {}),
        fetch('${base}/handled-fetch').catch(() => {}),
      ]).then(() => { globalThis.handled = true; });
    </script>`,
  );
  await page.goto(`${base}/handled`);
  await page.waitForFunction(() => Reflect.get(globalThis, 'handled'));
  // A timer task after the handled promises gives rejection reporting its turn.
  await page.evaluate(
    () => new Promise<void>((resolve) => setTimeout(resolve, 0)),
  );
  expect(monitor.errors).toEqual([]);
  expect([...monitor.documents.values()]).toContainEqual(
    expect.stringContaining(`${base}/handled`),
  );
  monitor.dispose();
});

test('runtime guard preserves navigation-cancellation diagnostics without inventing an uncaught exception', async ({
  page,
}, testInfo) => {
  const monitor = await monitorRuntimeErrors(page);
  let replacementStarted!: () => void;
  const started = new Promise<void>((resolve) => {
    replacementStarted = resolve;
  });
  let pendingFetchStarted!: () => void;
  const fetchStarted = new Promise<void>((resolve) => {
    pendingFetchStarted = resolve;
  });
  let pending: ServerResponse | undefined;
  let initialServed = false;
  const releaseReplacement = () => {
    pending?.end('<!doctype html><title>Replacement document</title>');
    pending = undefined;
  };
  // Hold real HTTP responses so the browser, rather than route interception,
  // owns cancellation of the old document's requests.
  const server = createServer((request, response) => {
    response.setHeader('Content-Type', 'text/html');
    if (request.url === `${base}/initial` && !initialServed) {
      initialServed = true;
      response.end('<!doctype html><title>Initial document</title>');
    } else if (request.url === `${base}/initial`) {
      pending = response;
      replacementStarted();
    } else if (request.url === `${base}/pending-fetch`) {
      pendingFetchStarted();
    } else if (request.url === `${base}/during-replacement`) {
      response.setHeader('Content-Type', 'application/json');
      response.end('{}');
    } else {
      response.writeHead(404);
      response.end();
    }
  });
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      server.off('error', reject);
      resolve();
    });
  });
  const origin = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  try {
    await page.goto(`${origin}${base}/initial`);
    const acknowledgment = `navigation-fetch:${monitor.marker}`;
    await page.evaluate(
      ({ pendingEndpoint, replacementEndpoint, acknowledgment }) => {
        const pendingFetch = new AbortController();
        void fetch(pendingEndpoint, { signal: pendingFetch.signal }).catch(
          () => {},
        );
        addEventListener('beforeunload', () => {
          pendingFetch.abort();
          void fetch(replacementEndpoint).catch(() => {});
          console.debug(acknowledgment);
        });
      },
      {
        pendingEndpoint: `${base}/pending-fetch`,
        replacementEndpoint: `${base}/during-replacement`,
        acknowledgment,
      },
    );
    await fetchStarted;
    const cancelled = page.waitForEvent('requestfailed', {
      predicate: (request) => request.url().endsWith(`${base}/pending-fetch`),
    });
    const received = page.waitForEvent('console', {
      predicate: (message) =>
        message.type() === 'debug' && message.text() === acknowledgment,
    });
    await Promise.all([
      // Reload discards the old document instead of retaining it in BFCache.
      page.reload(),
      cancelled,
      (async () => {
        try {
          // The native callback starts fetch during replacement; no evaluation
          // reply from the old document is needed to release the new document.
          await Promise.all([started, received]);
        } finally {
          releaseReplacement();
        }
      })(),
    ]);
    await expect(page).toHaveTitle('Replacement document');
    expect(monitor.errors).toEqual([]);
    expect(
      [...monitor.documents.values()].filter((url) =>
        url.endsWith(`${base}/initial`),
      ),
    ).toHaveLength(2);
    if (monitor.pageErrors.length)
      await testInfo.attach('navigation-page-errors', {
        body: JSON.stringify(monitor.pageErrors, null, 2),
        contentType: 'application/json',
      });
  } finally {
    releaseReplacement();
    server.closeAllConnections();
    await new Promise<void>((resolve, reject) =>
      server.close((error) => (error ? reject(error) : resolve())),
    );
    monitor.dispose();
  }
});
