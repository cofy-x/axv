import {
  copyFile,
  lstat,
  mkdir,
  readFile,
  readdir,
  writeFile,
} from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { dirname, join, relative } from 'node:path';
import { createRequire } from 'node:module';
import { captureCommand, go, root } from './lib.mjs';

const noticeName =
  /^(?:(?:licen[sc]e|copying|copyright|notice|patents)(?:[-_.]|$)|third[-_ ]party[-_ ](?:licen[sc]es?|notices?)(?:[-_. ]|$))/i;

export async function findNoticeFiles(directory, recursive = true) {
  const files = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (entry.isFile() && noticeName.test(entry.name))
      files.push(join(directory, entry.name));
    else if (
      recursive &&
      entry.isDirectory() &&
      !entry.name.startsWith('.') &&
      entry.name !== 'node_modules'
    )
      files.push(...(await findNoticeFiles(join(directory, entry.name))));
  }
  return files.sort();
}

async function captureJSON(executable, args, options) {
  return JSON.parse((await captureCommand(executable, args, options)).stdout);
}

function noticeCollector(output, inventory) {
  return async (kind, name, version, directory, metadata = {}, files) => {
    files ??= await findNoticeFiles(directory);
    if (
      !files.some((file) =>
        /^(licen[sc]e|copying)/i.test(file.split('/').at(-1)),
      )
    )
      throw new Error(`No license text found for ${name}@${version}.`);
    const prefix = join(kind, `${encodeURIComponent(name)}@${version}`);
    const retained = [];
    for (const file of files) {
      const target = join(prefix, relative(directory, file));
      await mkdir(dirname(join(output, target)), { recursive: true });
      await copyFile(file, join(output, target));
      retained.push(target);
    }
    inventory.push({ kind, name, version, ...metadata, files: retained });
  };
}

async function catalogFile(directory, path) {
  if (
    typeof path !== 'string' ||
    path.includes('\\') ||
    path.split('/').some((part) => !part || part === '.' || part === '..')
  )
    throw new Error(
      'Supplemental notice paths must stay inside their catalog.',
    );
  let current = directory;
  const parts = path.split('/');
  for (const [index, part] of parts.entries()) {
    current = join(current, part);
    const state = await lstat(current);
    if (
      state.isSymbolicLink() ||
      (index === parts.length - 1 ? !state.isFile() : !state.isDirectory())
    )
      throw new Error(
        'Supplemental notice paths must be regular, not symlinks.',
      );
  }
  return current;
}

export async function collectSupplementalNotices(
  output,
  buildInfo,
  directory = join(root, 'third_party/notices'),
) {
  const state = await lstat(directory);
  if (!state.isDirectory() || state.isSymbolicLink())
    throw new Error(
      'The supplemental notice catalog must be a real directory.',
    );
  const entries = JSON.parse(
    await readFile(await catalogFile(directory, 'manifest.json'), 'utf8'),
  );
  const approved = [];
  for (const entry of entries) {
    const dependency = buildInfo.Deps?.find(
      ({ Path }) => Path === entry.via.module,
    );
    if (!dependency) continue;
    if (dependency.Version !== entry.via.version)
      throw new Error(
        `Re-review embedded assets for ${dependency.Path}@${dependency.Version}; supplemental notices cover ${entry.via.version}.`,
      );
    if (
      entry.kind !== 'embedded-asset' ||
      typeof entry.name !== 'string' ||
      !entry.name ||
      typeof entry.version !== 'string' ||
      !/^[0-9A-Za-z._+-]+$/.test(entry.version)
    )
      throw new Error('Invalid supplemental notice identity.');
    const files = [];
    for (const file of entry.files) {
      const path = await catalogFile(directory, file.path);
      const bytes = await readFile(path);
      if (
        !bytes.length ||
        createHash('sha256').update(bytes).digest('hex') !== file.sha256
      )
        throw new Error(`Supplemental notice checksum mismatch: ${file.path}.`);
      files.push(path);
    }
    approved.push({ entry, files });
  }
  const inventory = [];
  const collect = noticeCollector(output, inventory);
  for (const { entry, files } of approved) {
    const { kind, name, version, files: licenseSources, ...metadata } = entry;
    await collect(
      kind,
      name,
      version,
      directory,
      { ...metadata, licenseSources },
      files,
    );
  }
  return inventory;
}

/** Collect only the toolchain and modules actually linked into this binary. */
export async function collectGoNotices(output, buildInfo) {
  const dependencies = buildInfo.Deps ?? [];
  if (
    dependencies.some(
      ({ Path }) => Path === 'github.com/tailscale/web-client-prebuilt',
    )
  )
    throw new Error(
      'The binary links the Tailscale node web client, whose embedded frontend and font notices are not collected. Rebuild with ts_omit_webclient before packaging.',
    );
  const inventory = [];
  const collect = noticeCollector(output, inventory);
  const toolchain = await captureJSON(go, [
    'env',
    '-json',
    'GOROOT',
    'GOVERSION',
  ]);
  if (toolchain.GOVERSION !== buildInfo.GoVersion)
    throw new Error(
      "Use the binary's exact Go toolchain to collect its license notices.",
    );
  const goroot = toolchain.GOROOT;
  const goFiles = [
    ...(await findNoticeFiles(goroot, false)),
    ...(await findNoticeFiles(join(goroot, 'src', 'vendor'))),
    ...(await findNoticeFiles(
      join(goroot, 'src', 'crypto', 'internal', 'boring'),
    )),
  ];
  await collect(
    'toolchain',
    'Go',
    buildInfo.GoVersion,
    goroot,
    {
      source: 'https://go.dev/LICENSE',
    },
    goFiles,
  );
  for (const dependency of dependencies) {
    if (dependency.Replace)
      throw new Error(
        `Review replaced Go module ${dependency.Path} before packaging.`,
      );
  }
  const moduleOptions = {
    cwd: join(root, 'apps', 'server'),
    env: { ...process.env, GOWORK: 'off', GOTOOLCHAIN: 'local' },
  };
  const graph = new Set(
    (
      await captureCommand(
        go,
        [
          'list',
          '-m',
          '-f',
          '{{if not .Replace}}{{.Path}}@{{.Version}}{{end}}',
          'all',
        ],
        moduleOptions,
      )
    ).stdout.split(/\r?\n/),
  );
  const sources = [];
  for (const dependency of dependencies) {
    if (!graph.has(`${dependency.Path}@${dependency.Version}`))
      throw new Error(
        `Rebuild with this checkout's Go module versions: ${dependency.Path}.`,
      );
    const module = await captureJSON(
      go,
      ['mod', 'download', '-json', `${dependency.Path}@${dependency.Version}`],
      moduleOptions,
    );
    if (!module.Dir || module.Sum !== dependency.Sum)
      throw new Error(
        `Module source does not match binary: ${dependency.Path}.`,
      );
    sources.push({ dependency, module });
  }
  // download reports cached checksums; verify rehashes the extracted files.
  await captureCommand(go, ['mod', 'verify'], moduleOptions);
  for (const { dependency, module } of sources) {
    await collect('go', dependency.Path, dependency.Version, module.Dir, {
      source: `https://pkg.go.dev/${dependency.Path}@${dependency.Version}`,
      checksum: dependency.Sum,
    });
  }
  inventory.push(...(await collectSupplementalNotices(output, buildInfo)));
  return inventory;
}

/** Retain original texts; package metadata alone is not a license notice. */
export async function writeReleaseNotices(output, buildInfo) {
  const inventory = await collectGoNotices(output, buildInfo);
  const collect = noticeCollector(output, inventory);
  const packages = await captureJSON('pnpm', [
    'licenses',
    'list',
    '--prod',
    '--json',
    '--recursive',
  ]);
  const visited = new Set();
  for (const group of Object.values(packages)) {
    for (const entry of group) {
      for (const directory of entry.paths) {
        const manifest = JSON.parse(
          await readFile(join(directory, 'package.json'), 'utf8'),
        );
        const identity = `${manifest.name}@${manifest.version}`;
        if (visited.has(identity)) continue;
        visited.add(identity);
        if (
          !manifest.license ||
          /^(UNLICENSED|UNKNOWN)$/i.test(manifest.license)
        )
          throw new Error(`Review missing license metadata for ${identity}.`);
        await collect('npm', manifest.name, manifest.version, directory, {
          license: manifest.license,
          source: `https://www.npmjs.com/package/${manifest.name}/v/${manifest.version}`,
        });
      }
    }
  }
  if (!visited.size)
    throw new Error('No production JavaScript licenses found. Run make deps.');
  // Their generated preload/interoperability helpers are embedded in the UI.
  const require = createRequire(join(root, 'package.json'));
  const vite = require.resolve('vite/package.json');
  for (const path of [
    vite,
    createRequire(vite).resolve('rolldown/package.json'),
  ]) {
    const manifest = JSON.parse(await readFile(path, 'utf8'));
    await collect(
      'build-helper',
      manifest.name,
      manifest.version,
      dirname(path),
      {
        license: manifest.license,
        source: `https://www.npmjs.com/package/${manifest.name}/v/${manifest.version}`,
      },
    );
  }
  inventory.sort((a, b) =>
    `${a.kind}/${a.name}@${a.version}`.localeCompare(
      `${b.kind}/${b.name}@${b.version}`,
    ),
  );
  await writeFile(
    join(output, 'manifest.json'),
    `${JSON.stringify(inventory, null, 2)}\n`,
  );
  await copyFile(
    join(root, 'THIRD_PARTY_NOTICES.md'),
    join(output, '..', 'THIRD_PARTY_NOTICES.md'),
  );
  return inventory;
}
