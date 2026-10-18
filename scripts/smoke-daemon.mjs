import { initializeTestTemp } from './test-temp.mjs';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { access, mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { createCodexFixture } from './codex-fixture.mjs';
import { verifyProfileLog } from './profile-log-check.mjs';
import {
  captureCommand,
  dispose,
  launchServer,
  root,
  scratch,
  stop,
} from './lib.mjs';

if (!['darwin', 'linux'].includes(process.platform))
  throw new Error(
    'Native daemon smoke requires macOS launchd or Linux systemd.',
  );

initializeTestTemp({ isolatedCache: true });

const dir = await scratch('axv-daemon-');
const installDirectory = join(dir, 'go bin');
const executable = join(installDirectory, 'axv');
let fixture;
let environment;
let foreground;
let activeSession;
let started = false;
let cleaning;

async function daemon(action) {
  return captureCommand(executable, ['daemon', action], {
    cwd: dir,
    env: environment,
    timeout: 45_000,
  });
}

async function install() {
  await captureCommand(process.execPath, [join(root, 'scripts/install.mjs')], {
    cwd: dir,
    env: {
      ...process.env,
      GOBIN: installDirectory,
      AXV_HOME: join(dir, 'installation-must-not-create-profile'),
    },
    timeout: 45_000,
  });
  await assert.rejects(
    access(join(dir, 'installation-must-not-create-profile')),
    { code: 'ENOENT' },
  );
}

async function reopen(env = environment) {
  return captureCommand(executable, [], { cwd: dir, env, timeout: 45_000 });
}

function cleanup() {
  cleaning ??= (async () => {
    const errors = [];
    try {
      if (foreground) await stop(foreground.child);
    } catch (error) {
      errors.push(error);
    }
    try {
      if (started) await daemon('stop');
    } catch (error) {
      errors.push(error);
    }
    try {
      await fixture?.cleanup();
    } catch (error) {
      errors.push(error);
    }
    try {
      // Preserve ownership records if any owned resource failed to stop.
      await dispose(errors.length ? undefined : dir);
    } catch (error) {
      errors.push(error);
    }
    if (errors.length)
      throw new AggregateError(
        errors,
        `Daemon smoke cleanup failed; retained ${dir}.`,
      );
  })();
  return cleaning;
}

for (const signal of ['SIGINT', 'SIGTERM'])
  process.once(signal, () => {
    void cleanup().then(
      () => process.exit(1),
      (error) => {
        console.error(`Daemon smoke cleanup failed; retained ${dir}:`, error);
        process.exit(1);
      },
    );
  });

async function runtimeState() {
  return JSON.parse(
    await readFile(join(environment.AXV_HOME, 'run', 'desktop.json'), 'utf8'),
  );
}

async function api(base, path, method = 'GET', body) {
  const response = await fetch(base + path, {
    method,
    headers: { Origin: base, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(10_000),
  });
  assert.equal(response.ok, true, `${method} ${path}: HTTP ${response.status}`);
  return response.json();
}

async function tmux(...args) {
  return captureCommand('tmux', ['-L', fixture.tmuxSocket, ...args], {
    cwd: dir,
    env: environment,
    timeout: 5000,
  });
}

async function input(session, text) {
  await tmux('send-keys', '-t', `=${session.tmuxSession}:0.0`, '-l', text);
  await tmux('send-keys', '-t', `=${session.tmuxSession}:0.0`, 'Enter');
}

async function waitForOutput(session, pattern) {
  const deadline = Date.now() + 15_000;
  let lastOutput = '';
  while (Date.now() < deadline) {
    const { stdout } = await tmux(
      'capture-pane',
      '-p',
      '-t',
      `=${session.tmuxSession}:0.0`,
      '-S',
      '-200',
    );
    lastOutput = stdout;
    const match = stdout.match(pattern);
    if (match) return match;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(
    `Daemon Codex fixture output did not become ready. Last pane output: ${JSON.stringify(lastOutput.slice(-4000))}`,
  );
}

async function inspect(session, tag) {
  await input(session, `inspect ${tag}`);
  const match = await waitForOutput(
    session,
    new RegExp(`STATE ${tag} pid=(\\d+) value=([^\\r\\n]+)`),
  );
  return { pid: Number(match[1]), value: match[2].trim() };
}

try {
  await install();
  fixture = await createCodexFixture(dir);
  // Start with no server so the daemon creates tmux itself. A pre-existing
  // sentinel would hide supervisor cleanup of newly created descendants.
  await fixture.cleanup();
  environment = {
    ...process.env,
    ...fixture.env,
    AXV_WORKSPACE_DIR: '',
    AXV_COMPUTE_SOCKET: '',
    ADDR: '',
    PORT: '',
    DATA_DIR: '',
    CODEX_HOME: join(dir, 'codex-home'),
    OPENAI_API_KEY: '',
    OPENAI_BASE_URL: '',
  };
  const browserLog = join(dir, 'browser-attempts');
  const browserCommand = process.platform === 'darwin' ? 'open' : 'xdg-open';
  await writeFile(
    join(dir, 'codex-bin', browserCommand),
    '#!/bin/sh\nprintf "%s\\n" "$1" >> "$AXV_SMOKE_BROWSER_LOG"\nexit 17\n',
    { mode: 0o700 },
  );
  environment.AXV_SMOKE_BROWSER_LOG = browserLog;
  const profile = {
    runtime_version: 1,
    desktop: { addr: '127.0.0.1:0' },
    compute: { mode: 'disabled' },
    workspace_dir: fixture.defaultProjectPath,
    proxy_url: 'http://127.0.0.1:17891',
    future: { retained: true },
  };
  await mkdir(dirname(fixture.configPath), { recursive: true });
  const saveProfile = () =>
    writeFile(fixture.configPath, JSON.stringify(profile), { mode: 0o600 });

  await saveProfile();
  started = true;
  const launchResults = await Promise.allSettled([
    reopen(),
    reopen(),
    reopen(),
  ]);
  const launchErrors = launchResults
    .filter((result) => result.status === 'rejected')
    .map((result) => result.reason);
  if (launchErrors.length)
    throw new AggregateError(launchErrors, 'Concurrent daemon launch failed.');
  const launches = launchResults.map((result) => result.value);
  const first = await runtimeState();
  assert.ok(first.launchId);
  assert.equal((await api(first.url, '/api/runtime')).launchId, first.launchId);
  const logPath = (await api(first.url, '/api/compute/environment')).logPath;
  assert.equal(typeof logPath, 'string');
  for (const result of launches) assert.ok(result.stdout.includes(first.url));
  assert.equal(
    (await readFile(browserLog, 'utf8')).trim().split('\n').length,
    3,
  );
  await access(join(environment.AXV_HOME, 'data', 'desktop.sqlite'));
  await daemon('start');
  assert.equal((await runtimeState()).instanceId, first.instanceId);
  await writeFile(fixture.configPath, '{invalid configuration', {
    mode: 0o600,
  });
  const reused = await reopen({
    ...environment,
    PATH: `${join(dir, 'different-shell')}:${environment.PATH}`,
    HTTP_PROXY: 'http://127.0.0.1:17899',
  });
  assert.ok(reused.stdout.includes(first.url));
  assert.equal((await runtimeState()).instanceId, first.instanceId);
  assert.equal(
    await readFile(fixture.configPath, 'utf8'),
    '{invalid configuration',
  );
  await saveProfile();
  const info = await api(first.url, '/api/host/codex/info');
  assert.equal(info.ready, true);
  assert.equal(info.defaultProjectPath, profile.workspace_dir);
  assert.equal(info.network.proxySource, 'environment');
  assert.equal((await api(first.url, '/api/compute/info')).configured, false);
  const { session } = await api(first.url, '/api/host/codex/sessions', 'POST', {
    requestId: randomUUID(),
  });
  activeSession = session;
  await waitForOutput(session, /codex-test>/);
  await input(session, 'state daemon-survives');
  const original = await inspect(session, 'before');
  assert.equal(original.value, 'daemon-survives');
  await input(session, 'network');
  await waitForOutput(session, new RegExp(`NETWORK ${fixture.networkDigest}`));
  await install();
  await reopen();
  assert.equal((await runtimeState()).instanceId, first.instanceId);
  assert.deepEqual(await inspect(session, 'reinstalled'), original);
  profile.workspace_dir = join(environment.AXV_HOME, 'new-workspace');
  await saveProfile();
  await daemon('restart');
  const second = await runtimeState();
  assert.notEqual(second.instanceId, first.instanceId);
  assert.notEqual(second.pid, first.pid);
  assert.notEqual(second.launchId, first.launchId);
  assert.equal(
    (await api(second.url, '/api/host/codex/info')).defaultProjectPath,
    profile.workspace_dir,
  );
  assert.equal(
    (await api(second.url, `/api/host/codex/sessions/${session.id}`)).session
      .status,
    'running',
  );
  assert.deepEqual(await inspect(session, 'restarted'), original);
  await daemon('stop');
  assert.deepEqual(await inspect(session, 'stopped'), original);
  await daemon('start');
  const third = await runtimeState();
  assert.equal(
    (await api(third.url, `/api/host/codex/sessions/${session.id}`)).session
      .status,
    'running',
  );
  assert.deepEqual(await inspect(session, 'started'), original);
  await api(
    third.url,
    `/api/host/codex/sessions/${session.id}/runs/${session.runId}/end`,
    'POST',
  );
  assert.equal(
    (await api(third.url, `/api/host/codex/sessions/${session.id}`)).session
      .status,
    'ended',
  );
  assert.deepEqual(
    JSON.parse(await readFile(fixture.configPath, 'utf8')),
    profile,
  );
  await daemon('stop');
  started = false;
  foreground = await launchServer({
    executable,
    cwd: dir,
    profileHome: environment.AXV_HOME,
    dataDir: join(environment.AXV_HOME, 'data'),
    env: environment,
  });
  const foregroundState = await runtimeState();
  assert.equal(foregroundState.launchId || '', '');
  await reopen();
  assert.equal((await runtimeState()).instanceId, foregroundState.instanceId);
  await stop(foreground.child);
  foreground = undefined;
  await verifyProfileLog(environment.AXV_HOME, {
    logPath,
    minimumDesktopStarts: 4,
    excluded: ['fixture-password', 'daemon-survives'],
  });
  assert.ok((await daemon('logs')).stdout.includes('service.start'));
  console.log(
    'Native daemon smoke passed: installed entry, concurrent launch/reopen, browser failure, default profile storage, unchanged live settings, reinstall continuity, foreground reuse, settings reload, and persistent Codex across restart and stop/start.',
  );
} catch (error) {
  console.error('Native daemon smoke failed:', error);
  console.error('Native daemon smoke launch context:', {
    executable,
    cwd: dir,
    profileHome: environment?.AXV_HOME,
    TMPDIR: process.env.TMPDIR,
    GOTMPDIR: process.env.GOTMPDIR,
    AXV_TEST_TMPDIR: process.env.AXV_TEST_TMPDIR,
  });
  const diagnostics = environment
    ? await Promise.allSettled([
        daemon('logs').then((result) => result.stdout),
        fixture?.diagnostics(),
        runtimeState().then(async (state) => ({
          runtime: state,
          session: activeSession
            ? await api(
                state.url,
                `/api/host/codex/sessions/${activeSession.id}`,
              )
            : undefined,
        })),
        activeSession
          ? tmux(
              'list-panes',
              '-t',
              `=${activeSession.tmuxSession}:0`,
              '-F',
              '#{pane_pid} dead=#{pane_dead} status=#{pane_dead_status} signal=#{pane_dead_signal} command=#{pane_start_command}',
            ).then((result) => result.stdout)
          : undefined,
      ])
    : [];
  for (const [index, result] of diagnostics.entries())
    console.error(
      `Native daemon diagnostic ${index + 1}:`,
      result.status === 'fulfilled' ? result.value : String(result.reason),
    );
  throw error;
} finally {
  await cleanup();
}
