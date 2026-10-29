import { initializeTestTemp } from './test-temp.mjs';
import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { access, readdir, readFile } from 'node:fs/promises';
import { basename, join } from 'node:path';
import { createCodexFixture } from './codex-fixture.mjs';
import { createShellEnvironment } from './shell-fixture.mjs';
import { verifyProfileLog } from './profile-log-check.mjs';
import {
  binary,
  captureCommand,
  dispose,
  launchServer,
  project,
  scratch,
  stop,
} from './lib.mjs';

initializeTestTemp({ isolatedCache: true });

const dir = await scratch();
let cleaning;
let codex;
let shellEnvironment;
const terminalSockets = new Set();
const servers = new Set();
const databaseIdentities = new Map();

function cleanup() {
  cleaning ??= (async () => {
    const errors = [];
    for (const session of terminalSockets) {
      try {
        session.socket.close();
      } catch (error) {
        errors.push(error);
      }
    }
    const results = await Promise.allSettled(
      [...servers].map((server) => stop(server.child)),
    );
    errors.push(
      ...results
        .filter((result) => result.status === 'rejected')
        .map((result) => result.reason),
    );
    try {
      await codex?.cleanup();
    } catch (error) {
      errors.push(error);
    }
    try {
      await dispose(errors.length ? undefined : dir);
    } catch (error) {
      errors.push(error);
    }
    if (errors.length)
      throw new AggregateError(
        errors,
        `Production smoke cleanup failed; retained ${dir}.`,
      );
  })();
  return cleaning;
}

for (const signal of ['SIGINT', 'SIGTERM'])
  process.once(signal, () => {
    void cleanup().then(
      () => process.exit(1),
      (error) => {
        console.error(error);
        process.exit(1);
      },
    );
  });

async function start(options = {}) {
  const { env, ...settings } = options;
  const server = await launchServer({
    cwd: dir,
    profileHome: codex?.env.AXV_HOME || join(dir, 'axv-home'),
    dataDir: join(dir, 'data'),
    ...settings,
    env: {
      ...shellEnvironment,
      AXV_WORKSPACE_DIR: join(dir, 'axv-home', 'workspace'),
      AXV_TMUX_SOCKET: `axv-test-${basename(dir)}`,
      ...codex?.env,
      ...env,
    },
  });
  servers.add(server);
  const bootstrap = await fetch(
    `${server.base}/api/bootstrap?clientId=smoke-binding`,
    {
      signal: AbortSignal.timeout(10_000),
    },
  );
  assert.equal(bootstrap.status, 200);
  databaseIdentities.set(server.base, (await bootstrap.json()).databaseId);
  return server;
}

function invoke(args, env = {}) {
  return captureCommand(binary, args, {
    cwd: dir,
    env: {
      ...process.env,
      AXV_COMPUTE_SOCKET: '',
      ADDR: '127.0.0.1:0',
      DATA_DIR: join(dir, 'unexpected-data'),
      AXV_HOME: join(dir, 'axv-home'),
      AXV_WORKSPACE_DIR: join(dir, 'axv-home', 'workspace'),
      AXV_TMUX_SOCKET: `axv-test-${basename(dir)}`,
      ...env,
    },
    timeout: 5000,
    allowFailure: true,
  });
}

function fetchBounded(url, options = {}) {
  const headers = new Headers(options.headers);
  const identity = databaseIdentities.get(new URL(url).origin);
  if (identity && !headers.has('X-AXV-Database-ID'))
    headers.set('X-AXV-Database-ID', identity);
  return fetch(url, {
    ...options,
    headers,
    signal: AbortSignal.timeout(10_000),
  });
}

async function json(base, path, method = 'GET', body) {
  const response = await fetchBounded(`${base}/api${path}`, {
    method,
    headers:
      body === undefined
        ? { Origin: base }
        : { Origin: base, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const value = await response.json();
  assert.ok(
    response.ok,
    `${method} ${path}: ${response.status} ${JSON.stringify(value)}`,
  );
  return value;
}

async function remoteDisabledSmoke(base) {
  const runtime = await json(base, '/runtime');
  const status = await json(base, '/remote/status');
  assert.deepEqual(status, {
    instanceId: runtime.instanceId,
    enabled: false,
    supported: process.platform === 'darwin' || process.platform === 'linux',
    state: 'disabled',
  });
  const home = codex?.env.AXV_HOME || join(dir, 'axv-home');
  const inspected = await invoke(['remote', 'status', '--json'], {
    AXV_HOME: home,
  });
  assert.equal(inspected.code, 0, inspected.stderr);
  assert.equal(inspected.stderr, '');
  assert.deepEqual(JSON.parse(inspected.stdout), status);
  await assert.rejects(access(join(home, 'tailscale')), { code: 'ENOENT' });
  const summary = await json(base, '/connection');
  assert.equal(summary.local, true);
  assert.equal(summary.remote.enabled, false);
  assert.equal(summary.remote.supported, status.supported);
  assert.equal(summary.remote.state, 'disabled');
  assert.equal('host' in summary, false);
  assert.equal(JSON.stringify(summary).includes('authUrl'), false);
  assert.equal(JSON.stringify(summary).includes('ownerUserId'), false);
  const foreignSummary = await fetchBounded(`${base}/api/connection`, {
    headers: { Origin: 'https://outside.invalid' },
  });
  assert.equal(foreignSummary.status, 403);
  const foreign = await fetchBounded(`${base}/api/remote/status`, {
    headers: { Origin: 'https://outside.invalid' },
  });
  assert.equal(foreign.status, 403);
  assert.equal((await foreign.json()).code, 'remote_local_only');

  // Keep every smoke mutation disabled: acceptance must not enroll a real node.
  for (const origin of [undefined, 'https://outside.invalid']) {
    const rejected = await fetchBounded(`${base}/api/remote`, {
      method: 'PUT',
      headers: {
        ...(origin ? { Origin: origin } : {}),
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ enabled: false }),
    });
    assert.equal(rejected.status, 403);
    assert.equal((await rejected.json()).code, 'remote_local_only');
  }
  for (const body of [
    '{}',
    'null',
    '[]',
    '{"enabled":"false"}',
    '{"enabled":false,"unknown":true}',
    '{"enabled":false',
  ]) {
    const rejected = await fetchBounded(`${base}/api/remote`, {
      method: 'PUT',
      headers: { Origin: base, 'Content-Type': 'application/json' },
      body,
    });
    assert.equal(rejected.status, 400, body);
  }
  const disabled = await json(base, '/remote', 'PUT', { enabled: false });
  assert.equal(disabled.enabled, false);
  assert.equal(disabled.state, 'disabled');
  assert.deepEqual(await json(base, '/remote/status'), status);
  await assert.rejects(access(join(home, 'tailscale')), { code: 'ENOENT' });
}

async function cloudflareDisabledSmoke(base) {
  const runtime = await json(base, '/runtime');
  const status = await json(base, '/cloudflare/status');
  assert.deepEqual(status, {
    instanceId: runtime.instanceId,
    enabled: false,
    configured: false,
    state: 'disabled',
    listen: '127.0.0.1:3002',
  });
  const home = codex?.env.AXV_HOME || join(dir, 'axv-home');
  const inspected = await invoke(['cloudflare', 'status', '--json'], {
    AXV_HOME: home,
  });
  assert.equal(inspected.code, 0, inspected.stderr);
  assert.deepEqual(JSON.parse(inspected.stdout), status);
  const summary = await json(base, '/connection');
  assert.equal(summary.cloudflare.enabled, false);
  assert.equal(summary.cloudflare.configured, false);
  assert.equal(summary.cloudflare.state, 'disabled');
  for (const field of [
    'listen',
    'error',
    'issuer',
    'audience',
    'owner_subject',
  ])
    assert.equal(field in summary.cloudflare, false);
  const config = await json(base, '/cloudflare/config');
  assert.equal(config.instanceId, runtime.instanceId);
  assert.equal(config.account.hasToken, false);
  assert.equal(config.account.configured, false);
  assert.equal('token' in config.account, false);
  assert.equal('apiToken' in config.account, false);
  assert.deepEqual(await json(base, '/cloudflare/capabilities'), {
    configure: true,
    publish: true,
    health: true,
    traffic: true,
  });
  assert.equal((await json(base, '/cloudflare/health')).state, 'unconfigured');
  assert.equal(
    (await json(base, '/cloudflare/traffic?hours=24')).supported,
    false,
  );
  const publications = await json(base, '/cloudflare-publications');
  assert.equal(publications.supported, false);
  assert.match(publications.reason, /Cloudflare|publishing|account/i);
  assert.deepEqual(publications.publications, []);
  for (const command of ['config', 'check', 'publications']) {
    const result = await invoke(['cloudflare', command], { AXV_HOME: home });
    assert.equal(result.code, 0, result.stderr);
    assert.equal(typeof JSON.parse(result.stdout), 'object');
  }
  await assert.rejects(access(join(home, 'cloudflare')), { code: 'ENOENT' });
  for (const path of ['/cloudflare', '/cloudflare/status']) {
    const rejected = await fetchBounded(`${base}/api${path}`, {
      method: path === '/cloudflare' ? 'PUT' : 'GET',
      headers: {
        Origin: 'https://outside.invalid',
        'Content-Type': 'application/json',
      },
      body: path === '/cloudflare' ? '{"enabled":false}' : undefined,
    });
    assert.equal(rejected.status, 403);
    assert.equal((await rejected.json()).code, 'cloudflare_local_only');
  }
  for (const body of [
    '{}',
    'null',
    '{"enabled":"false"}',
    '{"enabled":false,"issuer":"https://outside.invalid"}',
    '{"enabled":false} {}',
  ]) {
    const rejected = await fetchBounded(`${base}/api/cloudflare`, {
      method: 'PUT',
      headers: { Origin: base, 'Content-Type': 'application/json' },
      body,
    });
    assert.equal(rejected.status, 400, body);
  }
  const disabled = await json(base, '/cloudflare', 'PUT', { enabled: false });
  assert.equal(disabled.state, 'disabled');
  assert.deepEqual(await json(base, '/cloudflare/status'), status);
}

async function until(check, description, details = () => '') {
  const deadline = Date.now() + 10_000;
  while (!(await check())) {
    assert.ok(Date.now() < deadline, `Timed out: ${description}. ${details()}`);
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
}

async function connectTerminal(base, path = '/api/terminal?cols=80&rows=24') {
  const url = new URL(path, base);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  // Node 24's built-in WebSocket accepts handshake headers in WebSocketInit.
  const socket = new WebSocket(url, {
    headers: {
      Origin: base,
      'X-AXV-Database-ID': databaseIdentities.get(base),
    },
  });
  socket.binaryType = 'arraybuffer';
  const session = {
    socket,
    ready: false,
    closed: false,
    code: null,
    output: '',
    error: '',
  };
  const decoder = new TextDecoder();
  terminalSockets.add(session);
  socket.addEventListener('message', ({ data }) => {
    if (typeof data === 'string') {
      const control = JSON.parse(data);
      if (control.ready) session.ready = true;
      if (control.error) session.error = control.error;
    } else session.output += decoder.decode(data, { stream: true });
  });
  socket.addEventListener('error', () => {
    session.error = 'WebSocket connection failed';
  });
  socket.addEventListener('close', ({ code }) => {
    session.closed = true;
    session.code = code;
    terminalSockets.delete(session);
  });
  await until(
    () => session.ready || session.closed || session.error,
    'terminal ready',
  );
  assert.equal(session.error, '', session.output);
  assert.equal(session.ready, true, 'The server must acknowledge a real PTY.');
  return session;
}

function shellInput(session, command) {
  session.socket.send(Buffer.from(command));
}

async function codexState(connection, tag) {
  shellInput(connection, `inspect ${tag}\r`);
  // tmux may end a rendered row with a cursor escape rather than CR/LF.
  // Require that boundary so a partial WebSocket frame cannot count as state.
  const boundary = `[\\r\\n${String.fromCharCode(27)}]`;
  const pattern = new RegExp(
    `STATE ${tag} pid=(\\d+) value=([^\\r\\n${String.fromCharCode(27)}]+)(?=${boundary})`,
  );
  await until(
    () => pattern.test(connection.output),
    `Codex fixture state ${tag}`,
    () => JSON.stringify(connection.output.slice(-2000)),
  );
  const match = connection.output.match(pattern);
  return { pid: Number(match[1]), value: match[2] };
}

async function codexActivity(base, session, state) {
  let activity;
  await until(
    async () => {
      const current = await json(
        base,
        `/host/codex/sessions/${session.id}/runs/${session.runId}`,
      );
      activity = current.session.activity;
      return activity?.state === state;
    },
    `Codex exact-run activity is ${state}`,
    () => JSON.stringify(activity),
  );
  return activity;
}

function activityContent(activity) {
  const { observation, ...content } = activity;
  assert.equal(typeof observation.instanceId, 'string');
  assert.ok(observation.instanceId.length > 0);
  assert.ok(Number.isSafeInteger(observation.revision));
  assert.ok(observation.revision > 0);
  return content;
}

async function codexTerminal(base, session) {
  let connection;
  try {
    connection = await connectTerminal(
      base,
      `/api/host/codex/sessions/${session.id}/runs/${session.runId}/terminal?cols=80&rows=24`,
    );
  } catch (error) {
    const current = await json(
      base,
      `/host/codex/sessions/${session.id}/runs/${session.runId}`,
    );
    throw new Error(
      `Codex terminal connection failed for exact run: ${JSON.stringify(current)}`,
      { cause: error },
    );
  }
  await until(
    () =>
      connection.output.includes('codex-test>') ||
      connection.closed ||
      connection.error,
    'Codex fixture is ready for input',
    () => JSON.stringify(connection.output.slice(-2000)),
  );
  assert.equal(connection.error, '', connection.output);
  assert.equal(connection.closed, false, connection.output);
  return connection;
}

async function codexSmoke(base) {
  if (!codex) return null;
  const info = await json(base, '/host/codex/info');
  assert.equal(info.ready, true, JSON.stringify(info));
  assert.equal(info.installed, true);
  assert.equal(info.defaultProjectPath, codex.defaultProjectPath);
  assert.equal(info.configPath, codex.configPath);
  assert.equal(info.network.proxySource, 'environment');
  assert.ok(info.network.proxy.includes('127.0.0.1:17890'));
  assert.ok(!JSON.stringify(info).includes('fixture-password'));
  assert.ok(!JSON.stringify(info).includes('fixture-user'));
  const configText = await readFile(codex.configPath, 'utf8');
  assert.equal(typeof JSON.parse(configText), 'object');
  await assert.rejects(access(codex.defaultProjectPath));
  const directories = await json(
    base,
    `/host/directories?path=${encodeURIComponent(codex.projectPath)}`,
  );
  assert.equal(directories.path, codex.projectPath);
  assert.ok(directories.directories.some((folder) => folder.name === 'nested'));
  const request = { requestId: randomUUID() };
  const { session } = await json(base, '/host/codex/sessions', 'POST', request);
  assert.equal(session.projectPath, codex.defaultProjectPath);
  assert.ok(session.tmuxSession.startsWith('axv-codex-'));
  assert.ok(session.tmuxSession.endsWith(session.runId));
  assert.ok(session.tmuxSocket.endsWith(`/${codex.tmuxSocket}`));
  assert.ok(session.attachCommand.includes(session.tmuxSession));
  assert.ok(session.attachCommand.includes(codex.tmuxSocket));
  assert.deepEqual(
    (await codex.listTmuxSessions()).sort(),
    [session.tmuxSession, 'fixture-sentinel'].sort(),
  );
  await codex.verifyIsolation();
  await access(codex.defaultProjectPath);
  const repeated = await json(base, '/host/codex/sessions', 'POST', request);
  assert.equal(
    repeated.session.id,
    session.id,
    'Create retries must be idempotent.',
  );
  const first = await codexTerminal(base, session);
  await until(async () => {
    const current = (await json(base, `/host/codex/sessions/${session.id}`))
      .session;
    if (current.status !== 'running' || !current.conversationId) return false;
    Object.assign(session, current);
    return true;
  }, 'official Codex conversation is identified');
  shellInput(first, 'network\r');
  await until(
    () => first.output.includes(`NETWORK ${codex.networkDigest}`),
    'Codex inherits explicit proxy environment',
  );
  shellInput(first, 'state survives-restart\r');
  const original = await codexState(first, 'original');
  assert.equal(original.value, 'survives-restart');
  const peer = await codexTerminal(base, session);
  assert.deepEqual(await codexState(peer, 'peer'), original);
  assert.equal(
    first.closed,
    false,
    'A second client must not detach the first.',
  );
  first.socket.close();
  peer.socket.close();
  await until(() => first.closed && peer.closed, 'Codex clients detach');
  const detached = await json(base, `/host/codex/sessions/${session.id}`);
  assert.equal(detached.session.status, 'running');
  const reattached = await codexTerminal(base, session);
  assert.deepEqual(await codexState(reattached, 'reattached'), original);
  const { session: renamed } = await json(
    base,
    `/host/codex/sessions/${session.id}`,
    'PATCH',
    { revision: session.metadataRevision, title: 'Production acceptance' },
  );
  assert.equal(renamed.title, 'Production acceptance');
  assert.equal(renamed.tmuxSession, session.tmuxSession);
  assert.equal(renamed.tmuxSocket, session.tmuxSocket);
  const { session: archived } = await json(
    base,
    `/host/codex/sessions/${session.id}`,
    'PATCH',
    { revision: renamed.metadataRevision, archived: true },
  );
  assert.ok(archived.archivedAt);
  assert.equal(archived.status, 'running');
  assert.deepEqual(await codexState(reattached, 'after-metadata'), original);
  const conflict = await fetchBounded(
    `${base}/api/host/codex/sessions/${session.id}`,
    {
      method: 'PATCH',
      headers: { Origin: base, 'Content-Type': 'application/json' },
      body: JSON.stringify({
        revision: session.metadataRevision,
        title: 'Stale name',
      }),
    },
  );
  assert.equal(conflict.status, 409);
  assert.equal((await conflict.json()).code, 'metadata_conflict');
  const independent = await json(base, '/host/codex/sessions', 'POST', {
    requestId: randomUUID(),
    projectPath: codex.projectPath,
  });
  assert.equal(independent.session.projectPath, codex.projectPath);
  assert.equal(independent.session.tmuxSocket, session.tmuxSocket);
  const other = await codexTerminal(base, independent.session);
  const otherState = await codexState(other, 'independent');
  assert.notEqual(otherState.pid, original.pid);
  assert.equal(otherState.value, 'initial');
  shellInput(other, 'message desktop-independent activity\r');
  await codexActivity(base, independent.session, 'working');
  shellInput(other, 'activity approval\r');
  const offlineActivity = await codexActivity(
    base,
    independent.session,
    'waiting_approval',
  );
  other.socket.close();
  await until(() => other.closed, 'independent Codex client detaches');
  assert.deepEqual(await codexState(reattached, 'unaffected'), original);
  const recent = await json(base, '/host/codex/projects');
  assert.deepEqual(
    recent.projects.map((project) => project.path),
    [codex.projectPath, codex.defaultProjectPath],
  );
  shellInput(reattached, 'message production activity persistence\r');
  const working = await codexActivity(base, session, 'working');
  assert.equal(working.needsAttention, false);
  shellInput(reattached, 'activity approval\r');
  const waiting = await codexActivity(base, session, 'waiting_approval');
  assert.equal(waiting.needsAttention, true);
  assert.equal(waiting.turnId, working.turnId);
  assert.ok(waiting.sequence > working.sequence);
  shellInput(reattached, 'activity resolve\r');
  const resolved = await codexActivity(base, session, 'working');
  assert.equal(resolved.turnId, waiting.turnId);
  assert.ok(resolved.sequence > waiting.sequence);
  shellInput(reattached, 'activity fail\r');
  const failed = await codexActivity(base, session, 'failed');
  assert.equal(failed.needsAttention, true);
  assert.equal(failed.turnId, working.turnId);
  assert.ok(failed.sequence > resolved.sequence);
  const { session: reviewed } = await json(
    base,
    `/host/codex/sessions/${session.id}/runs/${session.runId}/activity/review`,
    'POST',
    { eventId: failed.eventId },
  );
  assert.deepEqual(activityContent(reviewed.activity), {
    ...activityContent(failed),
    needsAttention: false,
  });
  reattached.socket.close();
  await until(() => reattached.closed, 'reviewed Codex client detaches');
  assert.deepEqual(
    activityContent(await codexActivity(base, session, 'failed')),
    activityContent(reviewed.activity),
    'Activity and its exact-event review survive terminal detachment.',
  );
  return {
    id: session.id,
    runId: session.runId,
    conversationId: session.conversationId,
    configText,
    title: archived.title,
    archivedAt: archived.archivedAt,
    metadataRevision: archived.metadataRevision,
    projects: recent.projects,
    activity: reviewed.activity,
    offline: { ...independent.session, activity: offlineActivity },
    ...original,
  };
}

async function resumedCodexSmoke(base, previous, networkDigest) {
  if (!previous) return;
  const listed = await json(base, '/host/codex/sessions');
  assert.ok(
    listed.sessions.some(
      (session) => session.id === previous.id && session.status === 'running',
    ),
  );
  const persisted = listed.sessions.find(
    (session) => session.id === previous.id,
  );
  assert.equal(persisted.title, previous.title);
  assert.equal(persisted.archivedAt, previous.archivedAt);
  assert.equal(persisted.metadataRevision, previous.metadataRevision);
  assert.equal(persisted.runId, previous.runId);
  assert.equal(persisted.conversationId, previous.conversationId);
  assert.deepEqual(
    activityContent(persisted.activity),
    activityContent(previous.activity),
    'Desktop restart preserves the exact activity event and its saved review.',
  );
  assert.notEqual(
    persisted.activity.observation.instanceId,
    previous.activity.observation.instanceId,
    'A restarted desktop publishes a distinct activity observation instance.',
  );
  const offline = await codexActivity(base, previous.offline, 'completed');
  assert.equal(offline.turnId, previous.offline.activity.turnId);
  assert.ok(offline.sequence > previous.offline.activity.sequence);
  assert.equal(
    offline.needsAttention,
    false,
    'A successful turn completed while the desktop was stopped requires no action.',
  );
  const offlineRun = (
    await json(
      base,
      `/host/codex/sessions/${previous.offline.id}/runs/${previous.offline.runId}`,
    )
  ).session;
  assert.equal(offlineRun.status, 'running');
  assert.equal(offlineRun.runId, previous.offline.runId);
  await json(
    base,
    `/host/codex/sessions/${previous.offline.id}/runs/${previous.offline.runId}/end`,
    'POST',
  );
  assert.deepEqual(
    (await json(base, '/host/codex/projects')).projects,
    previous.projects,
  );
  const connection = await codexTerminal(base, previous);
  assert.deepEqual(await codexState(connection, 'after-restart'), {
    pid: previous.pid,
    value: previous.value,
  });
  assert.equal(await readFile(codex.configPath, 'utf8'), previous.configText);
  shellInput(connection, 'network\r');
  await until(
    () => connection.output.includes(`NETWORK ${codex.networkDigest}`),
    'persistent Codex retains its original environment',
  );
  const { session: fresh } = await json(base, '/host/codex/sessions', 'POST', {
    requestId: randomUUID(),
  });
  const freshConnection = await codexTerminal(base, fresh);
  shellInput(freshConnection, 'network\r');
  await until(
    () => freshConnection.output.includes(`NETWORK ${networkDigest}`),
    'new Codex receives updated proxy environment from reused tmux server',
    () =>
      JSON.stringify({
        closed: freshConnection.closed,
        error: freshConnection.error,
        output: freshConnection.output.slice(-2000),
      }),
  );
  await json(
    base,
    `/host/codex/sessions/${fresh.id}/runs/${fresh.runId}/end`,
    'POST',
  );
  await until(() => freshConnection.closed, 'new proxy test session ends');
  await json(
    base,
    `/host/codex/sessions/${previous.id}/runs/${previous.runId}/end`,
    'POST',
  );
  await until(() => connection.closed, 'resumed Codex session ends');
  const ended = await json(base, `/host/codex/sessions/${previous.id}`);
  assert.equal(ended.session.status, 'ended');
  const repeated = await json(
    base,
    `/host/codex/sessions/${previous.id}/runs/${previous.runId}/end`,
    'POST',
  );
  assert.equal(repeated.session.status, 'ended');
  const recoveryRequest = {
    requestId: randomUUID(),
    expectedRunId: previous.runId,
  };
  const { session: recovered } = await json(
    base,
    `/host/codex/sessions/${previous.id}/resume`,
    'POST',
    recoveryRequest,
  );
  assert.equal(recovered.id, previous.id);
  assert.notEqual(recovered.runId, previous.runId);
  assert.equal(recovered.conversationId, previous.conversationId);
  assert.equal(recovered.title, previous.title);
  assert.equal(recovered.archivedAt, previous.archivedAt);
  const retried = await json(
    base,
    `/host/codex/sessions/${previous.id}/resume`,
    'POST',
    recoveryRequest,
  );
  assert.equal(retried.session.runId, recovered.runId);
  const restored = await codexTerminal(base, recovered);
  const recoveredState = await codexState(restored, 'history-recovered');
  assert.notEqual(recoveredState.pid, previous.pid);
  assert.equal(recoveredState.value, previous.value);
  const staleEnd = await json(
    base,
    `/host/codex/sessions/${previous.id}/runs/${previous.runId}/end`,
    'POST',
  );
  assert.equal(staleEnd.session.runId, previous.runId);
  assert.deepEqual(
    await codexState(restored, 'old-end-isolated'),
    recoveredState,
  );
  const originalRun = await json(
    base,
    `/host/codex/sessions/${previous.id}/runs/${previous.runId}`,
  );
  assert.equal(originalRun.session.status, 'ended');
  await json(
    base,
    `/host/codex/sessions/${previous.id}/runs/${recovered.runId}/end`,
    'POST',
  );
  await until(() => restored.closed, 'recovered Codex run ends');
}

function shellEncoded(text) {
  return [...Buffer.from(text)]
    .map((byte) => `\\${byte.toString(8).padStart(3, '0')}`)
    .join('');
}

async function shellMarker(session, marker) {
  shellInput(session, `printf '${shellEncoded(marker)}\\n'\r`);
  await until(
    () => session.output.includes(marker),
    `shell output ${marker}`,
    () => JSON.stringify(session.output.slice(-2000)),
  );
}

async function shellDimensions(session, marker, expected) {
  // Encode the prefix so echoed commands cannot satisfy the assertion. Capture
  // stty output inside printf to keep shell prompt/title escapes out of the value.
  shellInput(session, `printf '${shellEncoded(marker)}%s\\n' "$(stty size)"\r`);
  await until(
    () => session.output.includes(`${marker}${expected}\r\n`),
    `PTY dimensions ${expected}`,
    () => JSON.stringify(session.output.slice(-2000)),
  );
}

async function terminalSmoke(base) {
  const info = await json(base, '/terminal/info');
  assert.equal(
    info.supported,
    true,
    `PTY verification blocked: ${info.reason}`,
  );
  assert.ok(info.shell);
  const session = await connectTerminal(base);
  if (process.platform === 'win32') return session;
  await shellMarker(session, 'production-pty-ready');
  // Use a known prompt for the interrupt handshake without depending on the
  // user's login-shell theme. exec keeps this session's PTY and process owner.
  const prompt = 'production-shell-ready> ';
  shellInput(
    session,
    `exec env ENV= PS1="$(printf '${shellEncoded(prompt)}')" /bin/sh -i\r`,
  );
  await until(
    () => session.output.includes(prompt),
    'controlled shell prompt',
    () =>
      JSON.stringify({
        closed: session.closed,
        code: session.code,
        error: session.error,
        output: session.output.slice(-2000),
      }),
  );
  await shellDimensions(session, 'initial-size:', '24 80');
  session.socket.send(JSON.stringify({ resize: [101, 31] }));
  await shellDimensions(session, 'resized-size:', '31 101');
  shellInput(
    session,
    // The child prints readiness only after the shell has put its job in the
    // foreground; printing from the parent before launching cat races Ctrl+C.
    `/bin/sh -c 'printf "${shellEncoded('cat-ready')}\\n"; cat'\r`,
  );
  await until(
    () => session.output.includes('cat-ready'),
    'foreground job ready',
  );
  const interruptedAt = session.output.length;
  shellInput(session, '\u0003');
  await until(
    () => session.output.slice(interruptedAt).includes(prompt),
    'shell prompt after interrupt',
    () => JSON.stringify(session.output.slice(-2000)),
  );
  await shellMarker(session, 'production-interrupt-ok');
  session.socket.close();
  await until(() => session.closed, 'disconnected terminal closes');
  if (codex) {
    const projectShell = await connectTerminal(
      base,
      `/api/terminal?cols=80&rows=24&projectPath=${encodeURIComponent(codex.projectPath)}`,
    );
    shellInput(
      projectShell,
      `printf '${shellEncoded('PROJECT_CWD:')}%s\\n' "$PWD"\r`,
    );
    await until(
      () => projectShell.output.includes(`PROJECT_CWD:${codex.projectPath}`),
      'project Terminal starts in the selected directory',
    );
    projectShell.socket.close();
    await until(
      () => projectShell.closed,
      'project Terminal closes independently',
    );
  }
  // All four slots must be reusable after disconnect. A fifth is refused before upgrade.
  const sessions = [];
  for (let index = 0; index < 4; index++)
    sessions.push(await connectTerminal(base));
  const limited = await fetchBounded(`${base}/api/terminal`, {
    headers: { Origin: base },
  });
  assert.equal(limited.status, 503);
  assert.equal((await limited.json()).code, 'terminal_limit');
  shellInput(sessions[0], 'exit 17\r');
  await until(() => sessions[0].closed, 'natural shell exit');
  assert.equal(sessions[0].code, 1000);
  for (const closed of sessions.slice(1, 3)) {
    closed.socket.close();
    await until(() => closed.closed, 'extra terminal closes');
  }
  // Keep one owned foreground job alive through SIGTERM to exercise manager shutdown.
  shellInput(
    sessions[3],
    `/bin/sh -c 'printf "${shellEncoded('drain-ready')}\\n"; cat'\r`,
  );
  await until(
    () => sessions[3].output.includes('drain-ready'),
    'shutdown foreground job',
  );
  return sessions[3];
}

try {
  const invalidEnvironment = { ADDR: 'invalid', PORT: 'invalid' };
  for (const args of [
    ['help'],
    ['--help'],
    ['-h'],
    ['serve', '--help'],
    ['serve', '-h'],
  ]) {
    const result = await invoke(args, invalidEnvironment);
    assert.equal(result.code, 0);
    assert.match(result.stdout, new RegExp(`Usage:\\s+${project} `));
    assert.equal(result.stderr, '');
  }
  for (const args of [['version'], ['--version']]) {
    const result = await invoke(args, invalidEnvironment);
    assert.equal(result.code, 0);
    assert.equal(result.stdout, `${project} ${process.env.VERSION ?? 'dev'}\n`);
    assert.equal(result.stderr, '');
  }
  for (const args of [
    ['unknown'],
    ['--addr', '127.0.0.1:0'],
    ['version', 'extra'],
    ['serve', '--unknown'],
    ['serve', 'extra'],
    ['serve', '--addr'],
    ['serve', '--addr', ''],
    ['serve', '--data-dir', ' '],
    ['serve', '--addr', '--data-dir', 'unused'],
    ['serve', '--addr', 'invalid'],
  ]) {
    const result = await invoke(args);
    assert.equal(
      result.code,
      args.at(-1) === 'invalid' ? 1 : 2,
      `Expected failure for ${JSON.stringify(args)}`,
    );
    assert.equal(result.stdout, '');
    assert.match(result.stderr, /^error: /);
  }
  const invalidConfig = await invoke(['serve'], invalidEnvironment);
  assert.equal(invalidConfig.code, 1);
  assert.match(invalidConfig.stderr, /invalid listen address/);
  assert.deepEqual(
    await readdir(dir),
    [],
    'Information and invalid commands must not create data.',
  );

  shellEnvironment = await createShellEnvironment(dir);
  codex = await createCodexFixture(dir);
  const first = await start();
  await remoteDisabledSmoke(first.base);
  await cloudflareDisabledSmoke(first.base);
  const logPath = (await json(first.base, '/compute/environment')).logPath;
  assert.equal(
    typeof logPath,
    process.platform === 'win32' ? 'undefined' : 'string',
  );
  const duplicate = await invoke(['serve']);
  assert.equal(duplicate.code, 1);
  assert.match(duplicate.stderr, /desktop already running for this profile/);
  const collision = await invoke(
    [
      'serve',
      '--addr',
      new URL(first.base).host,
      '--data-dir',
      join(dir, 'collision-data'),
    ],
    { AXV_HOME: join(dir, 'collision-profile') },
  );
  assert.equal(collision.code, 1);
  assert.match(collision.stderr, /error: listen tcp/);
  assert.ok(!collision.stdout.includes('DESKTOP_READY'));
  assert.equal((await fetchBounded(`${first.base}/api/health`)).status, 200);
  const html = await (await fetchBounded(first.base)).text();
  assert.match(html, /<div id="root">/);
  assert.match(html, /rel="manifest"[^>]*crossorigin="use-credentials"/);
  const manifestResponse = await fetchBounded(
    `${first.base}/manifest.webmanifest`,
  );
  assert.equal(manifestResponse.status, 200);
  assert.match(
    manifestResponse.headers.get('content-type'),
    /^application\/manifest\+json/,
  );
  assert.equal(manifestResponse.headers.get('cache-control'), 'no-cache');
  const manifest = await manifestResponse.json();
  assert.deepEqual(
    [manifest.id, manifest.start_url, manifest.scope, manifest.display],
    ['/', '/', '/', 'standalone'],
  );
  assert.equal(manifest.name, 'AXV');
  assert.equal(manifest.short_name, 'AXV');
  assert.equal(manifest.description, 'A personal cloud workstation.');
  for (const [path, size] of [
    ['/icons/axv-192.png', 192],
    ['/icons/axv-512.png', 512],
    ['/icons/apple-touch-icon.png', 180],
  ]) {
    const icon = await fetchBounded(`${first.base}${path}`);
    assert.equal(icon.status, 200);
    assert.match(icon.headers.get('content-type'), /^image\/png/);
    assert.equal(icon.headers.get('cache-control'), 'no-cache');
    const bytes = Buffer.from(await icon.arrayBuffer());
    assert.equal(bytes.subarray(0, 8).toString('hex'), '89504e470d0a1a0a');
    assert.equal(bytes.readUInt32BE(16), size);
    assert.equal(bytes.readUInt32BE(20), size);
  }
  const oldClient = await fetch(`${first.base}/api/entries`, {
    signal: AbortSignal.timeout(10_000),
  });
  assert.equal(oldClient.status, 428);
  assert.equal((await oldClient.json()).code, 'database_identity_required');
  const asset = html.match(/src="([^"]+\.js)"/)?.[1];
  assert.ok(asset, 'Production HTML must reference a bundled script.');
  assert.equal((await fetchBounded(`${first.base}${asset}`)).status, 200);
  assert.equal(
    (await fetchBounded(`${first.base}/desktop/documents`)).status,
    200,
  );
  const terminal = await terminalSmoke(first.base);
  const persistentCodex = await codexSmoke(first.base);
  const initial = await json(first.base, '/bootstrap?clientId=smoke-browser');
  assert.ok(initial.databaseId);
  const { entry: folder } = await json(first.base, '/entries', 'POST', {
    parentId: 'documents',
    name: 'Production folder',
    kind: 'folder',
  });
  const { entry: original } = await json(first.base, '/entries', 'POST', {
    parentId: 'documents',
    name: 'Production smoke.txt',
    kind: 'file',
    content: 'Persisted through a restart.',
  });
  const { entry } = await json(first.base, `/entries/${original.id}`, 'PATCH', {
    parentId: folder.id,
    name: 'Moved smoke.txt',
    revision: original.revision,
  });
  await json(first.base, '/entries/desktop-note/trash', 'POST');
  const preferences = {
    wallpaper: 'dither',
    iconSnap: false,
    clock24Hour: false,
  };
  await json(first.base, '/preferences', 'PUT', preferences);
  const windowId = `notes:${entry.id}`;
  const layout = {
    version: 2,
    currentWorkspaceId: 'workspace-2',
    activeWindowId: windowId,
    icons: {},
    windows: [
      {
        id: windowId,
        appId: 'notes',
        title: entry.name,
        params: { entryId: entry.id },
        workspaceId: 'workspace-2',
        sticky: false,
        minimized: false,
        x: 150,
        y: 80,
        width: 620,
        height: 410,
        shaded: false,
        zoomed: false,
        viewState: {},
      },
      {
        id: 'files:documents',
        appId: 'files',
        title: 'Documents',
        params: { folderId: 'documents' },
        workspaceId: 'workspace-1',
        sticky: true,
        minimized: true,
        x: 80,
        y: 90,
        width: 700,
        height: 420,
        shaded: false,
        zoomed: false,
        viewState: { path: ['documents', folder.id], selected: [entry.id] },
      },
      {
        id: 'inspector:main',
        appId: 'inspector',
        title: 'Inspector',
        params: {},
        workspaceId: 'workspace-2',
        sticky: false,
        minimized: false,
        x: 800,
        y: 90,
        width: 300,
        height: 400,
        shaded: false,
        zoomed: false,
        viewState: { pinnedEntryId: entry.id },
      },
    ],
    workspaces: Array.from({ length: 4 }, (_, index) => ({
      id: `workspace-${index + 1}`,
      name: index === 1 ? 'Writing' : `Workspace ${index + 1}`,
      order:
        index === 1
          ? [windowId, 'inspector:main', 'files:documents']
          : ['files:documents'],
      activeWindowId: index === 1 ? windowId : null,
      clip:
        index === 1
          ? [
              {
                id: 'favorite',
                appId: 'notes',
                title: entry.name,
                params: { entryId: entry.id },
              },
            ]
          : [],
    })),
    dock: [
      { appId: 'files', pinned: true },
      { appId: 'notes', pinned: true },
    ],
    pinnedMenus: [
      {
        id: 'saved-menu',
        kind: 'application',
        groupId: 'file',
        ownerWindowId: windowId,
        x: 8,
        y: 160,
        title: 'Text Editor — File',
      },
    ],
  };
  await json(first.base, '/layout', 'PUT', {
    clientId: 'smoke-browser',
    layout,
  });
  await stop(first.child);
  assert.equal(
    first.child.exitCode,
    0,
    'SIGTERM must shut the service down cleanly.',
  );
  if (terminal)
    await until(() => terminal.closed, 'server shutdown closes active PTY');
  if (persistentCodex) {
    const offline = persistentCodex.offline;
    // Only the isolated fixture's recorded pane is addressed. No desktop
    // service is running to observe or forward these provider transitions.
    await captureCommand(
      'tmux',
      [
        '-S',
        offline.tmuxSocket,
        'send-keys',
        '-t',
        offline.tmuxSession,
        '-l',
        'activity resolve\ractivity complete\r',
      ],
      { cwd: dir, timeout: 5000 },
    );
    await until(async () => {
      const pane = await captureCommand(
        'tmux',
        [
          '-S',
          offline.tmuxSocket,
          'capture-pane',
          '-p',
          '-t',
          offline.tmuxSession,
        ],
        { cwd: dir, timeout: 5000 },
      );
      return pane.stdout.includes('ACTIVITY complete');
    }, 'independent runner observes completion while the desktop is stopped');
  }
  const updatedProxy = 'http://next-fixture:next-password@127.0.0.1:17891';
  const updatedProxyEnvironment = Object.fromEntries(
    (codex?.proxyKeys || []).map((key) => [key, updatedProxy]),
  );
  const updatedNetworkDigest = createHash('sha256')
    .update((codex?.proxyKeys || []).map(() => updatedProxy).join('\0'))
    .digest('hex');
  const second = await start({
    args: ['serve', '--addr', '127.0.0.1:0', '--data-dir', join(dir, 'data')],
    env: {
      ...invalidEnvironment,
      DATA_DIR: join(dir, 'ignored-data'),
      ...updatedProxyEnvironment,
    },
  });
  await remoteDisabledSmoke(second.base);
  await cloudflareDisabledSmoke(second.base);
  await resumedCodexSmoke(second.base, persistentCodex, updatedNetworkDigest);
  assert.ok(
    !(await readdir(dir)).includes('ignored-data'),
    'Explicit flags must override environment configuration.',
  );
  const saved = await json(second.base, `/entries/${entry.id}`);
  assert.equal(saved.content, 'Persisted through a restart.');
  assert.equal(saved.entry.parentId, folder.id);
  assert.equal(saved.entry.name, 'Moved smoke.txt');
  const recovered = await json(
    second.base,
    '/bootstrap?clientId=smoke-browser',
  );
  assert.equal(recovered.databaseId, initial.databaseId);
  assert.deepEqual(recovered.preferences, preferences);
  assert.deepEqual(recovered.layout, layout);
  assert.ok(
    recovered.entries.find((item) => item.id === 'desktop-note')?.trashedAt,
  );
  const other = await json(second.base, '/bootstrap?clientId=another-browser');
  assert.equal(other.layout, null, 'Client layouts must remain isolated.');
  await json(second.base, '/entries/desktop-note/restore', 'POST');
  const restored = await json(second.base, '/entries/desktop-note');
  assert.ok(!restored.entry.trashedAt);
  const download = await (
    await fetchBounded(
      `${second.base}/api/entries/${entry.id}/content?download=1`,
    )
  ).text();
  assert.equal(download, saved.content);
  if (terminal) {
    const restarted = await connectTerminal(second.base);
    if (process.platform !== 'win32')
      await shellMarker(restarted, 'production-restarted-pty');
    restarted.socket.close();
    await until(() => restarted.closed, 'restarted terminal closes');
  }
  await stop(second.child);
  assert.equal(second.child.exitCode, 0);
  const logRecords = await verifyProfileLog(
    codex?.env.AXV_HOME || join(dir, 'axv-home'),
    {
      logPath,
      minimumDesktopStarts: 2,
      excluded: [
        'fixture-password',
        'next-password',
        'survives-restart',
        'production-pty-ready',
        'Persisted through a restart.',
      ],
    },
  );
  if (process.platform !== 'win32')
    assert.ok(
      logRecords.some(
        (record) =>
          record.event === 'service.diagnostic' &&
          /^host terminal process exited .*exit_code=17 signal=0 .*close_requested=false /u.test(
            record.message,
          ),
      ),
      'Host shell exit status must reach the private profile log.',
    );
  await codex?.verifyIsolation();
  console.log(
    'Production smoke passed: CLI help/version/errors, disabled Tailscale and Cloudflare status and CLI identity without node or connector state, local-only remote controls and strict mutation input, isolated global configuration, flag precedence, dynamic ports, listener failure, graceful shutdown with active PTYs, host shell input/output/resize/interrupt/session limits/restart, Host Codex workspace/project/tmux identity/redacted proxy diagnostics/environment refresh/idempotent creation and continuation/multiple clients/detach/reattach/session isolation/metadata/recent projects/termination/process and exact-event activity review persistence across service restart/approval resolution/history context in a new process/old-run isolation, embedded UI, independent cwd, database identity, files, trash, preferences, workspaces, Dock, Clip, panels, and client layouts survive restart.',
  );
} catch (error) {
  console.error('Production smoke failed:', error);
  try {
    const diagnostics = await codex?.diagnostics();
    if (diagnostics) console.error(diagnostics);
  } catch (diagnosticError) {
    console.error('Production smoke diagnostics unavailable:', diagnosticError);
  }
  throw error;
} finally {
  await cleanup();
}
