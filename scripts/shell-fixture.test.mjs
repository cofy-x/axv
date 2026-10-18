import { spawn } from 'node:child_process';
import { mkdir, mkdtemp, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { createShellEnvironment } from './shell-fixture.mjs';

const directories = [];

afterEach(async () => {
  await Promise.all(
    directories
      .splice(0)
      .map((directory) => rm(directory, { recursive: true, force: true })),
  );
});

describe.skipIf(process.platform === 'win32')('shell fixture isolation', () => {
  it('keeps the real login-shell path while excluding inherited home profiles and startup hooks', async () => {
    const directory = await mkdtemp(join(tmpdir(), 'axv-shell-fixture-'));
    directories.push(directory);
    const inheritedHome = join(directory, 'inherited-home');
    const startupHook = join(directory, 'startup-hook');
    await mkdir(inheritedHome, { mode: 0o700 });
    await writeFile(
      join(inheritedHome, '.profile'),
      "printf 'INHERITED_PROFILE_LOADED\\n'\n",
    );
    await writeFile(startupHook, "printf 'INHERITED_HOOK_LOADED\\n'\n");
    const inherited = {
      ...process.env,
      HOME: inheritedHome,
      ENV: startupHook,
      BASH_ENV: startupHook,
    };
    const run = (env) =>
      new Promise((resolve, reject) => {
        const child = spawn(
          '/bin/sh',
          ['-i', '-c', 'printf "FIXTURE_HOME=%s\\n" "$HOME"'],
          {
            argv0: '-sh',
            cwd: env.HOME,
            env,
            stdio: ['ignore', 'pipe', 'pipe'],
            timeout: 5000,
          },
        );
        let stdout = '';
        let stderr = '';
        child.stdout.on('data', (data) => (stdout += data));
        child.stderr.on('data', (data) => (stderr += data));
        child.once('error', reject);
        child.once('close', (code, signal) => {
          if (code === 0) resolve({ stdout, stderr });
          else
            reject(new Error(`Shell exited with ${signal || code}: ${stderr}`));
        });
      });

    // Match the production adapter's leading-dash argv[0], proving why only
    // clearing ENV cannot isolate an interactive login shell from .profile.
    expect((await run({ ...inherited, ENV: '' })).stdout).toContain(
      'INHERITED_PROFILE_LOADED',
    );
    const environment = await createShellEnvironment(directory);
    const result = await run({ ...inherited, ...environment });
    expect(result.stdout).toBe(`FIXTURE_HOME=${environment.HOME}\n`);
    expect(environment.SHELL).toBe('/bin/sh');
    expect(environment.ENV).toBe('');
    expect(environment.BASH_ENV).toBe('');
    expect((await stat(environment.HOME)).mode & 0o777).toBe(0o700);
    expect(inherited.HOME).toBe(inheritedHome);
  });
});
