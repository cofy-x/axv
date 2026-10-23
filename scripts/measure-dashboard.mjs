import { createHash, randomUUID } from 'node:crypto';
import {
  chmod,
  copyFile,
  open,
  readFile,
  rm,
  writeFile,
} from 'node:fs/promises';
import { basename, join, resolve } from 'node:path';
import { chromium, expect, request } from '@playwright/test';
import { createCodexFixture } from './codex-fixture.mjs';
import { createShellEnvironment } from './shell-fixture.mjs';
import { initializeTestTemp } from './test-temp.mjs';
import { prepareTestOutputPath } from './test-output.mjs';
import { binary, launchServer, scratch, stop } from './lib.mjs';

// This diagnostic intentionally has no timing thresholds or automatic retries.
const temporary = initializeTestTemp({ isolatedCache: true });
// Keep the local helper manifest aligned with imports that affect the fixture.
const harnessSources = await Promise.all(
  [
    'measure-dashboard.mjs',
    'codex-fixture.mjs',
    'shell-fixture.mjs',
    'test-temp.mjs',
    'test-output.mjs',
    'lib.mjs',
  ].map(async (name) => ({
    path: `scripts/${name}`,
    sha256: createHash('sha256')
      .update(await readFile(new URL(name, import.meta.url)))
      .digest('hex'),
  })),
);
const harnessSHA256 = harnessSources[0].sha256;
const harnessSourcesSHA256 = createHash('sha256')
  .update(JSON.stringify(harnessSources))
  .digest('hex');
const options = { binary, samples: 3, conditions: 'local' };
for (let index = 2; index < process.argv.length; index += 2) {
  const key = process.argv[index];
  const value = process.argv[index + 1];
  if (
    !value ||
    !['--binary', '--output', '--samples', '--conditions'].includes(key)
  )
    throw new Error(
      'Usage: measure-dashboard.mjs [--binary PATH] [--output DIR] [--samples N] [--conditions local|mobile]',
    );
  options[key.slice(2)] =
    key === '--samples'
      ? Number(value)
      : key === '--conditions'
        ? value
        : resolve(value);
}
if (!Number.isSafeInteger(options.samples) || options.samples < 1)
  throw new Error('--samples must be a positive integer.');
if (!['local', 'mobile'].includes(options.conditions))
  throw new Error('--conditions must be local or mobile.');
const reportDirectory = await prepareTestOutputPath(
  temporary.root,
  options.output || (await scratch('dashboard-measure-')),
  { directory: true },
);
const reportPath = await prepareTestOutputPath(
  temporary.root,
  join(reportDirectory, 'measurements.json'),
);
// Reserve evidence before acquiring resources; another run cannot replace it.
// Keep the handle so a later path replacement cannot redirect the final write.
const reportFile = await open(reportPath, 'wx', 0o600);
const runtimeDirectory = await scratch('dashboard-runtime-');
let fixture;
let server;
let browser;
let requestContext;
let shuttingDown;
let setupPromise = Promise.resolve();
let interrupted = false;
let binarySnapshot;
const measurements = [];
const runtimeErrors = [];

async function shutdown() {
  shuttingDown ??= (async () => {
    await setupPromise.catch(() => {});
    const results = await Promise.allSettled([
      browser?.close(),
      requestContext?.dispose(),
      server ? stop(server.child) : undefined,
    ]);
    const failures = results
      .filter((result) => result.status === 'rejected')
      .map((result) => result.reason);
    try {
      await fixture?.verifyIsolation();
    } catch (error) {
      failures.push(error);
    } finally {
      try {
        await fixture?.cleanup();
      } catch (error) {
        failures.push(error);
      }
    }
    if (failures.length)
      throw new AggregateError(
        failures,
        `Measurement cleanup failed; retained ${runtimeDirectory}`,
      );
    await rm(runtimeDirectory, { recursive: true, force: true });
  })();
  return shuttingDown;
}

for (const [signal, code] of [
  ['SIGINT', 130],
  ['SIGTERM', 143],
])
  process.once(signal, () => {
    interrupted = true;
    process.exitCode = code;
    console.error(`Measurement interrupted (${signal}); cleaning up.`);
    void shutdown().catch((error) => {
      console.error(error);
      process.exitCode = 1;
    });
  });

async function api(method, path, data) {
  const response = await requestContext[method](`${server.base}${path}`, {
    data,
    headers: { Origin: server.base },
  });
  if (!response.ok())
    throw new Error(
      `${method} ${path}: ${response.status()} ${await response.text()}`,
    );
  return response.json();
}

function makeLayout(template, windows) {
  const activeWindowId = windows.at(-1)?.id || null;
  return {
    ...template,
    windows,
    activeWindowId,
    workspaces: template.workspaces.map((workspace) => ({
      ...workspace,
      order:
        workspace.id === template.currentWorkspaceId
          ? windows.map((window) => window.id)
          : [],
      activeWindowId:
        workspace.id === template.currentWorkspaceId ? activeWindowId : null,
    })),
  };
}

async function createPage(viewport, layout) {
  const context = await browser.newContext({
    viewport,
    hasTouch: viewport.width < 500,
  });
  const clientId = randomUUID();
  await context.addInitScript((id) => {
    globalThis.localStorage.setItem('nextstep-browser-id', id);
    globalThis.__axvLongTasks = [];
    new PerformanceObserver((list) => {
      globalThis.__axvLongTasks.push(
        ...list.getEntries().map((entry) => ({
          startTime: entry.startTime,
          duration: entry.duration,
        })),
      );
    }).observe({ type: 'longtask', buffered: true });
  }, clientId);
  if (layout) await api('put', '/api/layout', { clientId, layout });
  const page = await context.newPage();
  page.on('pageerror', (error) => runtimeErrors.push(error.message));
  const cdp = await context.newCDPSession(page);
  await cdp.send('Performance.enable');
  if (options.conditions === 'mobile') {
    await cdp.send('Emulation.setCPUThrottlingRate', { rate: 4 });
    await cdp.send('Network.enable');
    await cdp.send('Network.emulateNetworkConditions', {
      offline: false,
      latency: 150,
      downloadThroughput: 200_000,
      uploadThroughput: 93_750,
    });
  }
  return { context, page, cdp, clientId };
}

async function sample(target, metadata, action, ready) {
  const responses = [];
  const failedRequests = [];
  const collect = (response) => {
    if (new URL(response.url()).pathname.endsWith('.js'))
      responses.push(response);
  };
  const failed = (request) =>
    failedRequests.push({
      path: new URL(request.url()).pathname,
      error: request.failure()?.errorText,
    });
  const previous = Object.fromEntries(
    (await target.cdp.send('Performance.getMetrics')).metrics.map((item) => [
      item.name,
      item.value,
    ]),
  );
  target.page.on('response', collect);
  target.page.on('requestfailed', failed);
  const stageStart =
    metadata.stage === 'first-open'
      ? await target.page.evaluate(() => globalThis.performance.now())
      : 0;
  const started = performance.now();
  await action();
  await expect(
    target.page.getByRole('button', { name: 'Workspace menu', exact: true }),
  ).toBeVisible();
  const shellVisibleMs = performance.now() - started;
  await ready();
  const featureReadyMs = performance.now() - started;
  await target.page.waitForLoadState('networkidle');
  target.page.off('response', collect);
  target.page.off('requestfailed', failed);
  const current = Object.fromEntries(
    (await target.cdp.send('Performance.getMetrics')).metrics.map((item) => [
      item.name,
      item.value,
    ]),
  );
  const navigation = metadata.stage !== 'first-open';
  const metrics = Object.fromEntries(
    [
      'ScriptDuration',
      'TaskDuration',
      'LayoutDuration',
      'RecalcStyleDuration',
    ].map((key) => [
      key,
      (current[key] || 0) - (navigation ? 0 : previous[key] || 0),
    ]),
  );
  const assets = await Promise.all(
    responses.map(async (response) => {
      await response.finished();
      const headers = await response.allHeaders();
      const body = await response.body();
      return {
        path: new URL(response.url()).pathname,
        status: response.status(),
        rawBytes: body.byteLength,
        actualResponseContentLength: headers['content-length']
          ? Number(headers['content-length'])
          : null,
        contentEncoding: headers['content-encoding'] || 'identity',
        cacheControl: headers['cache-control'],
      };
    }),
  );
  const browserEvidence = await target.page.evaluate(() => ({
    resources: globalThis.performance
      .getEntriesByType('resource')
      .filter((entry) => new URL(entry.name).pathname.endsWith('.js'))
      .map((entry) => ({
        path: new URL(entry.name).pathname,
        transferSize: entry.transferSize,
        encodedBodySize: entry.encodedBodySize,
        decodedBodySize: entry.decodedBodySize,
        durationMs: entry.duration,
      })),
    longTasks: globalThis.__axvLongTasks,
    windows: [...globalThis.document.querySelectorAll('[data-window-id]')].map(
      (window) => ({
        id: window.getAttribute('data-window-id'),
        appId: window.getAttribute('data-app-id'),
      }),
    ),
  }));
  measurements.push({
    ...metadata,
    shellVisibleMs,
    featureReadyMs,
    metrics,
    assets,
    failedRequests,
    ...browserEvidence,
    stageLongTasks: browserEvidence.longTasks.filter(
      (entry) => entry.startTime >= stageStart,
    ),
  });
  if (failedRequests.length)
    throw new Error(
      `Request failure in ${JSON.stringify(metadata)}: ${JSON.stringify(failedRequests)}`,
    );
  console.log(
    `${metadata.viewport.width}px ${metadata.scenario} ${metadata.stage}: ${assets.reduce((total, asset) => total + asset.rawBytes, 0)} requested JS bytes`,
  );
}

async function launch(page, name) {
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await page
    .getByRole('menuitem', { name: 'Applications', exact: true })
    .click();
  await page.getByRole('menuitem', { name, exact: true }).click();
}

function checkInterrupted() {
  if (interrupted) throw new Error('Measurement interrupted during setup.');
}

async function setup() {
  const executable = join(runtimeDirectory, 'axv');
  await copyFile(options.binary, executable);
  await chmod(executable, 0o700);
  binarySnapshot = {
    sourcePath: options.binary,
    sha256: createHash('sha256')
      .update(await readFile(executable))
      .digest('hex'),
  };
  checkInterrupted();
  fixture = await createCodexFixture(runtimeDirectory);
  checkInterrupted();
  if (!fixture)
    throw new Error('This diagnostic requires macOS or Linux with tmux.');
  await writeFile(
    join(fixture.projectPath, 'example.py'),
    'def hello(name: str) -> str:\n    return f"Hello, {name}"\n',
  );
  checkInterrupted();
  const shellEnvironment = await createShellEnvironment(runtimeDirectory);
  checkInterrupted();
  server = await launchServer({
    executable,
    cwd: runtimeDirectory,
    profileHome: fixture.env.AXV_HOME,
    dataDir: join(runtimeDirectory, 'data'),
    env: {
      ...shellEnvironment,
      AXV_TMUX_SOCKET: `axv-test-${basename(runtimeDirectory)}`,
      ...fixture.env,
    },
  });
  checkInterrupted();
  browser = await chromium.launch();
  checkInterrupted();
  requestContext = await request.newContext();
  checkInterrupted();
}

try {
  setupPromise = setup();
  await setupPromise;
  const preparation = await createPage({ width: 1400, height: 1000 });
  await preparation.page.goto(server.base);
  await expect(
    preparation.page.locator('[data-window-id="files:documents"]'),
  ).toBeVisible();
  let template;
  await expect
    .poll(async () => {
      template = (
        await api('get', `/api/bootstrap?clientId=${preparation.clientId}`)
      ).layout;
      return template?.windows?.length || 0;
    })
    .toBeGreaterThan(0);
  const project = (
    await api('post', '/api/projects', { rootPath: fixture.projectPath })
  ).project;
  const session = (
    await api('post', '/api/host/codex/sessions', {
      requestId: randomUUID(),
      projectPath: fixture.projectPath,
    })
  ).session;
  const baseWindow = template.windows[0];
  const restored = makeLayout(template, [
    {
      ...baseWindow,
      id: `codex:${session.id}:${session.runId}`,
      appId: 'codex',
      title: 'Codex · Sample',
      params: { sessionId: session.id, runId: session.runId },
      viewState: {},
      width: 720,
      height: 480,
      x: 8,
      y: 30,
    },
    {
      ...baseWindow,
      id: `project-files:${project.id}`,
      appId: 'project-files',
      title: 'Project Files · Sample',
      params: { projectId: project.id, file: 'example.py' },
      viewState: {},
      width: 720,
      height: 480,
      x: 180,
      y: 180,
    },
  ]);
  await preparation.context.close();
  const viewports =
    options.conditions === 'mobile'
      ? [{ width: 390, height: 844 }]
      : [
          { width: 1400, height: 1000 },
          { width: 390, height: 844 },
          { width: 320, height: 844 },
        ];
  for (const viewport of viewports) {
    for (let iteration = 1; iteration <= options.samples; iteration++) {
      for (const [scenario, layout] of [
        ['default', null],
        ['empty', makeLayout(template, [])],
        ['restored-codex-project', restored],
      ]) {
        const target = await createPage(viewport, layout);
        const ready = async () => {
          if (scenario === 'default')
            await expect(
              target.page.locator('[data-window-id="files:documents"]'),
            ).toBeVisible();
          if (scenario === 'restored-codex-project') {
            await expect(
              target.page.getByTestId('codex-status'),
            ).toHaveAttribute('data-state', 'connected');
            await expect(
              target.page.locator('.project-preview .cm-editor'),
            ).toHaveCount(1);
          }
        };
        await sample(
          target,
          { viewport, iteration, scenario, stage: 'cold' },
          () => target.page.goto(server.base),
          ready,
        );
        await sample(
          target,
          { viewport, iteration, scenario, stage: 'cached' },
          () => target.page.reload(),
          ready,
        );
        await target.context.close();
      }
      for (const name of ['Codex', 'Terminal', 'Project Files']) {
        const target = await createPage(viewport, makeLayout(template, []));
        await target.page.goto(server.base);
        await expect(
          target.page.getByRole('button', {
            name: 'Workspace menu',
            exact: true,
          }),
        ).toBeVisible();
        await target.page.waitForLoadState('networkidle');
        await sample(
          target,
          { viewport, iteration, scenario: name, stage: 'first-open' },
          () => launch(target.page, name),
          async () => {
            if (name === 'Codex')
              await expect(
                target.page.getByTestId('codex-manager'),
              ).toBeVisible();
            if (name === 'Terminal')
              await expect(
                target.page.getByTestId('terminal-status'),
              ).toHaveAttribute('data-state', 'connected');
            if (name === 'Project Files')
              await expect(
                target.page.getByRole('tablist', { name: 'Project location' }),
              ).toBeVisible();
          },
        );
        await target.context.close();
      }
    }
  }
  expect(runtimeErrors).toEqual([]);
} catch (error) {
  process.exitCode ||= 1;
  if (!interrupted) console.error(error);
} finally {
  const browserVersion = browser ? await browser.version() : null;
  try {
    await shutdown();
  } catch (error) {
    console.error(error);
    process.exitCode = 1;
  }
  try {
    await reportFile.writeFile(
      `${JSON.stringify(
        {
          schemaVersion: 1,
          harnessSHA256,
          harnessSourcesSHA256,
          harnessSources,
          binary: binarySnapshot,
          environment: {
            browser: browserVersion,
            platform: process.platform,
            node: process.version,
            conditions: options.conditions,
            cpuThrottling: options.conditions === 'mobile' ? 4 : false,
            networkThrottling:
              options.conditions === 'mobile'
                ? {
                    latencyMs: 150,
                    downloadBytesPerSecond: 200_000,
                    uploadBytesPerSecond: 93_750,
                  }
                : false,
            origin: 'local HTTP, production embedded UI',
          },
          interpretation:
            'Controlled local Chromium samples, no real VM/model, private HTTPS or physical mobile performance claim. Narrow viewports emulate touch. Cold means a fresh browser context and asset cache; restored layouts contain a live deterministic host Codex fixture and a registered host Python preview. First-open starts from an empty desktop with initial assets already idle; resource entries and long tasks retain their document timeline, while assets and CDP duration deltas cover the measured first-open. Timings include the action and explicit readiness but exclude the subsequent network-idle settling period; no timing threshold is enforced.',
          samplesRequested: options.samples,
          measurements,
          runtimeErrors,
          successful: !process.exitCode,
        },
        null,
        2,
      )}\n`,
    );
  } finally {
    await reportFile.close();
  }
  console.log(`Dashboard measurements: ${reportPath}`);
}
