import { execFile } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { constants } from 'node:fs';
import {
  access,
  chmod,
  copyFile,
  lstat,
  mkdir,
  open,
  realpath,
  rename,
  rm,
  stat,
} from 'node:fs/promises';
import {
  basename,
  delimiter,
  dirname,
  isAbsolute,
  join,
  resolve,
  win32,
} from 'node:path';
import { promisify } from 'node:util';

const execute = promisify(execFile);
const commandPath = 'github.com/cofy-x/axv/apps/server/cmd/server';

export async function goEnvironment(
  go,
  { cwd, env = process.env, signal } = {},
) {
  const { stdout } = await execute(
    go,
    [
      'env',
      '-json',
      'GOBIN',
      'GOPATH',
      'GOOS',
      'GOARCH',
      'GOHOSTOS',
      'GOHOSTARCH',
    ],
    { cwd, env, signal, timeout: 15_000, maxBuffer: 1 << 20 },
  );
  return JSON.parse(stdout);
}

export function installationTarget(environment, platform = process.platform) {
  const hostOS = platform === 'win32' ? 'windows' : platform;
  if (
    environment.GOOS !== environment.GOHOSTOS ||
    environment.GOARCH !== environment.GOHOSTARCH ||
    environment.GOHOSTOS !== hostOS ||
    !environment.GOARCH
  ) {
    throw new Error(
      'make install requires a native build. Clear cross-compilation GOOS and GOARCH overrides.',
    );
  }
  const paths = platform === 'win32' ? win32 : { isAbsolute, join };
  const firstGopath = (environment.GOPATH || '').split(
    platform === 'win32' ? ';' : ':',
  )[0];
  const directory =
    environment.GOBIN || (firstGopath && paths.join(firstGopath, 'bin'));
  if (!directory || !paths.isAbsolute(directory)) {
    throw new Error(
      'Go must provide an absolute GOBIN or an absolute first GOPATH entry.',
    );
  }
  return paths.join(directory, platform === 'win32' ? 'axv.exe' : 'axv');
}

export function validateProductionBuild(build, environment) {
  const settings = Object.fromEntries(
    (build.Settings || []).map(({ Key, Value }) => [Key, Value]),
  );
  if (
    build.Path !== commandPath ||
    !(settings['-tags'] || '').split(',').includes('production')
  ) {
    throw new Error(
      'The executable is not an AXV production bundle. Run make build before installing.',
    );
  }
  if (
    settings.GOOS !== environment.GOHOSTOS ||
    settings.GOARCH !== environment.GOHOSTARCH
  ) {
    throw new Error(
      'The built AXV executable targets another platform or architecture. Rebuild for this computer.',
    );
  }
}

async function fileState(path) {
  try {
    return await lstat(path, { bigint: true });
  } catch (error) {
    if (error.code === 'ENOENT') return null;
    throw error;
  }
}

function sameFile(first, second) {
  if (!first || !second) return first === second;
  return ['dev', 'ino', 'size', 'mtimeNs', 'mode'].every(
    (key) => first[key] === second[key],
  );
}

async function digestFile(path, signal) {
  const file = await open(path, 'r');
  try {
    const digest = createHash('sha256');
    const buffer = Buffer.alloc(1 << 20);
    for (;;) {
      signal?.throwIfAborted();
      const { bytesRead } = await file.read(buffer, 0, buffer.length, null);
      if (bytesRead === 0) return digest.digest('hex');
      digest.update(buffer.subarray(0, bytesRead));
    }
  } finally {
    await file.close();
  }
}

/** Publish only after the complete staged executable has passed validation. */
export async function publishExecutable({
  source,
  destination,
  verify,
  signal,
}) {
  if (!isAbsolute(source) || !isAbsolute(destination))
    throw new Error('Executable paths must be absolute.');
  if (typeof verify !== 'function')
    throw new Error('Executable validation is required before installation.');
  signal?.throwIfAborted();
  const sourceState = await fileState(source);
  if (!sourceState?.isFile() || sourceState.isSymbolicLink())
    throw new Error('The built executable must be a regular file.');
  const previous = await fileState(destination);
  if (previous && (!previous.isFile() || previous.isSymbolicLink())) {
    throw new Error(
      `Refusing to replace a directory, symlink, or special file at ${destination}.`,
    );
  }
  const digest = await digestFile(source, signal);
  const directory = dirname(destination);
  await mkdir(directory, { recursive: true });
  // Use a non-hidden name so native executable validation works on macOS.
  const temporary = join(
    directory,
    `axv-install-${randomUUID()}${process.platform === 'win32' ? '.exe' : ''}`,
  );
  let directoryHandle;
  let ownsTemporary = false;
  try {
    await copyFile(source, temporary, constants.COPYFILE_EXCL);
    ownsTemporary = true;
    await chmod(temporary, 0o755);
    if ((await digestFile(temporary, signal)) !== digest)
      throw new Error(
        'The build changed while it was being copied. The installed executable was preserved.',
      );
    // A writable open after execution invalidates macOS code-signature caches.
    const staged = await open(temporary, 'r+');
    try {
      await staged.sync();
    } finally {
      await staged.close();
    }
    const version = await verify(temporary);
    if ((await digestFile(temporary, signal)) !== digest)
      throw new Error(
        'The staged executable changed during verification. The installed executable was preserved.',
      );
    if (!sameFile(previous, await fileState(destination))) {
      throw new Error(
        'The installation destination changed during preparation. Its current contents were preserved.',
      );
    }
    if (process.platform !== 'win32') {
      directoryHandle = await open(directory, 'r');
      await directoryHandle.sync();
    }
    signal?.throwIfAborted();
    await rename(temporary, destination);
    ownsTemporary = false;
    try {
      await directoryHandle?.sync();
    } catch (error) {
      throw new Error(
        `AXV was installed at ${destination}, but flushing the directory failed: ${error.message}`,
        { cause: error },
      );
    }
    return { path: destination, digest, version };
  } finally {
    await directoryHandle?.close();
    if (ownsTemporary) await rm(temporary, { force: true });
  }
}

export async function installProductionExecutable({
  source,
  go,
  cwd,
  env = process.env,
  signal,
}) {
  const environment = await goEnvironment(go, { cwd, env, signal });
  const destination = installationTarget(environment);
  return publishExecutable({
    source,
    destination,
    signal,
    verify: async (temporary) => {
      const { stdout: metadata } = await execute(
        go,
        ['version', '-m', '-json', temporary],
        {
          cwd,
          env,
          signal,
          timeout: 15_000,
          maxBuffer: 1 << 20,
        },
      );
      validateProductionBuild(JSON.parse(metadata), environment);
      const { stdout, stderr } = await execute(temporary, ['version'], {
        cwd,
        env,
        signal,
        timeout: 5000,
        maxBuffer: 64 << 10,
      });
      const expected = `axv ${env.VERSION ?? 'dev'}`;
      if (stdout.trim() !== expected || stderr !== '') {
        throw new Error(
          `The built executable did not report ${expected}. Rebuild with the same VERSION before installing.`,
        );
      }
      return expected;
    },
  });
}

/** Inspect executable PATH entries without executing another axv or editing a shell. */
export async function installationPathDiagnostics(
  destination,
  pathValue = process.env.PATH || '',
  cwd = process.cwd(),
) {
  const expected = await realpath(destination);
  const directory = await realpath(dirname(destination));
  let listed = false;
  let first;
  for (const entry of pathValue.split(delimiter)) {
    const path = resolve(cwd, entry || '.');
    try {
      if ((await realpath(path)) === directory) listed = true;
      if (!first) {
        const candidate = join(path, basename(destination));
        await access(candidate, constants.X_OK);
        if ((await stat(candidate)).isFile())
          first = { path: candidate, real: await realpath(candidate) };
      }
    } catch (error) {
      if (!['ENOENT', 'ENOTDIR', 'EACCES', 'EPERM'].includes(error.code))
        throw error;
    }
  }
  const messages = [];
  if (!listed && first?.real !== expected)
    messages.push(`Add ${directory} to PATH to run axv from any directory.`);
  if (first && first.real !== expected)
    messages.push(
      `Another axv appears earlier on PATH: ${first.path}. Put ${directory} before its directory to use this installation.`,
    );
  return messages;
}
