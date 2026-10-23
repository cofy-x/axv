import { describe, expect, it } from 'vitest';
import {
  access,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { join } from 'node:path';
import { build } from 'vite';
import {
  analyzeDashboardBundle,
  checkDashboardBudget,
  dashboardBundlePlugin,
} from './dashboard-bundle.mjs';

function chunk(fileName, imports = [], options = {}) {
  return {
    type: 'chunk',
    fileName,
    imports,
    dynamicImports: [],
    isEntry: false,
    code: fileName,
    modules: {},
    ...options,
  };
}

describe('dashboard startup graph budget', () => {
  it('counts shared and cyclic static imports once across entries, excluding dynamic code and assets', () => {
    const report = analyzeDashboardBundle(
      {
        main: chunk('entry.js', ['left.js', 'right.js'], {
          isEntry: true,
          dynamicImports: ['terminal.js'],
        }),
        another: chunk('another.js', ['shared.js'], { isEntry: true }),
        left: chunk('left.js', ['shared.js']),
        right: chunk('right.js', ['shared.js']),
        shared: chunk('shared.js', ['left.js']),
        terminal: chunk('terminal.js', [], { code: 'x'.repeat(10_000) }),
        css: { type: 'asset', fileName: 'style.css', source: 'style' },
      },
      '/repo',
    );
    expect(report.initial.files).toEqual([
      'another.js',
      'entry.js',
      'left.js',
      'right.js',
      'shared.js',
    ]);
    expect(report.initial.rawBytes).toBe(
      Buffer.byteLength(report.initial.files.join('')),
    );
    expect(() =>
      checkDashboardBudget(report, {
        rawBytes: report.initial.rawBytes,
        gzipEstimateBytes: report.initial.gzipEstimateBytes,
      }),
    ).not.toThrow();
    expect(() =>
      checkDashboardBudget(report, {
        rawBytes: report.initial.rawBytes - 1,
        gzipEstimateBytes: report.initial.gzipEstimateBytes,
      }),
    ).toThrow('rawBytes');
    expect(() =>
      checkDashboardBudget(report, {
        rawBytes: report.initial.rawBytes,
        gzipEstimateBytes: report.initial.gzipEstimateBytes - 1,
      }),
    ).toThrow('gzipEstimateBytes');
  });

  it('rejects incomplete graphs and invalid budgets instead of reporting a smaller total', () => {
    expect(() => analyzeDashboardBundle({}, '/repo')).toThrow('entry');
    expect(() =>
      analyzeDashboardBundle(
        { main: chunk('entry.js', ['missing.js'], { isEntry: true }) },
        '/repo',
      ),
    ).toThrow('missing.js');
    expect(() =>
      checkDashboardBudget({ initial: { rawBytes: 1 } }, { rawBytes: NaN }),
    ).toThrow('Invalid');
  });

  it('budgets final written artifacts and preserves a failure report outside the output', async () => {
    const directory = await mkdtemp(join(process.env.TMPDIR, 'bundle-budget-'));
    try {
      const outputDirectory = join(directory, 'dist');
      await mkdir(outputDirectory);
      await writeFile(join(outputDirectory, 'entry.js'), 'final artifact');
      const budgetPath = join(directory, 'budget.json');
      const reportPath = join(directory, 'report.json');
      await writeFile(
        budgetPath,
        JSON.stringify({ rawBytes: 1, gzipEstimateBytes: 100 }),
      );
      const plugin = dashboardBundlePlugin({
        root: '/repo',
        budgetPath,
        reportPath,
      });
      await expect(
        plugin.writeBundle(
          { dir: outputDirectory },
          { main: chunk('entry.js', [], { isEntry: true, code: '' }) },
        ),
      ).rejects.toThrow('rawBytes is 14 bytes');
      const report = JSON.parse(await readFile(reportPath, 'utf8'));
      expect(report.initial.rawBytes).toBe(14);
      expect(report.chunks[0].sha256).toHaveLength(64);
      for (const name of ['private-report.json', '..private/report.json']) {
        await expect(
          dashboardBundlePlugin({
            root: '/repo',
            reportPath: join(outputDirectory, name),
          }).writeBundle(
            { dir: outputDirectory },
            { main: chunk('entry.js', [], { isEntry: true }) },
          ),
        ).rejects.toThrow('outside the served UI output');
      }
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });

  it('changes actual emitted chunk fingerprints when preload policy changes and stays deterministic', async () => {
    const directory = await mkdtemp(join(process.env.TMPDIR, 'preload-hash-'));
    try {
      await Promise.all(
        [
          ['index.html', '<script type="module" src="/main.js"></script>'],
          [
            'main.js',
            'globalThis.load = () => import("./feature.js"); globalThis.other = () => import("./other.js");',
          ],
          [
            'feature.js',
            'import { value } from "./shared.js"; export const feature = value;',
          ],
          [
            'other.js',
            'import { value } from "./shared.js"; export const other = value;',
          ],
          ['shared.js', 'export const value = Math.random();'],
        ].map(([name, content]) => writeFile(join(directory, name), content)),
      );
      const identity = (_filename, dependencies) => dependencies;
      const filtered = (filename, dependencies) =>
        dependencies.filter((dependency) => dependency !== filename);
      const compile = async (resolver) => {
        const result = await build({
          configFile: false,
          root: directory,
          publicDir: false,
          logLevel: 'silent',
          build: {
            write: false,
            modulePreload: { resolveDependencies: resolver },
          },
          plugins: [dashboardBundlePlugin({ root: directory })],
        });
        return result.output
          .filter((item) => item.type === 'chunk')
          .map((item) => ({ file: item.fileName, code: item.code }));
      };
      const before = await compile(identity);
      const after = await compile(filtered);
      const repeat = await compile(filtered);
      expect(after).toEqual(repeat);
      expect(after.map((item) => item.file)).not.toEqual(
        before.map((item) => item.file),
      );
      expect(
        after.find((item) => item.code.includes('__vite__mapDeps'))?.code,
      ).not.toEqual(
        before.find((item) => item.code.includes('__vite__mapDeps'))?.code,
      );
      expect(after.some((item) => item.code.includes('__vite__mapDeps'))).toBe(
        true,
      );
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });

  it.skipIf(process.platform === 'win32')(
    'rejects report aliases before creating escaped directories or overwriting served artifacts',
    async () => {
      const directory = await mkdtemp(join(process.env.TMPDIR, 'bundle-path-'));
      const previousRoot = process.env.AXV_TEST_TMPDIR;
      const selectedRoot = join(directory, 'test-root');
      const output = join(selectedRoot, 'dist');
      const outside = join(directory, 'outside');
      const outputAlias = join(selectedRoot, 'output-alias');
      const outsideAlias = join(selectedRoot, 'outside-alias');
      const fileAlias = join(selectedRoot, 'report.json');
      const artifact = join(output, 'entry.js');
      try {
        await mkdir(output, { recursive: true, mode: 0o700 });
        await mkdir(outside, { mode: 0o700 });
        await writeFile(artifact, 'original served artifact');
        await symlink(output, outputAlias);
        await symlink(outside, outsideAlias);
        await symlink(artifact, fileAlias);
        process.env.AXV_TEST_TMPDIR = selectedRoot;
        const emit = (reportPath, outputDirectory = output) =>
          dashboardBundlePlugin({ root: directory, reportPath }).writeBundle(
            { dir: outputDirectory },
            { main: chunk('entry.js', [], { isEntry: true }) },
          );
        await expect(emit(join(outputAlias, 'report.json'))).rejects.toThrow(
          'symbolic links',
        );
        await expect(
          emit(join(outsideAlias, 'new-directory/report.json')),
        ).rejects.toThrow('symbolic links');
        await expect(emit(fileAlias)).rejects.toThrow('symbolic links');
        await expect(
          emit(join(output, 'report.json'), outputAlias),
        ).rejects.toThrow('outside the served UI output');
        await expect(access(join(output, 'report.json'))).rejects.toMatchObject(
          { code: 'ENOENT' },
        );
        await expect(
          access(join(outside, 'new-directory')),
        ).rejects.toMatchObject({ code: 'ENOENT' });
        expect(await readFile(artifact, 'utf8')).toBe(
          'original served artifact',
        );
        const safeReport = join(selectedRoot, 'reports', 'bundle.json');
        await emit(safeReport);
        expect(
          JSON.parse(await readFile(safeReport, 'utf8')).initial.rawBytes,
        ).toBe(Buffer.byteLength('original served artifact'));
      } finally {
        if (previousRoot === undefined) delete process.env.AXV_TEST_TMPDIR;
        else process.env.AXV_TEST_TMPDIR = previousRoot;
        await rm(directory, { recursive: true, force: true });
      }
    },
  );
});
