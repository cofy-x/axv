import { readFile } from 'node:fs/promises';
import { expect, test } from 'vitest';
import { validateReleaseVersion } from './package-release.mjs';

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
