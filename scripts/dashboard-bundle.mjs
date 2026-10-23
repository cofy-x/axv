import { gzipSync } from 'node:zlib';
import { createHash } from 'node:crypto';
import { constants } from 'node:fs';
import { open, readFile } from 'node:fs/promises';
import { join, relative, resolve } from 'node:path';
import { prepareTestOutputPath } from './test-output.mjs';

/** Count the complete static entry graph once, including shared preloads. */
export function analyzeDashboardBundle(bundle, root) {
  const chunks = Object.values(bundle)
    .filter((item) => item.type === 'chunk')
    .map((chunk) => ({
      fileName: chunk.fileName,
      isEntry: chunk.isEntry,
      imports: chunk.imports,
      dynamicImports: chunk.dynamicImports,
      rawBytes: Buffer.byteLength(chunk.code),
      sha256: createHash('sha256').update(chunk.code).digest('hex'),
      gzipEstimateBytes: gzipSync(chunk.code).byteLength,
      modules: Object.entries(chunk.modules).map(([id, module]) => ({
        id: id.startsWith(`${root}/`) ? relative(root, id) : id,
        renderedLength: module.renderedLength,
        originalLength: module.originalLength,
      })),
    }))
    .sort((left, right) => left.fileName.localeCompare(right.fileName));
  const byName = new Map(chunks.map((chunk) => [chunk.fileName, chunk]));
  const initial = new Set();
  const visit = (name) => {
    if (initial.has(name)) return;
    const chunk = byName.get(name);
    if (!chunk) throw new Error(`Missing static JavaScript import: ${name}`);
    initial.add(name);
    for (const dependency of chunk.imports) visit(dependency);
  };
  const entries = chunks.filter((chunk) => chunk.isEntry);
  if (entries.length === 0)
    throw new Error('The dashboard has no JavaScript entry chunk.');
  for (const entry of entries) visit(entry.fileName);
  const initialChunks = chunks.filter((chunk) => initial.has(chunk.fileName));
  return {
    schemaVersion: 1,
    interpretation: {
      initialGraph:
        'All emitted entry chunks and their recursive static imports, counted once; dynamic imports are excluded until requested.',
      gzipEstimate:
        'Sum of independent Node zlib default gzip streams, not actual Go HTTP gzip response bytes or network transfer.',
      moduleAttribution:
        'Rendered module lengths describe contribution before chunk compression; they are not additive final minified or compressed byte contributions.',
    },
    initial: {
      files: initialChunks.map((chunk) => chunk.fileName),
      rawBytes: initialChunks.reduce(
        (total, chunk) => total + chunk.rawBytes,
        0,
      ),
      gzipEstimateBytes: initialChunks.reduce(
        (total, chunk) => total + chunk.gzipEstimateBytes,
        0,
      ),
    },
    chunks,
  };
}

export function checkDashboardBudget(report, budget) {
  for (const key of ['rawBytes', 'gzipEstimateBytes']) {
    if (!Number.isSafeInteger(budget[key]) || budget[key] <= 0)
      throw new Error(`Invalid dashboard startup budget: ${key}`);
    if (report.initial[key] > budget[key])
      throw new Error(
        `Dashboard startup ${key} is ${report.initial[key]} bytes; budget is ${budget[key]}. Review the full static entry graph before changing the budget.`,
      );
  }
}

/** Build evidence stays outside the served UI; ordinary builds enforce bytes. */
export function dashboardBundlePlugin({ root, budgetPath, reportPath }) {
  let preloadPolicy;
  return {
    name: 'axv-dashboard-bundle-budget',
    apply: 'build',
    configResolved(config) {
      // Vite rewrites preload dependencies after its chunk hashes are assigned.
      // Include the self-contained resolver and options in the bundler hash.
      preloadPolicy = JSON.stringify(
        config.build.modulePreload,
        (_key, value) =>
          typeof value === 'function' ? value.toString() : value,
      );
    },
    augmentChunkHash() {
      return preloadPolicy;
    },
    async writeBundle(options, bundle) {
      if (!options.dir)
        throw new Error('The dashboard bundle requires a directory output.');
      // Vite may still rewrite chunk code in later generateBundle hooks.
      // Read the emitted artifact for exact bytes and a verifiable digest.
      const emitted = Object.fromEntries(
        await Promise.all(
          Object.entries(bundle).map(async ([name, item]) => [
            name,
            item.type === 'chunk'
              ? {
                  ...item,
                  code: await readFile(
                    join(options.dir, item.fileName),
                    'utf8',
                  ),
                }
              : item,
          ]),
        ),
      );
      const report = analyzeDashboardBundle(emitted, root);
      const budget = budgetPath
        ? JSON.parse(await readFile(budgetPath, 'utf8'))
        : null;
      if (reportPath) {
        const allowedRoot = resolve(
          process.env.AXV_TEST_TMPDIR || resolve(root, '.data/test'),
        );
        const destination = await prepareTestOutputPath(
          allowedRoot,
          resolve(root, reportPath),
          { excludedDirectories: [options.dir] },
        );
        const file = await open(
          destination,
          constants.O_WRONLY |
            constants.O_CREAT |
            constants.O_TRUNC |
            (constants.O_NOFOLLOW || 0),
          0o600,
        );
        try {
          await file.writeFile(`${JSON.stringify(report, null, 2)}\n`);
        } finally {
          await file.close();
        }
      }
      if (budget) checkDashboardBudget(report, budget);
      console.log(
        `Dashboard startup graph: ${report.initial.rawBytes} raw bytes, ${report.initial.gzipEstimateBytes} estimated gzip bytes (${report.initial.files.length} chunks).`,
      );
    },
  };
}
