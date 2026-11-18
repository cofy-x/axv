import { createHash } from 'node:crypto';
import { readFileSync, readdirSync, realpathSync } from 'node:fs';
import { createRequire } from 'node:module';
import { isAbsolute, join, relative, sep } from 'node:path';

// Reviewed file trees after frozen installation with the two checked-in patches.
const picomatch = {
  name: 'picomatch',
  version: '4.0.7',
  entry: 'index.js',
  patch: 'f4108af11342c215766e4a4da5f12b1cdb64163185fcdb41786b9712958c2ec8',
  files: '2e031ce8df3e56ab4281ad43378e4b811204dc1a39605d30a1ffaa7102ecd8e6',
};
const prettier = {
  name: 'prettier',
  version: '3.10.1',
  entry: 'index.cjs',
  patch: '76794269eae7c423229495a673f51c81efc94f9143b7ac693668444b00adf95b',
  files: 'a4c7862c2c62088662f8e65f75c715d7972ff57c4a9f7422ff76432ff8256562',
};
const npmName = /^(?:@[a-z0-9][a-z0-9._-]*\/)?[a-z0-9][a-z0-9._-]*$/;
const manifest = (dir) => JSON.parse(readFileSync(join(dir, 'package.json')));
const sha256 = (file) =>
  createHash('sha256').update(readFileSync(file)).digest('hex');

function inside(root, path) {
  const rel = relative(root, path);
  return !isAbsolute(rel) && rel.split(sep)[0] !== '..';
}

function workspacePaths(root, projects) {
  if (!Array.isArray(projects)) throw new Error('Invalid pnpm workspace list.');
  const workspaces = new Map();
  for (const project of projects) {
    if (typeof project?.path !== 'string' || !isAbsolute(project.path))
      throw new Error('Missing pnpm workspace path.');
    const dir = realpathSync(project.path);
    // pnpm audit flattens relative workspace IDs with a double underscore.
    const id = relative(root, dir).split(sep).join('__') || '.';
    if (!inside(root, dir) || workspaces.has(id))
      throw new Error('Unexpected pnpm workspace path.');
    workspaces.set(id, dir);
  }
  if (!workspaces.has('.')) throw new Error('Missing root workspace.');
  return workspaces;
}

function devDependency(dir, name) {
  const project = manifest(dir);
  return (
    Object.hasOwn(project.devDependencies ?? {}, name) &&
    ['dependencies', 'optionalDependencies', 'peerDependencies'].every(
      (group) => !Object.hasOwn(project[group] ?? {}, name),
    )
  );
}

function installedPackage(root, from, name, entry) {
  const req = createRequire(join(from, 'package.json'));
  for (const search of req.resolve.paths(name) ?? []) {
    let dir;
    try {
      dir = realpathSync(join(search, name));
    } catch (error) {
      if (error.code === 'ENOENT') continue;
      throw error;
    }
    if (!inside(root, dir) || manifest(dir).name !== name)
      throw new Error(`Unexpected resolution for ${name} from ${from}.`);
    const resolved = realpathSync(req.resolve(name));
    if (!inside(dir, resolved) || (entry && resolved !== join(dir, entry)))
      throw new Error(`Unexpected installed entry for ${name} from ${from}.`);
    return dir;
  }
  throw new Error(`Missing installed dependency ${name} from ${from}.`);
}

function packageFilesDigest(dir) {
  const files = readdirSync(dir, { recursive: true, withFileTypes: true })
    .filter((entry) => !entry.isDirectory())
    .map((entry) => {
      if (!entry.isFile())
        throw new Error('Unexpected installed package entry.');
      return relative(dir, join(entry.parentPath, entry.name))
        .split(sep)
        .join('/');
    })
    .sort();
  const hash = createHash('sha256');
  for (const file of files)
    hash.update(JSON.stringify([file, sha256(join(dir, file))]) + '\n');
  return hash.digest('hex');
}

function verifyPackage(root, reviewed, directories) {
  const label = `${reviewed.name}@${reviewed.version}`;
  if (sha256(join(root, 'patches', `${label}.patch`)) !== reviewed.patch)
    throw new Error(`Patch digest changed for ${label}.`);
  for (const dir of directories) {
    if (
      manifest(dir).version !== reviewed.version ||
      packageFilesDigest(dir) !== reviewed.files
    )
      throw new Error(
        `Installed files do not match the reviewed ${label} in ${dir}.`,
      );
  }
}

/** Resolve every reported path, including separate workspace and peer copies. */
export function verifyGlobPatches(checkout, projects, paths) {
  const root = realpathSync(checkout);
  const workspaces = workspacePaths(root, projects);
  const copies = new Set();
  for (const path of paths) {
    const [id, ...names] = path.split('>');
    let dir = workspaces.get(id);
    if (
      !dir ||
      names.at(-1) !== picomatch.name ||
      names.some((name) => !npmName.test(name)) ||
      !devDependency(dir, names[0])
    )
      throw new Error(`Unexpected development dependency path ${path}.`);
    for (const [index, name] of names.entries())
      dir = installedPackage(
        root,
        dir,
        name,
        index === names.length - 1 ? picomatch.entry : undefined,
      );
    copies.add(dir);
  }
  if (!copies.size) throw new Error('No installed picomatch copies verified.');
  verifyPackage(root, picomatch, copies);

  // The Prettier bundle contains a second parser outside pnpm's audit graph.
  verifyPackage(root, prettier, [
    installedPackage(root, root, prettier.name, prettier.entry),
  ]);
  return { paths: paths.length, copies: copies.size };
}
