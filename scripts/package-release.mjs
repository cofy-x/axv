import { constants } from 'node:fs';
import { copyFile, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { binary, captureCommand, exitAfterCleanup, go, root } from './lib.mjs';
import { writeReleaseNotices } from './release-notices.mjs';
import { initializeTestTemp } from './test-temp.mjs';

export async function validateReleaseVersion(version) {
  if (
    typeof version !== 'string' ||
    version.trim() !== version ||
    !/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$/.test(
      version ?? '',
    )
  )
    throw new Error(
      'VERSION must be a release version such as 0.1.0 or 0.1.0-rc.1.',
    );
  if (
    version
      .split('-')
      .slice(1)
      .join('-')
      .split('.')
      .some((part) => /^0\d+$/.test(part))
  )
    throw new Error(
      'Numeric prerelease identifiers must not have leading zeroes.',
    );
  for (const path of [
    'package.json',
    'apps/dashboard/package.json',
    'packages/nextstep/package.json',
  ]) {
    const manifest = JSON.parse(await readFile(join(root, path), 'utf8'));
    if (manifest.version !== version.split('-')[0])
      throw new Error(`VERSION must have the same base version as ${path}.`);
  }
  return version;
}

async function packageRelease(version) {
  if (process.platform !== 'linux' || process.arch !== 'x64')
    throw new Error(
      'Release packaging currently requires a native Linux amd64 host.',
    );
  const temporary = initializeTestTemp();
  const buildInfo = JSON.parse(
    (await captureCommand(go, ['version', '-m', '-json', binary])).stdout,
  );
  const settings = Object.fromEntries(
    buildInfo.Settings.map(({ Key, Value }) => [Key, Value]),
  );
  const expectedGo = `go${(await readFile(join(root, '.go-version'), 'utf8')).trim()}`;
  if (
    buildInfo.GoVersion !== expectedGo ||
    settings.GOOS !== 'linux' ||
    settings.GOARCH !== 'amd64' ||
    settings['-tags'] !== 'production' ||
    settings.CGO_ENABLED !== '0'
  )
    throw new Error(
      'Build with the pinned Go toolchain and VERSION=<version> make build on native Linux amd64.',
    );
  const reported = (
    await captureCommand(binary, ['version'], { cwd: temporary.directory })
  ).stdout;
  if (reported !== `axv ${version}\n`)
    throw new Error(
      'The binary version does not match VERSION. Rebuild and smoke-test this version.',
    );

  const name = `axv_${version}_linux_amd64`;
  const directory = join(temporary.directory, name);
  await mkdir(directory);
  await copyFile(binary, join(directory, 'axv'));
  await copyFile(join(root, 'LICENSE'), join(directory, 'LICENSE'));
  await mkdir(join(directory, 'licenses'));
  const inventory = await writeReleaseNotices(
    join(directory, 'licenses'),
    buildInfo,
  );
  await writeFile(
    join(directory, 'build-info.json'),
    `${JSON.stringify(
      {
        version,
        repository: 'https://github.com/cofy-x/axv',
        build: buildInfo,
      },
      null,
      2,
    )}\n`,
  );
  await writeFile(
    join(directory, 'README.txt'),
    [
      `AXV ${version} — Linux amd64 release candidate`,
      '',
      'Run ./axv version, then ./axv serve for a foreground workstation.',
      'Node.js is not required at runtime. Host Codex requires your own tmux and Codex CLI.',
      'VM images, kernels, VM providers and coding-agent clients are installed separately.',
      'Startup and prerequisites: https://github.com/cofy-x/axv/blob/main/docs/startup.md',
      'Availability and limitations: https://github.com/cofy-x/axv#project-status',
      'Security reports: https://github.com/cofy-x/axv/security/advisories/new',
      '',
      'AXV source: MIT (LICENSE). Third-party terms: THIRD_PARTY_NOTICES.md and licenses/.',
      'build-info.json records the source revision and build settings.',
      '',
    ].join('\n'),
  );

  const archive = `${name}.tar.gz`;
  await captureCommand('tar', [
    '-czf',
    join(temporary.directory, archive),
    '-C',
    temporary.directory,
    name,
  ]);
  const digest = createHash('sha256')
    .update(await readFile(join(temporary.directory, archive)))
    .digest('hex');
  const outputRoot = join(root, 'dist', 'release');
  await mkdir(outputRoot, { recursive: true });
  const output = join(outputRoot, version);
  await mkdir(output);
  try {
    await copyFile(
      join(temporary.directory, archive),
      join(output, archive),
      constants.COPYFILE_EXCL,
    );
    await writeFile(join(output, 'SHA256SUMS'), `${digest}  ${archive}\n`, {
      flag: 'wx',
    });
  } catch (error) {
    await rm(output, { recursive: true, force: true });
    throw error;
  }
  console.log(
    `Packaged ${join(output, archive)} with ${inventory.length} dependency notices.`,
  );
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  for (const signal of ['SIGINT', 'SIGTERM'])
    process.once(
      signal,
      () => void exitAfterCleanup(signal === 'SIGINT' ? 130 : 143),
    );
  try {
    if (
      process.argv.length > 3 ||
      (process.argv[2] && process.argv[2] !== '--check-version')
    )
      throw new Error(
        'Usage: VERSION=0.1.0-rc.1 node scripts/package-release.mjs [--check-version]',
      );
    const version = await validateReleaseVersion(process.env.VERSION);
    if (process.argv[2] === '--check-version')
      console.log(`Release version: ${version}`);
    else await packageRelease(version);
    await exitAfterCleanup(0);
  } catch (error) {
    console.error(error);
    await exitAfterCleanup(1);
  }
}
