import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { resolveConfig } from 'prettier';
import { describe, expect, it } from 'vitest';

const require = createRequire(import.meta.url);
const disabled = 'Repeated extglob groups are disabled in AXV tooling.';

describe('development glob boundary', () => {
  it('keeps ordinary path selection and rejects unbounded group operators in the installed matcher', () => {
    const match = createRequire(require.resolve('vite/package.json'))(
      'picomatch',
    );

    expect(match('src/**/*.{ts,tsx}')('src/components/view.tsx')).toBe(true);
    expect(match('src/**/*.{ts,tsx}')('src/components/readme.md')).toBe(false);
    expect(() => match('+(fixture)')).toThrow(disabled);
    expect(() => match('*(fixture)')).toThrow(disabled);
  });

  it('reports the restriction through Prettier configuration resolution', async () => {
    const directory = await mkdtemp(join(tmpdir(), 'axv-toolchain-globs-'));
    try {
      const config = join(directory, '.prettierrc.json');
      await writeFile(
        config,
        JSON.stringify({
          overrides: [{ files: '*(fixture)', options: { semi: false } }],
        }),
      );
      await expect(
        resolveConfig(join(directory, 'example.js'), {
          config,
          useCache: false,
        }),
      ).rejects.toThrow(disabled);
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
