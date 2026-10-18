import { spawn } from 'node:child_process';
import { readFile, mkdtemp, rm } from 'node:fs/promises';
import { dirname, isAbsolute, join, resolve } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import { initializeTestTemp } from './test-temp.mjs';

export { testTempRoot } from './test-temp.mjs';

export const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
export const project = JSON.parse(
  await readFile(join(root, 'package.json'), 'utf8'),
)
  .name.split('/')
  .at(-1);
export const binary = join(
  root,
  'dist',
  `${project}${process.platform === 'win32' ? '.exe' : ''}`,
);
export const go = process.env.GO || 'go';
const children = new Set();
const ownedGroups = new WeakMap();
let closing = false;
let cleanup;

/** Register before any readiness wait so signals also stop startup commands. */
export function spawnChild(executable, args, options = {}) {
  if (closing) throw new Error('Cannot start a child during shutdown.');
  const { ownProcessGroup = false, ...spawnOptions } = options;
  const grouped = ownProcessGroup && process.platform !== 'win32';
  const child = spawn(executable, args, {
    ...spawnOptions,
    ...(grouped ? { detached: true } : {}),
  });
  if (grouped) ownedGroups.set(child, {});
  children.add(child);
  child.once('close', () => {
    // A compiler/test parent can exit before its descendants. Keep ownership
    // until stop() has also stopped the invocation's dedicated process group.
    if (!grouped || !child.pid) children.delete(child);
  });
  return child;
}

export async function command(executable, args, options = {}) {
  const child = spawnChild(executable, args, {
    cwd: root,
    stdio: 'inherit',
    ...options,
  });
  try {
    await new Promise((resolve, reject) => {
      child.once('error', reject);
      child.once('exit', (code, signal) =>
        code === 0
          ? resolve()
          : reject(new Error(`${executable} exited with ${signal || code}`)),
      );
    });
  } finally {
    if (ownedGroups.has(child)) await stop(child);
  }
}

/** Capture a finite command, including pipe drain and its owned descendants. */
export async function captureCommand(
  executable,
  args,
  {
    cwd = root,
    env = process.env,
    timeout = 30_000,
    allowFailure = false,
  } = {},
) {
  const child = spawnChild(executable, args, {
    cwd,
    env,
    stdio: ['ignore', 'pipe', 'pipe'],
    ownProcessGroup: true,
  });
  let stdout = '';
  let stderr = '';
  let timedOut = false;
  let timer;
  const failure = (message, result = {}, cause) =>
    Object.assign(
      new Error(
        `${message}\n${JSON.stringify({ executable, args, cwd, TMPDIR: env.TMPDIR, GOTMPDIR: env.GOTMPDIR })}\n${stderr.slice(-64 * 1024)}\n${stdout.slice(-64 * 1024)}`,
        cause ? { cause } : undefined,
      ),
      { ...result, stdout, stderr, timedOut },
    );
  child.stdout.setEncoding('utf8');
  child.stderr.setEncoding('utf8');
  child.stdout.on('data', (chunk) => (stdout += chunk));
  child.stderr.on('data', (chunk) => (stderr += chunk));
  const closed = new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('close', (code, signal) => resolve({ code, signal }));
  });
  const expired = new Promise((_, reject) => {
    timer = setTimeout(() => {
      timedOut = true;
      void stop(child).then(
        () => reject(failure(`Command timed out after ${timeout} ms.`)),
        (error) =>
          reject(failure('Timed-out command cleanup failed.', {}, error)),
      );
    }, timeout);
  });
  let result;
  let commandError;
  try {
    result = await Promise.race([closed, expired]);
  } catch (error) {
    commandError = error;
  } finally {
    clearTimeout(timer);
  }
  try {
    await stop(child);
  } catch (error) {
    throw failure('Command cleanup failed.', result, error);
  }
  if (commandError) throw commandError;
  if (timedOut) throw failure(`Command timed out after ${timeout} ms.`, result);
  if (result.signal || (!allowFailure && result.code !== 0))
    throw failure(
      `Command exited with ${result.signal || result.code}.`,
      result,
    );
  return { ...result, stdout, stderr };
}

export async function scratch(prefix = `${project}-`) {
  if (!prefix || /[/\\\0]/.test(prefix))
    throw new Error('A scratch prefix must be a nonempty filename prefix.');
  return mkdtemp(join(initializeTestTemp().root, prefix));
}

export async function stop(child) {
  const group = ownedGroups.get(child);
  if (group) {
    group.stopping ??= stopOwnedGroup(child).finally(() =>
      children.delete(child),
    );
    return group.stopping;
  }
  if (!child.pid || child.exitCode !== null || child.signalCode !== null)
    return;
  await new Promise((resolve) => {
    const timer = setTimeout(() => child.kill('SIGKILL'), 5000);
    child.once('exit', () => {
      clearTimeout(timer);
      resolve();
    });
    child.kill('SIGTERM');
  });
}

async function stopOwnedGroup(child) {
  if (!child.pid) return;
  let permissionError;

  function signalGroup(signal) {
    try {
      process.kill(-child.pid, signal);
      return true;
    } catch (error) {
      if (error.code === 'ESRCH') return false;
      if (process.platform === 'darwin' && error.code === 'EPERM') {
        // Darwin reports EPERM for a group containing only unreaped zombies.
        // Keep observing within the existing deadline; only ESRCH confirms
        // removal. A real permission failure remains an error at the deadline.
        permissionError ??= error;
        return true;
      }
      throw error;
    }
  }

  // An outer launcher allows five seconds for this wrapper to stop. Leave
  // time for hard-kill observation and the wrapper's own temporary cleanup.
  let deadline = Date.now() + 3500;
  let killed = false;
  if (!signalGroup('SIGTERM')) return;
  while (signalGroup(0)) {
    if (Date.now() >= deadline) {
      if (permissionError) throw permissionError;
      if (killed)
        throw new Error(`Owned process group ${child.pid} did not stop.`);
      if (!signalGroup('SIGKILL')) return;
      killed = true;
      deadline = Date.now() + 1000;
    }
    await delay(25);
  }
}

export async function waitFor(url, child, timeout = 30000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (child.exitCode !== null || child.signalCode !== null)
      throw new Error('Server exited before it was ready.');
    try {
      if ((await fetch(url, { signal: AbortSignal.timeout(1000) })).ok) return;
    } catch {
      /* Wait until the listener is ready. */
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`Timed out waiting for ${url}`);
}

export function dispose(dir) {
  closing = true;
  cleanup ??= (async () => {
    const results = await Promise.allSettled([...children].map(stop));
    const errors = results
      .filter((result) => result.status === 'rejected')
      .map((result) => result.reason);
    if (errors.length)
      throw new AggregateError(errors, 'Owned child cleanup failed.');
    if (dir) await rm(dir, { recursive: true, force: true });
  })();
  return cleanup;
}

/** Cleanup failures must not turn a failed or cancelled launcher into success. */
export async function exitAfterCleanup(code, dir) {
  try {
    await dispose(dir);
  } catch (error) {
    console.error('Launcher cleanup failed:', error);
    code = 1;
  }
  process.exit(code);
}

export function serverEnvironment({
  profileHome,
  dataDir,
  addr,
  env = {},
  inherited = process.env,
}) {
  if (
    !profileHome ||
    !isAbsolute(profileHome) ||
    !dataDir ||
    !isAbsolute(dataDir)
  ) {
    throw new Error(
      'A launcher must provide explicit absolute profileHome and dataDir paths.',
    );
  }
  return {
    ...inherited,
    AXV_COMPUTE_SOCKET: '',
    ADDR: addr,
    DATA_DIR: dataDir,
    ...env,
    AXV_HOME: profileHome,
  };
}

/** Resolve only after our foreground child has bound its listener, including port zero. */
export async function launchServer({
  executable = binary,
  cwd,
  profileHome,
  dataDir,
  addr = '127.0.0.1:0',
  args = ['serve'],
  env = {},
}) {
  const child = spawnChild(executable, args, {
    cwd,
    stdio: ['ignore', 'pipe', 'pipe'],
    env: serverEnvironment({ profileHome, dataDir, addr, env }),
  });
  child.stderr.pipe(process.stderr);
  try {
    const base = await new Promise((resolve, reject) => {
      let pending = '';
      const timer = setTimeout(
        () => reject(new Error('Server did not announce its listener.')),
        30_000,
      );
      const fail = (error) => {
        clearTimeout(timer);
        reject(error);
      };
      child.once('error', fail);
      child.once('exit', (code, signal) =>
        fail(new Error(`Server exited before readiness (${signal || code}).`)),
      );
      child.stdout.on('data', (chunk) => {
        process.stdout.write(chunk);
        pending += chunk.toString();
        const lines = pending.split('\n');
        pending = lines.pop() || '';
        for (const line of lines) {
          if (line.startsWith('DESKTOP_READY ')) {
            clearTimeout(timer);
            resolve(line.slice('DESKTOP_READY '.length).trim());
          }
        }
      });
    });
    await waitFor(`${base}/api/health`, child);
    return { child, base };
  } catch (error) {
    await stop(child);
    throw error;
  }
}
