import { expect, test as base } from '@playwright/test';
import type { Locator, Page, Route, TestInfo } from '@playwright/test';
import { monitorRuntimeErrors } from './runtime-errors';

const test = base.extend<{ runtimeGuard: void }>({
  runtimeGuard: [
    async ({ page, javaScriptEnabled }, use, info) => {
      const runtime = await monitorRuntimeErrors(page);
      try {
        await use();
      } finally {
        try {
          if (javaScriptEnabled !== false)
            expect(
              runtime.documents.size,
              'The native runtime error monitor must be installed.',
            ).toBeGreaterThan(0);
          if (runtime.pageErrors.length)
            await info.attach('browser-page-errors', {
              body: JSON.stringify(runtime.pageErrors, null, 2),
              contentType: 'application/json',
            });
          expect(
            runtime.errors,
            'Startup failures must be handled without uncaught JavaScript.',
          ).toEqual([]);
        } finally {
          runtime.dispose();
        }
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

async function choose(control: Locator, touch: boolean) {
  if (touch) await control.tap();
  else await control.click();
}

async function screenshotPixels(
  page: Page,
  screenshot: Buffer,
  panelPoint: { x: number; y: number },
) {
  // Decode the unchanged screenshot outside the application document. Firefox
  // does not resolve dynamically created Image.decode() with JavaScript off.
  const decoder = await page.context().browser()!.newPage({
    javaScriptEnabled: true,
  });
  try {
    await decoder.setContent(
      `<img src="data:image/png;base64,${screenshot.toString('base64')}">`,
      { waitUntil: 'load', timeout: 10_000 },
    );
    // WebKit can finish loading before the bitmap is ready for drawImage.
    await decoder.waitForFunction(
      async () => {
        await document.querySelector('img')!.decode();
        return true;
      },
      undefined,
      { timeout: 10_000 },
    );
    return await decoder.locator('img').evaluate((element, point) => {
      const image = element as HTMLImageElement;
      const canvas = document.createElement('canvas');
      canvas.width = image.naturalWidth;
      canvas.height = image.naturalHeight;
      const context = canvas.getContext('2d')!;
      context.drawImage(image, 0, 0);
      const sample = (x: number, y: number) =>
        Array.from(
          context.getImageData(Math.floor(x), Math.floor(y), 1, 1).data,
        );
      return {
        background: sample(1, 1),
        oppositeCorner: sample(canvas.width - 2, canvas.height - 2),
        panel: sample(point.x, point.y),
      };
    }, panelPoint);
  } finally {
    await decoder.close();
  }
}

async function startupFrame(page: Page, info: TestInfo, name: string) {
  const panel = page.locator('.startup-panel');
  await expect(panel).toBeVisible();
  await expect(panel.locator('.startup-title')).toHaveText('AXV');
  await expect(panel.locator('.startup-description')).toHaveText(
    'Personal cloud workstation',
  );
  await expect(panel.locator('.startup-computer > img')).toBeVisible();
  await expect
    .poll(() =>
      panel.locator('.startup-computer > img').evaluate((element) => {
        const image = element as HTMLImageElement;
        return image.complete && image.naturalWidth > 0;
      }),
    )
    .toBe(true);
  const geometry = await panel.evaluate((element) => {
    const rect = (target: Element) => {
      const { x, y, width, height } = target.getBoundingClientRect();
      return { x, y, width, height };
    };
    return {
      panel: rect(element),
      title: rect(element.querySelector('.startup-title')!),
      content: rect(element.querySelector('.startup-content')!),
      computer: rect(element.querySelector('.startup-computer')!),
      image: rect(element.querySelector('.startup-computer > img')!),
      message: rect(element.querySelector('.startup-message')!),
      status: rect(element.querySelector('.startup-status')!),
      description: rect(element.querySelector('.startup-description')!),
    };
  });
  const viewport = page.viewportSize()!;
  expect(geometry.panel.x).toBeGreaterThanOrEqual(0);
  expect(geometry.panel.y).toBeGreaterThanOrEqual(0);
  expect(geometry.panel.x + geometry.panel.width).toBeLessThanOrEqual(
    viewport.width,
  );
  expect(geometry.panel.y + geometry.panel.height).toBeLessThanOrEqual(
    viewport.height,
  );
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    'The first screen must fit without horizontal scrolling.',
  ).toBe(true);

  // Read the actual screenshot, not only computed CSS: a parsed, styled DOM
  // can still sit behind a white frame while a render-blocking resource waits.
  // Firefox's fonts.ready can wait for the deliberately gated module's
  // DOMContentLoaded. Capture the system-font first frame without that wait.
  const fontWait = process.env.PW_TEST_SCREENSHOT_NO_FONTS_READY;
  let screenshot: Buffer;
  try {
    process.env.PW_TEST_SCREENSHOT_NO_FONTS_READY = '1';
    screenshot = await page.screenshot({ scale: 'css', timeout: 10_000 });
  } finally {
    if (fontWait === undefined)
      delete process.env.PW_TEST_SCREENSHOT_NO_FONTS_READY;
    else process.env.PW_TEST_SCREENSHOT_NO_FONTS_READY = fontWait;
  }
  await info.attach(name, { body: screenshot, contentType: 'image/png' });
  const pixels = await screenshotPixels(page, screenshot, {
    x: geometry.panel.x + 5,
    y: geometry.title.y + geometry.title.height + 5,
  });
  for (const corner of [pixels.background, pixels.oppositeCorner]) {
    expect(corner[3]).toBe(255);
    expect(
      corner.slice(0, 3).every((channel) => channel > 245),
      'The rendered first screen must not be a blank white page.',
    ).toBe(false);
  }
  expect(pixels.panel).not.toEqual(pixels.background);
  return geometry;
}

function expectStableFrame(
  before: Awaited<ReturnType<typeof startupFrame>>,
  after: Awaited<ReturnType<typeof startupFrame>>,
) {
  for (const key of Object.keys(before) as (keyof typeof before)[]) {
    for (const dimension of ['x', 'y', 'width', 'height'] as const)
      expect(
        Math.abs(before[key][dimension] - after[key][dimension]),
        `${key} ${dimension} must stay stable when the application takes over.`,
      ).toBeLessThanOrEqual(1);
  }
}

test('cold startup paints before scripts and styles, then keeps the same frame while opening the workspace', async ({
  page,
}, info) => {
  const scripts = gate();
  const styles = gate();
  const bootstrap = gate();
  const requests = { script: 0, stylesheet: 0, bootstrap: 0 };
  const assets = async (route: Route) => {
    const type = route.request().resourceType();
    if (type === 'script') {
      requests.script += 1;
      await scripts.pending;
    } else if (type === 'stylesheet') {
      requests.stylesheet += 1;
      await styles.pending;
    }
    await route.continue();
  };
  const bootstrapURL = (url: URL) => url.pathname === '/api/bootstrap';
  const boot = async (route: Route) => {
    requests.bootstrap += 1;
    await bootstrap.pending;
    await route.continue();
  };
  await page.route('**/*', assets);
  await page.route(bootstrapURL, boot);
  try {
    await page.emulateMedia({ reducedMotion: 'no-preference' });
    await page.goto('/', { waitUntil: 'commit' });
    await expect(page.getByRole('status')).toHaveText('Loading AXV…');
    await expect.poll(() => requests.script).toBeGreaterThan(0);
    expect(requests.bootstrap).toBe(0);
    const initial = await startupFrame(page, info, 'static-before-scripts');
    const runningAnimations = () =>
      page
        .locator('.startup-computer')
        .evaluate(
          (element) =>
            element
              .getAnimations({ subtree: true })
              .filter((animation) => animation.playState === 'running').length,
        );
    await expect.poll(runningAnimations).toBeGreaterThan(0);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await expect.poll(runningAnimations).toBe(0);
    await page.emulateMedia({ reducedMotion: 'no-preference' });

    scripts.release();
    await expect.poll(() => requests.stylesheet).toBeGreaterThan(0);
    await expect(page.getByRole('status')).toHaveText('Loading AXV…');
    expect(requests.bootstrap).toBe(0);
    expectStableFrame(
      initial,
      await startupFrame(page, info, 'static-while-styles-wait'),
    );

    styles.release();
    await expect.poll(() => requests.bootstrap).toBeGreaterThan(0);
    await expect(page.getByRole('status')).toHaveText('Opening workspace…');
    expectStableFrame(
      initial,
      await startupFrame(page, info, 'react-while-bootstrap-waits'),
    );

    bootstrap.release();
    await expect(
      page.getByRole('complementary', { name: 'Application Dock' }),
    ).toBeVisible();
    await expect(page.locator('.startup-screen')).toHaveCount(0);
  } finally {
    scripts.release();
    styles.release();
    bootstrap.release();
    await page.unroute('**/*', assets);
    await page.unroute(bootstrapURL, boot);
  }
});

test('bootstrap errors remain visible and Retry opens the workspace without a reload', async ({
  page,
  hasTouch,
}, info) => {
  const bootstrapURL = (url: URL) => url.pathname === '/api/bootstrap';
  const retry = gate();
  let fail = true;
  let attempts = 0;
  let navigations = 0;
  page.on('framenavigated', (frame) => {
    if (frame === page.mainFrame()) navigations += 1;
  });
  const boot = async (route: Route) => {
    attempts += 1;
    if (fail) {
      await route.fulfill({
        status: 503,
        json: { error: 'The workstation is temporarily unavailable.' },
      });
    } else {
      await retry.pending;
      await route.continue();
    }
  };
  await page.route(bootstrapURL, boot);
  try {
    await page.goto('/');
    const panel = page.locator('.startup-panel');
    await expect(panel.getByRole('status')).toHaveText(
      'Unable to open workspace',
    );
    await expect(panel.getByRole('alert')).toHaveText(
      'The workstation is temporarily unavailable.',
    );
    await startupFrame(page, info, 'bootstrap-error');
    const failedAttempts = attempts;
    const initialNavigations = navigations;
    fail = false;
    await choose(
      panel.getByRole('button', { name: 'Retry', exact: true }),
      hasTouch,
    );
    await expect.poll(() => attempts).toBeGreaterThan(failedAttempts);
    await expect(panel.getByRole('status')).toHaveText('Opening workspace…');
    await expect(panel.getByRole('alert')).toHaveCount(0);
    retry.release();
    await expect(
      page.getByRole('complementary', { name: 'Application Dock' }),
    ).toBeVisible();
    await expect(page.locator('.startup-screen')).toHaveCount(0);
    expect(navigations).toBe(initialNavigations);
  } finally {
    retry.release();
    await page.unroute(bootstrapURL, boot);
  }
});

test('an application bundle failure keeps a useful first screen and Reload can recover', async ({
  page,
  request,
  hasTouch,
}, info) => {
  const response = await request.get('/');
  expect(response.ok()).toBe(true);
  const entry = (await response.text()).match(/<script\b[^>]*\bsrc="([^"]+)"/);
  expect(
    entry,
    'The served HTML must contain its real module entry.',
  ).toBeTruthy();
  const entryPath = new URL(entry![1]!, response.url()).pathname;
  const failed: string[] = [];
  let fail = true;
  const assets = async (route: Route) => {
    const resource = route.request();
    if (
      fail &&
      resource.resourceType() === 'script' &&
      new URL(resource.url()).pathname !== entryPath
    ) {
      failed.push(resource.url());
      await route.fulfill({
        status: 503,
        contentType: 'text/javascript',
        headers: { 'Cache-Control': 'no-store' },
        body: 'Service unavailable',
      });
    } else await route.continue();
  };
  await page.route('**/*', assets);
  try {
    await page.goto('/');
    const panel = page.locator('.startup-panel');
    await expect(panel.getByRole('status')).toHaveText('Unable to load AXV');
    await expect(panel.getByRole('alert')).toBeVisible();
    await expect(panel.getByRole('alert')).not.toBeEmpty();
    expect(failed.length).toBeGreaterThan(0);
    await startupFrame(page, info, 'application-bundle-error');
    fail = false;
    await Promise.all([
      page.waitForEvent('domcontentloaded'),
      choose(
        panel.getByRole('button', { name: 'Reload', exact: true }),
        hasTouch,
      ),
    ]);
    await expect(
      page.getByRole('complementary', { name: 'Application Dock' }),
    ).toBeVisible();
    await expect(page.locator('.startup-screen')).toHaveCount(0);
  } finally {
    await page.unroute('**/*', assets);
  }
});

test.describe('without JavaScript', () => {
  test.use({ javaScriptEnabled: false });

  test('the first screen explains how to open AXV', async ({ page }, info) => {
    const bootstrapRequests: string[] = [];
    page.on('request', (request) => {
      if (new URL(request.url()).pathname === '/api/bootstrap')
        bootstrapRequests.push(request.url());
    });
    await page.goto('/');
    await expect(
      page.getByText('Enable JavaScript to open AXV.', { exact: true }),
    ).toBeVisible();
    await startupFrame(page, info, 'javascript-disabled');
    await expect(
      page.getByRole('button', { name: /^(Retry|Reload)$/ }),
    ).toBeHidden();
    expect(bootstrapRequests).toEqual([]);
  });
});
