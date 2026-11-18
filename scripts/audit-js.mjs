import { captureCommand, dispose, root } from './lib.mjs';
import { initializeTestTemp } from './test-temp.mjs';
import { verifyGlobPatches } from './verify-glob-patches.mjs';

const advisoryId = 'GHSA-fc5q-39xc-vjpf';
const levels = ['info', 'low', 'moderate', 'high', 'critical'];
const blockingLevels = levels.slice(1);
const record = (value) =>
  value !== null && typeof value === 'object' && !Array.isArray(value);

function blockingAdvisories({ stdout, stderr, code }) {
  const report = JSON.parse(stdout);
  const counts = report?.metadata?.vulnerabilities;
  if (
    !record(report?.advisories) ||
    !record(counts) ||
    Object.hasOwn(report, 'error')
  )
    throw new Error('pnpm did not return a complete audit report.');

  const advisories = Object.values(report.advisories);
  if (advisories.some((entry) => !levels.includes(entry?.severity)))
    throw new Error('pnpm returned an unknown advisory severity.');
  for (const level of blockingLevels) {
    if (
      counts[level] !==
      advisories.filter((entry) => entry.severity === level).length
    )
      throw new Error('pnpm audit counts do not match its advisory records.');
  }
  const blocking = advisories.filter((entry) => entry.severity !== 'info');
  if (code !== (blocking.length ? 1 : 0) || (code !== 0 && stderr.trim()))
    throw new Error('pnpm audit did not complete with a recognized result.');
  return blocking;
}

export async function assessAudit(result, verify) {
  const blocking = blockingAdvisories(result);
  if (!blocking.length) return;

  const [advisory] = blocking;
  if (
    blocking.length !== 1 ||
    advisory.github_advisory_id !== advisoryId ||
    advisory.module_name !== 'picomatch' ||
    advisory.severity !== 'high' ||
    advisory.vulnerable_versions !== '<=4.0.7' ||
    advisory.patched_versions !== null ||
    !Array.isArray(advisory.findings) ||
    !advisory.findings.length ||
    advisory.findings.some(
      (finding) =>
        finding?.version !== '4.0.7' ||
        finding.dev !== true ||
        finding.bundled !== false ||
        finding.optional !== false ||
        !Array.isArray(finding.paths) ||
        !finding.paths.length ||
        finding.paths.some((path) => typeof path !== 'string'),
    )
  )
    throw new Error('Unresolved JavaScript dependency advisories.');

  const paths = [
    ...new Set(advisory.findings.flatMap((finding) => finding.paths)),
  ];
  const evidence = await verify(paths);
  return { advisoryId, ...evidence };
}

if (import.meta.main) {
  initializeTestTemp();
  let stopping = false;
  const shutdown = async (status) => {
    if (stopping) return;
    stopping = true;
    process.exitCode = status;
    try {
      await dispose();
    } catch (error) {
      console.error('Launcher cleanup failed:', error);
      process.exitCode = 1;
    }
  };
  process.once('SIGINT', () => void shutdown(130));
  process.once('SIGTERM', () => void shutdown(143));
  try {
    const [pnpm = 'pnpm', ...args] = process.argv.slice(2);
    const result = await captureCommand(
      pnpm,
      [...args, 'audit', '--audit-level=low', '--json'],
      { allowFailure: true },
    );
    // Keep the registry result visible even when an installed patch is proven.
    process.stdout.write(result.stdout);
    process.stderr.write(result.stderr);
    const verified = await assessAudit(result, async (paths) => {
      const { stdout } = await captureCommand(pnpm, [
        ...args,
        'list',
        '--recursive',
        '--depth=-1',
        '--json',
      ]);
      return verifyGlobPatches(root, JSON.parse(stdout), paths);
    });
    if (verified)
      console.log(
        `${verified.advisoryId}: locally mitigated. Verified both patch digests, ${verified.paths} dependency paths, ${verified.copies} distinct picomatch installations, and the bundled Prettier matcher.`,
      );
    else console.log('No blocking JavaScript dependency advisories.');
    await shutdown(0);
  } catch (error) {
    if (!stopping) console.error(error);
    await shutdown(1);
  }
}
