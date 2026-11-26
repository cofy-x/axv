import { readFile } from 'node:fs/promises';
import { expect, test } from 'vitest';
import { renderHomebrewFormula } from './render-homebrew-formula.mjs';

const { version } = JSON.parse(
  await readFile(new URL('../package.json', import.meta.url), 'utf8'),
);

test('renders a multi-platform Homebrew formula with exact release URLs and digests', async () => {
  const formula = await renderHomebrewFormula({
    version,
    checksums: {
      darwin_arm64: 'a'.repeat(64),
      linux_amd64: 'b'.repeat(64),
      linux_arm64: 'c'.repeat(64),
    },
  });
  expect(formula).toContain('class Axv < Formula');
  expect(formula).toContain(`axv_${version}_darwin_arm64.tar.gz`);
  expect(formula).toContain(`axv_${version}_linux_amd64.tar.gz`);
  expect(formula).toContain(`axv_${version}_linux_arm64.tar.gz`);
  expect(formula).toContain(`sha256 "${'a'.repeat(64)}"`);
  expect(formula).toContain('bin.install "axv"');
});

test('rejects missing or malformed digests', async () => {
  await expect(
    renderHomebrewFormula({ version, checksums: {} }),
  ).rejects.toThrow(/At least one/);
  await expect(
    renderHomebrewFormula({
      version,
      checksums: { darwin_arm64: 'not-a-sha256' },
    }),
  ).rejects.toThrow(/SHA-256/);
});
