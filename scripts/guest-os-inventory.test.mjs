import { createHash } from 'node:crypto';
import {
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, expect, test } from 'vitest';
import { captureCommand, root } from './lib.mjs';

const directories = [];
const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');
const sourceText = `Types: deb
URIs: https://archive.ubuntu.com/ubuntu/
Suites: noble noble-updates
Components: main
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
`;

afterEach(async () => {
  await Promise.all(
    directories
      .splice(0)
      .map((directory) => rm(directory, { recursive: true, force: true })),
  );
});

async function fixture() {
  const directory = await mkdtemp(join(tmpdir(), 'axv-os-inventory-'));
  directories.push(directory);
  const output = join(directory, 'usr/share/doc/axv-guest');
  const database = join(directory, 'var/lib/dpkg');
  for (const path of [
    'etc/apt/sources.list.d',
    'etc/apt/auth.conf.d',
    'etc/apt/apt.conf.d',
    'var/lib/apt/lists/partial',
    'var/lib/dpkg',
    'usr/share/doc/fixture',
    'usr/share/doc/fixture/LICENSES',
    'usr/share/common-licenses',
    'usr/share/doc/axv-guest',
  ])
    await mkdir(join(directory, path), { recursive: true });
  await writeFile(
    join(directory, 'etc/apt/sources.list.d/ubuntu.sources'),
    sourceText,
  );
  await writeFile(
    join(directory, 'etc/apt/auth.conf.d/private.conf'),
    'machine private.invalid login owner password secret-fixture\n',
  );
  await writeFile(
    join(directory, 'etc/apt/apt.conf.d/proxy'),
    'Acquire::https::Proxy "https://owner:secret-fixture@proxy.invalid";\n',
  );
  await writeFile(
    join(directory, 'var/lib/apt/lists/repo_InRelease'),
    'signed index\n',
  );
  await writeFile(join(directory, 'var/lib/apt/lists/lock'), '');
  await writeFile(join(directory, 'etc/os-release'), 'ID=ubuntu\n');
  await writeFile(
    join(directory, 'usr/share/doc/fixture/copyright'),
    'See /usr/share/common-licenses/GPL-2\n',
  );
  await writeFile(
    join(directory, 'usr/share/doc/fixture/NOTICE'),
    'Additional original notice\n',
  );
  await writeFile(
    join(directory, 'usr/share/doc/fixture/LICENSES/MIT.txt'),
    'Additional original license\n',
  );
  await writeFile(
    join(directory, 'usr/share/common-licenses/GPL-2'),
    'Original license\n',
  );
  await symlink('fixture', join(directory, 'usr/share/doc/fixture-alias'));
  await writeFile(
    join(database, 'status'),
    `Package: fixture-alias
Status: install ok installed
Architecture: amd64
Multi-Arch: same
Version: 2.1-2
Source: fixture-source (2.1-1)
Maintainer: Fixture <fixture@example.invalid>
Description: Fixture package

`,
  );
  const env = { ...process.env, DPKG_ROOT: directory, DPKG_ADMINDIR: database };
  return {
    directory,
    output,
    database,
    env,
    distribution: join(output, 'os-distribution'),
    async collect(extra = {}) {
      return captureCommand(
        'sh',
        [join(root, 'images/guest/package-inventory.sh'), output],
        {
          allowFailure: true,
          env: { ...env, ...extra },
        },
      );
    },
  };
}

async function pausedCollection(f) {
  const bin = join(f.directory, 'bin');
  await mkdir(bin);
  await writeFile(join(bin, 'package.json'), '{"type":"commonjs"}\n');
  const started = join(f.directory, 'query-started');
  const resume = join(f.directory, 'query-resume');
  const executable = join(bin, 'dpkg-query');
  const original = (
    await captureCommand('sh', ['-c', 'command -v dpkg-query'])
  ).stdout.trim();
  await writeFile(
    executable,
    `#!${process.execPath}
const fs = require('node:fs');
const { spawnSync } = require('node:child_process');
fs.writeFileSync(process.env.AXV_OS_TEST_STARTED, 'started');
const wait = new Int32Array(new SharedArrayBuffer(4));
while (!fs.existsSync(process.env.AXV_OS_TEST_RESUME)) Atomics.wait(wait, 0, 0, 20);
const result = spawnSync(process.env.AXV_OS_TEST_DPKG, process.argv.slice(2), { stdio: 'inherit' });
process.exit(result.status ?? 1);
`,
    { mode: 0o755 },
  );
  const result = f.collect({
    PATH: `${bin}:${process.env.PATH}`,
    AXV_OS_TEST_STARTED: started,
    AXV_OS_TEST_RESUME: resume,
    AXV_OS_TEST_DPKG: original,
  });
  let completed;
  void result.then(
    (value) => (completed = value),
    (error) => (completed = error),
  );
  const deadline = Date.now() + 2000;
  while (Date.now() < deadline) {
    try {
      await readFile(started);
      return {
        result,
        async resume() {
          await writeFile(resume, 'resume');
          return result;
        },
      };
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
      if (completed)
        throw new Error(
          `Inventory query exited before pausing: ${completed.stderr ?? completed}`,
          { cause: error },
        );
      await new Promise((resolve) => setTimeout(resolve, 20));
    }
  }
  await writeFile(resume, 'resume');
  await result;
  throw new Error('Timed out waiting for the inventory query.');
}

describe.runIf(process.platform === 'linux')('guest OS review inputs', () => {
  test('retains exact public sources, index identity, safe notice links and pending source versions', async () => {
    const f = await fixture();
    const result = await f.collect();
    expect(result.code, result.stderr).toBe(0);
    expect(
      JSON.parse(
        await readFile(join(f.distribution, 'apt-sources.json'), 'utf8'),
      ),
    ).toEqual([
      {
        path: '/etc/apt/sources.list.d/ubuntu.sources',
        bytes: Buffer.byteLength(sourceText),
        sha256: hash(sourceText),
        content: sourceText,
      },
    ]);
    const indexes = JSON.parse(
      await readFile(join(f.distribution, 'apt-indexes.json'), 'utf8'),
    );
    expect(indexes).toEqual([
      {
        path: '/var/lib/apt/lists/repo_InRelease',
        bytes: 13,
        sha256: hash('signed index\n'),
      },
    ]);
    const notices = JSON.parse(
      await readFile(join(f.distribution, 'notices.json'), 'utf8'),
    );
    expect(notices.map(({ path }) => path)).toContain(
      '/usr/share/doc/fixture/NOTICE',
    );
    expect(notices.map(({ path }) => path)).toContain(
      '/usr/share/doc/fixture/LICENSES/MIT.txt',
    );
    const alias = notices.find(
      ({ path }) => path === '/usr/share/doc/fixture-alias/copyright',
    );
    expect(alias.sha256).toBe(hash('See /usr/share/common-licenses/GPL-2\n'));
    expect((await lstat(join(f.distribution, alias.file))).isFile()).toBe(true);
    const review = JSON.parse(
      await readFile(join(f.distribution, 'source-review.json'), 'utf8'),
    );
    expect(review.reviewRequired).toBe(true);
    expect(review.sourcePackages).toEqual([
      {
        package: 'fixture-source',
        version: '2.1-1',
        binaryPackages: ['fixture-alias:amd64'],
        reviewStatus: 'pending',
      },
    ]);
    const sums = await readFile(join(f.distribution, 'SHA256SUMS'), 'utf8');
    expect(sums).not.toContain('auth.conf');
    expect(sums).not.toContain('proxy');
    for (const line of sums.trim().split('\n')) {
      const [digest, path] = line.split('  ');
      const bytes = await readFile(join(f.distribution, path));
      expect(hash(bytes)).toBe(digest);
      expect(bytes.toString()).not.toContain('secret-fixture');
    }
    const second = await f.collect();
    expect(second.code).toBe(1);
    expect(second.stderr).toContain('Refusing to overwrite');
    expect(await readFile(join(f.distribution, 'SHA256SUMS'), 'utf8')).toBe(
      sums,
    );
  });

  test.each([
    [
      'compact deb822',
      'Types:deb\nURIs:https://archive.ubuntu.com/ubuntu/\nSuites:noble\nComponents:main\nArchitectures:amd64\n',
    ],
    [
      'continued deb822 URIs',
      'Types: deb\nURIs:\n https://archive.ubuntu.com/ubuntu/\n https://security.ubuntu.com/ubuntu/\nSuites: noble\nComponents: main\n',
    ],
    [
      'one-line with options',
      'deb [arch=amd64 signed-by=/usr/share/keyrings/ubuntu-archive-keyring.gpg] https://archive.ubuntu.com/ubuntu/ noble main # retained comment\n',
    ],
  ])('retains valid %s source syntax', async (name, text) => {
    const f = await fixture();
    const source = join(f.directory, 'etc/apt/sources.list.d/ubuntu.sources');
    await rm(source);
    await writeFile(
      name.startsWith('one-line')
        ? source.replace('.sources', '.list')
        : source,
      text,
    );
    const result = await f.collect();
    expect(result.code, result.stderr).toBe(0);
    const sources = JSON.parse(
      await readFile(join(f.distribution, 'apt-sources.json'), 'utf8'),
    );
    expect(sources[0].content).toBe(text);
    expect((await readdir(f.output)).sort()).toEqual([
      'os-distribution',
      'os-packages.tsv',
      'os-release',
    ]);
  });

  test('a rejected repeat preserves the entire existing snapshot', async () => {
    const f = await fixture();
    expect((await f.collect()).code).toBe(0);
    const packages = await readFile(join(f.output, 'os-packages.tsv'));
    const os = await readFile(join(f.output, 'os-release'));
    const sums = await readFile(join(f.distribution, 'SHA256SUMS'));
    const database = join(f.database, 'status');
    await writeFile(
      database,
      (await readFile(database, 'utf8')).replace('2.1-2', '9.0-1'),
    );
    await writeFile(join(f.directory, 'etc/os-release'), 'ID=changed\n');
    const result = await f.collect();
    expect(result.code).toBe(1);
    expect(result.stderr).toContain('Refusing to overwrite');
    expect(await readFile(join(f.output, 'os-packages.tsv'))).toEqual(packages);
    expect(await readFile(join(f.output, 'os-release'))).toEqual(os);
    expect(await readFile(join(f.distribution, 'SHA256SUMS'))).toEqual(sums);
    expect((await readdir(f.output)).sort()).toEqual([
      'os-distribution',
      'os-packages.tsv',
      'os-release',
    ]);
  });

  test.each(['os-packages.tsv', 'os-release', 'os-distribution'])(
    'rejects an existing or dangling %s link without touching its target',
    async (name) => {
      const f = await fixture();
      const sentinel = join(f.directory, 'sentinel');
      await writeFile(sentinel, 'preserve');
      const path = join(f.output, name);
      await symlink(sentinel, path);
      expect((await f.collect()).code).toBe(1);
      expect(await readFile(sentinel, 'utf8')).toBe('preserve');
      expect(await readdir(f.output)).toEqual([name]);
      await rm(sentinel);
      expect((await f.collect()).code).toBe(1);
      expect((await lstat(path)).isSymbolicLink()).toBe(true);
      expect(await readdir(f.output)).toEqual([name]);
    },
  );

  test('an existing claim belongs to its owner and is preserved', async () => {
    const f = await fixture();
    const lock = join(f.output, '.os-inventory.lock');
    await mkdir(lock);
    await writeFile(join(lock, 'owner'), 'preserve');
    const result = await f.collect();
    expect(result.code).toBe(1);
    expect(result.stderr).toContain('Another OS inventory collection owns');
    expect(await readFile(join(lock, 'owner'), 'utf8')).toBe('preserve');
    expect(await readdir(f.output)).toEqual(['.os-inventory.lock']);
  });

  test('concurrent collection cannot delete or publish over the first owner', async () => {
    const f = await fixture();
    const first = await pausedCollection(f);
    try {
      const second = await f.collect();
      expect(second.code).toBe(1);
      expect(second.stderr).toContain('Another OS inventory collection owns');
      expect(
        (await readdir(f.output)).some((name) =>
          name.startsWith('.os-inventory-'),
        ),
      ).toBe(true);
    } finally {
      const result = await first.resume();
      expect(result.code, result.stderr).toBe(0);
    }
    expect((await readdir(f.output)).sort()).toEqual([
      'os-distribution',
      'os-packages.tsv',
      'os-release',
    ]);
  });

  test('preserves an output created during collection and removes only its own staging', async () => {
    const f = await fixture();
    const first = await pausedCollection(f);
    let result;
    try {
      await mkdir(f.distribution);
      await writeFile(join(f.distribution, 'owner'), 'preserve');
    } finally {
      result = await first.resume();
    }
    expect(result.code).toBe(1);
    expect(result.stderr).toContain('Refusing to overwrite');
    expect(await readFile(join(f.distribution, 'owner'), 'utf8')).toBe(
      'preserve',
    );
    expect(await readdir(f.output)).toEqual(['os-distribution']);
  });

  test.each([
    [
      'credentials',
      'URIs: https://owner:password@archive.ubuntu.com/ubuntu/\n',
      /credential-free/,
    ],
    [
      'query token',
      'URIs: https://archive.ubuntu.com/ubuntu/?token=value\n',
      /credentials|credential-free/,
    ],
    [
      'private key',
      'Signed-By:\n -----BEGIN OPENSSH PRIVATE KEY-----\n private\n',
      /private credentials/,
    ],
    [
      'PGP private key',
      'Signed-By:\n -----BEGIN PGP PRIVATE KEY BLOCK-----\n private\n',
      /private credentials/,
    ],
    [
      'credentials in a comment',
      sourceText + '# mirror https://owner:pass@private.invalid/ubuntu/\n',
      /credential-free/,
    ],
    [
      'APT auth syntax',
      'machine private.invalid login owner password secret-fixture\n',
      /private credentials/,
    ],
    ['local source', 'URIs: file:/private/packages\n', /credential-free/],
  ])(
    'rejects %s instead of exporting private source configuration',
    async (_, text, message) => {
      const f = await fixture();
      await writeFile(
        join(f.directory, 'etc/apt/sources.list.d/ubuntu.sources'),
        text,
      );
      const result = await f.collect();
      expect(result.code).toBe(1);
      expect(result.stderr).toMatch(message);
      await expect(readdir(f.distribution)).rejects.toMatchObject({
        code: 'ENOENT',
      });
      expect(await readdir(f.output)).toEqual([]);
    },
  );

  test('rejects a source symlink to credentials without copying it', async () => {
    const f = await fixture();
    const path = join(f.directory, 'etc/apt/sources.list.d/ubuntu.sources');
    await rm(path);
    await symlink('../auth.conf.d/private.conf', path);
    const result = await f.collect();
    expect(result.code).toBe(1);
    expect(result.stderr).toContain('must not be a symlink');
    await expect(readdir(f.distribution)).rejects.toMatchObject({
      code: 'ENOENT',
    });
  });

  test('retains a comment-only sources.list alongside the active deb822 file', async () => {
    const f = await fixture();
    const comment = '# Ubuntu sources moved to sources.list.d/ubuntu.sources\n';
    await writeFile(join(f.directory, 'etc/apt/sources.list'), comment);
    const result = await f.collect();
    expect(result.code, result.stderr).toBe(0);
    const sources = JSON.parse(
      await readFile(join(f.distribution, 'apt-sources.json'), 'utf8'),
    );
    expect(
      sources.find(({ path }) => path === '/etc/apt/sources.list').content,
    ).toBe(comment);
  });

  test('skips ordinary documentation directory links outside the notice areas', async () => {
    const f = await fixture();
    await mkdir(join(f.directory, 'usr/share/git-core/contrib/hooks'), {
      recursive: true,
    });
    await writeFile(
      join(f.directory, 'usr/share/git-core/contrib/hooks/NOTICE'),
      'Outside the selected notice areas\n',
    );
    await mkdir(join(f.directory, 'usr/share/doc/fixture/contrib'));
    await symlink(
      '../../../git-core/contrib/hooks',
      join(f.directory, 'usr/share/doc/fixture/contrib/hooks'),
    );
    expect(
      await readFile(
        join(f.directory, 'usr/share/doc/fixture/contrib/hooks/NOTICE'),
        'utf8',
      ),
    ).toBe('Outside the selected notice areas\n');
    const result = await f.collect();
    expect(result.code, result.stderr).toBe(0);
    const notices = JSON.parse(
      await readFile(join(f.distribution, 'notices.json'), 'utf8'),
    );
    expect(notices.some(({ path }) => path.includes('contrib/hooks'))).toBe(
      false,
    );
  });

  test('rejects a selected license directory link outside the notice areas', async () => {
    const f = await fixture();
    await rm(join(f.directory, 'usr/share/doc/fixture/LICENSES'), {
      recursive: true,
    });
    await mkdir(join(f.directory, 'usr/share/private-licenses'));
    await symlink(
      '../../private-licenses',
      join(f.directory, 'usr/share/doc/fixture/LICENSES'),
    );
    const result = await f.collect();
    expect(result.code).toBe(1);
    expect(result.stderr).toContain('escapes its allowed directory');
    await expect(readdir(f.distribution)).rejects.toMatchObject({
      code: 'ENOENT',
    });
  });

  test.each(['copyright', 'package inventory', 'index', 'escape link'])(
    'fails with an actionable error for missing or unsafe %s',
    async (failure) => {
      const f = await fixture();
      if (failure === 'copyright')
        await rm(join(f.directory, 'usr/share/doc/fixture/copyright'));
      else if (failure === 'package inventory')
        await writeFile(join(f.database, 'status'), '');
      else if (failure === 'index')
        await rm(join(f.directory, 'var/lib/apt/lists/repo_InRelease'));
      else {
        await rm(join(f.directory, 'usr/share/doc/fixture/NOTICE'));
        await symlink(
          '../../../../etc/apt/auth.conf.d/private.conf',
          join(f.directory, 'usr/share/doc/fixture/NOTICE'),
        );
      }
      const result = await f.collect();
      expect(result.code).toBe(1);
      expect(result.stderr).toMatch(
        /copyright file|package inventory is empty|APT index identities|escapes/,
      );
      await expect(readdir(f.distribution)).rejects.toMatchObject({
        code: 'ENOENT',
      });
    },
  );
});
