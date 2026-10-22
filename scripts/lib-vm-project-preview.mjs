import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdtemp, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { chromium } from '@playwright/test';
import { launchServer, stop } from './lib.mjs';

const developmentServer = String.raw`
import base64, hashlib, http.server, struct

class App(http.server.BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'
    def log_message(self, *args): pass
    def do_HEAD(self):
        self.send_response(200)
        self.send_header('Content-Length', '0')
        self.end_headers()
    def do_GET(self):
        if self.path.startswith('/hmr'):
            key = self.headers['Sec-WebSocket-Key']
            accept = base64.b64encode(hashlib.sha1((key + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest()).decode()
            self.send_response(101)
            self.send_header('Upgrade', 'websocket')
            self.send_header('Connection', 'Upgrade')
            self.send_header('Sec-WebSocket-Accept', accept)
            self.end_headers()
            self.wfile.flush()
            while True:
                head = self.rfile.read(2)
                if len(head) != 2: return
                opcode, size = head[0] & 15, head[1] & 127
                if size == 126: size = struct.unpack('!H', self.rfile.read(2))[0]
                if size > 65535: return
                mask = self.rfile.read(4) if head[1] & 128 else b''
                data = self.rfile.read(size)
                if len(data) != size: return
                if mask: data = bytes(c ^ mask[i % 4] for i, c in enumerate(data))
                if opcode == 8: return
                if opcode not in (1, 2): continue
                self.wfile.write(bytes([128 | opcode, len(data)]) + data)
                self.wfile.flush()
        else:
            body = b'''document.body.dataset.module = 'loaded';
fetch('/session', {headers: {Authorization: 'Bearer native-application-session', 'X-AXV-Preview-Application-Authorization': 'forged-application-credential'}}).then(response => response.text()).then(value => document.body.dataset.session = value);
const ws = new WebSocket(location.origin.replace(/^http/, 'ws') + '/hmr?native=1');
ws.onopen = () => ws.send('native-hot-update');
ws.onmessage = (event) => document.body.dataset.hmr = event.data;
ws.onclose = () => document.body.dataset.closed = 'yes';
''' if self.path == '/main.js' else (('session-ok' if self.headers.get('Authorization') == 'Bearer native-application-session' and 'application-session=native-session' in self.headers.get('Cookie', '') and 'axv_preview_' not in self.headers.get('Cookie', '') and not self.headers.get('X-AXV-Preview-Application-Authorization') else 'session-failed').encode() if self.path == '/session' else b'<!doctype html><title>AXV native preview</title><body><script type="module" src="/main.js"></script></body>')
            self.send_response(200)
            self.send_header('Content-Type', 'text/javascript' if self.path == '/main.js' else 'text/html')
            self.send_header('Content-Length', str(len(body)))
            if self.path == '/': self.send_header('Set-Cookie', 'application-session=native-session; Path=/; HttpOnly; SameSite=Strict')
            self.end_headers()
            self.wfile.write(body)

server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), App)
print('AXV_PREVIEW_PORT=' + str(server.server_port), flush=True)
server.serve_forever()
`;

// Runs against the real guest, persistent task journal and production desktop.
// Invoke while the reviewed project VM is running; cleanup is exact and finishes
// before returning, so a caller can safely proceed to replacement acceptance.
export async function verifyVMProjectPreview({
  executable,
  directory,
  profileHome,
  socket,
  env,
  project,
  binding,
  request,
  cleanupRequest = request,
  signal,
}) {
  const owned = await mkdtemp(join(directory, 'preview-workflow-'));
  const dataDir = join(owned, 'data');
  const requestId = `preview-native-${randomUUID()}`;
  let task;
  let desktop;
  let browser;
  let preview;
  let taskSubmitted = false;
  let cleaning = false;
  let failure;
  const results = [];
  const taskPath = () => `/projects/${project.id}/tasks/${task.task.id}`;
  const final = (status) =>
    ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(status);

  async function until(check, description, timeout = 30_000) {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      if (!cleaning) signal?.throwIfAborted();
      const value = await check();
      if (value) return value;
      await delay(100, undefined, { signal: cleaning ? undefined : signal });
    }
    throw new Error(`Timed out waiting for ${description}.`);
  }

  async function api(path, method = 'GET', body, expected = 200) {
    const response = await fetch(`${desktop.base}/api${path}`, {
      method,
      headers: {
        Origin: desktop.base,
        'Content-Type': 'application/json',
        'X-AXV-Host-ID': binding.hostId,
      },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.any(
        [cleaning ? undefined : signal, AbortSignal.timeout(30_000)].filter(
          Boolean,
        ),
      ),
    });
    const value = await response.json();
    assert.equal(response.status, expected, JSON.stringify(value));
    return value;
  }

  try {
    taskSubmitted = true;
    task = await request(`/projects/${project.id}/tasks`, 'POST', {
      requestId,
      binding,
      spec: {
        argv: ['python3', '-u', '-c', developmentServer],
        directory: '',
        limits: {
          timeoutSeconds: 180,
          cpus: 1,
          memoryMiB: 128,
          maxProcesses: 16,
          maxLogBytes: 65536,
        },
      },
    });
    const port = await until(async () => {
      const current = await request(taskPath());
      assert.ok(!final(current.attempt.status), JSON.stringify(current));
      const log = await request(
        `${taskPath()}/attempts/${task.attempt.id}/logs?offset=0&limit=65536`,
      );
      const text = Buffer.from(log.data, 'base64').toString('utf8');
      return Number(/AXV_PREVIEW_PORT=(\d+)/.exec(text)?.[1]) || undefined;
    }, 'the real guest development server');
    desktop = await launchServer({
      executable,
      cwd: owned,
      profileHome,
      dataDir,
      args: ['serve', '--addr', '127.0.0.1:0', '--data-dir', dataDir],
      env: {
        ...env,
        AXV_COMPUTE_SOCKET: socket,
        DATA_DIR: dataDir,
        ADDR: '127.0.0.1:0',
      },
    });
    const controls = `/project-previews/${project.id}`;
    const inventory = await api(controls);
    assert.equal(inventory.supported, true);
    const registration = {
      instanceId: inventory.instanceId,
      requestId: `preview-registration-${randomUUID()}`,
      binding,
      port,
    };
    preview = await api(controls, 'POST', registration, 201);
    assert.equal(
      (await api(controls, 'POST', registration, 201)).id,
      preview.id,
    );
    assert.equal(preview.state, 'ready');
    assert.notEqual(new URL(preview.url).origin, desktop.base);
    await api(
      controls,
      'POST',
      {
        ...registration,
        requestId: `preview-stale-${randomUUID()}`,
        binding: { ...binding, bootId: `${binding.bootId}-stale` },
      },
      409,
    );
    results.push({ check: 'stale-preview-binding-rejected', passed: true });

    browser = await chromium.launch({ headless: true });
    const context = await browser.newContext();
    const controlPage = await context.newPage();
    await controlPage.goto(desktop.base);
    await controlPage.evaluate((url) => {
      const link = document.createElement('a');
      link.href = url;
      link.target = '_blank';
      link.rel = 'noopener noreferrer';
      link.textContent = 'Native preview acceptance';
      document.body.append(link);
    }, preview.url);
    const opened = context.waitForEvent('page');
    await controlPage
      .getByRole('link', { name: 'Native preview acceptance' })
      .click();
    const page = await opened;
    await page.waitForFunction(
      () =>
        document.body.dataset.module === 'loaded' &&
        document.body.dataset.session === 'session-ok' &&
        document.body.dataset.hmr === 'native-hot-update',
    );
    const origin = new URL(page.url()).origin;
    const isolation = await page.evaluate(async (desktopOrigin) => {
      let denied = false;
      try {
        await fetch(`${desktopOrigin}/api/entries`);
      } catch {
        denied = true;
      }
      let domainDenied = false;
      try {
        const expectedDomain = document.domain;
        document.domain = expectedDomain;
      } catch {
        domainDenied = true;
      }
      return { denied, domainDenied, opener: window.opener === null };
    }, desktop.base);
    assert.deepEqual(isolation, {
      denied: true,
      domainDenied: true,
      opener: true,
    });
    const denied = await fetch(`${desktop.base}/api${controls}`, {
      method: 'POST',
      headers: { Origin: origin, 'Content-Type': 'application/json' },
      body: JSON.stringify({ binding, port }),
      signal: AbortSignal.timeout(10_000),
    });
    assert.equal(denied.status, 403);
    results.push({
      check: 'independent-origin-module-http-websocket-control-isolation',
      passed: true,
    });
    await api(`${controls}/${preview.id}/stop`, 'POST', {});
    await page.waitForFunction(() => document.body.dataset.closed === 'yes');
    const current = await request(taskPath());
    assert.equal(current.attempt.id, task.attempt.id);
    assert.equal(current.attempt.status, 'running');
    const revoked = await page.goto(preview.url).catch(() => null);
    assert.ok(revoked === null || revoked.status() >= 400);
    results.push({
      check: 'exact-stop-disconnects-websocket-preserves-development-task',
      passed: true,
    });
    await writeFile(
      join(owned, 'preview-acceptance.json'),
      `${JSON.stringify({ projectId: project.id, results }, null, 2)}\n`,
      { mode: 0o600 },
    );
  } catch (error) {
    failure = error;
  }
  cleaning = true;
  const errors = [];
  const cleanup = async (action) => {
    try {
      await action();
    } catch (error) {
      errors.push(error);
    }
  };
  if (browser) await cleanup(() => browser.close());
  if (desktop && preview)
    await cleanup(() =>
      api(`/project-previews/${project.id}/${preview.id}/stop`, 'POST', {}),
    );
  if (desktop) await cleanup(() => stop(desktop.child));
  // An interrupted submission may already have started the task. Recover its
  // receipt without submitting a second action before cancelling its exact
  // attempt. The caller provides a request independent of the work signal.
  if (taskSubmitted && !task)
    await cleanup(async () => {
      try {
        task = await cleanupRequest(
          `/projects/${project.id}/task-requests/${requestId}`,
        );
      } catch (error) {
        if (error.statusCode !== 404) throw error;
      }
    });
  if (task) {
    await cleanup(async () => {
      const current = await cleanupRequest(taskPath());
      assert.equal(current.attempt.id, task.attempt.id);
      if (!final(current.attempt.status))
        await cleanupRequest(
          `${taskPath()}/attempts/${task.attempt.id}/cancel`,
          'POST',
          { binding: task.attempt.binding },
        );
      await until(async () => {
        const current = await cleanupRequest(taskPath());
        assert.equal(current.attempt.id, task.attempt.id);
        return final(current.attempt.status);
      }, 'the exact development task cancellation');
    });
  }
  if (errors.length)
    throw new AggregateError(
      failure ? [failure, ...errors] : errors,
      'Native preview verification cleanup failed.',
    );
  if (failure) throw failure;
  return results;
}
