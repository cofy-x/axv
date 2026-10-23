import { lstat, mkdir, realpath } from 'node:fs/promises';
import { isAbsolute, join, relative, resolve, sep } from 'node:path';

function contains(directory, path) {
  const offset = relative(directory, path);
  return (
    !isAbsolute(offset) && offset !== '..' && !offset.startsWith(`..${sep}`)
  );
}

/** Create evidence parents without following aliases out of the selected root. */
export async function prepareTestOutputPath(
  root,
  destination,
  { directory = false, excludedDirectories = [] } = {},
) {
  const requestedRoot = resolve(root);
  const requestedPath = resolve(destination);
  if (
    requestedRoot === requestedPath ||
    !contains(requestedRoot, requestedPath)
  )
    throw new Error('Test output must be a child of AXV_TEST_TMPDIR.');
  const rootState = await lstat(requestedRoot);
  if (!rootState.isDirectory() || rootState.isSymbolicLink())
    throw new Error('The test output root must be a real directory.');
  const canonicalRoot = await realpath(requestedRoot);
  const offset = relative(requestedRoot, requestedPath);
  const canonicalPath = resolve(canonicalRoot, offset);
  for (const excluded of excludedDirectories) {
    if (contains(await realpath(excluded), canonicalPath))
      throw new Error('Test output must be outside the served UI output.');
  }

  let parent = canonicalRoot;
  const parts = offset.split(sep);
  for (const [index, part] of parts.entries()) {
    const path = join(parent, part);
    const needsDirectory = directory || index < parts.length - 1;
    let state;
    try {
      state = await lstat(path);
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
      if (!needsDirectory) return path;
      try {
        await mkdir(path, { mode: 0o700 });
      } catch (error) {
        if (error.code !== 'EEXIST') throw error;
      }
      state = await lstat(path);
    }
    if (state.isSymbolicLink())
      throw new Error('Test output must not contain symbolic links.');
    if (needsDirectory ? !state.isDirectory() : !state.isFile())
      throw new Error('Test output has an unexpected file type.');
    parent = path;
  }
  return canonicalPath;
}
