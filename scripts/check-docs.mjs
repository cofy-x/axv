import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const IGNORED_DIRS = new Set([
  '.cache',
  '.data',
  '.e2e',
  '.git',
  'coverage',
  'dist',
  'node_modules',
  'playwright-report',
  'screenshots',
  'test-results',
]);

const CODE_SCAN_ROOTS = ['.github', 'apps', 'packages', 'scripts'];

const CODE_FILE_EXTS = new Set([
  '.go',
  '.js',
  '.mjs',
  '.cjs',
  '.ts',
  '.tsx',
  '.yml',
  '.yaml',
  '.md',
]);

const LINK_RE = /\[([^\]]*)\]\(([^)]+)\)/g;

const CODE_DOC_REF_RE =
  /(?:^|[\s"'`(,/])(docs\/[A-Za-z0-9._/-]+\.md)(?:#([A-Za-z0-9_-]+))?/g;

export function slugifyHeading(text) {
  return text
    .toLowerCase()
    .replace(/[`*_~]/g, '')
    .replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
    .replace(/[^\p{L}\p{N}\s-]/gu, '')
    .trim()
    .replace(/\s/g, '-');
}

export function collectHeadings(markdown) {
  const seen = new Map();
  const anchors = new Set();
  let inFence = false;

  for (const line of markdown.split(/\r?\n/)) {
    if (/^```/.test(line.trim())) {
      inFence = !inFence;
      continue;
    }
    if (inFence) continue;
    const match = /^(#{1,6})\s+(.*)$/.exec(line);
    if (!match) continue;
    const base = slugifyHeading(match[2]);
    const count = seen.get(base) ?? 0;
    seen.set(base, count + 1);
    anchors.add(count === 0 ? base : `${base}-${count}`);
  }

  return anchors;
}

function listFiles(dir, predicate, out = []) {
  if (!existsSync(dir)) return out;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (IGNORED_DIRS.has(entry.name)) continue;
    const fullPath = join(dir, entry.name);
    if (entry.isDirectory()) {
      listFiles(fullPath, predicate, out);
    } else if (predicate(entry.name, fullPath)) {
      out.push(fullPath);
    }
  }
  return out;
}

function isTestFile(name) {
  return (
    name.endsWith('_test.go') ||
    /\.test\.[cm]?[jt]sx?$/.test(name) ||
    /\.spec\.[cm]?[jt]sx?$/.test(name)
  );
}

function hasCodeExtension(name) {
  const dot = name.lastIndexOf('.');
  return dot >= 0 && CODE_FILE_EXTS.has(name.slice(dot));
}

function extractRelativeTargets(filePath) {
  const targets = new Set();
  const lines = readFileSync(filePath, 'utf8').split(/\r?\n/);
  let inFence = false;

  for (const line of lines) {
    if (/^```/.test(line.trim())) {
      inFence = !inFence;
      continue;
    }
    if (inFence) continue;
    for (const match of line.matchAll(LINK_RE)) {
      const target = match[2].trim();
      if (/^(https?:|mailto:|#)/i.test(target)) continue;
      const hashIndex = target.indexOf('#');
      const filePart = hashIndex >= 0 ? target.slice(0, hashIndex) : target;
      targets.add(resolve(dirname(filePath), filePart));
    }
  }

  return targets;
}

export function checkCodeDocReferences(root, anchorsByFile) {
  const errors = [];
  const codeFiles = [];

  for (const scanRoot of CODE_SCAN_ROOTS) {
    listFiles(
      join(root, scanRoot),
      (name) => hasCodeExtension(name) && !isTestFile(name),
      codeFiles,
    );
  }
  codeFiles.sort();

  for (const file of codeFiles) {
    const relFile = relative(root, file).replace(/\\/g, '/');
    const lines = readFileSync(file, 'utf8').split(/\r?\n/);
    lines.forEach((line, index) => {
      for (const match of line.matchAll(CODE_DOC_REF_RE)) {
        const docRel = match[1];
        const anchor = match[2] ?? '';
        const docPath = resolve(root, docRel);
        if (!existsSync(docPath)) {
          errors.push(`${relFile}:${index + 1} missing doc ${docRel}`);
        } else if (anchor && !anchorsByFile.get(docPath)?.has(anchor)) {
          errors.push(
            `${relFile}:${index + 1} broken doc anchor ${docRel}#${anchor}`,
          );
        }
      }
    });
  }

  return errors;
}

export function checkOrphanDocs(root, markdownFiles) {
  const errors = [];
  const docsIndex = resolve(root, 'docs/README.md');
  const apiIndex = resolve(root, 'docs/api/README.md');

  if (existsSync(docsIndex)) {
    const linkedFromDocs = extractRelativeTargets(docsIndex);
    for (const file of markdownFiles) {
      const rel = relative(root, file).replace(/\\/g, '/');
      if (
        /^docs\/[^/]+\.md$/.test(rel) &&
        rel !== 'docs/README.md' &&
        !linkedFromDocs.has(file)
      ) {
        errors.push(`orphan doc not indexed in docs/README.md: ${rel}`);
      }
    }
  }

  if (existsSync(apiIndex)) {
    const linkedFromApi = extractRelativeTargets(apiIndex);
    for (const file of markdownFiles) {
      const rel = relative(root, file).replace(/\\/g, '/');
      if (
        /^docs\/api\/[^/]+\.md$/.test(rel) &&
        rel !== 'docs/api/README.md' &&
        !linkedFromApi.has(file)
      ) {
        errors.push(`orphan API doc not indexed in docs/api/README.md: ${rel}`);
      }
    }
  }

  return errors;
}

export function checkMarkdownLinks(
  rootDir,
  { checkCodeRefs = true, checkOrphans = true } = {},
) {
  const root = resolve(rootDir);
  const files = listFiles(root, (name) => name.endsWith('.md')).sort();
  const anchorsByFile = new Map();

  for (const file of files) {
    anchorsByFile.set(file, collectHeadings(readFileSync(file, 'utf8')));
  }

  const errors = [];

  for (const file of files) {
    const relFile = relative(root, file).replace(/\\/g, '/');
    const lines = readFileSync(file, 'utf8').split(/\r?\n/);
    let inFence = false;

    lines.forEach((line, index) => {
      if (/^```/.test(line.trim())) {
        inFence = !inFence;
        return;
      }
      if (inFence) return;

      for (const match of line.matchAll(LINK_RE)) {
        const target = match[2].trim();
        if (/^(https?:|mailto:)/i.test(target)) continue;

        if (target.startsWith('#')) {
          const anchor = target.slice(1);
          if (!anchorsByFile.get(file)?.has(anchor)) {
            errors.push(`${relFile}:${index + 1} broken self-anchor ${target}`);
          }
          continue;
        }

        const hashIndex = target.indexOf('#');
        const filePart = hashIndex >= 0 ? target.slice(0, hashIndex) : target;
        const fragment = hashIndex >= 0 ? target.slice(hashIndex + 1) : '';
        const resolved = resolve(dirname(file), filePart);

        if (!existsSync(resolved)) {
          errors.push(`${relFile}:${index + 1} missing path ${target}`);
        } else if (fragment && resolved.endsWith('.md')) {
          const anchors = anchorsByFile.get(resolved);
          if (anchors && !anchors.has(fragment)) {
            errors.push(`${relFile}:${index + 1} broken anchor ${target}`);
          }
        }
      }
    });
  }

  if (checkCodeRefs) {
    errors.push(...checkCodeDocReferences(root, anchorsByFile));
  }
  if (checkOrphans) {
    errors.push(...checkOrphanDocs(root, files));
  }

  return { fileCount: files.length, errors };
}

function main() {
  const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  const { fileCount, errors } = checkMarkdownLinks(repoRoot);

  if (errors.length > 0) {
    for (const error of errors) {
      console.error(error);
    }
    console.error(
      `Documentation check failed: ${errors.length} issue(s) across ${fileCount} Markdown files.`,
    );
    process.exit(1);
  }

  console.log(
    `Documentation check passed (${fileCount} Markdown files, 0 broken links, code refs or orphan docs).`,
  );
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  main();
}
