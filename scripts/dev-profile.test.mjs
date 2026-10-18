import { createHash } from 'node:crypto';
import {
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  realpath,
  rm,
  stat,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import {
  createDevelopmentRun,
  developmentEnvironment,
  developmentProfile,
} from './dev-profile.mjs';
import { serverEnvironment } from './lib.mjs';

const directories = [];

async function temporary() {
  const directory = await mkdtemp(join(tmpdir(), 'axv-dev-test-'));
  directories.push(directory);
  return directory;
}

afterEach(async () => {
  await Promise.all(
    directories
      .splice(0)
      .map((directory) => rm(directory, { recursive: true, force: true })),
  );
});

describe('persistent development profiles', () => {
  it('uses the canonical checkout hash, keeps checkouts separate and does not create a profile while resolving', async () => {
    const home = await temporary();
    const checkout = join(home, 'checkout');
    const other = join(home, 'other-checkout');
    await mkdir(checkout);
    await mkdir(other);
    const expected = createHash('sha256')
      .update(await realpath(checkout))
      .digest('hex')
      .slice(0, 12);
    const first = await developmentProfile({
      checkout,
      home,
      env: { AXV_HOME: '/production', DATA_DIR: '/production-data' },
    });
    expect(first).toEqual({
      profileHome: join(home, '.axv', 'dev', expected),
      dataDir: join(home, '.axv', 'dev', expected, 'data'),
    });
    expect(await developmentProfile({ checkout, home, env: {} })).toEqual(
      first,
    );
    expect(
      (await developmentProfile({ checkout: other, home, env: {} }))
        .profileHome,
    ).not.toBe(first.profileHome);
    await expect(stat(first.profileHome)).rejects.toMatchObject({
      code: 'ENOENT',
    });
  });

  it.skipIf(process.platform === 'win32')(
    'resolves a symlinked checkout to the same profile',
    async () => {
      const home = await temporary();
      const checkout = join(home, 'checkout');
      const alias = join(home, 'alias');
      await mkdir(checkout);
      await symlink(checkout, alias);
      expect(
        await developmentProfile({ checkout: alias, home, env: {} }),
      ).toEqual(await developmentProfile({ checkout, home, env: {} }));
    },
  );

  it('treats AXV_DEV_HOME as the final profile and rejects relative overrides', async () => {
    const home = await temporary();
    const profileHome = join(home, 'my dev profile');
    expect(
      await developmentProfile({
        checkout: home,
        home,
        env: { AXV_DEV_HOME: profileHome },
      }),
    ).toEqual({ profileHome, dataDir: join(profileHome, 'data') });
    await expect(
      developmentProfile({
        checkout: home,
        home,
        env: { AXV_DEV_HOME: 'relative' },
      }),
    ).rejects.toThrow('absolute');
  });

  it('clears inherited production overrides while retaining the development root and ordinary environment', () => {
    const original = {
      AXV_HOME: '/production',
      DATA_DIR: '/production-data',
      AXV_COMPUTE_SOCKET: '/production.sock',
      AXV_CACHE_HOME: '/shared-downloads',
      AXV_DEV_HOME: '/development',
      PATH: '/tools',
      HTTPS_PROXY: 'http://proxy',
    };
    expect(developmentEnvironment(original)).toEqual({
      AXV_CACHE_HOME: '/shared-downloads',
      AXV_DEV_HOME: '/development',
      PATH: '/tools',
      HTTPS_PROXY: 'http://proxy',
    });
    expect(original.AXV_HOME).toBe('/production');
  });
});

describe('invocation-owned development builds', () => {
  it('shares the runtime owner and removes no persistent or peer-run files', async () => {
    const profileHome = join(await temporary(), 'profile');
    const first = await createDevelopmentRun(profileHome);
    await mkdir(first.dataDir);
    await writeFile(join(first.dataDir, 'persistent'), 'saved data');
    await writeFile(
      join(first.profileHome, 'config.json'),
      '{"future":"preserved"}',
    );
    const second = await createDevelopmentRun(profileHome);
    expect(second.profileHome).toBe(first.profileHome);
    expect(second.directory).not.toBe(first.directory);
    expect(
      second.directory.startsWith(
        join(first.profileHome, 'run', 'development-'),
      ),
    ).toBe(true);
    const owner = createHash('sha256')
      .update(first.profileHome)
      .digest('hex')
      .slice(0, 16);
    expect(
      await readFile(join(first.profileHome, 'run', '.axv-owner'), 'utf8'),
    ).toBe(owner);
    await rm(first.directory, { recursive: true });
    expect((await stat(second.directory)).isDirectory()).toBe(true);
    expect(await readFile(join(first.dataDir, 'persistent'), 'utf8')).toBe(
      'saved data',
    );
    expect(await readFile(join(first.profileHome, 'config.json'), 'utf8')).toBe(
      '{"future":"preserved"}',
    );
  });

  it('preserves an unowned nonempty run directory and a different owner', async () => {
    const profileHome = join(await temporary(), 'profile');
    const run = join(profileHome, 'run');
    await mkdir(run, { recursive: true });
    await writeFile(join(run, 'unrelated'), 'keep');
    await expect(createDevelopmentRun(profileHome)).rejects.toThrow(
      'no AXV owner',
    );
    expect(await readdir(run)).toEqual(['unrelated']);
    await writeFile(join(run, '.axv-owner'), 'another-profile');
    await expect(createDevelopmentRun(profileHome)).rejects.toThrow(
      'different AXV owner',
    );
    expect(await readFile(join(run, 'unrelated'), 'utf8')).toBe('keep');
  });

  it.skipIf(process.platform === 'win32')(
    'uses the canonical profile identity and rejects symlinked run directories',
    async () => {
      const directory = await temporary();
      const profileHome = join(directory, 'profile');
      const alias = join(directory, 'alias');
      await mkdir(profileHome);
      await symlink(profileHome, alias);
      const first = await createDevelopmentRun(alias);
      expect(first.profileHome).toBe(await realpath(profileHome));
      const other = join(directory, 'other');
      await mkdir(other);
      await symlink(join(profileHome, 'run'), join(other, 'run'));
      await expect(createDevelopmentRun(other)).rejects.toThrow(
        'real directory',
      );
    },
  );
});

describe('explicit foreground launcher profiles', () => {
  it('replaces inherited production paths and keeps deliberate request-specific test overrides', async () => {
    const directory = await temporary();
    const profileHome = join(directory, 'profile');
    const dataDir = join(directory, 'data');
    const inherited = {
      AXV_HOME: '/production',
      DATA_DIR: '/production-data',
      AXV_COMPUTE_SOCKET: '/production.sock',
      PATH: '/tools',
    };
    expect(
      serverEnvironment({
        profileHome,
        dataDir,
        addr: '127.0.0.1:0',
        inherited,
      }),
    ).toEqual({
      AXV_HOME: profileHome,
      DATA_DIR: dataDir,
      AXV_COMPUTE_SOCKET: '',
      PATH: '/tools',
      ADDR: '127.0.0.1:0',
    });
    const explicit = serverEnvironment({
      profileHome,
      dataDir,
      addr: '127.0.0.1:0',
      inherited,
      env: {
        AXV_HOME: '/ignored-profile',
        DATA_DIR: '/deliberate-precedence-test',
        ADDR: 'invalid-for-flag-test',
      },
    });
    expect(explicit.AXV_HOME).toBe(profileHome);
    expect(explicit.DATA_DIR).toBe('/deliberate-precedence-test');
    expect(explicit.ADDR).toBe('invalid-for-flag-test');
  });

  it('rejects implicit and relative profile paths', () => {
    expect(() => serverEnvironment({ dataDir: '/data' })).toThrow(
      'explicit absolute',
    );
    expect(() =>
      serverEnvironment({ profileHome: 'relative', dataDir: '/data' }),
    ).toThrow('explicit absolute');
  });
});
