import { execFileSync } from 'node:child_process';
import { appendFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const ROOT_DOC_FILES = new Set([
  'AGENTS.md',
  'CODE_OF_CONDUCT.md',
  'CONTRIBUTING.md',
  'README.md',
  'SECURITY.md',
  'SUPPORT.md',
  'THIRD_PARTY_NOTICES.md',
]);

const NON_RUNTIME_EXACT = new Set([
  '.github/dependabot.yml',
  '.github/pull_request_template.md',
  '.github/workflows/codeql.yml',
  '.github/workflows/guest-images.yml',
  '.github/workflows/release.yml',
]);

const MACOS_NATIVE_EXACT = new Set([
  '.github/workflows/ci.yml',
  '.go-version',
  'Makefile',
  'go.work',
  'go.work.sum',
  'scripts/ci-changes.mjs',
  'scripts/lib.mjs',
  'scripts/test-go.mjs',
]);

const E2E_EXACT = new Set([
  '.github/workflows/ci.yml',
  '.go-version',
  '.node-version',
  'Makefile',
  'go.work',
  'go.work.sum',
  'package.json',
  'playwright.config.ts',
  'pnpm-lock.yaml',
  'pnpm-workspace.yaml',
  'scripts/build.mjs',
  'scripts/ci-changes.mjs',
  'scripts/dashboard-bundle-budget.json',
  'scripts/dashboard-bundle.mjs',
  'scripts/lib.mjs',
  'tsconfig.json',
  'vitest.config.ts',
]);

const NON_BROWSER_SERVER_PREFIXES = [
  'apps/server/internal/cli/',
  'apps/server/internal/compute/hostinstall/',
  'apps/server/internal/daemon/',
];

const PR_BROWSER_PROJECTS = [{ project: 'chromium', browser: 'chromium' }];

const FULL_BROWSER_PROJECTS = [
  { project: 'chromium', browser: 'chromium' },
  { project: 'firefox', browser: 'firefox' },
  { project: 'webkit', browser: 'webkit' },
  { project: 'chromium-dpr2', browser: 'chromium' },
  { project: 'chromium-touch', browser: 'chromium' },
  { project: 'webkit-touch', browser: 'webkit' },
];

const SHARDS = [1, 2, 3];

export function isNonRuntimePath(file) {
  if (ROOT_DOC_FILES.has(file) || NON_RUNTIME_EXACT.has(file)) return true;
  if (
    file.startsWith('docs/') ||
    file.startsWith('images/guest/') ||
    file.startsWith('.github/ISSUE_TEMPLATE/')
  )
    return true;
  if (file.startsWith('recipes/') && file.endsWith('.md')) return true;
  return false;
}

export function isMacOSNativePath(file) {
  return file.startsWith('apps/server/') || MACOS_NATIVE_EXACT.has(file);
}

export function isE2EPath(file) {
  if (E2E_EXACT.has(file)) return true;
  if (
    file.startsWith('apps/dashboard/') ||
    file.startsWith('packages/nextstep/') ||
    file.startsWith('tests/') ||
    file.startsWith('scripts/test-e2e')
  )
    return true;
  if (file.startsWith('apps/server/')) {
    return !NON_BROWSER_SERVER_PREFIXES.some((prefix) =>
      file.startsWith(prefix),
    );
  }
  return false;
}

export function buildBrowserMatrix(enabled, full) {
  if (!enabled) return { include: [] };
  const projects = full ? FULL_BROWSER_PROJECTS : PR_BROWSER_PROJECTS;
  const include = [];
  for (const entry of projects) {
    for (const shard of SHARDS) {
      include.push({ ...entry, shard });
    }
  }
  return { include };
}

export function classifyChanges(
  files,
  { eventName = 'pull_request', fullBrowserMatrix = true } = {},
) {
  const normalized = (files ?? [])
    .map((line) => line.trim())
    .filter((line) => line.length > 0);
  const guestScripts = normalized.some(
    (file) => file.startsWith('images/guest/') && file.endsWith('.sh'),
  );

  if (eventName !== 'pull_request') {
    const fullE2E = fullBrowserMatrix !== false;
    return {
      runtime: true,
      macosNative: true,
      security: true,
      e2e: true,
      fullE2E,
      guestScripts: true,
      browserMatrix: buildBrowserMatrix(true, fullE2E),
    };
  }

  // Fail-safe: if changed files cannot be determined on a PR, run full PR checks.
  if (normalized.length === 0) {
    return {
      runtime: true,
      macosNative: true,
      security: true,
      e2e: true,
      fullE2E: false,
      guestScripts: false,
      browserMatrix: buildBrowserMatrix(true, false),
    };
  }

  const runtime = normalized.some((file) => !isNonRuntimePath(file));
  const macosNative = runtime && normalized.some(isMacOSNativePath);
  const security = runtime;
  const e2e = runtime && normalized.some(isE2EPath);
  const fullE2E = false;

  return {
    runtime,
    macosNative,
    security,
    e2e,
    fullE2E,
    guestScripts,
    browserMatrix: buildBrowserMatrix(e2e, fullE2E),
  };
}

function readChangedFilesFromGit() {
  try {
    execFileSync('git', ['rev-parse', '--verify', '--quiet', 'HEAD^2'], {
      stdio: 'ignore',
    });
    const output = execFileSync(
      'git',
      ['diff', '--name-only', 'HEAD^1', 'HEAD'],
      { encoding: 'utf8' },
    );
    return output.split(/\r?\n/).filter(Boolean);
  } catch {
    return [];
  }
}

function main() {
  const eventName = process.env.GITHUB_EVENT_NAME || 'pull_request';
  const fullBrowserMatrix = process.env.FULL_BROWSER_MATRIX !== 'false';
  const files = eventName === 'pull_request' ? readChangedFilesFromGit() : [];
  const result = classifyChanges(files, { eventName, fullBrowserMatrix });

  const outputs = [
    `runtime=${result.runtime}`,
    `macos_native=${result.macosNative}`,
    `security=${result.security}`,
    `e2e=${result.e2e}`,
    `full_e2e=${result.fullE2E}`,
    `guest_scripts=${result.guestScripts}`,
    `browser_matrix=${JSON.stringify(result.browserMatrix)}`,
  ];

  if (process.env.GITHUB_OUTPUT) {
    appendFileSync(process.env.GITHUB_OUTPUT, `${outputs.join('\n')}\n`);
  }
  console.log(
    JSON.stringify(
      {
        eventName,
        changedFileCount: files.length,
        ...result,
        browserShardCount: result.browserMatrix.include.length,
      },
      null,
      2,
    ),
  );
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  main();
}
