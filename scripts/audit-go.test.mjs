import { execFile, spawn } from 'node:child_process';
import {
  mkdir,
  mkdtemp,
  readFile,
  rm,
  stat,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { delimiter, dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { afterEach, describe, expect, it } from 'vitest';
import { stop } from './lib.mjs';

const execute = promisify(execFile);
const root = dirname(dirname(fileURLToPath(import.meta.url)));
const directories = [];
const children = [];

afterEach(async () => {
  await Promise.all(children.splice(0).map(stop));
  await Promise.all(
    directories
      .splice(0)
      .map((directory) => rm(directory, { recursive: true, force: true })),
  );
});

async function fixture(mode) {
  const directory = await mkdtemp(join(tmpdir(), 'axv-audit-contract-'));
  directories.push(directory);
  await writeFile(join(directory, 'package.json'), '{"type":"commonjs"}');
  const selected = join(directory, 'selected Go');
  const poison = join(directory, 'other-go');
  const observation = join(directory, 'observation.json');
  const stopped = join(directory, 'descendant-stopped');
  await mkdir(join(selected, 'bin'), { recursive: true, mode: 0o700 });
  await mkdir(poison, { mode: 0o700 });
  await writeFile(
    join(poison, 'go'),
    '#!/bin/sh\necho wrong-go >&2\nexit 77\n',
    {
      mode: 0o700,
    },
  );
  const scanner = `#!${process.execPath}
const fs = require('node:fs');
const { spawn, spawnSync } = require('node:child_process');
const selected = spawnSync('go', ['version'], { encoding: 'utf8' });
if (selected.status !== 0) throw new Error(selected.stderr);
fs.writeFileSync(${JSON.stringify(observation)}, JSON.stringify({
  go: selected.stdout.trim(), cwd: process.cwd(),
  args: process.argv.slice(2), cgo: process.env.CGO_ENABLED,
  temporary: process.env.GOTMPDIR, tmp: process.env.TMPDIR,
  toolchain: process.env.GOTOOLCHAIN, goversion: process.env.GOVERSION ?? null,
}));
if (${JSON.stringify(mode)} === 'scan-failure') {
  console.error('Synthetic reachable vulnerability.');
  process.exit(3);
}
if (${JSON.stringify(mode)} === 'scan-signal') process.kill(process.pid, 'SIGTERM');
if (${JSON.stringify(mode)} === 'cancel') {
  process.on('SIGTERM', () => {});
  const child = spawn(process.execPath, ['-e', ${JSON.stringify(`
    const fs = require('node:fs');
    process.on('SIGTERM', () => {
      fs.writeFileSync(${JSON.stringify(stopped)}, String(fs.existsSync(process.env.GOTMPDIR)));
      process.exit(0);
    });
    console.log('SCAN_READY');
    setInterval(() => {}, 1000);
  `)}], { stdio: 'inherit' });
  child.once('exit', () => process.exit(0));
}
`;
  const executable = join(selected, 'bin/go');
  await writeFile(
    executable,
    `#!${process.execPath}
const fs = require('node:fs');
const path = require('node:path');
const args = process.argv.slice(2);
if (args[0] === 'env') console.log(${JSON.stringify(selected)});
else if (args[0] === 'version') console.log('selected-go');
else if (args[0] === 'install') {
  if (${JSON.stringify(mode)} === 'install-failure') {
    console.error('Synthetic scanner download failure.');
    process.exit(23);
  }
  fs.writeFileSync(path.join(process.env.GOBIN, 'govulncheck'), ${JSON.stringify(scanner)}, { mode: 0o700 });
} else process.exit(99);
`,
    { mode: 0o700 },
  );
  return {
    observation,
    stopped,
    env: {
      ...process.env,
      GO: executable,
      PATH: `${poison}${delimiter}${process.env.PATH || ''}`,
      GOBIN: join(directory, 'unrelated-installation'),
      GOVERSION: 'go1.99.0',
      CGO_ENABLED: '1',
      AXV_TEST_TMPDIR: join(directory, 'temporary'),
    },
  };
}

describe.skipIf(process.platform === 'win32')(
  'Go vulnerability launcher',
  () => {
    it('scans with the selected Go and removes only its private temporary tool', async () => {
      const { env, observation } = await fixture('success');
      await execute(process.execPath, [join(root, 'scripts/audit-go.mjs')], {
        env,
      });
      const result = JSON.parse(await readFile(observation, 'utf8'));
      expect(result.go).toBe('selected-go');
      expect(result.cwd).toBe(join(root, 'apps/server'));
      expect(result.args).toEqual([
        '-test',
        '-tags',
        'production,ts_omit_webclient',
        './...',
      ]);
      expect(result.cgo).toBe('0');
      expect(result.toolchain).toBe('local');
      expect(result.goversion).toBeNull();
      expect(result.tmp).toBe(result.temporary);
      expect(dirname(result.temporary)).toBe(env.AXV_TEST_TMPDIR);
      await expect(stat(result.temporary)).rejects.toMatchObject({
        code: 'ENOENT',
      });
      await expect(stat(env.GOBIN)).rejects.toMatchObject({ code: 'ENOENT' });
    });

    it.each([
      ['install-failure', 'Synthetic scanner download failure.'],
      ['scan-failure', 'Synthetic reachable vulnerability.'],
      ['scan-signal', 'SIGTERM'],
    ])('keeps %s visible as a failed check', async (mode, diagnostic) => {
      const { env, observation } = await fixture(mode);
      const error = await execute(
        process.execPath,
        [join(root, 'scripts/audit-go.mjs')],
        { env },
      ).catch((error) => error);
      expect(error.code).toBe(1);
      expect(error.stderr).toContain(diagnostic);
      if (mode === 'install-failure')
        await expect(stat(observation)).rejects.toMatchObject({
          code: 'ENOENT',
        });
      else {
        const { temporary } = JSON.parse(await readFile(observation, 'utf8'));
        await expect(stat(temporary)).rejects.toMatchObject({ code: 'ENOENT' });
      }
    });

    it('waits for owned scanner descendants before removing compiler temporary files', async () => {
      const { env, observation, stopped } = await fixture('cancel');
      const wrapper = spawn(
        process.execPath,
        [join(root, 'scripts/audit-go.mjs')],
        { env, stdio: ['ignore', 'pipe', 'pipe'] },
      );
      children.push(wrapper);
      let output = '';
      const ended = new Promise((resolve, reject) => {
        wrapper.once('error', reject);
        wrapper.once('close', (code) => resolve(code));
      });
      await new Promise((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error(output)), 10_000);
        wrapper.stdout.on('data', (data) => {
          output += data;
          if (output.includes('SCAN_READY')) {
            clearTimeout(timer);
            resolve();
          }
        });
        wrapper.stderr.on('data', (data) => (output += data));
        ended.then(() => {
          clearTimeout(timer);
          reject(new Error(`Scanner exited before cancellation: ${output}`));
        }, reject);
      });
      const { temporary } = JSON.parse(await readFile(observation, 'utf8'));
      wrapper.kill('SIGTERM');
      expect(await ended).not.toBe(0);
      expect(await readFile(stopped, 'utf8')).toBe('true');
      await expect(stat(temporary)).rejects.toMatchObject({ code: 'ENOENT' });
    });
  },
);
