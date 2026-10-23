import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdir, readFile, realpath, stat, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { chromium, expect } from '@playwright/test';
import {
  dispose,
  launchServer,
  root,
  scratch,
  spawnChild,
  stop,
} from './lib.mjs';

const args = process.argv.slice(2);
if (args.length > 1 || args.some((argument) => argument.startsWith('-'))) {
  console.log('Usage: pnpm verify:codex-live [absolute-project-directory]');
  console.log(
    'Uses your installed Codex CLI, login, and model configuration for two real requests.',
  );
  process.exit(args.length === 1 && args[0] === '--help' ? 0 : 1);
}
assert.ok(
  process.platform === 'darwin' || process.platform === 'linux',
  'Live Codex verification requires macOS or Linux with tmux.',
);
const projectPath = await realpath(resolve(args[0] || root));
assert.ok(
  (await stat(projectPath)).isDirectory(),
  'Choose a project directory.',
);

const dir = await scratch();
const socketName = `axv-test-live-${randomUUID().replaceAll('-', '')}`;
const settings = {
  cwd: dir,
  profileHome: join(dir, 'axv-home'),
  dataDir: join(dir, 'data'),
  env: {
    AXV_WORKSPACE_DIR: projectPath,
    AXV_TMUX_SOCKET: socketName,
  },
};
let server;
let browser;
let session;
let cleaning;

async function capture(executable, arguments_) {
  const child = spawnChild(executable, arguments_, {
    cwd: dir,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => (stdout += chunk));
  // Do not print CLI output, environment values, or credential-related details.
  child.stderr.on('data', (chunk) => (stderr += chunk));
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    child.kill('SIGKILL');
  }, 5000);
  try {
    const code = await new Promise((resolve, reject) => {
      child.once('error', reject);
      child.once('close', resolve);
    });
    assert.ok(!timedOut, `${executable} timed out.`);
    return { code, stdout: stdout.trim(), stderr: stderr.trim() };
  } finally {
    clearTimeout(timer);
  }
}

async function tmux(arguments_) {
  assert.ok(session?.tmuxSocket && session?.tmuxSession);
  const result = await capture('tmux', [
    '-S',
    session.tmuxSocket,
    ...arguments_,
  ]);
  assert.equal(result.code, 0, 'The owned tmux command failed.');
  return result.stdout;
}

function runtimeMissing(result) {
  return (
    result.code === 1 &&
    /can't find session|no server running|No such file or directory/.test(
      result.stderr,
    )
  );
}

async function verifyRuntimeEnded() {
  const result = await capture('tmux', [
    '-L',
    socketName,
    'has-session',
    '-t',
    `=${session.tmuxSession}`,
  ]);
  assert.ok(
    runtimeMissing(result),
    'The owned session must be explicitly absent after End Session.',
  );
}

async function json(path, method = 'GET', body) {
  const response = await fetch(`${server.base}/api${path}`, {
    method,
    headers: { Origin: server.base, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(30_000),
  });
  const result = await response.json();
  assert.ok(
    response.ok,
    `${method} ${path}: ${result.code || response.status}`,
  );
  return result;
}

function cleanup() {
  cleaning ??= (async () => {
    const errors = [];
    try {
      await browser?.close();
    } catch {
      errors.push('Could not close the verification browser.');
    }
    try {
      // Stop in-flight creation before inspecting our private socket. A POST
      // may have started its process even if the response never reached us.
      if (server) await stop(server.child);
    } catch {
      errors.push('Could not stop the verification server.');
    }
    let runtimesCleaned = false;
    try {
      const listed = await capture('tmux', [
        '-L',
        socketName,
        'list-sessions',
        '-F',
        '#{session_name}',
      ]);
      if (!runtimeMissing(listed)) {
        assert.equal(
          listed.code,
          0,
          'Could not inspect the private verification socket.',
        );
        const names = listed.stdout.split('\n').filter(Boolean);
        // This random socket is created only by this invocation. Still restrict
        // cleanup to AXV targets and terminate each session by its exact name.
        for (const name of names.filter((name) =>
          name.startsWith('axv-codex-'),
        )) {
          const removed = await capture('tmux', [
            '-L',
            socketName,
            'kill-session',
            '-t',
            `=${name}`,
          ]);
          assert.ok(
            removed.code === 0 || runtimeMissing(removed),
            'Could not stop the owned verification runtime.',
          );
        }
      }
      runtimesCleaned = true;
    } catch {
      errors.push(
        `Could not verify runtime cleanup; retained test data at ${dir}.`,
      );
    } finally {
      await dispose(runtimesCleaned ? dir : undefined);
    }
    assert.equal(errors.length, 0, errors.join(' '));
  })();
  return cleaning;
}

for (const signal of ['SIGINT', 'SIGTERM'])
  process.once(signal, () => {
    void cleanup().finally(() => process.exit(1));
  });

async function openSession(page) {
  await page.goto(server.base);
  // Launcher availability is discovered asynchronously. Menus capture their
  // items when opened, so wait for discovery before opening Applications.
  await expect(
    page.getByRole('group', { name: 'Desktop icons' }).getByRole('button', {
      name: 'Codex',
      exact: true,
      includeHidden: true,
    }),
  ).toHaveCount(1);
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await page
    .getByRole('menuitem', { name: 'Applications', exact: true })
    .click();
  await page.getByRole('menuitem', { name: 'Codex', exact: true }).click();
  const sessions = page
    .locator('.ns-window.is-active[data-app-id="codex"]')
    .getByRole('button', { name: 'Sessions', exact: true });
  if (await sessions.count()) await sessions.click();
  await page
    .getByTestId('codex-manager')
    .locator(`[data-session-id="${session.id}"]`)
    .getByRole('button', { name: 'Open', exact: true })
    .click();
  const window = page.locator(
    `.ns-window:has([data-testid="codex-session"][data-session-id="${session.id}"][data-run-id="${session.runId}"])`,
  );
  await expect(window.getByTestId('codex-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  return window;
}

async function requestModel(window, prompt, expected) {
  const output = window.locator('.xterm-accessibility-tree');
  await expect
    .poll(async () => /Codex/i.test(await output.innerText()), {
      message: 'The installed Codex terminal did not become ready.',
      timeout: 20_000,
    })
    .toBe(true);
  const input = window.getByLabel('Codex terminal input', { exact: true });
  await input.focus();
  // Type rather than paste so the CLI's large-paste guard cannot hold Enter.
  await input.pressSequentially(prompt, { delay: 30 });
  await input.press('Enter');
  await expect
    .poll(async () => (await output.innerText()).includes(expected), {
      message: 'The model did not return the requested verification response.',
      timeout: 120_000,
    })
    .toBe(true);
  await expect
    .poll(
      async () => {
        ({ session } = await json(
          `/host/codex/sessions/${session.id}/runs/${session.runId}`,
        ));
        return session.activity?.state;
      },
      {
        timeout: 20_000,
        message: 'The real model turn did not report completed activity.',
      },
    )
    .toBe('completed');
  assert.equal(session.activity.needsAttention, false);
  await expect(window.getByTestId('codex-activity')).toHaveAttribute(
    'data-activity-state',
    'completed',
  );
  await expect(window.getByTestId('codex-activity')).toHaveAttribute(
    'data-needs-attention',
    'false',
  );
  await expect(
    window.getByRole('button', { name: 'Dismiss alert', exact: true }),
  ).toHaveCount(0);
}

async function identifiedRun() {
  await expect
    .poll(
      async () => {
        ({ session } = await json(
          `/host/codex/sessions/${session.id}/runs/${session.runId}`,
        ));
        return session.status === 'running' && !!session.conversationId;
      },
      {
        timeout: 30_000,
        message: 'Codex did not identify its saved conversation.',
      },
    )
    .toBe(true);
}

try {
  console.log(
    'Live Codex acceptance: using the installed CLI and existing login.',
  );
  server = await launchServer(settings);
  const info = await json('/host/codex/info');
  assert.ok(info.ready, info.reason || 'Codex or tmux is not ready.');
  ({ session } = await json('/host/codex/sessions', 'POST', {
    requestId: randomUUID(),
  }));
  assert.equal(session.projectPath, projectPath);
  const originalRunId = session.runId;
  assert.ok(session.tmuxSocket.endsWith(`/${socketName}`));
  const runtime = await tmux([
    'display-message',
    '-p',
    '-t',
    `=${session.tmuxSession}:0.0`,
    '#{pane_pid}:#{pane_id}',
  ]);
  browser = await chromium.launch();
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  const page = await context.newPage();
  let window = await openSession(page);
  await identifiedRun();
  const conversationId = session.conversationId;
  const token = randomUUID().replaceAll('-', '').slice(0, 8);
  await requestModel(
    window,
    `Do not use tools, run commands, or access files. Remember verification token ${token}. Reply only with AXV_LIVE_A_ followed immediately by that token.`,
    `AXV_LIVE_A_${token}`,
  );
  const completedActivity = { ...session.activity };
  console.log(
    'Verified: a real model response arrived through the AXV terminal.',
  );

  for (const archived of [true, false]) {
    ({ session } = await json(`/host/codex/sessions/${session.id}`, 'PATCH', {
      revision: session.metadataRevision,
      title: 'Live Codex acceptance',
      archived,
    }));
    assert.equal(session.status, 'running');
    assert.equal(!!session.archivedAt, archived);
  }
  await window.getByRole('button', { name: /^Close / }).click();
  await expect
    .poll(
      async () =>
        (await json(`/host/codex/sessions/${session.id}`)).session.connections,
    )
    .toBe(0);
  assert.equal(
    (await json(`/host/codex/sessions/${session.id}`)).session.status,
    'running',
  );
  await page.goto('about:blank');
  const addr = new URL(server.base).host;
  await stop(server.child);
  server = await launchServer({ ...settings, addr });
  ({ session } = await json(`/host/codex/sessions/${session.id}`));
  assert.equal(session.status, 'running');
  assert.equal(session.title, 'Live Codex acceptance');
  assert.equal(session.activity.state, 'completed');
  assert.equal(session.activity.eventId, completedActivity.eventId);
  assert.equal(session.activity.turnId, completedActivity.turnId);
  assert.equal(session.activity.sequence, completedActivity.sequence);
  assert.equal(session.activity.needsAttention, false);
  assert.equal(
    await tmux([
      'display-message',
      '-p',
      '-t',
      `=${session.tmuxSession}:0.0`,
      '#{pane_pid}:#{pane_id}',
    ]),
    runtime,
  );
  await json(
    `/host/codex/sessions/${session.id}/runs/${originalRunId}/end`,
    'POST',
  );
  await verifyRuntimeEnded();
  await page.goto('about:blank');
  await stop(server.child);
  server = await launchServer({ ...settings, addr });
  const ended = (await json(`/host/codex/sessions/${session.id}`)).session;
  assert.equal(ended.runId, originalRunId);
  assert.equal(ended.status, 'ended');
  ({ session } = await json(
    `/host/codex/sessions/${session.id}/resume`,
    'POST',
    {
      requestId: randomUUID(),
      expectedRunId: originalRunId,
    },
  ));
  assert.notEqual(session.runId, originalRunId);
  assert.equal(session.conversationId, conversationId);
  const restoredRuntime = await tmux([
    'display-message',
    '-p',
    '-t',
    `=${session.tmuxSession}:0.0`,
    '#{pane_pid}:#{pane_id}',
  ]);
  assert.notEqual(restoredRuntime, runtime);
  window = await openSession(page);
  await identifiedRun();
  assert.notEqual(session.activity?.eventId, completedActivity.eventId);
  assert.notEqual(session.activity?.turnId, completedActivity.turnId);
  await requestModel(
    window,
    'Do not use tools or access files. Using the verification token from my first message, reply only with AXV_LIVE_B_ followed immediately by that token.',
    `AXV_LIVE_B_${token}`,
  );
  console.log(
    'Verified: a new Codex process recovered the same saved conversation and remembered its earlier token.',
  );
  await json(
    `/host/codex/sessions/${session.id}/runs/${originalRunId}/end`,
    'POST',
  );
  assert.equal(
    (await json(`/host/codex/sessions/${session.id}/runs/${session.runId}`))
      .session.status,
    'running',
  );
  ({ session } = await json(
    `/host/codex/sessions/${session.id}/runs/${session.runId}/end`,
    'POST',
  ));
  assert.equal(session.status, 'ended');
  await verifyRuntimeEnded();
  const report = {
    verifiedAt: new Date().toISOString(),
    codexVersion: info.codexVersion,
    proxySource: info.network.proxySource,
    realModelRequests: 2,
    sameRuntimeAfterRestart: true,
    newRuntimeAfterHistoryRecovery: true,
    sameConversationAfterHistoryRecovery: true,
    checks: [
      'real model response',
      'rename and archive preserve runtime',
      'close detaches',
      'service restart preserves runtime',
      'completed model turns require no acknowledgment',
      'service restart preserves the exact completed activity event',
      'explicit history recovery creates a new runtime',
      'a continued run does not inherit the previous run activity',
      'saved conversation context survives process exit',
      'ending an older runtime cannot stop the new runtime',
      'End Session removes the owned runtime',
    ],
  };
  const reportDir = join(root, '.cache', 'codex-live');
  await mkdir(reportDir, { recursive: true });
  await writeFile(
    join(reportDir, 'last-run.json'),
    `${JSON.stringify(report, null, 2)}\n`,
    { mode: 0o600 },
  );
  console.log(JSON.stringify(report, null, 2));
} catch (error) {
  console.error(`Live Codex acceptance failed: ${error.message}`);
  if (server && session) {
    try {
      const current = (
        await json(`/host/codex/sessions/${session.id}/runs/${session.runId}`)
      ).session;
      const state = JSON.parse(
        await readFile(
          join(settings.dataDir, 'codex-runs', session.runId, 'state.json'),
          'utf8',
        ),
      );
      console.error(
        JSON.stringify({
          status: current.status,
          reason: current.reason,
          identityKnown: !!current.conversationId,
          runnerStatus: state.status,
          runnerErrorCode: state.errorCode,
        }),
      );
    } catch {
      console.error('Runtime startup diagnostics were unavailable.');
    }
  }
  console.error(
    'Check CLI sign-in, project trust, model access, and proxy settings in your host terminal.',
  );
  process.exitCode = 1;
} finally {
  try {
    await cleanup();
  } catch (error) {
    console.error(`Live Codex cleanup failed: ${error.message}`);
    process.exitCode = 1;
  }
}
