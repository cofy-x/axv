import { execFile, spawn } from 'node:child_process';
import { mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { afterEach, describe, expect, it } from 'vitest';
import { stop } from './lib.mjs';

const execute = promisify(execFile);
const root = dirname(dirname(fileURLToPath(import.meta.url)));
const directories = [];

async function fixture() {
  const directory = await mkdtemp(join(tmpdir(), 'axv-build-contract-'));
  directories.push(directory);
  return directory;
}

afterEach(async () => {
  await Promise.all(
    directories
      .splice(0)
      .map((directory) => rm(directory, { recursive: true, force: true })),
  );
});

describe.skipIf(process.platform === 'win32')('Go build launcher', () => {
  it.each([false, true])(
    'make build-go keeps compiler temporary files local (override=%s)',
    async (override) => {
      const directory = await fixture();
      const executable = join(directory, 'go-fixture.cjs');
      await writeFile(
        executable,
        `#!${process.execPath}
console.log(JSON.stringify({ args: process.argv.slice(2), cwd: process.cwd(), root: process.env.AXV_TEST_TMPDIR, tmp: process.env.TMPDIR, go: process.env.GOTMPDIR }));
`,
        { mode: 0o700 },
      );
      const selectedRoot = override
        ? join(directory, 'private')
        : join(root, '.data/test');
      const child = await execute('make', ['--silent', 'build-go'], {
        cwd: root,
        env: {
          ...process.env,
          GO: executable,
          AXV_TEST_TMPDIR: override ? selectedRoot : '',
        },
      });
      const result = JSON.parse(child.stdout);
      expect(result.args).toEqual([
        'build',
        '-o',
        'dist/server-dev',
        './apps/server/cmd/server',
      ]);
      expect(result.cwd).toBe(root);
      expect(result.root).toBe(selectedRoot);
      expect(dirname(result.tmp)).toBe(selectedRoot);
      expect(result.go).toBe(result.tmp);
      await expect(stat(result.tmp)).rejects.toMatchObject({ code: 'ENOENT' });
      expect((await stat(selectedRoot)).isDirectory()).toBe(true);
    },
  );

  it.each(['cancel', 'cancel-hard', 'parent-exits'])(
    'stops only owned compiler descendants before temporary cleanup (%s)',
    async (mode) => {
      const directory = await fixture();
      const stopped = join(directory, 'compiler-stopped');
      const compiler = join(directory, 'compiler.cjs');
      const executable = join(directory, 'go-fixture.cjs');
      await writeFile(
        compiler,
        `const fs = require('node:fs');
process.on('SIGTERM', () => {
  fs.writeFileSync(${JSON.stringify(stopped)}, String(fs.existsSync(process.env.GOTMPDIR)));
  if (${JSON.stringify(mode)} !== 'cancel-hard') process.exit(0);
});
console.log('ready');
setInterval(() => {}, 1000);
`,
      );
      await writeFile(
        executable,
        `#!${process.execPath}
const { spawn } = require('node:child_process');
const child = spawn(process.execPath, [${JSON.stringify(compiler)}], { stdio: ['ignore', 'pipe', 'inherit'] });
process.on('SIGTERM', () => {});
child.stdout.once('data', () => {
  console.log(JSON.stringify({ child: child.pid, parent: process.pid, temporary: process.env.GOTMPDIR }));
  if (${JSON.stringify(mode)} === 'parent-exits') process.exit(0);
});
child.once('exit', () => process.exit(0));
`,
        { mode: 0o700 },
      );
      const wrapper = spawn(
        process.execPath,
        [join(root, 'scripts/build-go.mjs')],
        {
          cwd: directory,
          env: {
            ...process.env,
            GO: executable,
            AXV_TEST_TMPDIR: join(directory, 'temporary'),
          },
          stdio: ['ignore', 'pipe', 'pipe'],
        },
      );
      let diagnostic = '';
      wrapper.stderr.on('data', (data) => {
        diagnostic += data;
      });
      const ended = new Promise((resolve, reject) => {
        wrapper.once('error', reject);
        wrapper.once('exit', (code) => resolve(code));
      });
      let owned;
      let timeout;
      let finished = false;
      const cleanupErrors = [];
      try {
        owned = await new Promise((resolve, reject) => {
          let output = '';
          timeout = setTimeout(
            () =>
              reject(
                new Error(`Build fixture did not become ready: ${diagnostic}`),
              ),
            5000,
          );
          wrapper.once('error', reject);
          wrapper.stdout.on('data', (data) => {
            output += data;
            if (!output.includes('\n')) return;
            clearTimeout(timeout);
            resolve(JSON.parse(output.trim()));
          });
        });
        if (mode === 'cancel-hard') await stop(wrapper);
        else if (mode === 'cancel') wrapper.kill('SIGTERM');
        const code = await Promise.race([
          ended,
          new Promise((_, reject) => {
            timeout = setTimeout(
              () => reject(new Error('Build wrapper did not stop.')),
              7000,
            );
          }),
        ]);
        expect(code, diagnostic).toBe(mode === 'parent-exits' ? 0 : 143);
        expect(await readFile(stopped, 'utf8')).toBe('true');
        await expect(stat(owned.temporary)).rejects.toMatchObject({
          code: 'ENOENT',
        });
        for (const pid of [owned.parent, owned.child])
          expect(() => process.kill(pid, 0)).toThrow();
        finished = true;
      } finally {
        clearTimeout(timeout);
        if (!finished) {
          for (const pid of [owned?.child, owned?.parent, wrapper.pid]) {
            if (!pid) continue;
            try {
              process.kill(pid, 'SIGKILL');
            } catch (error) {
              if (error.code !== 'ESRCH') cleanupErrors.push(error);
            }
          }
          await ended;
        }
      }
      expect(cleanupErrors).toEqual([]);
    },
    15_000,
  );
});
