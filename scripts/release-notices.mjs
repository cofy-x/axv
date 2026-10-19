import {
  copyFile,
  mkdir,
  readFile,
  readdir,
  writeFile,
} from 'node:fs/promises';
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

/** Collect only the toolchain and modules actually linked into this binary. */
export async function collectGoNotices(output, buildInfo) {
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
  const dependencies = buildInfo.Deps ?? [];
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
