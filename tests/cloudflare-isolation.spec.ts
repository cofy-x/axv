import { randomUUID } from 'node:crypto';
import { expect } from '@playwright/test';
import type { BrowserContext, Page } from '@playwright/test';
import { isolationTest as test } from './cloudflare-browser-fixture';
import type { EdgeRequest } from './cloudflare-browser-fixture';
import type { CloudflarePublication } from '../apps/dashboard/src/features/cloudflare/types';

// Only the HTTPS provider edge is intercepted. Cookies, SameSite, PSL lookup,
// secure-context APIs and origin storage are owned by each actual browser.
// This does not establish TLS certificate validation or live Cloudflare access.
test.beforeEach(async ({ browserName }, info) => {
  await info.attach('browser-proof-scope', {
    body: JSON.stringify({
      browser: browserName,
      browserCookieJar: 'native',
      browserStorage: 'native',
      httpsEdge: 'Playwright route fixture',
      fetchMetadata:
        'representative edge values when interception omits them; not a network-header proof',
      tlsValidation: 'not exercised',
      cloudflareProvider: 'local fake',
      publicationSurface: 'real Go manager for gate/lifecycle cases',
    }),
    contentType: 'application/json',
  });
});

async function staticEdge(context: BrowserContext, origins: string[]) {
  const allowed = new Set(origins);
  const requests: EdgeRequest[] = [];
  await context.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (!allowed.has(url.origin)) {
      await route.abort('blockedbyclient');
      return;
    }
    const headers: Record<string, string> = {
      'content-type': 'text/html; charset=utf-8',
      'cache-control': 'no-store',
    };
    if (url.pathname === '/owner-session')
      headers['set-cookie'] =
        'ordinary-session=owner; Path=/; HttpOnly; Secure; SameSite=Strict\n__Host-control=owner; Path=/; HttpOnly; Secure; SameSite=Strict';
    requests.push({
      url: request.url(),
      method: request.method(),
      headers: await request.allHeaders(),
      status: 200,
    });
    await route.fulfill({
      status: 200,
      headers,
      body: '<!doctype html><title>Browser boundary fixture</title><h1>Browser boundary fixture</h1>',
    });
  });
  return requests;
}

async function storedState(page: Page, value?: string) {
  return page.evaluate(async (value) => {
    if (value !== undefined) localStorage.setItem('publication-state', value);
    const database = await new Promise<IDBDatabase>((resolve, reject) => {
      const opening = indexedDB.open('publication-state', 1);
      opening.onupgradeneeded = () =>
        opening.result.createObjectStore('values');
      opening.onsuccess = () => resolve(opening.result);
      opening.onerror = () => reject(opening.error);
    });
    try {
      const indexed = await new Promise<string | null>((resolve, reject) => {
        const transaction = database.transaction(
          'values',
          value === undefined ? 'readonly' : 'readwrite',
        );
        const store = transaction.objectStore('values');
        if (value !== undefined) store.put(value, 'state');
        const reading = store.get('state');
        transaction.oncomplete = () =>
          resolve((reading.result as string | undefined) || null);
        transaction.onerror = () => reject(transaction.error);
      });
      return { local: localStorage.getItem('publication-state'), indexed };
    } finally {
      database.close();
    }
  }, value);
}

async function writableCookieAncestors(page: Page, suffix: string) {
  return page.evaluate((suffix) => {
    const positive = `axv_fixture_canary_${Date.now()}`;
    document.cookie = `${positive}=1; Secure; Path=/`;
    const cookieAvailable = document.cookie
      .split(';')
      .some((value) => value.trim() === `${positive}=1`);
    document.cookie = `${positive}=; Secure; Path=/; Max-Age=0`;
    const writable: string[] = [];
    for (
      let domain = suffix;
      domain;
      domain = domain.includes('.') ? domain.slice(domain.indexOf('.') + 1) : ''
    ) {
      const name = `axv_fixture_probe_${domain.replaceAll('.', '_')}`;
      document.cookie = `${name}=1; Domain=${domain}; Secure; Path=/; SameSite=Strict`;
      if (
        document.cookie.split(';').some((value) => value.trim() === `${name}=1`)
      )
        writable.push(domain);
      document.cookie = `${name}=; Domain=${domain}; Secure; Path=/; Max-Age=0`;
    }
    return { cookieAvailable, writable };
  }, suffix);
}

test('negative control: sibling origins share parent-domain cookies while __Host cookies resist tossing', async ({
  context,
  page,
  browserName,
}, info) => {
  const owner = 'https://desktop.shared.example.com';
  const attacker = 'https://evil.shared.example.com';
  const requests = await staticEdge(context, [owner, attacker]);
  await page.goto(`${owner}/owner-session`);
  await page.goto(attacker);
  const prefix = await page.evaluate(() => {
    document.cookie =
      'parent-poison=attacker; Domain=shared.example.com; Secure; Path=/; SameSite=Strict';
    document.cookie =
      'ordinary-session=attacker; Domain=shared.example.com; Secure; Path=/; SameSite=Strict';
    document.cookie =
      '__Host-control=attacker; Domain=shared.example.com; Secure; Path=/; SameSite=Strict';
    document.cookie = '__Host-canary=valid; Secure; Path=/';
    document.cookie = `__Host-canary=invalid; Domain=${location.hostname}; Secure; Path=/`;
    return document.cookie;
  });
  expect(prefix).toContain('__Host-canary=valid');
  expect(prefix).not.toContain('__Host-canary=invalid');
  await page.evaluate(async (owner) => {
    await fetch(`${owner}/capture`, { credentials: 'include' }).catch(
      () => undefined,
    );
  }, owner);
  const crossOrigin = requests.find(
    (request) => request.url === `${owner}/capture`,
  )!;
  if (browserName === 'webkit') {
    await info.attach('SameSite-transport-observation', {
      body: 'WebKit interception did not expose a Cookie header for this cross-origin fetch. SameSite network transmission is not established by this fixture.',
      contentType: 'text/plain',
    });
  } else {
    expect(crossOrigin.headers.cookie).toContain('ordinary-session=owner');
    expect(crossOrigin.headers.cookie).toContain('ordinary-session=attacker');
    expect(crossOrigin.headers.cookie).toContain('__Host-control=owner');
    expect(crossOrigin.headers.cookie).not.toContain('__Host-control=attacker');
  }
  await page.goto(`${owner}/capture-parent`);
  expect(await page.evaluate(() => document.cookie)).toContain(
    'parent-poison=attacker',
  );
  const protectedCookies = (await context.cookies(owner)).filter(
    (cookie) => cookie.name === '__Host-control',
  );
  expect(protectedCookies).toHaveLength(1);
  expect(protectedCookies[0]).toMatchObject({
    value: 'owner',
    domain: 'desktop.shared.example.com',
    httpOnly: true,
    secure: true,
  });
});

test('independent registered sites isolate cookies, localStorage and IndexedDB', async ({
  context,
  page,
  browserName,
}) => {
  const owner = 'https://desktop.example.com';
  const first = 'https://first.example.net';
  const second = 'https://second.example.org';
  const requests = await staticEdge(context, [owner, first, second]);
  await page.goto(`${owner}/owner-session`);
  await storedState(page, 'desktop-only');
  await page.goto(first);
  expect(await storedState(page)).toEqual({ local: null, indexed: null });
  await storedState(page, 'first-project');
  await page.evaluate(() => {
    document.cookie =
      'first-session=app; Domain=example.net; Secure; Path=/; SameSite=Strict';
    document.cookie = 'desktop-toss=attack; Domain=example.com; Secure; Path=/';
  });
  await page.evaluate(async (owner) => {
    await fetch(`${owner}/capture`, { credentials: 'include' }).catch(
      () => undefined,
    );
  }, owner);
  const crossSite = requests.find(
    (request) => request.url === `${owner}/capture`,
  )!;
  if (browserName !== 'webkit') {
    expect(crossSite.headers.cookie || '').not.toContain(
      'ordinary-session=owner',
    );
    expect(crossSite.headers.cookie || '').not.toContain(
      '__Host-control=owner',
    );
    expect(crossSite.headers.cookie || '').not.toContain('desktop-toss=attack');
  }
  await page.goto(second);
  expect(await storedState(page)).toEqual({ local: null, indexed: null });
  expect(await page.evaluate(() => document.cookie)).not.toContain(
    'first-session',
  );
  await storedState(page, 'second-project');
  await page.goto(first);
  expect(await storedState(page)).toEqual({
    local: 'first-project',
    indexed: 'first-project',
  });
  await page.goto(owner);
  expect(await page.evaluate(() => document.cookie)).not.toContain(
    'desktop-toss',
  );
  expect(await storedState(page)).toEqual({
    local: 'desktop-only',
    indexed: 'desktop-only',
  });
});

for (const suffix of ['github.io', 'co.uk']) {
  test(`PSL boundary ${suffix} rejects every ancestor cookie scope between two direct child sites`, async ({
    context,
    page,
  }) => {
    const first = `https://first.${suffix}`;
    const second = `https://second.${suffix}`;
    await staticEdge(context, [first, second]);
    await page.goto(first);
    expect(await writableCookieAncestors(page, suffix)).toEqual({
      cookieAvailable: true,
      writable: [],
    });
    await storedState(page, 'first-PSL-site');
    await page.goto(second);
    expect(await storedState(page)).toEqual({ local: null, indexed: null });
    expect(await writableCookieAncestors(page, suffix)).toEqual({
      cookieAvailable: true,
      writable: [],
    });
  });
}

test.describe('real gate and lifecycle through the simulated HTTPS edge', () => {
  test.beforeEach(({ browserName }) => {
    test.skip(
      browserName === 'webkit',
      'WebKit route interception omits native Cookie headers even for same-origin requests. This fixture cannot faithfully transport its gate cookies; use the separate trusted-TLS acceptance. Native cookie, PSL and origin-storage rules remain covered above.',
    );
  });

  test('real publication gate uses native __Host cookies and preserves state while allocating fresh immutable origins', async ({
    publishingFixture,
    context,
    page,
  }) => {
    const first = await publishingFixture.publish('github.io', 'editor');
    const second = await publishingFixture.publish(
      'github.io',
      'console',
      'another-project',
    );
    const publications = [first, second];
    const requests = await publishingFixture.route(context, publications);
    const firstURL = `https://${first.hostname}/deep/link?draft=1#selection`;
    await page.goto(firstURL);
    await expect(page.locator('[data-fixture-application]')).toHaveText(
      'Guest application: original-project',
    );
    await expect(page).toHaveURL(firstURL);
    expect(
      requests.some(
        (request) =>
          request.method === 'POST' &&
          request.url.endsWith('/__axv/isolation/confirm') &&
          request.status === 204,
      ),
    ).toBe(true);
    const firstProof = (
      await context.cookies(`https://${first.hostname}`)
    ).find((cookie) => cookie.name === '__Host-AXV-Isolation');
    expect(firstProof).toMatchObject({
      domain: first.hostname,
      path: '/',
      secure: true,
      httpOnly: true,
      sameSite: 'Strict',
    });
    expect(await page.evaluate(() => document.cookie)).not.toContain(
      '__Host-AXV-Isolation',
    );
    await storedState(page, 'original-project-state');
    await page.evaluate(() => {
      document.cookie = 'application-session=original; Secure; Path=/';
    });
    const upstream = await page.evaluate(
      async () => (await fetch('/echo')).json() as Promise<{ cookie: string }>,
    );
    expect(upstream.cookie).toContain('application-session=original');
    expect(upstream.cookie).not.toContain('__Host-AXV-');
    await page.goto(`https://${second.hostname}`);
    await expect(page.locator('[data-fixture-application]')).toHaveText(
      'Guest application: another-project',
    );
    expect(await storedState(page)).toEqual({ local: null, indexed: null });
    expect(await page.evaluate(() => document.cookie)).not.toContain(
      'application-session',
    );
    await publishingFixture.command('unpublish', {
      domain: first.domain,
      publication: first.id,
    });
    const revoked = await page.goto(`https://${first.hostname}`);
    expect(revoked?.status()).toBe(404);
    expect(await storedState(page)).toEqual({
      local: 'original-project-state',
      indexed: 'original-project-state',
    });
    const replacement = await publishingFixture.publish(
      'github.io',
      'editor',
      'replacement-project',
    );
    publications.push(replacement);
    expect(replacement.generation).toMatch(/^[a-f0-9]{32}$/);
    expect(replacement.hostname).not.toBe(first.hostname);
    await page.goto(`https://${replacement.hostname}`);
    await expect(page.locator('[data-fixture-application]')).toHaveText(
      'Guest application: replacement-project',
    );
    expect(await storedState(page)).toEqual({ local: null, indexed: null });
    expect(await page.evaluate(() => document.cookie)).not.toContain(
      'application-session',
    );
  });

  test('real dedicated-site journals reject reuse after removal and restart without clearing browser state', async ({
    publishingFixture,
    context,
    page,
  }) => {
    const request = {
      domain: 'example.net',
      name: 'reserved',
      requestId: randomUUID(),
    };
    const original = await publishingFixture.command<CloudflarePublication>(
      'publish',
      request,
    );
    const requests = await publishingFixture.route(context, [original]);
    await page.goto(`https://${original.hostname}`);
    await expect(page.locator('[data-fixture-application]')).toBeVisible();
    await storedState(page, 'retained-dedicated-site');
    await publishingFixture.command('unpublish', {
      domain: original.domain,
      publication: original.id,
    });
    const revoked = await page.goto(`https://${original.hostname}`);
    expect(revoked?.status()).toBe(404);
    expect(await storedState(page)).toEqual({
      local: 'retained-dedicated-site',
      indexed: 'retained-dedicated-site',
    });
    await publishingFixture.command('restart', { domain: original.domain });
    expect(await publishingFixture.command('publish', request)).toMatchObject({
      id: original.id,
      hostname: original.hostname,
      generation: original.generation,
      state: 'removed',
    });
    await expect(
      publishingFixture.publish(
        'example.net',
        'replacement',
        'replacement-project',
      ),
    ).rejects.toMatchObject({ code: 'publication_site_retired' });
    const response = await page
      .goto(`https://${original.hostname}`)
      .catch(() => null);
    expect(response).toBeNull();
    expect(requests.at(-1)?.status).toBe(0);
  });

  test('restart stays unavailable until explicit original resume and requires a fresh browser proof', async ({
    publishingFixture,
    context,
    page,
  }) => {
    const original = await publishingFixture.publish(
      'co.uk',
      'resume',
      'resume-original-project',
    );
    const requests = await publishingFixture.route(context, [original]);
    await page.goto(`https://${original.hostname}`, { waitUntil: 'commit' });
    await expect(page.locator('[data-fixture-application]')).toBeVisible();
    await storedState(page, 'resume-original-state');
    const proof = (await context.cookies()).find(
      (cookie) => cookie.name === '__Host-AXV-Isolation',
    )!.value;
    const restored = await publishingFixture.command<CloudflarePublication[]>(
      'restart',
      { domain: original.domain },
    );
    expect(restored.find((record) => record.id === original.id)).toMatchObject({
      state: 'unavailable',
      phase: 'restart-required',
      hostname: original.hostname,
      generation: original.generation,
    });
    // Keep the expected error-page navigation off the tab that resumes.
    // Both tabs share the original cookies and origin storage.
    const offlinePage = await context.newPage();
    try {
      await expect(
        offlinePage.goto(`https://${original.hostname}`),
      ).rejects.toThrow();
      expect(requests.at(-1)).toMatchObject({
        url: `https://${original.hostname}/`,
        status: 0,
      });
    } finally {
      await offlinePage.close();
    }
    const resumed = await publishingFixture.command<CloudflarePublication>(
      'resume',
      { domain: original.domain, publication: original.id },
    );
    expect(resumed).toMatchObject({
      hostname: original.hostname,
      generation: original.generation,
      binding: original.binding,
    });
    const previousConfirmations = requests.filter((request) =>
      request.url.endsWith('/__axv/isolation/confirm'),
    ).length;
    await page.goto(`https://${original.hostname}`, { waitUntil: 'commit' });
    await expect(page.locator('[data-fixture-application]')).toHaveText(
      'Guest application: resume-original-project',
    );
    expect(
      requests.filter((request) =>
        request.url.endsWith('/__axv/isolation/confirm'),
      ),
    ).toHaveLength(previousConfirmations + 1);
    expect(
      (await context.cookies()).find(
        (cookie) => cookie.name === '__Host-AXV-Isolation',
      )!.value,
    ).not.toBe(proof);
    expect(await storedState(page)).toEqual({
      local: 'resume-original-state',
      indexed: 'resume-original-state',
    });
  });

  test('nested private suffix safety follows actual browser ancestor-cookie behavior before any guest content', async ({
    publishingFixture,
    context,
    page,
  }, info) => {
    const probe = 'https://probe.s3.amazonaws.com';
    await staticEdge(context, [probe]);
    await page.goto(probe);
    const observed = await writableCookieAncestors(page, 's3.amazonaws.com');
    expect(observed.cookieAvailable).toBe(true);
    await info.attach('nested-private-suffix-probe', {
      body: JSON.stringify(observed),
      contentType: 'application/json',
    });
    await context.unrouteAll({ behavior: 'wait' });
    const publication = await publishingFixture.publish(
      's3.amazonaws.com',
      'nested',
    );
    const requests = await publishingFixture.route(context, [publication]);
    await page.goto(`https://${publication.hostname}`);
    if (observed.writable.length) {
      await expect(page.locator('#isolation-status')).toContainText('blocked');
      await expect(page.locator('[data-fixture-application]')).toHaveCount(0);
      expect(
        requests.some((request) =>
          request.url.endsWith('/__axv/isolation/confirm'),
        ),
      ).toBe(false);
      expect(
        await publishingFixture.command('stats', {
          domain: publication.domain,
        }),
      ).toEqual({});
    } else {
      await expect(page.locator('[data-fixture-application]')).toBeVisible();
      expect(
        requests.some(
          (request) =>
            request.url.endsWith('/__axv/isolation/confirm') &&
            request.status === 204,
        ),
      ).toBe(true);
    }
  });

  test('service-worker script fetches are rejected by the real publication surface', async ({
    publishingFixture,
    context,
    page,
  }) => {
    const publication = await publishingFixture.publish('co.uk', 'worker');
    const requests = await publishingFixture.route(context, [publication]);
    await page.goto(`https://${publication.hostname}`);
    await expect(page.locator('[data-fixture-application]')).toBeVisible();
    const response = await page.evaluate(async () => {
      const response = await fetch('/sw.js', {
        headers: { 'Service-Worker': 'script' },
      });
      return {
        status: response.status,
        body: (await response.json()) as { code: string },
      };
    });
    expect(response).toEqual({
      status: 403,
      body: {
        code: 'publication_service_worker_unsupported',
        error:
          'Service workers and offline applications are not supported on published application origins.',
      },
    });
    expect(
      requests.find((request) => request.url.endsWith('/sw.js'))?.headers[
        'service-worker'
      ],
    ).toBe('script');
  });

  test('two simultaneous first tabs each confirm once using real bootstrap cookies', async ({
    publishingFixture,
    context,
    page,
  }) => {
    const publication = await publishingFixture.publish('co.uk', 'concurrent');
    const requests = await publishingFixture.route(context, [publication], {
      bootstrapBarrier: 2,
    });
    const other = await context.newPage();
    await Promise.all([
      page.goto(`https://${publication.hostname}/first`),
      other.goto(`https://${publication.hostname}/second`),
    ]);
    await expect(page.locator('[data-fixture-application]')).toBeVisible();
    await expect(other.locator('[data-fixture-application]')).toBeVisible();
    const confirmations = requests.filter((request) =>
      request.url.endsWith('/__axv/isolation/confirm'),
    );
    expect(confirmations).toHaveLength(2);
    expect(confirmations.every((request) => request.status === 204)).toBe(true);
    expect(confirmations[0].body).not.toBe(confirmations[1].body);
    const replay = await page.evaluate(async (body) => {
      const result = await fetch('/__axv/isolation/confirm', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body,
      });
      return {
        status: result.status,
        code: ((await result.json()) as { code: string }).code,
      };
    }, confirmations[0].body);
    expect(replay).toEqual({
      status: 403,
      code: 'publication_isolation_expired',
    });
  });
});

test('a browser with JavaScript disabled sees the actual gate and never loads guest content', async ({
  publishingFixture,
  browser,
}) => {
  const publication = await publishingFixture.publish('co.uk', 'no-scripts');
  const before = await publishingFixture.command('stats', {
    domain: publication.domain,
  });
  const context = await browser.newContext({ javaScriptEnabled: false });
  try {
    const requests = await publishingFixture.route(context, [publication]);
    const page = await context.newPage();
    await page.goto(`https://${publication.hostname}/scripts-disabled`);
    await expect(page).toHaveTitle('AXV browser isolation');
    await expect(page.locator('noscript')).toBeVisible();
    // Playwright's text matcher intentionally excludes noscript contents. Read
    // its native DOM text after checking the no-script instruction is visible.
    expect(await page.locator('noscript').textContent()).toContain(
      'JavaScript and first-party cookies are required',
    );
    await expect(page.locator('[data-fixture-application]')).toHaveCount(0);
    expect(
      requests.some((request) =>
        request.url.endsWith('/__axv/isolation/confirm'),
      ),
    ).toBe(false);
    expect(
      await publishingFixture.command('stats', { domain: publication.domain }),
    ).toEqual(before);
  } finally {
    await context.close();
  }
});
