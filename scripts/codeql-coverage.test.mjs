import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { captureCommand, root } from './lib.mjs';
import {
  coverageReport,
  inventoryFiles,
  parsePackages,
} from './codeql-coverage.mjs';

const server = 'apps/server/cmd/server/main.go';
const guest = 'apps/server/cmd/guest/main.go';
const production = 'apps/server/internal/webui/production.go';
const directories = [];

afterEach(async () => {
  await Promise.all(
    directories
      .splice(0)
      .map((path) => rm(path, { recursive: true, force: true })),
  );
});

describe('CodeQL extraction coverage', () => {
  it('parses Go package objects without confusing nested data or braces in strings', () => {
    const packages = [
      {
        Dir: '/workspace/}{/server',
        GoFiles: ['main.go'],
        Module: { Go: '1' },
      },
      { Dir: '/workspace/guest', GoFiles: ['main.go'] },
    ];
    expect(
      parsePackages(
        packages.map((entry) => JSON.stringify(entry, null, '\t')).join('\n') +
          '\n',
      ),
    ).toEqual(packages);
    expect(() => parsePackages('{"invalid":')).toThrow();
  });

  it('records the selected workspace files and identifies tests and conditional exclusions', () => {
    const inventory = inventoryFiles(
      [
        { Dir: join(root, 'apps/server/cmd/server'), GoFiles: ['main.go'] },
        { Dir: join(root, 'apps/server/cmd/guest'), GoFiles: ['main.go'] },
        {
          Dir: join(root, 'apps/server/internal/webui'),
          GoFiles: ['production.go'],
        },
      ],
      [
        production,
        server,
        guest,
        'apps/server/internal/webui/development.go',
        'apps/server/cmd/server/main_test.go',
        'apps/server/go.mod',
        '',
      ],
    );
    expect(inventory.expected).toEqual([guest, server, production]);
    expect(inventory.excluded).toEqual([
      {
        path: 'apps/server/cmd/server/main_test.go',
        reason: 'Go test files are excluded from extraction',
      },
      {
        path: 'apps/server/internal/webui/development.go',
        reason: 'Not selected by the platform and production build tags',
      },
    ]);
  });

  it('fails completeness when a command or production source was not extracted', () => {
    const inventory = { expected: [guest, server, production], excluded: [] };
    const report = coverageReport(inventory, {
      '#select': { tuples: [[server], [server]] },
    });
    expect(report.complete).toBe(false);
    expect(report.missing).toEqual([guest, production]);
    expect(report.extracted).toEqual([server]);
  });

  it('permits additional extracted files without weakening the selected-file requirement', () => {
    const report = coverageReport(
      { expected: [server, production], excluded: [] },
      { '#select': { tuples: [[production], [guest], [server]] } },
    );
    expect(report.complete).toBe(true);
    expect(report.missing).toEqual([]);
  });

  it.each([
    {},
    { '#select': { tuples: [['apps/server/../main.go']] } },
    { '#select': { tuples: [[server, 'unexpected column']] } },
    { '#select': { tuples: [[42]] } },
  ])(
    'rejects malformed extraction results instead of reporting success',
    (decoded) => {
      expect(() => coverageReport({ expected: [server] }, decoded)).toThrow(
        'invalid extracted-file result',
      );
    },
  );

  it('rejects empty inventories and files outside the workspace owner', () => {
    expect(() => inventoryFiles([], [])).toThrow('valid AXV server');
    expect(() =>
      inventoryFiles(
        [{ Dir: join(root, 'outside'), GoFiles: ['main.go'] }],
        [],
      ),
    ).toThrow('valid AXV server');
    expect(() =>
      coverageReport({ expected: [] }, { '#select': { tuples: [] } }),
    ).toThrow('saved Go source inventory is invalid');
  });

  it.skipIf(process.platform === 'win32')(
    'retains missing-file evidence while making the real verification command fail',
    async () => {
      const directory = await mkdtemp(join(tmpdir(), 'axv-codeql-'));
      directories.push(directory);
      await writeFile(
        join(directory, 'inventory.json'),
        JSON.stringify({ expected: [server, guest, production], excluded: [] }),
      );
      const codeql = join(directory, 'codeql');
      await writeFile(
        codeql,
        `#!${process.execPath}\nif (process.argv[2] === 'bqrs') console.log(${JSON.stringify(JSON.stringify({ '#select': { tuples: [[server]] } }))});\n`,
        { mode: 0o700 },
      );
      const result = await captureCommand(
        process.execPath,
        [
          join(root, 'scripts/codeql-coverage.mjs'),
          'verify',
          directory,
          codeql,
          join(directory, 'database'),
        ],
        { allowFailure: true },
      );
      expect(result.code).toBe(1);
      expect(result.stderr).toContain('CodeQL missed 2 selected Go files');
      const report = JSON.parse(
        await readFile(join(directory, 'coverage.json'), 'utf8'),
      );
      expect(report.complete).toBe(false);
      expect(report.missing).toEqual([guest, production]);
    },
  );
});
