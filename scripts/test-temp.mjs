import {
  lstatSync,
  mkdirSync,
  mkdtempSync,
  realpathSync,
  rmSync,
} from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const defaultRoot = fileURLToPath(new URL('../.data/test', import.meta.url));

/** Configure one process without changing the caller's shell or shared caches. */
export function configureTestTemp(env = process.env, cwd = process.cwd()) {
  const requested = env.AXV_TEST_TMPDIR
    ? resolve(cwd, env.AXV_TEST_TMPDIR)
    : defaultRoot;
  mkdirSync(requested, { recursive: true, mode: 0o700 });
  const state = lstatSync(requested);
  if (
    !state.isDirectory() ||
    state.isSymbolicLink() ||
    (process.getuid && state.uid !== process.getuid()) ||
    (process.platform !== 'win32' && (state.mode & 0o077) !== 0)
  )
    throw new Error(
      'AXV_TEST_TMPDIR must be a real, private directory owned by the current user.',
    );
  const root = realpathSync(requested);
  const directory = mkdtempSync(join(root, 'axv-test-'));
  env.AXV_TEST_TMPDIR = root;
  env.TMPDIR = directory;
  env.GOTMPDIR = directory;
  let cleaned = false;
  return {
    root,
    directory,
    cleanup() {
      if (cleaned) return;
      rmSync(directory, { recursive: true, force: true });
      cleaned = true;
    },
  };
}

let temporary;
export let testTempRoot;

/** Only test/build entrypoints opt in; importing shared launch helpers is inert. */
export function initializeTestTemp({ isolatedCache = false } = {}) {
  if (temporary) {
    if (isolatedCache) configureIsolatedCache(temporary);
    return temporary;
  }
  temporary = configureTestTemp();
  if (isolatedCache) configureIsolatedCache(temporary);
  testTempRoot = temporary.root;
  // Scratch profiles have their own explicit cleanup. This hook removes only
  // this process's compiler/fixture temp directory, never the configured root.
  process.once('exit', () => temporary.cleanup());
  return temporary;
}

function configureIsolatedCache(temporary) {
  const cache = join(temporary.directory, 'cache');
  mkdirSync(cache, { mode: 0o700, recursive: true });
  process.env.AXV_CACHE_HOME = cache;
  process.env.AXV_ARTIFACT_MIRRORS = '';
}
