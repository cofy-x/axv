import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { expect, test } from 'vitest';
import { captureCommand } from './lib.mjs';
import {
  readReleaseSource,
  validateReleaseSource,
  validateReleaseVersion,
} from './package-release.mjs';

const { version } = JSON.parse(
  await readFile(new URL('../package.json', import.meta.url), 'utf8'),
);

test('release versions allow stable and prerelease builds of the manifest version', async () => {
  await expect(validateReleaseVersion(version)).resolves.toBe(version);
  await expect(validateReleaseVersion(`${version}-rc.1`)).resolves.toBe(
    `${version}-rc.1`,
  );
});

test('release versions reject unsafe names, invalid semantic versions and a different source version', async () => {
  for (const value of [
    undefined,
    '',
    'dev',
    `v${version}`,
    `../${version}`,
    `${version}/other`,
    `${version}\n`,
    `${version}-rc.1\n`,
    `${version};echo injected`,
    `${version}-rc.01`,
    '01.0.0',
    '999999.0.0',
  ])
    await expect(validateReleaseVersion(value)).rejects.toThrow();
});

const revision = 'a'.repeat(40);
const source = { revision, status: '' };
const buildInfo = {
  Settings: [
    { Key: 'vcs', Value: 'git' },
    { Key: 'vcs.revision', Value: revision },
    { Key: 'vcs.modified', Value: 'false' },
  ],
};

test('release source requires the clean binary and checkout to identify the same commit', () => {
  expect(() => validateReleaseSource(buildInfo, source)).not.toThrow();
  for (const invalid of [
    { ...source, status: ' M go.work.sum\n' },
    { ...source, status: 'M  tracked-source.go\n' },
    { ...source, status: '?? untracked-source.go\n' },
    { ...source, revision: '' },
    { ...source, revision: 'b'.repeat(40) },
  ])
    expect(() => validateReleaseSource(buildInfo, invalid)).toThrow();
});

test('release source rejects disabled, incomplete, dirty or ambiguous binary VCS metadata', () => {
  const invalid = [
    {},
    { Settings: [] },
    ...['vcs', 'vcs.revision', 'vcs.modified'].map((key) => ({
      Settings: buildInfo.Settings.filter((entry) => entry.Key !== key),
    })),
    ...['true', '', undefined].map((value) => ({
      Settings: buildInfo.Settings.map((entry) =>
        entry.Key === 'vcs.modified' ? { ...entry, Value: value } : entry,
      ),
    })),
    {
      Settings: [...buildInfo.Settings, { Key: 'vcs.modified', Value: 'true' }],
    },
  ];
  for (const value of invalid)
    expect(() => validateReleaseSource(value, source)).toThrow();
});

test('release checkout inspection permits ignored output and detects tracked, staged and untracked source', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'axv-release-source-'));
  const git = (args) => captureCommand('git', args, { cwd: directory });
  try {
    const hooks = join(directory, 'empty-hooks');
    await mkdir(hooks);
    await git(['init', '--quiet']);
    await writeFile(join(directory, '.gitignore'), 'dist/\nempty-hooks/\n');
    await writeFile(join(directory, 'source.txt'), 'reviewed source\n');
    await git(['add', '.gitignore', 'source.txt']);
    // Commit only this invocation-owned fixture repository, with no user hooks.
    await git([
      '-c',
      'user.name=AXV release test',
      '-c',
      'user.email=release-test@example.invalid',
      '-c',
      'commit.gpgsign=false',
      '-c',
      `core.hooksPath=${hooks}`,
      'commit',
      '--quiet',
      '-m',
      'Release source fixture',
    ]);
    const initial = await readReleaseSource(directory);
    expect(initial.status).toBe('');
    expect(initial.revision).toMatch(/^[0-9a-f]{40,64}$/);
    const fixtureBuild = {
      Settings: buildInfo.Settings.map((entry) =>
        entry.Key === 'vcs.revision'
          ? { ...entry, Value: initial.revision }
          : entry,
      ),
    };
    await mkdir(join(directory, 'dist'));
    await writeFile(join(directory, 'dist', 'axv'), 'ignored build output');
    expect(() => validateReleaseSource(fixtureBuild, initial)).not.toThrow();
    expect((await readReleaseSource(directory)).status).toBe('');

    await writeFile(join(directory, 'untracked.txt'), 'unreviewed source\n');
    const untracked = await readReleaseSource(directory);
    expect(untracked.status).toContain('?? untracked.txt');
    expect(() => validateReleaseSource(fixtureBuild, untracked)).toThrow(
      /clean Git checkout/,
    );
    await rm(join(directory, 'untracked.txt'));

    await writeFile(join(directory, 'source.txt'), 'unreviewed edit\n');
    const modified = await readReleaseSource(directory);
    expect(modified.status).toContain(' M source.txt');
    expect(() => validateReleaseSource(fixtureBuild, modified)).toThrow(
      /clean Git checkout/,
    );
    await git(['add', 'source.txt']);
    const staged = await readReleaseSource(directory);
    expect(staged.status).toContain('M  source.txt');
    expect(() => validateReleaseSource(fixtureBuild, staged)).toThrow(
      /clean Git checkout/,
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
