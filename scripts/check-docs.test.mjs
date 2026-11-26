import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  checkMarkdownLinks,
  collectHeadings,
  slugifyHeading,
} from './check-docs.mjs';

function withTempWorkspace(run) {
  const dir = mkdtempSync(join(tmpdir(), 'axv-check-docs-'));
  try {
    return run(dir);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

describe('check-docs', () => {
  it('slugifies headings with punctuation and duplicates like GitHub', () => {
    expect(slugifyHeading('Download a release binary (Linux & macOS)')).toBe(
      'download-a-release-binary-linux--macos',
    );
    const headings = collectHeadings(
      [
        '# Title',
        '## Setup',
        '```md',
        '## Ignored In Fence',
        '```',
        '## Setup',
      ].join('\n'),
    );
    expect([...headings]).toEqual(['title', 'setup', 'setup-1']);
  });

  it('detects missing files, broken anchors, broken code refs and orphan docs', () => {
    withTempWorkspace((dir) => {
      mkdirSync(join(dir, 'docs/api'), { recursive: true });
      mkdirSync(join(dir, 'apps/server'), { recursive: true });

      writeFileSync(
        join(dir, 'README.md'),
        [
          '# Root',
          '[ok](docs/guide.md#valid-section)',
          '[bad-file](docs/missing.md)',
          '[bad-anchor](docs/guide.md#nope)',
          '[bad-self](#missing-self)',
          '```md',
          '[ignored](docs/missing.md)',
          '```',
        ].join('\n'),
      );
      writeFileSync(
        join(dir, 'docs/README.md'),
        '# Map\n\n- [Guide](guide.md)\n',
      );
      writeFileSync(
        join(dir, 'docs/guide.md'),
        '# Guide\n\n## Valid section\n',
      );
      writeFileSync(join(dir, 'docs/orphan.md'), '# Unindexed\n');
      writeFileSync(join(dir, 'docs/api/README.md'), '# API\n');
      writeFileSync(join(dir, 'docs/api/unlisted.md'), '# Unlisted API\n');
      writeFileSync(
        join(dir, 'apps/server/help.go'),
        [
          'package server',
          'const ok = "see docs/guide.md#valid-section."',
          'const badAnchor = "see docs/guide.md#removed-anchor"',
          'const badFile = "https://github.com/cofy-x/axv/blob/main/docs/gone.md"',
        ].join('\n'),
      );
      writeFileSync(
        join(dir, 'apps/server/help_test.go'),
        'package server\nconst ignored = "docs/not-real.md#nope"\n',
      );

      const result = checkMarkdownLinks(dir);

      expect(result.fileCount).toBe(6);
      expect(result.errors).toEqual([
        'README.md:3 missing path docs/missing.md',
        'README.md:4 broken anchor docs/guide.md#nope',
        'README.md:5 broken self-anchor #missing-self',
        'apps/server/help.go:3 broken doc anchor docs/guide.md#removed-anchor',
        'apps/server/help.go:4 missing doc docs/gone.md',
        'orphan doc not indexed in docs/README.md: docs/orphan.md',
        'orphan API doc not indexed in docs/api/README.md: docs/api/unlisted.md',
      ]);
    });
  });

  it('passes on the repository documentation with zero issues', () => {
    const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
    const result = checkMarkdownLinks(repoRoot);
    expect(result.fileCount).toBeGreaterThan(0);
    expect(result.errors).toEqual([]);
  });
});
