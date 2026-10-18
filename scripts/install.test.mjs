import { execFile } from 'node:child_process';
import {
  chmod,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rm,
  stat,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { delimiter, dirname, join } from 'node:path';
import { promisify } from 'node:util';
import { afterEach, describe, expect, it } from 'vitest';
import {
  installationPathDiagnostics,
  installationTarget,
  publishExecutable,
  validateProductionBuild,
} from './install-lib.mjs';
import { root } from './lib.mjs';

const execute = promisify(execFile);
const directories = [];
const native = {
  GOBIN: '',
  GOPATH: '/home/developer/go',
  GOOS: 'darwin',
  GOHOSTOS: 'darwin',
  GOARCH: 'arm64',
  GOHOSTARCH: 'arm64',
};

async function temporary(prefix = 'axv-install-test-') {
  const directory = await mkdtemp(join(tmpdir(), prefix));
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

describe('Go installation destination', () => {
  it('uses effective GOBIN and otherwise the first GOPATH without losing spaces', () => {
    expect(
      installationTarget({ ...native, GOBIN: '/custom tools/bin' }, 'darwin'),
    ).toBe('/custom tools/bin/axv');
    expect(
      installationTarget(
        { ...native, GOPATH: '/first path:/second' },
        'darwin',
      ),
    ).toBe('/first path/bin/axv');
    expect(installationTarget(native, 'darwin')).toBe(
      '/home/developer/go/bin/axv',
    );
  });

  it('uses Windows Go path separators and the executable suffix', () => {
    const windows = {
      ...native,
      GOOS: 'windows',
      GOHOSTOS: 'windows',
      GOPATH: 'C:\\Go Work;D:\\Other',
    };
    expect(installationTarget(windows, 'win32')).toBe(
      'C:\\Go Work\\bin\\axv.exe',
    );
  });

  it('rejects relative or missing destinations and nonnative builds', () => {
    for (const environment of [
      { ...native, GOBIN: 'relative/bin' },
      { ...native, GOPATH: '' },
      { ...native, GOPATH: ':/another/path' },
      { ...native, GOOS: 'linux' },
      { ...native, GOARCH: 'amd64' },
      { ...native, GOHOSTOS: 'linux', GOOS: 'linux' },
    ])
      expect(() => installationTarget(environment, 'darwin')).toThrow();
  });
});

describe('production executable validation', () => {
  const production = () => ({
    Path: 'github.com/cofy-x/axv/apps/server/cmd/server',
    Settings: [
      { Key: '-tags', Value: 'production' },
      { Key: 'GOOS', Value: 'darwin' },
      { Key: 'GOARCH', Value: 'arm64' },
    ],
  });

  it('requires the AXV command, production assets and the native target', () => {
    expect(() => validateProductionBuild(production(), native)).not.toThrow();
    const otherCommand = { ...production(), Path: 'example.com/unrelated' };
    const development = {
      ...production(),
      Settings: production().Settings.slice(1),
    };
    const crossBuild = production();
    crossBuild.Settings[2].Value = 'amd64';
    for (const build of [otherCommand, development, crossBuild, {}]) {
      expect(() => validateProductionBuild(build, native)).toThrow();
    }
  });

  it.skipIf(process.env.AXV_TEST_PRODUCTION_INSTALL !== '1')(
    'repeatedly installs and executes the production bundle from a separate process without creating a profile',
    async () => {
      const directory = await temporary();
      const profile = join(directory, 'unused profile');
      const data = join(directory, 'unused data');
      const env = {
        ...process.env,
        GOBIN: join(directory, 'Go bin'),
        AXV_HOME: profile,
        DATA_DIR: data,
        AXV_COMPUTE_SOCKET: join(directory, 'unused.sock'),
      };
      const executable = join(
        env.GOBIN,
        process.platform === 'win32' ? 'axv.exe' : 'axv',
      );
      for (let attempt = 0; attempt < 6; attempt++) {
        const installed = await execute(
          process.execPath,
          [join(root, 'scripts/install.mjs')],
          { cwd: directory, env, timeout: 15_000 },
        );
        expect(installed.stdout).toContain(
          `Installed axv ${env.VERSION ?? 'dev'} at ${executable}`,
        );
        expect(
          (
            await execute(executable, ['version'], { cwd: directory, env })
          ).stdout.trim(),
        ).toBe(`axv ${env.VERSION ?? 'dev'}`);
        expect(
          (await execute(executable, ['help'], { cwd: directory, env })).stdout,
        ).toMatch(/Usage:\s+axv/);
        expect(await readdir(env.GOBIN)).toEqual([
          process.platform === 'win32' ? 'axv.exe' : 'axv',
        ]);
      }
      await expect(stat(profile)).rejects.toMatchObject({ code: 'ENOENT' });
      await expect(stat(data)).rejects.toMatchObject({ code: 'ENOENT' });
      await expect(stat(env.AXV_COMPUTE_SOCKET)).rejects.toMatchObject({
        code: 'ENOENT',
      });
    },
    30_000,
  );
});

async function installationFiles() {
  const directory = await temporary('axv install with spaces-');
  const source = join(directory, 'production bundle');
  const destination = join(
    directory,
    'Go bin',
    process.platform === 'win32' ? 'axv.exe' : 'axv',
  );
  await writeFile(source, 'new verified executable');
  await mkdir(dirname(destination));
  await writeFile(destination, 'old executable');
  return { directory, source, destination };
}

describe('atomic executable publication', () => {
  it('publishes the verified bytes, sets executable permissions and supports reinstalling', async () => {
    const files = await installationFiles();
    let verifications = 0;
    const verify = async (staged) => {
      expect(dirname(staged)).toBe(dirname(files.destination));
      expect(await readFile(staged, 'utf8')).toBe('new verified executable');
      expect(await readFile(files.destination, 'utf8')).toBe(
        verifications === 0 ? 'old executable' : 'new verified executable',
      );
      verifications++;
      return 'axv test';
    };
    for (let attempt = 0; attempt < 2; attempt++) {
      const installed = await publishExecutable({ ...files, verify });
      expect(installed.path).toBe(files.destination);
      expect(installed.version).toBe('axv test');
      expect(installed.digest).toMatch(/^[a-f0-9]{64}$/);
    }
    expect(await readFile(files.destination, 'utf8')).toBe(
      'new verified executable',
    );
    if (process.platform !== 'win32')
      expect((await stat(files.destination)).mode & 0o777).toBe(0o755);
    expect(await readdir(dirname(files.destination))).toEqual([
      process.platform === 'win32' ? 'axv.exe' : 'axv',
    ]);
  });

  it('retains the installed executable and removes only its temporary file after validation failure', async () => {
    const files = await installationFiles();
    const unrelated = join(dirname(files.destination), 'other-command');
    await writeFile(unrelated, 'keep');
    await expect(
      publishExecutable({
        ...files,
        verify: async () => {
          throw new Error('wrong build');
        },
      }),
    ).rejects.toThrow('wrong build');
    expect(await readFile(files.destination, 'utf8')).toBe('old executable');
    expect(await readFile(unrelated, 'utf8')).toBe('keep');
    expect((await readdir(dirname(files.destination))).sort()).toEqual(
      [
        process.platform === 'win32' ? 'axv.exe' : 'axv',
        'other-command',
      ].sort(),
    );
  });

  it('does not publish a staged executable changed during verification', async () => {
    const files = await installationFiles();
    await expect(
      publishExecutable({
        ...files,
        verify: async (staged) => {
          await writeFile(staged, 'changed bytes');
        },
      }),
    ).rejects.toThrow('changed during verification');
    expect(await readFile(files.destination, 'utf8')).toBe('old executable');
  });

  it('preserves a destination changed by another action during verification', async () => {
    const files = await installationFiles();
    await expect(
      publishExecutable({
        ...files,
        verify: async () => {
          await writeFile(
            files.destination,
            'new contents from another action',
          );
        },
      }),
    ).rejects.toThrow('destination changed');
    expect(await readFile(files.destination, 'utf8')).toBe(
      'new contents from another action',
    );
  });

  it('retains the existing executable when interrupted before publication', async () => {
    const files = await installationFiles();
    const controller = new AbortController();
    await expect(
      publishExecutable({
        ...files,
        signal: controller.signal,
        verify: async () => {
          controller.abort(new Error('cancelled install'));
        },
      }),
    ).rejects.toThrow('cancelled install');
    expect(await readFile(files.destination, 'utf8')).toBe('old executable');
  });

  it.skipIf(process.platform === 'win32')(
    'refuses symlink sources and destinations without changing their targets',
    async () => {
      const files = await installationFiles();
      const link = join(files.directory, 'linked-source');
      await symlink(files.source, link);
      await expect(
        publishExecutable({ ...files, source: link, verify: async () => {} }),
      ).rejects.toThrow('regular file');
      await rm(files.destination);
      await symlink(files.source, files.destination);
      await expect(
        publishExecutable({ ...files, verify: async () => {} }),
      ).rejects.toThrow('symlink');
      expect(await readFile(files.source, 'utf8')).toBe(
        'new verified executable',
      );
    },
  );
});

describe('PATH diagnostics', () => {
  it('reports a missing entry and an earlier executable without running either', async () => {
    const files = await installationFiles();
    await chmod(files.destination, 0o755);
    expect(
      await installationPathDiagnostics(
        files.destination,
        dirname(files.destination),
      ),
    ).toEqual([]);
    const shadowDirectory = join(files.directory, 'shadow');
    await mkdir(shadowDirectory);
    await writeFile(
      join(shadowDirectory, process.platform === 'win32' ? 'axv.exe' : 'axv'),
      'not executed',
      { mode: 0o755 },
    );
    const shadowed = await installationPathDiagnostics(
      files.destination,
      `${shadowDirectory}${delimiter}${dirname(files.destination)}`,
    );
    expect(shadowed).toHaveLength(1);
    expect(shadowed[0]).toContain('earlier on PATH');
    const missing = await installationPathDiagnostics(
      files.destination,
      files.directory,
    );
    expect(missing).toHaveLength(1);
    expect(missing[0]).toContain('Add ');
  });
});

it.skipIf(process.platform === 'win32')(
  'keeps dependency installation, production build and publication sequential under make -j',
  async () => {
    const directory = await temporary();
    const log = join(directory, 'order');
    const script =
      '#!/bin/sh\nprintf "%s\\n" "$* start" >> "$AXV_TEST_ORDER"\nsleep 0.03\nprintf "%s\\n" "$* done" >> "$AXV_TEST_ORDER"\n';
    for (const name of ['pnpm-fixture', 'go-fixture', 'node'])
      await writeFile(join(directory, name), script, { mode: 0o755 });
    await execute(
      'make',
      [
        '--no-print-directory',
        '-j4',
        'install',
        `PNPM=${join(directory, 'pnpm-fixture')}`,
        `GO=${join(directory, 'go-fixture')}`,
      ],
      {
        cwd: root,
        env: {
          ...process.env,
          AXV_TEST_ORDER: log,
          PATH: `${directory}${delimiter}${process.env.PATH || ''}`,
        },
        timeout: 5000,
      },
    );
    expect((await readFile(log, 'utf8')).trim().split('\n')).toEqual([
      'install --frozen-lockfile start',
      'install --frozen-lockfile done',
      'mod download start',
      'mod download done',
      'run build:production start',
      'run build:production done',
      'scripts/install.mjs start',
      'scripts/install.mjs done',
    ]);
  },
);
