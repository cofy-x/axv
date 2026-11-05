import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { cp, readFile, writeFile } from 'node:fs/promises';
import { basename, join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { chromium, expect } from '@playwright/test';
import { exitAfterCleanup, launchServer, scratch, stop } from './lib.mjs';
import { createShellEnvironment } from './shell-fixture.mjs';
import { initializeTestTemp } from './test-temp.mjs';

initializeTestTemp({ isolatedCache: true });
const { values } = parseArgs({
  options: {
    before: { type: 'string' },
    after: { type: 'string' },
    'before-report': { type: 'string' },
    'after-report': { type: 'string' },
  },
});
for (const name of ['before', 'after', 'before-report', 'after-report'])
  assert.ok(values[name], `Provide --${name}. See docs/development.md.`);

const dir = await scratch('reopen-');
const evidence = await scratch('reopen-evidence-');
let server;
let browser;
let stopping = false;
let code = 0;
const report = { checks: [], physicalInstallation: 'pending' };

async function shutdown(exitCode) {
  if (stopping) return;
  stopping = true;
  try {
    await browser?.close();
    if (server) await stop(server.child);
  } catch (error) {
    console.error(error);
    exitCode = 1;
  }
  await exitAfterCleanup(exitCode, dir);
}

process.on('SIGINT', () => void shutdown(130));
process.on('SIGTERM', () => void shutdown(143));

async function describeBuild(executable, bundleReport, label) {
  const source = resolve(executable);
  const copy = join(dir, `${label}-${basename(source)}`);
  await cp(source, copy);
  const chunks = JSON.parse(
    await readFile(resolve(bundleReport), 'utf8'),
  ).chunks;
  const terminal = chunks.filter((chunk) =>
    chunk.modules.some(
      (module) =>
        module.id === 'apps/dashboard/src/features/terminal/TerminalApp.tsx',
    ),
  );
  assert.equal(
    terminal.length,
    1,
    'The report must identify the actual terminal module.',
  );
  return {
    source,
    sourceReport: resolve(bundleReport),
    executable: copy,
    sha256: createHash('sha256')
      .update(await readFile(copy))
      .digest('hex'),
    terminal: `/${terminal[0].fileName}`,
  };
}

async function api(base, databaseId, path, method = 'GET', body) {
  const response = await fetch(`${base}/api${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      'X-Requested-With': 'XMLHttpRequest',
      ...(databaseId ? { 'X-AXV-Database-ID': databaseId } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(10_000),
  });
  assert.ok(
    response.ok,
    `${method} ${path}: ${response.status} ${await (response.ok ? Promise.resolve('') : response.text())}`,
  );
  return response.json();
}

async function launchTerminal(page) {
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await page
    .getByRole('menuitem', { name: 'Applications', exact: true })
    .click();
  await page
    .getByRole('menu', { name: 'Applications', exact: true })
    .getByRole('menuitem', { name: 'Terminal', exact: true })
    .click();
  return page.locator('[data-app-id="terminal"]').last();
}

async function drafts(page) {
  return page.evaluate(
    () =>
      new Promise((resolveRecords, reject) => {
        const open = globalThis.indexedDB.open('nextstep-drafts', 1);
        open.onerror = () => reject(open.error);
        open.onsuccess = () => {
          const db = open.result;
          const tx = db.transaction('drafts');
          const read = tx.objectStore('drafts').getAll();
          tx.oncomplete = () => {
            db.close();
            resolveRecords(
              read.result.map((draft) => {
                const retained = { ...draft };
                delete retained.updatedAt;
                return retained;
              }),
            );
          };
          tx.onabort = () => {
            db.close();
            reject(tx.error);
          };
        };
      }),
  );
}

try {
  const before = await describeBuild(
    values.before,
    values['before-report'],
    'before',
  );
  const after = await describeBuild(
    values.after,
    values['after-report'],
    'after',
  );
  assert.notEqual(
    before.sha256,
    after.sha256,
    'Use two different production executables.',
  );
  assert.notEqual(
    before.terminal,
    after.terminal,
    'The terminal feature must have a different content hash.',
  );
  report.builds = { before, after };
  const env = await createShellEnvironment(dir);
  const options = {
    cwd: dir,
    profileHome: join(dir, 'home'),
    dataDir: join(dir, 'data'),
    env,
  };
  server = await launchServer({ ...options, executable: before.executable });
  const base = server.base;
  const addr = new URL(base).host;
  const original = await api(
    base,
    null,
    '/bootstrap?clientId=reopen-acceptance',
  );
  const originalDocument = await api(
    base,
    original.databaseId,
    '/entries/readme',
    'PATCH',
    {
      content: 'Original database content.',
      revision: 1,
    },
  );
  report.databaseId = original.databaseId;
  browser = await chromium.launch();
  report.browser = browser.version();
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    acceptDownloads: true,
  });
  await context.addInitScript(() =>
    localStorage.setItem('nextstep-browser-id', 'reopen-acceptance'),
  );
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto(base);
  await page
    .locator('[data-app-id="welcome"]')
    .getByRole('button', { name: 'Documents', exact: true })
    .click();
  const files = page.locator('[data-window-id="files:documents"]');
  await files.getByRole('option', { name: 'Read Me.txt', exact: true }).click();
  await files.getByRole('button', { name: 'Open', exact: true }).click();
  const editor = page.locator('[data-window-id="notes:readme"]');
  const input = editor.getByRole('textbox', {
    name: 'Document text',
    exact: true,
  });
  const dirty = 'Unsaved upgrade draft — 保留最新文本\n';
  await input.fill(dirty);
  const node = await input.elementHandle();
  const timeOrigin = await page.evaluate(() => performance.timeOrigin);
  await expect
    .poll(async () =>
      (await drafts(page)).some((draft) => draft.text === dirty),
    )
    .toBe(true);
  const originalDrafts = await drafts(page);
  assert.equal(
    await page.evaluate(
      (asset) =>
        performance.getEntriesByName(globalThis.location.origin + asset).length,
      before.terminal,
    ),
    0,
  );
  await stop(server.child);
  server = await launchServer({
    ...options,
    addr,
    executable: after.executable,
  });
  const restarted = await api(
    base,
    null,
    '/bootstrap?clientId=reopen-acceptance',
  );
  assert.equal(restarted.databaseId, original.databaseId);
  assert.equal((await fetch(base + before.terminal)).status, 404);
  assert.equal((await fetch(base + after.terminal)).status, 200);
  const oldTerminal = await launchTerminal(page);
  await expect(oldTerminal.getByRole('alert')).toBeVisible();
  await expect(
    oldTerminal.getByRole('button', { name: 'Retry', exact: true }),
  ).toBeVisible();
  assert.equal(await page.evaluate(() => performance.timeOrigin), timeOrigin);
  await expect(input).toHaveValue(dirty);
  assert.ok(
    await input.evaluate((element, previous) => element === previous, node),
  );
  assert.deepEqual(await drafts(page), originalDrafts);
  await page.screenshot({ path: join(evidence, 'old-page-module-error.png') });
  const fresh = await context.newPage();
  await fresh.goto(base);
  await expect(
    fresh.getByRole('complementary', { name: 'Application Dock' }),
  ).toBeVisible();
  const freshTerminal = fresh.locator('[data-app-id="terminal"]').first();
  if ((await freshTerminal.count()) === 0) await launchTerminal(fresh);
  await expect(
    fresh.locator('[data-app-id="terminal"]').getByTestId('terminal-status'),
  ).toHaveAttribute('data-state', 'connected');
  assert.ok(
    await fresh.evaluate(
      (asset) =>
        performance.getEntriesByName(globalThis.location.origin + asset)
          .length > 0,
      after.terminal,
    ),
  );
  await fresh.close();
  report.checks.push(
    'same-origin same-database production upgrade; old DOM and draft retained; missing old module local error; new page loads new module',
  );

  // Replace only this stopped test service's desktop store. The seeded IDs and
  // revisions deliberately match while the actual document contents differ.
  await stop(server.child);
  server = await launchServer({
    ...options,
    addr,
    dataDir: join(dir, 'replacement'),
    executable: after.executable,
  });
  const replacement = await api(
    base,
    null,
    '/bootstrap?clientId=reopen-acceptance',
  );
  assert.notEqual(replacement.databaseId, original.databaseId);
  const replacementDocument = await api(
    base,
    replacement.databaseId,
    '/entries/readme',
    'PATCH',
    {
      content: 'Replacement database content.',
      revision: 1,
    },
  );
  assert.equal(
    replacementDocument.entry.revision,
    originalDocument.entry.revision,
  );
  await editor.locator('.ns-window-title').click();
  await editor.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(
    page.getByRole('link', { name: 'Open current AXV', exact: true }),
  ).toBeVisible();
  await expect(input).toHaveValue(dirty);
  await expect(
    editor.getByRole('button', { name: 'Save', exact: true }),
  ).toBeDisabled();
  assert.equal(
    (await api(base, replacement.databaseId, '/entries/readme')).content,
    'Replacement database content.',
  );
  assert.deepEqual(await drafts(page), originalDrafts);
  await editor
    .getByRole('button', { name: 'More document actions', exact: true })
    .click();
  const downloaded = page.waitForEvent('download');
  await page.getByRole('menuitem', { name: /^Download Draft(?:\s|$)/ }).click();
  const download = await downloaded;
  assert.equal(download.suggestedFilename(), 'Read Me.txt.draft.txt');
  assert.equal(await readFile(await download.path(), 'utf8'), dirty);
  await page.screenshot({ path: join(evidence, 'database-protection.png') });
  await stop(server.child);
  server = await launchServer({
    ...options,
    addr,
    executable: after.executable,
  });
  assert.equal(
    (await api(base, original.databaseId, '/entries/readme')).content,
    'Original database content.',
  );
  let writes = 0;
  page.on('request', (request) => {
    if (!['GET', 'HEAD'].includes(request.method())) writes++;
  });
  await expect(
    editor.getByRole('button', { name: 'Save', exact: true }),
  ).toBeDisabled();
  await input.press('Control+s');
  await expect(editor).toContainText('Saving is unavailable in this window.');
  assert.equal(writes, 0);
  assert.equal(await page.evaluate(() => performance.timeOrigin), timeOrigin);
  assert.deepEqual(await drafts(page), originalDrafts);
  assert.deepEqual(errors, []);
  report.checks.push(
    'replacement database with same entry ID/revision rejects old save; both stores and original drafts preserved; local UTF-8 download; sticky protection after original database returns',
  );
} catch (error) {
  console.error(error);
  report.error = String(error.stack || error);
  code = 1;
} finally {
  report.result = code === 0 ? 'passed' : 'failed';
  await writeFile(
    join(evidence, 'report.json'),
    `${JSON.stringify(report, null, 2)}\n`,
  );
  console.log(`Online reopen evidence: ${evidence}`);
  await shutdown(code);
}
