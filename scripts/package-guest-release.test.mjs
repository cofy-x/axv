import { createHash } from 'node:crypto';
import {
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rm,
  symlink,
  truncate,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { delimiter, join } from 'node:path';
import { expect, test } from 'vitest';
import {
  verifyGuestBundle,
  verifyGuestReleaseAsset,
  packageGuestRelease,
  snapshotGuestBundle,
} from './package-guest-release.mjs';
import { captureCommand, root } from './lib.mjs';
import { initializeTestTemp } from './test-temp.mjs';

const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');
const json = (value) => `${JSON.stringify(value)}\n`;

async function fixture() {
  const directory = await mkdtemp(join(tmpdir(), 'axv-guest-package-'));
  const guest = {
    imageVersion: 1,
    protocolVersion: 3,
    capabilities: [
      'exec',
      'terminals',
      'ssh',
      'network',
      'project-files',
      'project-tasks',
      'project-previews',
      'project-restore',
      'project-initialization',
    ],
    binarySHA256: 'b'.repeat(64),
  };
  const image = {
    version: 1,
    id: 'axv-test',
    name: 'AXV test',
    provider: 'firecracker',
    arch: 'amd64',
    guest,
    rootfs: { file: 'root.ext4', sha256: hash('unused filesystem fixture') },
  };
  const files = new Map([
    ['manifest.json', json(image)],
    ['root.ext4', 'unused filesystem fixture'],
    ['base-image.txt', `ubuntu:24.04@sha256:${'a'.repeat(64)}\n`],
    ['distribution/axv-guest/LICENSE', 'fixture license'],
    ['distribution/axv-guest/THIRD_PARTY_NOTICES.md', 'fixture notices'],
    ['distribution/axv-guest/os-packages.tsv', 'fixture packages'],
    ['distribution/axv-guest/os-release', 'ID=ubuntu\n'],
    ['distribution/axv-guest/os-distribution/apt-sources.json', '[]\n'],
    ['distribution/axv-guest/os-distribution/apt-indexes.json', '[]\n'],
    ['distribution/axv-guest/os-distribution/notices.json', '[]\n'],
    ['distribution/axv-guest/os-distribution/source-review.json', '{}\n'],
    ['distribution/axv-guest/os-distribution/SHA256SUMS', 'fixture sums\n'],
    ['distribution/axv-guest/licenses/manifest.json', '[]\n'],
    [
      'distribution/axv-guest/licenses/go/github.com%2Fexample%2Fmodule@v1.0.0/LICENSE',
      'fixture upstream license',
    ],
    [
      'distribution/axv-guest/build-info.json',
      json({
        image: { provider: 'firecracker', arch: 'amd64', guest },
        binary: { sha256: guest.binarySHA256 },
        build: {
          Path: 'github.com/cofy-x/axv/apps/server/cmd/guest',
          GoVersion: `go${(await readFile(join(root, '.go-version'), 'utf8')).trim()}`,
          Settings: [
            { Key: 'GOOS', Value: 'linux' },
            { Key: 'GOARCH', Value: 'amd64' },
            { Key: 'CGO_ENABLED', Value: '0' },
            { Key: 'vcs.modified', Value: 'true' },
          ],
        },
      }),
    ],
  ]);
  for (const [path, contents] of files) {
    await mkdir(join(directory, path, '..'), { recursive: true });
    await writeFile(join(directory, path), contents);
  }
  const sums = [...files]
    .map(([path, contents]) => `${hash(contents)}  ${path}\n`)
    .join('');
  await writeFile(join(directory, 'SHA256SUMS'), sums);
  return { directory, sums };
}

async function replaceBundleFile(directory, path, contents) {
  await writeFile(join(directory, path), contents);
  const checksumFile = join(directory, 'SHA256SUMS');
  const lines = (await readFile(checksumFile, 'utf8')).trimEnd().split('\n');
  await writeFile(
    checksumFile,
    `${lines.map((line) => (line.slice(66) === path ? `${hash(contents)}  ${path}` : line)).join('\n')}\n`,
  );
}

async function candidateDirectories() {
  return (await readdir(initializeTestTemp().root))
    .filter((name) => name.startsWith('guest-candidate-'))
    .sort();
}

test.each([
  [2147483647, true],
  [2147483648, false],
  [2147483649, false],
])('release asset checks sparse logical length %i', async (bytes, accepted) => {
  const directory = await mkdtemp(join(tmpdir(), 'axv-guest-size-'));
  try {
    const path = join(directory, 'sparse.ext4');
    await writeFile(path, '');
    await truncate(path, bytes);
    const state = await lstat(path);
    expect(state.size).toBe(bytes);
    if (typeof state.blocks === 'number')
      expect(state.blocks * 512).toBeLessThan(bytes);
    if (accepted)
      await expect(verifyGuestReleaseAsset(path)).resolves.toBe(bytes);
    else
      await expect(verifyGuestReleaseAsset(path)).rejects.toThrow(
        /smaller than 2 GiB/,
      );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('release asset checks reject empty files, directories and symlinks', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'axv-guest-size-'));
  try {
    const path = join(directory, 'empty');
    await writeFile(path, '');
    await expect(verifyGuestReleaseAsset(path)).rejects.toThrow(/nonempty/);
    await expect(verifyGuestReleaseAsset(directory)).rejects.toThrow(/regular/);
    await writeFile(path, 'asset');
    const alias = join(directory, 'alias');
    await symlink(path, alias);
    await expect(verifyGuestReleaseAsset(alias)).rejects.toThrow(/regular/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('an oversized raw disk fails before checksum work or candidate creation, including local packaging', async () => {
  const { directory } = await fixture();
  try {
    const before = await candidateDirectories();
    // Leave the old digest in place: the size failure must precede hashing.
    await truncate(join(directory, 'root.ext4'), 2147483648);
    await expect(verifyGuestBundle(directory)).rejects.toThrow(
      /smaller than 2 GiB/,
    );
    await expect(
      packageGuestRelease({
        bundle: directory,
        version: '0.1.0-rc.1',
        local: true,
      }),
    ).rejects.toThrow(/smaller than 2 GiB/);
    expect(await candidateDirectories()).toEqual(before);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('release validation uses the import contract and rejects ambiguous build settings', async () => {
  const { directory } = await fixture();
  try {
    const manifest = JSON.parse(
      await readFile(join(directory, 'manifest.json'), 'utf8'),
    );
    const original = json(manifest);
    manifest.guest.capabilities.push(manifest.guest.capabilities[0]);
    await replaceBundleFile(directory, 'manifest.json', json(manifest));
    await expect(verifyGuestBundle(directory)).rejects.toThrow(
      /unique lowercase/,
    );
    await replaceBundleFile(directory, 'manifest.json', original);
    const path = 'distribution/axv-guest/build-info.json';
    const info = JSON.parse(await readFile(join(directory, path), 'utf8'));
    info.build.Settings.push({ Key: 'CGO_ENABLED', Value: '0' });
    await replaceBundleFile(directory, path, json(info));
    await expect(verifyGuestBundle(directory)).rejects.toThrow(/unique string/);
    info.build.Settings.pop();
    info.build.Path = 'github.com/cofy-x/axv/apps/server/cmd/server';
    await replaceBundleFile(directory, path, json(info));
    await expect(verifyGuestBundle(directory)).rejects.toThrow(
      /matching Linux guest/,
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('guest packaging verifies every distribution file and rootfs metadata', async () => {
  const { directory } = await fixture();
  try {
    const result = await verifyGuestBundle(directory);
    expect(result.image.provider).toBe('firecracker');
    await writeFile(join(directory, 'root.ext4'), 'tampered');
    await expect(verifyGuestBundle(directory)).rejects.toThrow(
      /checksum mismatch/,
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test.each([
  'apt-sources.json',
  'apt-indexes.json',
  'notices.json',
  'source-review.json',
  'SHA256SUMS',
])('guest packaging requires OS distribution material %s', async (material) => {
  const { directory, sums } = await fixture();
  try {
    const path = `distribution/axv-guest/os-distribution/${material}`;
    await rm(join(directory, path));
    await writeFile(
      join(directory, 'SHA256SUMS'),
      sums
        .split('\n')
        .filter((line) => line.slice(66) !== path)
        .join('\n'),
    );
    await expect(verifyGuestBundle(directory)).rejects.toThrow(
      `Missing guest distribution material: os-distribution/${material}`,
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('guest packaging rejects unlisted private files, unsafe sums, and symlinks', async () => {
  const { directory, sums } = await fixture();
  try {
    await writeFile(join(directory, 'private-token'), 'secret');
    await expect(verifyGuestBundle(directory)).rejects.toThrow(
      /covered exactly once/,
    );
    await rm(join(directory, 'private-token'));
    await writeFile(
      join(directory, 'SHA256SUMS'),
      `${sums}${'f'.repeat(64)}  ../escape\n`,
    );
    await expect(verifyGuestBundle(directory)).rejects.toThrow(/unsafe/);
    await writeFile(join(directory, 'SHA256SUMS'), sums);
    await symlink(join(directory, 'root.ext4'), join(directory, 'alias'));
    await expect(verifyGuestBundle(directory)).rejects.toThrow(/symlink/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('only explicitly local candidates accept dirty sources and archives retain the full bundle', async () => {
  const { directory } = await fixture();
  let output;
  try {
    await expect(
      packageGuestRelease({ bundle: directory, version: '0.1.0-rc.1' }),
    ).rejects.toThrow(/clean|VCS|vcs/);
    output = await packageGuestRelease({
      bundle: directory,
      version: '0.1.0-rc.1',
      local: true,
    });
    const candidate = JSON.parse(
      await readFile(join(output, 'candidate.json'), 'utf8'),
    );
    expect(candidate.localOnly).toBe(true);
    await captureCommand('sha256sum', ['--check', 'SHA256SUMS'], {
      cwd: output,
    });
    const extracted = join(directory, 'extracted');
    await mkdir(extracted);
    await captureCommand('tar', [
      '-xzf',
      join(output, candidate.archive),
      '-C',
      extracted,
    ]);
    expect(
      await readFile(join(extracted, candidate.image.rootfs.file)),
    ).toEqual(await readFile(join(output, candidate.disk)));
    const listing = (
      await captureCommand('tar', ['-tzf', join(output, candidate.archive)])
    ).stdout;
    expect(listing).toContain('distribution/axv-guest/licenses/manifest.json');
    expect(listing).toContain('manifest.json');
  } finally {
    await rm(directory, { recursive: true, force: true });
    if (output) await rm(join(output, '..'), { recursive: true, force: true });
  }
});

test('assembly rejects a new coherent bundle generation instead of mixing its archive and disk', async () => {
  const { directory } = await fixture();
  try {
    const verified = await verifyGuestBundle(directory);
    const rootfs = join(directory, 'root.ext4');
    const changed = 'a different coherent filesystem';
    await writeFile(rootfs, changed);
    const manifestPath = join(directory, 'manifest.json');
    const manifest = JSON.parse(await readFile(manifestPath, 'utf8'));
    manifest.rootfs.sha256 = hash(changed);
    await writeFile(manifestPath, json(manifest));
    await writeFile(
      join(directory, 'SHA256SUMS'),
      [...verified.sums]
        .filter(([path]) => path !== 'SHA256SUMS')
        .map(
          ([path, digest]) =>
            `${path === 'root.ext4' ? hash(changed) : path === 'manifest.json' ? hash(json(manifest)) : digest}  ${path}\n`,
        )
        .join(''),
    );
    await expect(verifyGuestBundle(directory)).resolves.toBeDefined();
    await expect(
      snapshotGuestBundle(directory, join(directory, 'snapshot'), verified),
    ).rejects.toThrow(/changed during assembly/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('snapshot refuses a raw disk that grew after verification before copying it', async () => {
  const { directory } = await fixture();
  try {
    const verified = await verifyGuestBundle(directory);
    await truncate(join(directory, 'root.ext4'), 2147483648);
    const destination = join(directory, 'snapshot');
    await expect(
      snapshotGuestBundle(directory, destination, verified),
    ).rejects.toThrow(/smaller than 2 GiB/);
    await expect(lstat(destination)).rejects.toMatchObject({ code: 'ENOENT' });
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test.skipIf(process.platform === 'win32')(
  'an oversized assembled archive fails before hashing and removes its local candidate',
  async () => {
    const { directory } = await fixture();
    const bin = await mkdtemp(join(tmpdir(), 'axv-guest-tar-'));
    const originalPath = process.env.PATH;
    try {
      const before = await candidateDirectories();
      // Exercise the real assembly failure path with a sparse tar output.
      await writeFile(
        join(bin, 'tar'),
        `#!${process.execPath}
import * as fs from 'node:fs';
const output = fs.openSync(process.argv[3], 'wx');
fs.ftruncateSync(output, 2147483648);
fs.closeSync(output);
`,
        { mode: 0o700 },
      );
      process.env.PATH = `${bin}${delimiter}${originalPath ?? ''}`;
      await expect(
        packageGuestRelease({
          bundle: directory,
          version: '0.1.0-rc.1',
          local: true,
        }),
      ).rejects.toThrow(/smaller than 2 GiB/);
      expect(await candidateDirectories()).toEqual(before);
    } finally {
      if (originalPath === undefined) delete process.env.PATH;
      else process.env.PATH = originalPath;
      await rm(directory, { recursive: true, force: true });
      await rm(bin, { recursive: true, force: true });
    }
  },
);
