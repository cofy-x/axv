import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { captureCommand } from './lib.mjs';

const directories = [];

async function fixture() {
  const directory = await mkdtemp(join(tmpdir(), 'axv-capture-'));
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

describe.skipIf(process.platform === 'win32')('captured owned commands', () => {
  it('waits for inherited output pipes after the parent process exits', async () => {
    const directory = await fixture();
    const descendant = `setTimeout(() => {
      process.stdout.write('late stdout\\n');
      process.stderr.write('late stderr\\n');
    }, 100);`;
    const result = await captureCommand(
      process.execPath,
      [
        '-e',
        `require('node:child_process').spawn(process.execPath, ['-e', ${JSON.stringify(descendant)}], { stdio: ['ignore', 1, 2] }); process.exit(0);`,
      ],
      { cwd: directory, timeout: 5000 },
    );
    expect(result).toEqual({
      code: 0,
      signal: null,
      stdout: 'late stdout\n',
      stderr: 'late stderr\n',
    });
  });

  it('preserves UTF-8 characters split across stdout and stderr chunks', async () => {
    const directory = await fixture();
    const value = { path: join(directory, '中文文件') };
    const diagnostic = '诊断🙂';
    const program = `
      const stdout = Buffer.from(${JSON.stringify(JSON.stringify(value))});
      const stderr = Buffer.from(${JSON.stringify(diagnostic)});
      const boundary = stdout.indexOf(Buffer.from('中')) + 1;
      process.stdout.write(stdout.subarray(0, boundary));
      process.stderr.write(stderr.subarray(0, 1));
      setTimeout(() => {
        process.stdout.write(stdout.subarray(boundary));
        process.stderr.write(stderr.subarray(1));
      }, 25);
    `;
    const result = await captureCommand(process.execPath, ['-e', program], {
      cwd: directory,
    });
    expect(JSON.parse(result.stdout)).toEqual(value);
    expect(result.stderr).toBe(diagnostic);
  });

  it('fails an expired command and reaps its SIGTERM-resistant process group', async () => {
    const directory = await fixture();
    const descendant = `process.on('SIGTERM', () => {}); console.log('ready'); setInterval(() => {}, 1000);`;
    const program = `
        process.on('SIGTERM', () => {});
        const child = require('node:child_process').spawn(process.execPath, ['-e', ${JSON.stringify(descendant)}], { stdio: ['ignore', 'pipe', 'inherit'] });
        child.stdout.once('data', () => console.log(JSON.stringify({ parent: process.pid, child: child.pid })));
        setInterval(() => {}, 1000);
      `;
    let error;
    let owned;
    const cleanupErrors = [];
    try {
      try {
        await captureCommand(process.execPath, ['-e', program], {
          cwd: directory,
          timeout: 1000,
        });
      } catch (caught) {
        error = caught;
      }
      expect(error?.timedOut).toBe(true);
      expect(error.message).toContain('timed out');
      expect(error.message).toContain(process.execPath);
      expect(error.message).toContain(directory);
      owned = JSON.parse(error.stdout);
      for (const pid of [owned.parent, owned.child])
        expect(() => process.kill(pid, 0)).toThrow();
    } finally {
      for (const pid of [owned?.parent, owned?.child]) {
        if (!pid) continue;
        try {
          process.kill(pid, 'SIGKILL');
        } catch (cleanupError) {
          if (cleanupError.code !== 'ESRCH') cleanupErrors.push(cleanupError);
        }
      }
    }
    expect(cleanupErrors).toEqual([]);
  }, 10_000);

  it('reports signal exits with the command and temporary environment', async () => {
    const directory = await fixture();
    await expect(
      captureCommand(
        process.execPath,
        ['-e', "process.kill(process.pid, 'SIGKILL')"],
        {
          cwd: directory,
          env: { ...process.env, TMPDIR: directory, GOTMPDIR: directory },
          allowFailure: true,
        },
      ),
    ).rejects.toMatchObject({
      signal: 'SIGKILL',
      timedOut: false,
      message: expect.stringContaining(
        JSON.stringify({
          executable: process.execPath,
          args: ['-e', "process.kill(process.pid, 'SIGKILL')"],
          cwd: directory,
          TMPDIR: directory,
          GOTMPDIR: directory,
        }),
      ),
    });
  });

  it('returns nonzero command output only when explicitly requested', async () => {
    const directory = await fixture();
    const result = await captureCommand(
      process.execPath,
      ['-e', 'console.log(\'{"status":"failed"}\'); process.exitCode = 7;'],
      { cwd: directory, allowFailure: true },
    );
    expect(result.code).toBe(7);
    expect(JSON.parse(result.stdout)).toEqual({ status: 'failed' });
  });
});
