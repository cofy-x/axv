import { readFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { createRequire } from 'node:module';
import { expect, test } from 'vitest';
import { findNoticeFiles } from './release-notices.mjs';

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
