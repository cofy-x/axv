import { lstat } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { fileURLToPath } from 'node:url';
import { captureCommand, exitAfterCleanup } from './lib.mjs';

const repository = 'cofy-x/axv';
// Keep the immutable repository identity as well as its human-readable name.
const repositoryID = '1424477361';
const usage =
  'Usage: node scripts/verify-release-provenance.mjs --kind app|guest --version <version> --source <full-commit-sha> --directory <download-directory>';

export function releaseProvenancePolicy({ kind, version, source, directory }) {
  if (!['app', 'guest'].includes(kind)) throw new Error(usage);
  if (
    typeof version !== 'string' ||
    version.trim() !== version ||
    !/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$/.test(
      version,
    ) ||
    version
      .split('-')
      .slice(1)
      .join('-')
      .split('.')
      .some((part) => /^0\d+$/.test(part))
  )
    throw new Error('Select a release version such as 0.1.0 or 0.1.0-rc.1.');
  if (
    typeof source !== 'string' ||
    source.length !== 40 ||
    !/^[a-f0-9]{40}$/.test(source)
  )
    throw new Error('Select the reviewed full 40-character source commit SHA.');
  if (typeof directory !== 'string' || !directory.trim())
    throw new Error(usage);

  const download = resolve(directory);
  const name =
    kind === 'app'
      ? `axv_${version}_linux_amd64`
      : `axv_guest_${version}_firecracker_amd64`;
  const subjects =
    kind === 'app'
      ? [`${name}.tar.gz`, 'SHA256SUMS']
      : [`${name}.tar.gz`, `${name}.ext4`, 'candidate.json', 'SHA256SUMS'];
  const workflow = kind === 'app' ? 'release.yml' : 'guest-images.yml';
  return {
    directory: download,
    subjects,
    bundle: join(download, 'provenance.json'),
    arguments: [
      '--repo',
      repository,
      '--signer-workflow',
      `${repository}/.github/workflows/${workflow}`,
      '--signer-digest',
      source,
      '--source-digest',
      source,
      '--source-ref',
      'refs/heads/main',
      '--predicate-type',
      'https://slsa.dev/provenance/v1',
      '--deny-self-hosted-runners',
      '--bundle',
      join(download, 'provenance.json'),
      '--format',
      'json',
    ],
  };
}

export async function verifyReleaseProvenance(
  options,
  { run = captureCommand } = {},
) {
  const policy = releaseProvenancePolicy(options);
  if (!(await lstat(policy.directory)).isDirectory())
    throw new Error('Select a real download directory, not a symlink.');
  // Preflight every subject before verification so a missing sidecar or raw
  // disk can never turn a partially downloaded candidate into an accepted set.
  for (const path of [
    policy.bundle,
    ...policy.subjects.map((name) => join(policy.directory, name)),
  ]) {
    const state = await lstat(path);
    if (!state.isFile() || state.size === 0)
      throw new Error(
        `Provenance inputs must be nonempty regular files: ${path}`,
      );
  }
  for (const subject of policy.subjects) {
    const { stdout } = await run(
      'gh',
      [
        'attestation',
        'verify',
        join(policy.directory, subject),
        ...policy.arguments,
      ],
      { timeout: 300_000 },
    );
    let verified;
    try {
      verified = JSON.parse(stdout);
    } catch (cause) {
      throw new Error(`Invalid gh verification output for ${subject}.`, {
        cause,
      });
    }
    // Inspect only gh's successfully verified certificate results. Reading an
    // unsigned field directly from the supplied bundle would not pin identity.
    if (
      !Array.isArray(verified) ||
      !verified.some(
        (result) =>
          result?.verificationResult?.signature?.certificate
            ?.sourceRepositoryIdentifier === repositoryID,
      )
    )
      throw new Error(
        `Verified provenance for ${subject} must identify repository ID ${repositoryID}.`,
      );
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  for (const signal of ['SIGINT', 'SIGTERM'])
    process.once(
      signal,
      () => void exitAfterCleanup(signal === 'SIGINT' ? 130 : 143),
    );
  try {
    const { values } = parseArgs({
      options: {
        kind: { type: 'string' },
        version: { type: 'string' },
        source: { type: 'string' },
        directory: { type: 'string' },
      },
    });
    await verifyReleaseProvenance(values);
    console.log('Verified provenance for every release subject.');
    await exitAfterCleanup(0);
  } catch (error) {
    console.error(error);
    await exitAfterCleanup(1);
  }
}
