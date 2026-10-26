import { createHash } from 'node:crypto';
import {
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rm,
  stat,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, expect, test } from 'vitest';
import { captureCommand, root } from './lib.mjs';
import { validateGuestBuild } from './guest-release-notices.mjs';

const directories = [];
const pinnedGo = `go${(await readFile(join(root, '.go-version'), 'utf8')).trim()}`;
const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');

afterEach(async () => {
  await Promise.all(
    directories
      .splice(0)
      .map((directory) => rm(directory, { recursive: true, force: true })),
  );
});

async function fixture() {
  const directory = await mkdtemp(join(tmpdir(), 'axv-guest-notices-'));
  directories.push(directory);
  const toolchain = join(directory, 'goroot');
  const module = join(directory, 'module');
  await mkdir(join(toolchain, 'src/vendor/example'), { recursive: true });
  await mkdir(join(toolchain, 'src/crypto/internal/boring'), {
    recursive: true,
  });
  await mkdir(join(module, 'inherited'), { recursive: true });
  await writeFile(join(toolchain, 'LICENSE'), 'Fixture toolchain license\n');
  await writeFile(
    join(toolchain, 'src/vendor/example/NOTICE'),
    'Fixture bundled notice\n',
  );
  await writeFile(join(module, 'LICENSE'), 'Fixture module license\n');
  await writeFile(
    join(module, 'inherited/LICENSE-SQLITE'),
    'Fixture inherited license\n',
  );
  const executed = join(directory, 'guest-was-executed');
  const bytes = `#!/bin/sh\nprintf unsafe > '${executed}'\nexit 73\n`;
  const guest = join(directory, 'axv-guest');
  await writeFile(guest, bytes, { mode: 0o755 });
  const arch = process.arch === 'arm64' ? 'arm64' : 'amd64';
  const build = {
    Path: 'github.com/cofy-x/axv/apps/server/cmd/guest',
    GoVersion: pinnedGo,
    Deps: [
      {
        Path: 'example.invalid/guest-module',
        Version: 'v1.2.3',
        Sum: 'h1:fixture',
      },
    ],
    Settings: [
      { Key: 'GOOS', Value: 'linux' },
      { Key: 'GOARCH', Value: arch },
      { Key: 'CGO_ENABLED', Value: '0' },
      { Key: 'vcs.revision', Value: 'a'.repeat(40) },
      { Key: 'vcs.modified', Value: 'true' },
    ],
  };
  const image = {
    version: 1,
    provider: 'lima',
    arch,
    guest: {
      imageVersion: 1,
      protocolVersion: 3,
      capabilities: ['exec'],
      binarySHA256: hash(bytes),
    },
  };
  const manifest = join(directory, 'image.json');
  const statePath = join(directory, 'state.json');
  const state = {
    build,
    toolchain,
    module,
    bytes,
    image,
    moduleSum: 'h1:fixture',
  };
  await writeFile(manifest, JSON.stringify(image));
  await writeFile(statePath, JSON.stringify(state));
  const go = join(directory, 'go.cjs');
  await writeFile(
    go,
    `#!${process.execPath}
const fs = require('node:fs');
const state = JSON.parse(fs.readFileSync(process.env.AXV_GUEST_TEST_STATE, 'utf8'));
const args = process.argv.slice(2);
if (args[0] === 'version') console.log(JSON.stringify(state.build));
else if (args[0] === 'env') console.log(JSON.stringify({GOROOT: state.toolchain, GOVERSION: state.build.GoVersion}));
else if (args[0] === 'list') {
  for (const module of state.graph ?? state.build.Deps.map(dep => dep.Path + '@' + dep.Version)) console.log(module);
}
else if (args[0] === 'mod' && args[1] === 'download') console.log(JSON.stringify({Dir: state.module, Sum: state.moduleSum}));
else if (args[0] === 'mod' && args[1] === 'verify') {
  if (state.modifiedSource) { console.error('module source directory has been modified'); process.exit(1); }
  console.log('all modules verified');
}
else throw new Error('Unexpected fixture Go command: ' + JSON.stringify(args));
`,
    { mode: 0o700 },
  );
  return {
    directory,
    guest,
    manifest,
    statePath,
    state,
    executed,
    env: { ...process.env, GO: go, AXV_GUEST_TEST_STATE: statePath },
  };
}

async function collect(f, output = join(f.directory, 'notices')) {
  return captureCommand(
    process.execPath,
    [
      join(root, 'scripts/guest-release-notices.mjs'),
      '--guest',
      f.guest,
      '--manifest',
      f.manifest,
      '--output',
      output,
    ],
    { env: f.env, allowFailure: true },
  );
}

async function collectApplication(f, output) {
  const script = `import { readFile } from 'node:fs/promises'; import { writeReleaseNotices } from ${JSON.stringify(new URL('./release-notices.mjs', import.meta.url).href)}; const state = JSON.parse(await readFile(process.env.AXV_GUEST_TEST_STATE, 'utf8')); await writeReleaseNotices(${JSON.stringify(output)}, state.build);`;
  return captureCommand(
    process.execPath,
    ['--input-type=module', '-e', script],
    { env: f.env, allowFailure: true },
  );
}

describe.skipIf(process.platform === 'win32')(
  'guest distribution metadata',
  () => {
    test.each(['dirty', 'missing'])(
      'retains exact Go-only notices and honest %s VCS metadata without executing the guest',
      async (vcs) => {
        const f = await fixture();
        if (vcs === 'missing')
          f.state.build.Settings = f.state.build.Settings.filter(
            ({ Key }) => !Key.startsWith('vcs.'),
          );
        await writeFile(f.statePath, JSON.stringify(f.state));
        const result = await collect(f);
        expect(result.code, result.stderr).toBe(0);
        const output = join(f.directory, 'notices');
        const manifest = JSON.parse(
          await readFile(join(output, 'licenses/manifest.json'), 'utf8'),
        );
        expect(manifest.map(({ kind }) => kind).sort()).toEqual([
          'go',
          'toolchain',
        ]);
        expect(manifest.find(({ kind }) => kind === 'go')).toMatchObject({
          name: 'example.invalid/guest-module',
          version: 'v1.2.3',
          checksum: 'h1:fixture',
        });
        expect(
          await readFile(
            join(
              output,
              'licenses/go/example.invalid%2Fguest-module@v1.2.3/inherited/LICENSE-SQLITE',
            ),
            'utf8',
          ),
        ).toBe('Fixture inherited license\n');
        expect(
          await readFile(
            join(
              output,
              'licenses/toolchain/Go@' +
                pinnedGo +
                '/src/vendor/example/NOTICE',
            ),
            'utf8',
          ),
        ).toBe('Fixture bundled notice\n');
        expect(await readFile(join(output, 'LICENSE'))).toEqual(
          await readFile(join(root, 'LICENSE')),
        );
        const info = JSON.parse(
          await readFile(join(output, 'build-info.json'), 'utf8'),
        );
        expect(info).toMatchObject({
          image: f.state.image,
          binary: { sha256: hash(f.state.bytes) },
          build: f.state.build,
        });
        await expect(stat(f.executed)).rejects.toMatchObject({
          code: 'ENOENT',
        });
      },
    );

    test('rejects mismatched guest targets, non-guest binaries and manifest digests', async () => {
      const f = await fixture();
      const digest = hash(f.state.bytes);
      const changeSetting = (key, value) => ({
        ...f.state.build,
        Settings: f.state.build.Settings.map((setting) =>
          setting.Key === key ? { ...setting, Value: value } : setting,
        ),
      });
      for (const build of [
        {
          ...f.state.build,
          Path: 'github.com/cofy-x/axv/apps/server/cmd/server',
        },
        { ...f.state.build, GoVersion: 'go0.0.0' },
        changeSetting('GOOS', 'darwin'),
        changeSetting(
          'GOARCH',
          f.state.image.arch === 'amd64' ? 'arm64' : 'amd64',
        ),
        changeSetting('CGO_ENABLED', '1'),
      ])
        expect(() =>
          validateGuestBuild(build, f.state.image, digest, pinnedGo),
        ).toThrow();
      for (const image of [
        { ...f.state.image, provider: 'unknown' },
        { ...f.state.image, guest: { binarySHA256: '0'.repeat(64) } },
      ])
        expect(() =>
          validateGuestBuild(f.state.build, image, digest, pinnedGo),
        ).toThrow();
      f.state.build.Path = 'github.com/cofy-x/axv/apps/server/cmd/server';
      await writeFile(f.statePath, JSON.stringify(f.state));
      expect((await collect(f)).code).toBe(1);
      await expect(stat(join(f.directory, 'notices'))).rejects.toMatchObject({
        code: 'ENOENT',
      });
    });

    test('refuses replaced or mismatched module sources and preserves an existing output', async () => {
      const f = await fixture();
      f.state.moduleSum = 'h1:wrong-source';
      await writeFile(f.statePath, JSON.stringify(f.state));
      const result = await collect(f);
      expect(result.code).toBe(1);
      expect(result.stderr).toContain('Module source does not match binary');
      await expect(stat(join(f.directory, 'notices'))).rejects.toMatchObject({
        code: 'ENOENT',
      });
      f.state.moduleSum = 'h1:fixture';
      f.state.build.Deps[0].Replace = { Path: '/unreviewed/local/module' };
      await writeFile(f.statePath, JSON.stringify(f.state));
      expect((await collect(f)).stderr).toContain('Review replaced Go module');
      const output = join(f.directory, 'notices');
      await mkdir(output);
      await writeFile(join(output, 'existing'), 'keep this output');
      expect((await collect(f)).code).toBe(1);
      expect(await readdir(output)).toEqual(['existing']);
      expect(await readFile(join(output, 'existing'), 'utf8')).toBe(
        'keep this output',
      );
    });

    test.each(['outside-graph', 'modified-source'])(
      'rejects %s before retaining module notice text',
      async (failure) => {
        const f = await fixture();
        if (failure === 'outside-graph') f.state.graph = [];
        else f.state.modifiedSource = true;
        await writeFile(f.statePath, JSON.stringify(f.state));
        const result = await collect(f);
        expect(result.code).toBe(1);
        expect(result.stderr).toContain(
          failure === 'outside-graph'
            ? "Rebuild with this checkout's Go module versions"
            : 'module source directory has been modified',
        );
        await expect(stat(join(f.directory, 'notices'))).rejects.toMatchObject({
          code: 'ENOENT',
        });
      },
    );

    test.each(['application', 'guest'])(
      '%s notices reject the embedded Tailscale web client even with its top-level BSD license',
      async (consumer) => {
        const f = await fixture();
        f.state.build.Deps[0].Path = 'github.com/tailscale/web-client-prebuilt';
        await writeFile(f.statePath, JSON.stringify(f.state));
        await writeFile(
          join(f.state.module, 'LICENSE'),
          'BSD 3-Clause License\nCopyright Tailscale Inc & contributors.\n',
        );
        let result;
        if (consumer === 'guest') {
          result = await collect(f);
          await expect(
            stat(join(f.directory, 'notices')),
          ).rejects.toMatchObject({
            code: 'ENOENT',
          });
        } else {
          const output = join(f.directory, 'application');
          await mkdir(output);
          result = await collectApplication(f, output);
          expect(await readdir(output)).toEqual([]);
        }
        expect(result.code).toBe(1);
        expect(result.stderr).toContain(
          'The binary links the Tailscale node web client',
        );
        expect(result.stderr).toContain('Rebuild with ts_omit_webclient');
        await expect(stat(f.executed)).rejects.toMatchObject({
          code: 'ENOENT',
        });
      },
    );

    test('the application consumer still includes its JavaScript graph and build-helper notices', async () => {
      const f = await fixture();
      const output = join(f.directory, 'application');
      await mkdir(output);
      const result = await collectApplication(f, output);
      expect(result.code, result.stderr).toBe(0);
      const manifest = JSON.parse(
        await readFile(join(output, 'manifest.json'), 'utf8'),
      );
      expect(manifest.filter(({ kind }) => kind === 'go')).toHaveLength(1);
      expect(manifest.some(({ kind }) => kind === 'npm')).toBe(true);
      expect(
        manifest
          .filter(({ kind }) => kind === 'build-helper')
          .map(({ name }) => name)
          .sort(),
      ).toEqual(['rolldown', 'vite']);
    });
  },
);

describe.runIf(process.platform === 'linux')(
  'guest OS package inventory',
  () => {
    test('records the selected dpkg database with source versions and residual states', async () => {
      const f = await fixture();
      const database = join(f.directory, 'dpkg');
      const output = join(f.directory, 'inventory');
      await mkdir(database);
      await mkdir(output);
      for (const path of [
        'etc/apt/sources.list.d',
        'var/lib/apt/lists',
        'usr/share/doc/fixture-lib',
      ])
        await mkdir(join(f.directory, path), { recursive: true });
      await writeFile(join(f.directory, 'etc/os-release'), 'ID=ubuntu\n');
      await writeFile(
        join(f.directory, 'etc/apt/sources.list.d/ubuntu.sources'),
        'Types: deb\nURIs: https://archive.ubuntu.com/ubuntu/\nSuites: noble\nComponents: main\n',
      );
      await writeFile(
        join(f.directory, 'var/lib/apt/lists/archive_InRelease'),
        'Fixture signed index\n',
      );
      await writeFile(
        join(f.directory, 'usr/share/doc/fixture-lib/copyright'),
        'Fixture package license\n',
      );
      await writeFile(
        join(database, 'status'),
        `Package: fixture-lib
Status: hold ok installed
Architecture: amd64
Version: 2:1.2.3-1
Multi-Arch: same
Source: fixture-source (2:1.2.2-3)
Description: Fixture package

Package: fixture-residual
Status: deinstall ok config-files
Architecture: all
Version: 1.0-1
Description: Fixture residual package

`,
      );
      await captureCommand(
        'sh',
        [join(root, 'images/guest/package-inventory.sh'), output],
        {
          env: {
            ...process.env,
            DPKG_ADMINDIR: database,
            DPKG_ROOT: f.directory,
          },
        },
      );
      const inventory = await readFile(join(output, 'os-packages.tsv'), 'utf8');
      expect(inventory).toContain(
        'hi \tfixture-lib:amd64\t2:1.2.3-1\tamd64\tfixture-source\t2:1.2.2-3\n',
      );
      expect(inventory).toContain(
        'rc \tfixture-residual\t1.0-1\tall\tfixture-residual\t1.0-1\n',
      );
      expect(await readFile(join(output, 'os-release'))).toEqual(
        await readFile(join(f.directory, 'etc/os-release')),
      );
      expect((await readdir(output)).sort()).toEqual([
        'os-distribution',
        'os-packages.tsv',
        'os-release',
      ]);
    });
  },
);
