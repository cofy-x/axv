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
import dashboardConfig from '../apps/dashboard/vite.config.ts';
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

  it('counts every chunk containing required startup modules and their static imports without adding unrelated lazy features', () => {
    const mainModule = { renderedLength: 10, originalLength: 10 };
    const report = analyzeDashboardBundle(
      {
        entry: chunk('entry.js', ['shared.js'], {
          isEntry: true,
          dynamicImports: ['main.js'],
        }),
        main: chunk('main.js', ['dependency.js'], {
          modules: { '/repo/src/main.tsx': mainModule },
          dynamicImports: ['feature.js'],
        }),
        anotherMain: chunk('main-copy.js', ['shared.js'], {
          modules: { '/repo/src/main.tsx': mainModule },
        }),
        dependency: chunk('dependency.js', ['shared.js']),
        shared: chunk('shared.js'),
        feature: chunk('feature.js', ['feature-only.js']),
        featureOnly: chunk('feature-only.js'),
      },
      '/repo',
      ['src/main.tsx', '/repo/src/main.tsx'],
    );
    expect(report.initial.files).toEqual([
      'dependency.js',
      'entry.js',
      'main-copy.js',
      'main.js',
      'shared.js',
    ]);
    expect(report.initial.requiredStartupModules).toEqual(['src/main.tsx']);
    expect(report.initial.rawBytes).toBe(
      Buffer.byteLength(report.initial.files.join('')),
    );
    expect(() =>
      checkDashboardBudget(report, {
        rawBytes: report.initial.rawBytes - 1,
        gzipEstimateBytes: report.initial.gzipEstimateBytes,
      }),
    ).toThrow('rawBytes');
  });

  it('rejects missing required startup modules and incomplete required startup dependencies', () => {
    const bundle = {
      entry: chunk('entry.js', [], { isEntry: true }),
      main: chunk('main.js', ['missing-dependency.js'], {
        modules: { '/repo/src/main.tsx': { renderedLength: 10 } },
      }),
    };
    expect(() =>
      analyzeDashboardBundle(bundle, '/repo', ['src/missing.tsx']),
    ).toThrow(
      'Missing required startup JavaScript module: /repo/src/missing.tsx',
    );
    expect(() =>
      analyzeDashboardBundle(bundle, '/repo', ['src/main.tsx']),
    ).toThrow('Missing static JavaScript import: missing-dependency.js');
    expect(analyzeDashboardBundle(bundle, '/repo').initial.files).toEqual([
      'entry.js',
    ]);
  });

  it('reports required dynamically loaded startup code from actual emitted module identities', async () => {
    const directory = await mkdtemp(join(process.env.TMPDIR, 'startup-graph-'));
    try {
      await Promise.all(
        [
          ['index.html', '<script type="module" src="/entry.js"></script>'],
          ['entry.js', 'import("./main.js");'],
          [
            'main.js',
            'import { value } from "./shared.js"; globalThis.ready = value; globalThis.loadFeature = () => import("./feature.js");',
          ],
          [
            'feature.js',
            'import { value } from "./shared.js"; export const feature = value;',
          ],
          ['shared.js', 'export const value = Math.random();'],
        ].map(([name, content]) => writeFile(join(directory, name), content)),
      );
      const reportPath = join(directory, 'report.json');
      await build({
        configFile: false,
        root: directory,
        publicDir: false,
        logLevel: 'silent',
        plugins: [
          dashboardBundlePlugin({
            root: directory,
            requiredStartupModules: [join(directory, 'main.js')],
            reportPath,
          }),
        ],
      });
      const report = JSON.parse(await readFile(reportPath, 'utf8'));
      const containing = (id) =>
        report.chunks.find((chunk) =>
          chunk.modules.some((module) => module.id === id),
        );
      const main = containing('main.js');
      expect(main.isEntry).toBe(false);
      expect(report.initial.files).toContain(main.fileName);
      expect(report.initial.files).toContain(containing('shared.js').fileName);
      expect(report.initial.files).not.toContain(
        containing('feature.js').fileName,
      );
      expect(report.initial.requiredStartupModules).toEqual(['main.js']);
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
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

  it('scopes startup preload recovery to named loader chunks while preserving feature and HTML preloads', () => {
    const resolver = dashboardConfig.build.modulePreload.resolveDependencies;
    const dependencies = [
      'assets/main.js',
      'assets/shared.js',
      'assets/main.css',
    ];
    expect(
      resolver('assets/main.js', dependencies, {
        hostType: 'js',
        hostId: 'assets/startup-index-contenthash.js',
      }),
    ).toEqual(['assets/main.css']);
    expect(
      resolver('assets/main.js', dependencies, {
        hostType: 'js',
        hostId: 'assets/feature-contenthash.js',
      }),
    ).toEqual(['assets/shared.js', 'assets/main.css']);
    expect(
      resolver('assets/main.js', dependencies, {
        hostType: 'html',
        hostId: 'index.html',
      }),
    ).toEqual(dependencies);
  });

  it('emits startup CSS preloads without startup JavaScript dependency preloads and retains lazy feature dependencies', async () => {
    const directory = await mkdtemp(
      join(process.env.TMPDIR, 'startup-preload-'),
    );
    try {
      await Promise.all(
        [
          ['index.html', '<script type="module" src="/entry.js"></script>'],
          ['entry.js', 'import("./main.js");'],
          [
            'main.js',
            'import "./main.css"; import { startup } from "./startup-shared.js"; globalThis.ready = startup; globalThis.feature = () => import("./feature.js"); globalThis.other = () => import("./other.js");',
          ],
          ['main.css', 'body { color: #111; }'],
          [
            'feature.js',
            'import { startup } from "./startup-shared.js"; import { lazy } from "./feature-shared.js"; export const feature = startup + lazy;',
          ],
          [
            'other.js',
            'import { lazy } from "./feature-shared.js"; export const other = lazy;',
          ],
          ['startup-shared.js', 'export const startup = Math.random();'],
          ['feature-shared.js', 'export const lazy = Math.random();'],
        ].map(([name, content]) => writeFile(join(directory, name), content)),
      );
      const result = await build({
        configFile: false,
        root: directory,
        publicDir: false,
        logLevel: 'silent',
        build: {
          ...dashboardConfig.build,
          write: false,
          rolldownOptions: {
            output: {
              ...dashboardConfig.build.rolldownOptions.output,
              codeSplitting: {
                groups: [
                  { name: 'startup-shared', test: /startup-shared\.js$/ },
                  { name: 'feature-shared', test: /feature-shared\.js$/ },
                ],
              },
            },
          },
        },
        plugins: [dashboardBundlePlugin({ root: directory })],
      });
      const chunks = result.output.filter((item) => item.type === 'chunk');
      const containing = (id) =>
        chunks.find((chunk) =>
          Object.hasOwn(chunk.modules, join(directory, id)),
        );
      const entry = chunks.find((chunk) => chunk.isEntry);
      const main = containing('main.js');
      const startupShared = containing('startup-shared.js');
      const featureShared = containing('feature-shared.js');
      const css = result.output.find((item) => item.fileName.endsWith('.css'));
      expect(entry.fileName).toMatch(/^assets\/startup-/);
      expect(startupShared.fileName).not.toBe(main.fileName);
      expect(entry.code).toContain(css.fileName);
      expect(entry.code).not.toContain(startupShared.fileName);
      expect(featureShared.fileName).not.toBe(main.fileName);
      expect(main.code).toContain(featureShared.fileName);
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
