import { constants, createReadStream } from 'node:fs';
import { createHash } from 'node:crypto';
import {
  copyFile,
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rm,
  writeFile,
} from 'node:fs/promises';
import { basename, isAbsolute, join, resolve } from 'node:path';
import { isDeepStrictEqual, parseArgs } from 'node:util';
import { fileURLToPath } from 'node:url';
import { captureCommand, exitAfterCleanup, go, root } from './lib.mjs';
import { validateGuestBuild } from './guest-release-notices.mjs';
import {
  readReleaseSource,
  validateReleaseSource,
  validateReleaseVersion,
} from './package-release.mjs';
import { initializeTestTemp } from './test-temp.mjs';

async function digest(path) {
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest('hex');
}

function safePath(path) {
  return (
    typeof path === 'string' &&
    !path.includes('\\') &&
    Array.from(path).every(
      (char) => char.charCodeAt(0) > 31 && char.charCodeAt(0) !== 127,
    ) &&
    !isAbsolute(path) &&
    path.split('/').every((part) => part && part !== '.' && part !== '..')
  );
}

async function readMetadata(path, limit = 4 * 1024 ** 2) {
  const state = await lstat(path);
  if (!state.isFile() || state.size > limit)
    throw new Error(`Bundle metadata must be a bounded regular file: ${path}`);
  return readFile(path, 'utf8');
}

async function filesIn(directory, prefix = '') {
  const files = [];
  for (const entry of await readdir(join(directory, prefix), {
    withFileTypes: true,
  })) {
    const path = prefix ? `${prefix}/${entry.name}` : entry.name;
    if (!safePath(path)) throw new Error(`Unsafe bundle filename: ${path}`);
    if (entry.isDirectory()) files.push(...(await filesIn(directory, path)));
    else if (entry.isFile()) files.push(path);
    else throw new Error(`Bundle contains a symlink or special file: ${path}`);
  }
  return files.sort();
}

// Verify the complete bundle, including exported license and source materials.
// Unlisted files are rejected so a used/private bundle cannot acquire a release
// archive merely by keeping an older checksum manifest beside it.
export async function verifyGuestBundle(directory) {
  initializeTestTemp();
  if (!(await lstat(directory)).isDirectory())
    throw new Error('Select a real bundle directory, not a symlink.');
  const files = await filesIn(directory);
  const sums = new Map();
  const checksumText = await readMetadata(join(directory, 'SHA256SUMS'));
  for (const line of checksumText.trimEnd().split('\n')) {
    const match = /^([a-f0-9]{64}) {2}(.+)$/.exec(line);
    if (!match || !safePath(match[2]) || sums.has(match[2]))
      throw new Error('Invalid, unsafe or duplicate bundle checksum entry.');
    sums.set(match[2], match[1]);
  }
  const listed = [...sums.keys()].sort();
  if (
    JSON.stringify(listed) !==
    JSON.stringify(files.filter((p) => p !== 'SHA256SUMS'))
  )
    throw new Error(
      'Every bundle file must be covered exactly once by SHA256SUMS.',
    );
  for (const [path, expected] of sums)
    if ((await digest(join(directory, path))) !== expected)
      throw new Error(`Bundle checksum mismatch: ${path}`);
  const verifiedMetadata = async (path, limit) => {
    const value = await readMetadata(join(directory, path), limit);
    if (createHash('sha256').update(value).digest('hex') !== sums.get(path))
      throw new Error(`Bundle metadata changed during verification: ${path}`);
    return value;
  };
  const image = JSON.parse(await verifiedMetadata('manifest.json', 64 * 1024));
  // Import, builder and release validation share the Go image contract. This
  // also checks rootfs size/integrity and the current capability set.
  const validated = await captureCommand(
    go,
    [
      'run',
      './apps/server/cmd/image-manifest',
      '--check-bundle',
      join(directory, 'manifest.json'),
    ],
    { timeout: 300_000, env: { ...process.env, GOTOOLCHAIN: 'local' } },
  );
  if (!isDeepStrictEqual(JSON.parse(validated.stdout), image))
    throw new Error('Bundle manifest changed during contract validation.');
  if (
    !safePath(image.rootfs?.file) ||
    basename(image.rootfs.file) !== image.rootfs.file ||
    sums.get(image.rootfs.file) !== image.rootfs.sha256
  )
    throw new Error(
      'Bundle must declare a complete enhanced AXV guest contract.',
    );
  for (const path of files)
    if (
      ![
        'manifest.json',
        'SHA256SUMS',
        image.rootfs.file,
        image.provider === 'lima' ? 'base-image.sha256' : 'base-image.txt',
      ].includes(path) &&
      !path.startsWith('distribution/axv-guest/')
    )
      throw new Error(`Unexpected image-build output: ${path}`);
  const required = [
    'LICENSE',
    'THIRD_PARTY_NOTICES.md',
    'build-info.json',
    'os-packages.tsv',
    'os-release',
    'licenses/manifest.json',
  ];
  for (const path of required)
    if (!sums.has(`distribution/axv-guest/${path}`))
      throw new Error(`Missing guest distribution material: ${path}`);
  const base = (
    await verifiedMetadata(
      image.provider === 'lima' ? 'base-image.sha256' : 'base-image.txt',
    )
  ).trim();
  if (
    !(
      image.provider === 'lima'
        ? /^[a-f0-9]{64}$/
        : /^ubuntu:24\.04@sha256:[a-f0-9]{64}$/
    ).test(base)
  )
    throw new Error('Missing pinned base identity.');
  const info = JSON.parse(
    await verifiedMetadata('distribution/axv-guest/build-info.json'),
  );
  const expectedGo = `go${(await readFile(join(root, '.go-version'), 'utf8')).trim()}`;
  validateGuestBuild(info.build, image, info.binary?.sha256, expectedGo);
  if (
    info.binary?.sha256 !== image.guest.binarySHA256 ||
    info.image?.provider !== image.provider ||
    info.image?.arch !== image.arch ||
    !isDeepStrictEqual(info.image?.guest, image.guest)
  )
    throw new Error(
      'Guest build metadata does not match its bundle and pinned toolchain.',
    );
  sums.set(
    'SHA256SUMS',
    createHash('sha256').update(checksumText).digest('hex'),
  );
  return { image, build: info.build, files, base, sums };
}

// Assemble from private, verified copies. Revalidating a mutable source after
// tar finishes cannot prove which generation of bytes entered that archive.
export async function snapshotGuestBundle(bundle, destination, verified) {
  await mkdir(destination, { mode: 0o700 });
  for (const path of verified.files) {
    const target = join(destination, path);
    await mkdir(join(target, '..'), { recursive: true });
    await copyFile(
      join(bundle, path),
      target,
      constants.COPYFILE_EXCL | constants.COPYFILE_FICLONE,
    );
    if ((await digest(target)) !== verified.sums.get(path))
      throw new Error(`Bundle changed during assembly: ${path}`);
  }
}

export async function packageGuestRelease({ bundle, version, local = false }) {
  await validateReleaseVersion(version);
  const temporary = initializeTestTemp();
  const verified = await verifyGuestBundle(bundle);
  const source = await readReleaseSource();
  if (!local) validateReleaseSource(verified.build, source);
  const { image } = verified;
  const name = `axv_guest_${version}_${image.provider}_${image.arch}`;
  const parent = local
    ? await mkdtemp(join(temporary.root, 'guest-candidate-'))
    : join(root, 'dist', 'guest-release', version);
  await mkdir(parent, { recursive: true });
  const output = join(parent, `${image.provider}_${image.arch}`);
  await mkdir(output);
  const staging = await mkdtemp(join(temporary.directory, 'guest-snapshot-'));
  try {
    const snapshot = join(staging, 'bundle');
    await snapshotGuestBundle(bundle, snapshot, verified);
    const archive = `${name}.tar.gz`;
    await captureCommand(
      'tar',
      ['-czf', join(output, archive), '-C', snapshot, '--', ...verified.files],
      { timeout: 300_000 },
    );
    if (!local)
      validateReleaseSource(verified.build, await readReleaseSource());
    const checksum = await digest(join(output, archive));
    const disk = `${name}${image.provider === 'lima' ? '.qcow2' : '.ext4'}`;
    await copyFile(
      join(snapshot, image.rootfs.file),
      join(output, disk),
      constants.COPYFILE_EXCL | constants.COPYFILE_FICLONE,
    );
    if ((await digest(join(output, disk))) !== image.rootfs.sha256)
      throw new Error(
        'Copied release disk does not match the reviewed bundle.',
      );
    await writeFile(
      join(output, 'candidate.json'),
      `${JSON.stringify(
        {
          version,
          image,
          base: verified.base,
          archive,
          disk,
          sha256: checksum,
          source,
          build: verified.build,
          localOnly: local,
          publication:
            'Requires exact-bundle native acceptance and maintainer review; no artifact URL is implied.',
        },
        null,
        2,
      )}\n`,
      { flag: 'wx' },
    );
    await writeFile(
      join(output, 'SHA256SUMS'),
      `${checksum}  ${archive}\n${image.rootfs.sha256}  ${disk}\n${await digest(join(output, 'candidate.json'))}  candidate.json\n`,
      { flag: 'wx' },
    );
    return output;
  } catch (error) {
    await rm(output, { recursive: true, force: true });
    throw error;
  } finally {
    await rm(staging, { recursive: true, force: true });
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  for (const signal of ['SIGINT', 'SIGTERM'])
    process.once(
      signal,
      () => void exitAfterCleanup(signal === 'SIGINT' ? 130 : 143),
    );
  try {
    const { values } = parseArgs({
      options: {
        bundle: { type: 'string' },
        local: { type: 'boolean', default: false },
      },
      strict: true,
      allowPositionals: false,
    });
    if (!values.bundle || !isAbsolute(values.bundle))
      throw new Error('Supply --bundle /absolute/bundle-directory [--local].');
    const output = await packageGuestRelease({
      bundle: resolve(values.bundle),
      version: process.env.VERSION,
      local: values.local,
    });
    console.log(`Guest candidate: ${output}`);
    // Local candidates are intentionally retained as reviewable test artifacts.
    process.exitCode = 0;
  } catch (error) {
    console.error(error);
    await exitAfterCleanup(1);
  }
}
