import { createHash } from 'node:crypto';
import {
  mkdir,
  mkdtemp,
  readFile,
  rename,
  rm,
  stat,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { createRequire } from 'node:module';
import { afterEach, expect, test } from 'vitest';
import {
  collectSupplementalNotices,
  findNoticeFiles,
} from './release-notices.mjs';

const directories = [];

afterEach(async () => {
  await Promise.all(
    directories
      .splice(0)
      .map((directory) => rm(directory, { recursive: true, force: true })),
  );
});

async function supplementalFixture() {
  const directory = await mkdtemp(join(tmpdir(), 'axv-supplemental-notices-'));
  directories.push(directory);
  const catalog = join(directory, 'catalog');
  const path = join(catalog, 'component/LICENSE.txt');
  const bytes = 'Fixture original license text\n';
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, bytes);
  const entry = {
    kind: 'embedded-asset',
    name: 'fixture/asset',
    version: 'snapshot-1',
    license: 'MPL-2.0',
    source: 'https://example.invalid/asset/source',
    via: { module: 'example.invalid/module', version: 'v1.2.3' },
    files: [
      {
        path: 'component/LICENSE.txt',
        sha256: createHash('sha256').update(bytes).digest('hex'),
        source: 'https://example.invalid/asset/LICENSE',
      },
    ],
    generatedSources: ['https://example.invalid/generated/source'],
  };
  await writeFile(join(catalog, 'manifest.json'), JSON.stringify([entry]));
  return {
    catalog,
    path,
    bytes,
    entry,
    output: join(directory, 'output'),
    build: { Deps: [{ Path: entry.via.module, Version: entry.via.version }] },
  };
}

test('supplemental notices retain verified original text and source acquisition metadata', async () => {
  const f = await supplementalFixture();
  const inventory = await collectSupplementalNotices(
    f.output,
    f.build,
    f.catalog,
  );
  const retained =
    'embedded-asset/fixture%2Fasset@snapshot-1/component/LICENSE.txt';
  expect(inventory).toEqual([
    {
      ...f.entry,
      files: [retained],
      licenseSources: f.entry.files,
    },
  ]);
  expect(await readFile(join(f.output, retained), 'utf8')).toBe(f.bytes);
});

test('supplemental notices ignore modules absent from the actual binary', async () => {
  const f = await supplementalFixture();
  await rm(f.path);
  await expect(
    collectSupplementalNotices(f.output, { Deps: [] }, f.catalog),
  ).resolves.toEqual([]);
  await expect(stat(f.output)).rejects.toMatchObject({ code: 'ENOENT' });
});

test('supplemental notices reject a newly linked module version until its assets are reviewed', async () => {
  const f = await supplementalFixture();
  f.build.Deps[0].Version = 'v1.2.4';
  await expect(
    collectSupplementalNotices(f.output, f.build, f.catalog),
  ).rejects.toThrow(/Re-review embedded assets/);
  await expect(stat(f.output)).rejects.toMatchObject({ code: 'ENOENT' });
});

test.each(['changed', 'empty', 'missing'])(
  'supplemental notices reject %s license material before retaining it',
  async (failure) => {
    const f = await supplementalFixture();
    if (failure === 'missing') await rm(f.path);
    else await writeFile(f.path, failure === 'empty' ? '' : 'changed text');
    await expect(
      collectSupplementalNotices(f.output, f.build, f.catalog),
    ).rejects.toThrow();
    await expect(stat(f.output)).rejects.toMatchObject({ code: 'ENOENT' });
  },
);

test.skipIf(process.platform === 'win32').each(['file', 'directory'])(
  'supplemental notices reject a symlinked %s even when its original bytes match',
  async (kind) => {
    const f = await supplementalFixture();
    const original = kind === 'file' ? f.path : dirname(f.path);
    const moved = `${original}-original`;
    await rename(original, moved);
    await symlink(moved, original, kind === 'file' ? 'file' : 'dir');
    await expect(
      collectSupplementalNotices(f.output, f.build, f.catalog),
    ).rejects.toThrow(/not symlinks/);
    await expect(stat(f.output)).rejects.toMatchObject({ code: 'ENOENT' });
  },
);

test('supplemental notices reject paths outside the catalog', async () => {
  const f = await supplementalFixture();
  f.entry.files[0].path = '../LICENSE.txt';
  await writeFile(join(f.catalog, 'manifest.json'), JSON.stringify([f.entry]));
  await expect(
    collectSupplementalNotices(f.output, f.build, f.catalog),
  ).rejects.toThrow(/inside their catalog/);
});

test('license discovery retains the separate upstream terms linked by Rolldown', async () => {
  const require = createRequire(import.meta.url);
  const vite = require.resolve('vite/package.json');
  const directory = dirname(
    createRequire(vite).resolve('rolldown/package.json'),
  );
  const license = await readFile(join(directory, 'LICENSE'), 'utf8');
  const reference = license.match(/\[here\]\([^)]*\/([^/)]+)\)/)?.[1];
  expect(reference, 'the upstream LICENSE points to its bundled terms').toBe(
    'THIRD-PARTY-LICENSE',
  );
  const files = await findNoticeFiles(directory);
  expect(files).toContain(join(directory, reference));
  expect(await readFile(join(directory, reference), 'utf8')).toContain(
    'Copyright (c) 2020 Evan Wallace',
  );
});

test('Vite ships its bundled dependency notices inside the retained license file', async () => {
  const require = createRequire(import.meta.url);
  const directory = dirname(require.resolve('vite/package.json'));
  const files = await findNoticeFiles(directory);
  expect(files).toContain(join(directory, 'LICENSE.md'));
  expect(await readFile(join(directory, 'LICENSE.md'), 'utf8')).toContain(
    '# Licenses of bundled dependencies',
  );
});
