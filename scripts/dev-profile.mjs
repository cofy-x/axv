import { createHash } from 'node:crypto';
import {
  lstat,
  mkdir,
  mkdtemp,
  open,
  readFile,
  readdir,
  realpath,
} from 'node:fs/promises';
import { homedir } from 'node:os';
import { isAbsolute, join } from 'node:path';

/** One persistent profile per physical checkout, independent of production overrides. */
export async function developmentProfile({
  checkout,
  env = process.env,
  home = homedir(),
}) {
  const canonicalCheckout = await realpath(checkout);
  const identity = createHash('sha256')
    .update(canonicalCheckout)
    .digest('hex')
    .slice(0, 12);
  const profileHome = env.AXV_DEV_HOME || join(home, '.axv', 'dev', identity);
  if (!isAbsolute(profileHome) || profileHome.includes('\0')) {
    throw new Error(
      'AXV_DEV_HOME must be an absolute path to a dedicated development profile.',
    );
  }
  return { profileHome, dataDir: join(profileHome, 'data') };
}

export function developmentEnvironment(inherited = process.env) {
  // Preserve AXV_CACHE_HOME: downloads belong to the OS user, not the checkout.
  const environment = { ...inherited };
  for (const key of ['AXV_HOME', 'DATA_DIR', 'AXV_COMPUTE_SOCKET'])
    delete environment[key];
  return environment;
}

/** Claim the same run directory owner that profile.Identity and app.Settings.Prepare use. */
export async function createDevelopmentRun(profileHome) {
  await mkdir(profileHome, { recursive: true, mode: 0o700 });
  const canonicalHome = await realpath(profileHome);
  const runDirectory = join(canonicalHome, 'run');
  await mkdir(runDirectory, { recursive: true, mode: 0o700 });
  const runInfo = await lstat(runDirectory);
  if (!runInfo.isDirectory() || runInfo.isSymbolicLink())
    throw new Error('The development run path must be a real directory.');
  const owner = createHash('sha256')
    .update(canonicalHome)
    .digest('hex')
    .slice(0, 16);
  const marker = join(runDirectory, '.axv-owner');
  let existing;
  try {
    existing = await lstat(marker);
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
  if (!existing) {
    if ((await readdir(runDirectory)).length !== 0)
      throw new Error(
        'The development run directory is not empty and has no AXV owner; its files were preserved.',
      );
    let file;
    try {
      file = await open(marker, 'wx', 0o600);
      await file.writeFile(owner);
      await file.sync();
    } catch (error) {
      if (error.code !== 'EEXIST') throw error;
    } finally {
      await file?.close();
    }
    existing = await lstat(marker);
  }
  if (
    !existing.isFile() ||
    existing.isSymbolicLink() ||
    (await readFile(marker, 'utf8')) !== owner
  ) {
    throw new Error(
      'The development run directory has a different AXV owner; its files were preserved.',
    );
  }
  const directory = await mkdtemp(join(runDirectory, 'development-'));
  return {
    directory,
    profileHome: canonicalHome,
    dataDir: join(canonicalHome, 'data'),
  };
}
