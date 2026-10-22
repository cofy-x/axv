import { createHash } from 'node:crypto';
import {
  copyFile,
  lstat,
  mkdir,
  readFile,
  rm,
  writeFile,
} from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureCommand, exitAfterCleanup, go, root } from './lib.mjs';
import { collectGoNotices } from './release-notices.mjs';
import { initializeTestTemp } from './test-temp.mjs';

export function validateGuestBuild(build, image, digest, expectedGo) {
  if (!Array.isArray(build?.Settings))
    throw new Error('Guest build metadata must include its build settings.');
  const keys = new Set();
  for (const entry of build.Settings ?? []) {
    if (
      typeof entry?.Key !== 'string' ||
      typeof entry.Value !== 'string' ||
      keys.has(entry.Key)
    )
      throw new Error(
        'Guest build settings must have unique string keys and values.',
      );
    keys.add(entry.Key);
  }
  const settings = Object.fromEntries(
    (build.Settings ?? []).map(({ Key, Value }) => [Key, Value]),
  );
  if (
    !['lima', 'firecracker'].includes(image.provider) ||
    !['amd64', 'arm64'].includes(image.arch) ||
    image.guest?.binarySHA256 !== digest
  )
    throw new Error(
      'The image manifest must identify this guest binary and target.',
    );
  if (
    build.Path !== 'github.com/cofy-x/axv/apps/server/cmd/guest' ||
    build.GoVersion !== expectedGo ||
    settings.GOOS !== 'linux' ||
    settings.GOARCH !== image.arch ||
    settings.CGO_ENABLED !== '0'
  )
    throw new Error(
      'Build the matching Linux guest with pinned Go and CGO_ENABLED=0.',
    );
}

async function binaryDigest(path) {
  const state = await lstat(path);
  if (!state.isFile() || state.size === 0)
    throw new Error('The guest binary must be a nonempty regular file.');
  return createHash('sha256')
    .update(await readFile(path))
    .digest('hex');
}

export async function writeGuestReleaseNotices({ guest, manifest, output }) {
  const digest = await binaryDigest(guest);
  const image = JSON.parse(await readFile(manifest, 'utf8'));
  const build = JSON.parse(
    (await captureCommand(go, ['version', '-m', '-json', guest])).stdout,
  );
  const expectedGo = `go${(await readFile(join(root, '.go-version'), 'utf8')).trim()}`;
  validateGuestBuild(build, image, digest, expectedGo);
  // Collection describes local builds too. Preserve dirty or absent VCS data;
  // this metadata is not release approval and never claims a clean checkout.
  await mkdir(output, { mode: 0o755 });
  try {
    const licenses = join(output, 'licenses');
    await mkdir(licenses);
    const inventory = await collectGoNotices(licenses, build);
    inventory.sort((a, b) =>
      `${a.kind}/${a.name}`.localeCompare(`${b.kind}/${b.name}`),
    );
    await writeFile(
      join(licenses, 'manifest.json'),
      `${JSON.stringify(inventory, null, 2)}\n`,
    );
    await copyFile(join(root, 'LICENSE'), join(output, 'LICENSE'));
    await copyFile(
      join(root, 'images/guest/THIRD_PARTY_NOTICES.md'),
      join(output, 'THIRD_PARTY_NOTICES.md'),
    );
    await writeFile(
      join(output, 'build-info.json'),
      `${JSON.stringify({ repository: 'https://github.com/cofy-x/axv', image, binary: { path: '/usr/local/bin/axv-guest', sha256: digest }, build }, null, 2)}\n`,
    );
    if ((await binaryDigest(guest)) !== digest)
      throw new Error('The guest binary changed during notice collection.');
    return inventory;
  } catch (error) {
    await rm(output, { recursive: true, force: true });
    throw error;
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  initializeTestTemp();
  for (const signal of ['SIGINT', 'SIGTERM'])
    process.once(
      signal,
      () => void exitAfterCleanup(signal === 'SIGINT' ? 130 : 143),
    );
  try {
    const options = {};
    const args = process.argv.slice(2);
    for (let index = 0; index < args.length; index += 2) {
      const key = args[index].replace(/^--/, '');
      if (
        !args[index].startsWith('--') ||
        !['guest', 'manifest', 'output'].includes(key) ||
        options[key] ||
        !args[index + 1]
      )
        throw new Error(
          'Supply --guest <binary> --manifest <image.json> --output <new-directory>.',
        );
      options[key] = resolve(args[index + 1]);
    }
    if (Object.keys(options).length !== 3)
      throw new Error(
        'Supply --guest <binary> --manifest <image.json> --output <new-directory>.',
      );
    const inventory = await writeGuestReleaseNotices(options);
    console.log(
      `Retained ${inventory.length} guest Go dependency notices in ${options.output}.`,
    );
    await exitAfterCleanup(0);
  } catch (error) {
    console.error(error);
    await exitAfterCleanup(1);
  }
}
