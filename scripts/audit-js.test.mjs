import { cp, mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { describe, expect, it, vi } from 'vitest';
import { assessAudit } from './audit-js.mjs';
import { root } from './lib.mjs';
import { verifyGlobPatches } from './verify-glob-patches.mjs';

const advisory = {
  github_advisory_id: 'GHSA-fc5q-39xc-vjpf',
  module_name: 'picomatch',
  severity: 'high',
  vulnerable_versions: '<=4.0.7',
  patched_versions: null,
  findings: [
    {
      version: '4.0.7',
      dev: true,
      optional: false,
      bundled: false,
      paths: ['.>vite>picomatch', 'packages__nextstep>vitest>picomatch'],
    },
  ],
};

function report(advisories, result = {}) {
  const vulnerabilities = {
    info: 0,
    low: 0,
    moderate: 0,
    high: 0,
    critical: 0,
  };
  for (const entry of advisories) vulnerabilities[entry.severity]++;
  return {
    stdout: JSON.stringify({
      advisories: Object.fromEntries(advisories.map((entry, i) => [i, entry])),
      metadata: { vulnerabilities },
    }),
    stderr: '',
    code: advisories.length ? 1 : 0,
    ...result,
  };
}

describe('JavaScript dependency audit', () => {
  it('accepts a complete clean report without invoking a mitigation', async () => {
    const verify = vi.fn();
    await expect(assessAudit(report([]), verify)).resolves.toBeUndefined();
    expect(verify).not.toHaveBeenCalled();
  });

  it('requires proof for every path in the reviewed development finding', async () => {
    const verify = vi.fn(() => ({ paths: 2, copies: 1 }));
    await expect(assessAudit(report([advisory]), verify)).resolves.toEqual({
      advisoryId: advisory.github_advisory_id,
      paths: 2,
      copies: 1,
    });
    expect(verify).toHaveBeenCalledExactlyOnceWith(advisory.findings[0].paths);
  });

  it('keeps unrelated findings blocking even when the reviewed finding is present', async () => {
    const verify = vi.fn();
    const unknown = {
      github_advisory_id: 'GHSA-aaaa-bbbb-cccc',
      module_name: 'another-package',
      severity: 'low',
    };

    await expect(
      assessAudit(report([advisory, unknown]), verify),
    ).rejects.toThrow('Unresolved');
    expect(verify).not.toHaveBeenCalled();
  });

  it.each([
    [
      'production scope',
      { findings: [{ ...advisory.findings[0], dev: false }] },
    ],
    [
      'additional version',
      {
        findings: [
          ...advisory.findings,
          { ...advisory.findings[0], version: '4.0.6' },
        ],
      },
    ],
    ['available upstream fix', { patched_versions: '>=4.0.8' }],
    ['different affected range', { vulnerable_versions: '<=5.0.0' }],
  ])('requires a new review for %s', async (_label, fields) => {
    const verify = vi.fn();
    await expect(
      assessAudit(report([{ ...advisory, ...fields }]), verify),
    ).rejects.toThrow('Unresolved');
    expect(verify).not.toHaveBeenCalled();
  });

  it.each([
    [
      'registry failure',
      report([advisory], { stderr: 'Registry unavailable' }),
    ],
    ['unexpected exit', report([advisory], { code: 2 })],
    ['unexplained failure', report([], { code: 1 })],
    ['incomplete report', report([], { stdout: '{}' })],
    ['invalid JSON', report([], { stdout: 'Scan interrupted' })],
  ])('rejects %s before inspecting the local patch', async (_label, result) => {
    const verify = vi.fn();
    await expect(assessAudit(result, verify)).rejects.toThrow();
    expect(verify).not.toHaveBeenCalled();
  });
});

async function writeManifest(dir, name, devDependencies = {}) {
  await mkdir(dir, { recursive: true });
  await writeFile(
    join(dir, 'package.json'),
    JSON.stringify({ name, devDependencies }),
  );
}

describe('installed glob patches', () => {
  it('checks each resolved copy, the bundled copy and the tracked patch', async () => {
    const checkout = await mkdtemp(join(tmpdir(), 'axv-audit-globs-'));
    const require = createRequire(join(root, 'package.json'));
    const pico = dirname(
      createRequire(require.resolve('vite/package.json')).resolve(
        'picomatch/package.json',
      ),
    );
    const prettier = dirname(require.resolve('prettier/package.json'));

    try {
      const tool = join(checkout, 'node_modules/vite');
      const child = join(checkout, 'packages/fixture');
      const second = join(child, 'node_modules/picomatch');
      const bundle = join(checkout, 'node_modules/prettier');
      const tracked = join(checkout, 'patches/picomatch@4.0.7.patch');
      await writeManifest(checkout, 'fixture', { vite: '*', prettier: '*' });
      await writeManifest(tool, 'vite');
      await writeFile(join(tool, 'index.js'), '');
      await writeManifest(child, 'second-fixture', { picomatch: '*' });
      await mkdir(join(checkout, 'patches'));
      await Promise.all([
        cp(pico, join(tool, 'node_modules/picomatch'), { recursive: true }),
        cp(pico, second, { recursive: true }),
        cp(prettier, bundle, { recursive: true }),
        cp(join(root, 'patches/picomatch@4.0.7.patch'), tracked),
        cp(
          join(root, 'patches/prettier@3.10.1.patch'),
          join(checkout, 'patches/prettier@3.10.1.patch'),
        ),
      ]);
      const projects = [{ path: checkout }, { path: child }];
      const paths = ['.>vite>picomatch', 'packages__fixture>picomatch'];
      const verify = () => verifyGlobPatches(checkout, projects, paths);

      expect(verify()).toEqual({ paths: 2, copies: 2 });
      await writeFile(join(second, 'index.js'), '/* different bytes */');
      expect(verify).toThrow('reviewed picomatch@4.0.7');
      await cp(join(pico, 'index.js'), join(second, 'index.js'));

      await writeFile(join(bundle, 'index.mjs'), '/* different bundle */');
      expect(verify).toThrow('reviewed prettier@3.10.1');
      await cp(join(prettier, 'index.mjs'), join(bundle, 'index.mjs'));

      await rm(tracked);
      expect(verify).toThrow('picomatch@4.0.7.patch');
    } finally {
      await rm(checkout, { recursive: true, force: true });
    }
  });
});
