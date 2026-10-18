import { execFile, spawn } from 'node:child_process';
import {
  chmod,
  mkdir,
  mkdtemp,
  readFile,
  realpath,
  rm,
  stat,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { promisify } from 'node:util';
import { afterEach, describe, expect, it } from 'vitest';
import { configureTestTemp } from './test-temp.mjs';

const execute = promisify(execFile);
const scripts = dirname(fileURLToPath(import.meta.url));
const directories = [];
const configurations = [];

async function fixture() {
  const directory = await mkdtemp(join(tmpdir(), 'axv-temp-contract-'));
  directories.push(directory);
  return realpath(directory);
}

function configure(env, cwd) {
  const result = configureTestTemp(env, cwd);
  if (result) configurations.push(result);
  return result;
}

afterEach(async () => {
  for (const configuration of configurations.splice(0)) configuration.cleanup();
  await Promise.all(
    directories
      .splice(0)
      .map((directory) => rm(directory, { recursive: true, force: true })),
  );
});

describe('configured test temporary directories', () => {
  it('uses the repository default independently of the invoking directory', async () => {
    const directory = await fixture();
    const env = { TMPDIR: '/existing/tmp', GOTMPDIR: '/existing/go' };
    const configuration = configure(env, directory);
    expect(configuration.root).toBe(join(dirname(scripts), '.data/test'));
    expect(env.AXV_TEST_TMPDIR).toBe(configuration.root);
    expect(env.TMPDIR).toBe(configuration.directory);
    expect(env.GOTMPDIR).toBe(configuration.directory);
  });

  it('keeps library-only imports inert for development and normal installation', async () => {
    const directory = await fixture();
    const program = `
      await import(${JSON.stringify(new URL('./lib.mjs', import.meta.url).href)});
      console.log(JSON.stringify({ root: process.env.AXV_TEST_TMPDIR, tmp: process.env.TMPDIR, go: process.env.GOTMPDIR }));
    `;
    const child = await execute(
      process.execPath,
      ['--input-type=module', '-e', program],
      {
        cwd: directory,
        env: {
          ...process.env,
          AXV_TEST_TMPDIR: '',
          TMPDIR: directory,
          GOTMPDIR: directory,
        },
      },
    );
    expect(JSON.parse(child.stdout)).toEqual({
      root: '',
      tmp: directory,
      go: directory,
    });
  });

  it('resolves against the invoking directory and propagates private temporary storage to child tools', async () => {
    const directory = await fixture();
    const env = { ...process.env, AXV_TEST_TMPDIR: 'private tests' };
    const configuration = configure(env, directory);
    expect(configuration.root).toBe(join(directory, 'private tests'));
    expect(dirname(configuration.directory)).toBe(configuration.root);
    expect(env.AXV_TEST_TMPDIR).toBe(configuration.root);
    expect(env.TMPDIR).toBe(configuration.directory);
    expect(env.GOTMPDIR).toBe(configuration.directory);
    expect(env.HOME).toBe(process.env.HOME);
    expect(env.CODEX_HOME).toBe(process.env.CODEX_HOME);
    expect(env.GOCACHE).toBe(process.env.GOCACHE);
    if (process.platform !== 'win32') {
      expect((await stat(configuration.root)).mode & 0o777).toBe(0o700);
      expect((await stat(configuration.directory)).mode & 0o777).toBe(0o700);
    }
    const child = await execute(
      process.execPath,
      [
        '-e',
        "console.log(JSON.stringify({ tmp: require('node:os').tmpdir(), go: process.env.GOTMPDIR, root: process.env.AXV_TEST_TMPDIR }))",
      ],
      { cwd: '/', env },
    );
    expect(JSON.parse(child.stdout)).toEqual({
      tmp: configuration.directory,
      go: configuration.directory,
      root: configuration.root,
    });
  });

  it('removes only its own temporary child and preserves peer runs and existing root data', async () => {
    const root = await fixture();
    const saved = join(root, 'saved-data');
    await writeFile(saved, 'preserved');
    const first = configure({ AXV_TEST_TMPDIR: root });
    const second = configure({ AXV_TEST_TMPDIR: root });
    expect(first.directory).not.toBe(second.directory);
    await writeFile(join(first.directory, 'temporary'), 'first');
    const peer = join(second.directory, 'temporary');
    await writeFile(peer, 'second');
    first.cleanup();
    first.cleanup();
    await expect(stat(first.directory)).rejects.toMatchObject({
      code: 'ENOENT',
    });
    expect(await readFile(peer, 'utf8')).toBe('second');
    expect(await readFile(saved, 'utf8')).toBe('preserved');
  });

  it.skipIf(process.platform === 'win32')(
    'rejects symlinks and public directories without changing them',
    async () => {
      const directory = await fixture();
      const target = join(directory, 'private');
      const alias = join(directory, 'alias');
      await mkdir(target, { mode: 0o700 });
      await symlink(target, alias);
      expect(() => configure({ AXV_TEST_TMPDIR: alias })).toThrow(
        'real, private',
      );
      await chmod(target, 0o755);
      expect(() => configure({ AXV_TEST_TMPDIR: target })).toThrow(
        'real, private',
      );
      expect((await stat(target)).mode & 0o777).toBe(0o755);
    },
  );

  it('keeps scratch profile cleanup separate from process temporary cleanup', async () => {
    const root = await fixture();
    const saved = join(root, 'saved-data');
    await writeFile(saved, 'preserved');
    const program = `
      const { scratch, dispose } = await import(${JSON.stringify(new URL('./lib.mjs', import.meta.url).href)});
      const directory = await scratch('owned-profile-');
      console.log(JSON.stringify({ directory, temporary: process.env.TMPDIR }));
      await dispose(directory);
    `;
    const child = await execute(
      process.execPath,
      ['--input-type=module', '-e', program],
      {
        env: { ...process.env, AXV_TEST_TMPDIR: root },
      },
    );
    const result = JSON.parse(child.stdout);
    expect(dirname(result.directory)).toBe(root);
    expect(dirname(result.temporary)).toBe(root);
    expect(result.directory).not.toBe(result.temporary);
    await expect(stat(result.directory)).rejects.toMatchObject({
      code: 'ENOENT',
    });
    await expect(stat(result.temporary)).rejects.toMatchObject({
      code: 'ENOENT',
    });
    expect(await readFile(saved, 'utf8')).toBe('preserved');
  });

  it.skipIf(process.platform === 'win32')(
    'passes wrapper arguments and scoped temp paths to Go before cleaning its child',
    async () => {
      const directory = await fixture();
      const executable = join(directory, 'go fixture.cjs');
      await writeFile(
        executable,
        `#!/usr/bin/env node
const fs = require('node:fs');
const path = require('node:path');
fs.writeFileSync(path.join(process.env.GOTMPDIR, 'compiled-test'), 'owned');
console.log(JSON.stringify({ args: process.argv.slice(2), cwd: process.cwd(), root: process.env.AXV_TEST_TMPDIR, tmp: process.env.TMPDIR, go: process.env.GOTMPDIR }));
`,
        { mode: 0o700 },
      );
      const child = await execute(
        process.execPath,
        [
          join(scripts, 'test-go.mjs'),
          '-run',
          '^TestExample$',
          './apps/server/internal/cli',
        ],
        {
          cwd: directory,
          env: {
            ...process.env,
            AXV_TEST_TMPDIR: 'configured',
            GO: executable,
          },
        },
      );
      const result = JSON.parse(child.stdout);
      expect(result.args).toEqual([
        'test',
        '-run',
        '^TestExample$',
        './apps/server/internal/cli',
      ]);
      expect(result.cwd).toBe(dirname(scripts));
      expect(result.root).toBe(join(directory, 'configured'));
      expect(dirname(result.tmp)).toBe(result.root);
      expect(result.go).toBe(result.tmp);
      await expect(stat(result.tmp)).rejects.toMatchObject({ code: 'ENOENT' });
      expect((await stat(result.root)).isDirectory()).toBe(true);
    },
  );

  it.skipIf(process.platform === 'win32')(
    'reports cleanup failure and fails the wrapper after its Go child exits successfully',
    async () => {
      const directory = await fixture();
      const executable = join(directory, 'go-success.cjs');
      const preload = join(directory, 'cleanup-failure.mjs');
      await writeFile(executable, '#!/usr/bin/env node\nprocess.exit(0);\n', {
        mode: 0o700,
      });
      // The child has already exited and owns no descendants. Inject only the
      // group-cleanup failure, without leaving or signalling another process.
      await writeFile(
        preload,
        `const original = process.kill.bind(process);
process.kill = (pid, signal) => {
  if (pid < 0) {
    const error = new Error('Controlled owned-group cleanup failure');
    error.code = 'EPERM';
    throw error;
  }
  return original(pid, signal);
};\n`,
      );
      let result;
      try {
        result = {
          ...(await execute(
            process.execPath,
            [
              '--import',
              pathToFileURL(preload).href,
              join(scripts, 'test-go.mjs'),
            ],
            {
              env: {
                ...process.env,
                AXV_TEST_TMPDIR: directory,
                GO: executable,
              },
              timeout: 5000,
            },
          )),
          code: 0,
        };
      } catch (error) {
        result = error;
      }
      expect(result.code).toBe(1);
      expect(result.stderr).toContain('Controlled owned-group cleanup failure');
    },
  );

  it.skipIf(process.platform === 'win32')(
    'finishes cleanup of other owned children before reporting one cleanup failure',
    async () => {
      const directory = await fixture();
      const stopped = join(directory, 'peer-stopped');
      const program = `
        const { existsSync, writeFileSync } = await import('node:fs');
        const { spawnChild, stop, dispose } = await import(${JSON.stringify(new URL('./lib.mjs', import.meta.url).href)});
        const exited = spawnChild(process.execPath, ['-e', 'process.exit(0)'], { ownProcessGroup: true });
        await new Promise((resolve) => exited.once('exit', resolve));
        const peer = spawnChild(process.execPath, ['-e', ${JSON.stringify(`
          const { writeFileSync } = require('node:fs');
          process.on('SIGTERM', () => setTimeout(() => {
            writeFileSync(${JSON.stringify(stopped)}, 'stopped');
            process.exit(0);
          }, 100));
          console.log('ready');
          setInterval(() => {}, 1000);
        `)}], { stdio: ['ignore', 'pipe', 'inherit'] });
        await new Promise((resolve) => peer.stdout.once('data', resolve));
        const originalKill = process.kill.bind(process);
        process.kill = (pid, signal) => {
          if (pid === -exited.pid) throw new Error('Controlled completed-child cleanup failure');
          return originalKill(pid, signal);
        };
        try {
          await dispose();
          throw new Error('Expected cleanup to fail');
        } catch (error) {
          console.log(JSON.stringify({ stopped: existsSync(${JSON.stringify(stopped)}), exited: peer.exitCode === 0, error: error.message }));
        } finally {
          process.kill = originalKill;
          await stop(peer);
        }
      `;
      const result = await execute(
        process.execPath,
        ['--input-type=module', '-e', program],
        { env: { ...process.env, AXV_TEST_TMPDIR: directory }, timeout: 5000 },
      );
      expect(JSON.parse(result.stdout)).toEqual({
        stopped: true,
        exited: true,
        error: 'Owned child cleanup failed.',
      });
    },
  );

  it.skipIf(process.platform !== 'darwin').each(['before', 'after'])(
    'waits for a Darwin zombie group to be reaped when exit happens %s group termination',
    async (phase) => {
      const directory = await fixture();
      const program = `
        const { once } = await import('node:events');
        const { spawnChild, stop } = await import(${JSON.stringify(new URL('./lib.mjs', import.meta.url).href)});
        const child = spawnChild(process.execPath, ['-e', "process.on('SIGTERM', () => process.exit(0)); console.log('ready'); setInterval(() => {}, 1000);"], {
          cwd: ${JSON.stringify(directory)}, ownProcessGroup: true, stdio: ['ignore', 'pipe', 'pipe'],
        });
        const closed = once(child, 'close');
        await once(child.stdout, 'data');
        const originalKill = process.kill.bind(process);
        const observed = [];
        let outcome;
        process.kill = (pid, signal) => {
          try {
            const result = originalKill(pid, signal);
            // Hold this parent's event loop so the real child can exit before
            // Node reaps it. No syscall result or error is injected.
            if (${JSON.stringify(phase)} === 'after' && pid === -child.pid && signal === 'SIGTERM')
              Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 150);
            return result;
          } catch (error) {
            observed.push({ signal, code: error.code, syscall: error.syscall });
            throw error;
          }
        };
        try {
          if (${JSON.stringify(phase)} === 'before') {
            originalKill(child.pid, 'SIGTERM');
            Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 150);
          }
          await stop(child);
          outcome = 'stopped';
        } catch (error) {
          outcome = error.code;
        } finally {
          process.kill = originalKill;
          if (child.exitCode === null && child.signalCode === null)
            originalKill(child.pid, 'SIGKILL');
          await closed;
        }
        let absent;
        try { originalKill(-child.pid, 0); } catch (error) { absent = error.code; }
        console.log(JSON.stringify({ outcome, observed, absent, exitCode: child.exitCode }));
      `;
      const result = await execute(
        process.execPath,
        ['--input-type=module', '-e', program],
        {
          cwd: directory,
          env: { ...process.env, AXV_TEST_TMPDIR: directory },
          timeout: 5000,
        },
      );
      const observed = JSON.parse(result.stdout);
      expect(observed).toMatchObject({
        outcome: 'stopped',
        absent: 'ESRCH',
        exitCode: 0,
      });
      expect(observed.observed).toContainEqual({
        signal: phase === 'before' ? 'SIGTERM' : 0,
        code: 'EPERM',
        syscall: 'kill',
      });
    },
  );

  it.skipIf(process.platform === 'win32')(
    'cancels the owned Go process group and waits for its test child before temporary cleanup',
    async () => {
      const directory = await fixture();
      const stopped = join(directory, 'test-child-stopped');
      const childScript = join(directory, 'test-child.cjs');
      const executable = join(directory, 'go fixture.cjs');
      await writeFile(
        childScript,
        `const fs = require('node:fs');
process.on('SIGTERM', () => {
  fs.writeFileSync(${JSON.stringify(stopped)}, String(fs.existsSync(process.env.GOTMPDIR)));
  process.exit(0);
});
console.log('ready');
setInterval(() => {}, 1000);
`,
      );
      await writeFile(
        executable,
        `#!/usr/bin/env node
const { spawn } = require('node:child_process');
const child = spawn(process.execPath, [${JSON.stringify(childScript)}], { stdio: ['ignore', 'pipe', 'inherit'] });
process.on('SIGTERM', () => {});
child.stdout.once('data', () => console.log(JSON.stringify({ child: child.pid, parent: process.pid, temporary: process.env.GOTMPDIR })));
child.once('exit', () => process.exit(0));
`,
        { mode: 0o700 },
      );
      const wrapper = spawn(process.execPath, [join(scripts, 'test-go.mjs')], {
        cwd: directory,
        env: { ...process.env, AXV_TEST_TMPDIR: 'configured', GO: executable },
        stdio: ['ignore', 'pipe', 'pipe'],
      });
      let diagnostic = '';
      wrapper.stderr.on('data', (data) => {
        diagnostic += data;
      });
      const ended = new Promise((resolve, reject) => {
        wrapper.once('error', reject);
        wrapper.once('exit', (code) => resolve(code));
      });
      let owned;
      let exitTimeout;
      let finished = false;
      const cleanupErrors = [];
      try {
        owned = await new Promise((resolve, reject) => {
          let output = '';
          const timeout = setTimeout(
            () => reject(new Error('Go fixture did not become ready.')),
            5000,
          );
          wrapper.once('error', (error) => {
            clearTimeout(timeout);
            reject(error);
          });
          wrapper.stdout.on('data', (data) => {
            output += data;
            if (!output.includes('\n')) return;
            clearTimeout(timeout);
            resolve(JSON.parse(output.trim()));
          });
        });
        wrapper.kill('SIGTERM');
        const code = await Promise.race([
          ended,
          new Promise((_, reject) => {
            exitTimeout = setTimeout(
              () =>
                reject(new Error('Go wrapper did not stop its process group.')),
              7000,
            );
          }),
        ]);
        expect(code, diagnostic).toBe(143);
        expect(await readFile(stopped, 'utf8')).toBe('true');
        await expect(stat(owned.temporary)).rejects.toMatchObject({
          code: 'ENOENT',
        });
        for (const pid of [owned.parent, owned.child])
          expect(() => process.kill(pid, 0)).toThrow();
        finished = true;
      } finally {
        clearTimeout(exitTimeout);
        if (!finished) {
          for (const pid of [owned?.child, owned?.parent, wrapper.pid]) {
            if (!pid) continue;
            try {
              process.kill(pid, 'SIGKILL');
            } catch (error) {
              if (error.code !== 'ESRCH') cleanupErrors.push(error);
            }
          }
        }
      }
      expect(cleanupErrors).toEqual([]);
    },
    15_000,
  );
});

describe('unit-test cache isolation', () => {
  it('overrides an inherited user cache with an invocation-owned cache', async () => {
    const directory = await fixture();
    const program = `
      const { initializeTestTemp } = await import(${JSON.stringify(new URL('./test-temp.mjs', import.meta.url).href)});
      const temporary = initializeTestTemp({ isolatedCache: true });
      console.log(JSON.stringify({ cache: process.env.AXV_CACHE_HOME, mirrors: process.env.AXV_ARTIFACT_MIRRORS, temporary: temporary.directory }));
    `;
    const child = await execute(
      process.execPath,
      ['--input-type=module', '-e', program],
      {
        env: {
          ...process.env,
          AXV_TEST_TMPDIR: directory,
          AXV_CACHE_HOME: '/must-not-use-user-cache',
          AXV_ARTIFACT_MIRRORS: 'must-not-use-inherited-mirror',
        },
      },
    );
    const result = JSON.parse(child.stdout);
    expect(result.cache).toBe(join(result.temporary, 'cache'));
    expect(result.mirrors).toBe('');
    expect(result.cache.startsWith(`${directory}/`)).toBe(true);
    // The child exit hook removes its own cache, without touching user paths.
    await expect(stat(result.temporary)).rejects.toHaveProperty(
      'code',
      'ENOENT',
    );
  });
});
