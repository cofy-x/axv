import { lstat, mkdir, readFile, writeFile } from 'node:fs/promises';
import { isAbsolute, join, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureCommand, dispose, go, productionTags, root } from './lib.mjs';
import { initializeTestTemp } from './test-temp.mjs';

const projectPrefix = 'apps/server/';

function projectFile(path) {
  return (
    typeof path === 'string' &&
    path.startsWith(projectPrefix) &&
    path.endsWith('.go') &&
    !path.split('/').some((part) => ['.', '..', ''].includes(part))
  );
}

function sortedUnique(values) {
  return [...new Set(values)].sort();
}

export function parsePackages(stdout) {
  // go list emits pretty-printed JSON objects separated by newlines. Only root
  // object delimiters occupy column zero; strings cannot contain raw newlines.
  return JSON.parse(`[${stdout.trim().replace(/^}\r?\n\{/gm, '},{')}]`);
}

export function inventoryFiles(packages, tracked) {
  const expected = sortedUnique(
    packages.flatMap((entry) =>
      (entry.GoFiles || []).map((name) =>
        relative(root, join(entry.Dir, name)).split(sep).join('/'),
      ),
    ),
  );
  if (!expected.length || expected.some((path) => !projectFile(path)))
    throw new Error(
      'Go inventory did not select valid AXV server source files.',
    );
  const selected = new Set(expected);
  return {
    packages: packages.length,
    expected,
    excluded: sortedUnique(tracked.filter(projectFile))
      .filter((path) => !selected.has(path))
      .map((path) => ({
        path,
        reason: path.endsWith('_test.go')
          ? 'Go test files are excluded from extraction'
          : 'Not selected by the platform and production build tags',
      })),
  };
}

export function coverageReport(inventory, decoded) {
  const tuples = decoded?.['#select']?.tuples;
  if (
    !Array.isArray(tuples) ||
    tuples.some(
      (tuple) =>
        !Array.isArray(tuple) || tuple.length !== 1 || !projectFile(tuple[0]),
    )
  )
    throw new Error('CodeQL returned an invalid extracted-file result.');
  if (
    !Array.isArray(inventory.expected) ||
    !inventory.expected.length ||
    inventory.expected.some((path) => !projectFile(path))
  )
    throw new Error('The saved Go source inventory is invalid.');
  const extracted = sortedUnique(tuples.map(([path]) => path));
  const selected = new Set(extracted);
  const missing = inventory.expected.filter((path) => !selected.has(path));
  return {
    ...inventory,
    extracted,
    missing,
    complete: missing.length === 0,
  };
}

async function evidenceDirectory(requested) {
  const temporary = initializeTestTemp();
  const directory = resolve(requested);
  const local = relative(temporary.root, directory);
  if (!local || isAbsolute(local) || local.split(sep).includes('..'))
    throw new Error('CodeQL coverage output must be below AXV_TEST_TMPDIR.');
  let current = temporary.root;
  for (const part of local.split(sep)) {
    current = join(current, part);
    await mkdir(current, { mode: 0o700 }).catch((error) => {
      if (error.code !== 'EEXIST') throw error;
    });
    const state = await lstat(current);
    if (!state.isDirectory() || state.isSymbolicLink())
      throw new Error('CodeQL coverage output cannot contain symbolic links.');
  }
  return directory;
}

async function save(directory, name, value) {
  await writeFile(
    join(directory, name),
    `${JSON.stringify(value, null, 2)}\n`,
    {
      flag: 'wx',
      mode: 0o600,
    },
  );
}

async function recordInventory(directory) {
  const env = {
    ...process.env,
    CGO_ENABLED: '0',
    GOFLAGS: '-mod=readonly',
    GOTOOLCHAIN: 'local',
    GOWORK: join(root, 'go.work'),
  };
  const { stdout: configuration } = await captureCommand(
    go,
    ['env', '-json', 'GOOS', 'GOARCH', 'GOVERSION'],
    { env },
  );
  const { stdout: packageJSON } = await captureCommand(
    go,
    ['list', '-json', '-tags', productionTags, './apps/server/...'],
    { env, timeout: 5 * 60_000 },
  );
  const { stdout: tracked } = await captureCommand('git', [
    'ls-files',
    '-z',
    '--',
    'apps/server',
  ]);
  const inventory = {
    ...JSON.parse(configuration),
    tags: productionTags,
    ...inventoryFiles(parsePackages(packageJSON), tracked.split('\0')),
  };
  await save(directory, 'inventory.json', inventory);
  console.log(
    `Inventoried ${inventory.expected.length} Go files in ${inventory.packages} packages (${inventory.GOOS}/${inventory.GOARCH}).`,
  );
  return env;
}

async function verify(directory, codeql, database) {
  const inventory = JSON.parse(
    await readFile(join(directory, 'inventory.json'), 'utf8'),
  );
  const bqrs = join(directory, 'extracted-files.bqrs');
  await captureCommand(
    codeql,
    [
      'query',
      'run',
      `--database=${resolve(database)}`,
      `--output=${bqrs}`,
      join(root, '.github/codeql/extracted-files.ql'),
    ],
    { timeout: 5 * 60_000 },
  );
  const { stdout } = await captureCommand(codeql, [
    'bqrs',
    'decode',
    '--format=json',
    bqrs,
  ]);
  const report = coverageReport(inventory, JSON.parse(stdout));
  await save(directory, 'coverage.json', report);
  if (!report.complete)
    throw new Error(
      `CodeQL missed ${report.missing.length} selected Go files:\n${report.missing.join('\n')}`,
    );
  console.log(
    `Verified extraction of all ${report.expected.length} selected Go files; ${report.excluded.length} excluded source/test files are recorded in coverage.json.`,
  );
}

async function main(args) {
  const [action, output, codeql, database] = args;
  if (
    !output ||
    !['inventory', 'build', 'verify'].includes(action) ||
    (action === 'verify' ? args.length !== 4 : args.length !== 2)
  )
    throw new Error(
      'Usage: codeql-coverage.mjs inventory|build <output-directory> | verify <output-directory> <codeql-binary> <go-database>',
    );
  const directory = await evidenceDirectory(output);
  if (action === 'verify') return verify(directory, codeql, database);
  const env = await recordInventory(directory);
  if (action === 'build')
    await captureCommand(
      go,
      ['build', '-a', '-tags', productionTags, './apps/server/...'],
      { env, timeout: 20 * 60_000 },
    );
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  for (const [signal, code] of [
    ['SIGINT', 130],
    ['SIGTERM', 143],
  ]) {
    process.once(signal, () => {
      void dispose().then(
        () => process.exit(code),
        () => process.exit(1),
      );
    });
  }
  try {
    await main(process.argv.slice(2));
  } catch (error) {
    console.error(error);
    process.exitCode = 1;
  } finally {
    await dispose();
  }
}
