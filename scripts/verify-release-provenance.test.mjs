import { mkdir, mkdtemp, rm, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, expect, test, vi } from 'vitest';
import { captureCommand, root } from './lib.mjs';
import {
  releaseProvenancePolicy,
  verifyReleaseProvenance,
} from './verify-release-provenance.mjs';

const directories = [];
const source = 'a'.repeat(40);
const version = '8.3.2-rc.1';
const verifiedResult = {
  verificationResult: {
    signature: {
      certificate: { sourceRepositoryIdentifier: '1424477361' },
    },
  },
};
const validOutput = { stdout: JSON.stringify([verifiedResult]) };

async function fixture(kind = 'app') {
  const directory = await mkdtemp(join(tmpdir(), 'axv-provenance-'));
  directories.push(directory);
  const options = { kind, version, source, directory };
  const policy = releaseProvenancePolicy(options);
  // The command is mocked in these policy/orchestration tests. These are not
  // cryptographic fixtures; gh must validate actual Sigstore bundles itself.
  for (const file of [...policy.subjects, 'provenance.json'])
    await writeFile(join(directory, file), 'test input');
  return { options, policy };
}

afterEach(async () => {
  await Promise.all(
    directories
      .splice(0)
      .map((directory) => rm(directory, { recursive: true, force: true })),
  );
});

test.each([
  ['app', 'release.yml', 2],
  ['guest', 'guest-images.yml', 4],
])(
  'verifies every %s subject using the complete fixed policy',
  async (kind, workflow, count) => {
    const { options, policy } = await fixture(kind);
    const run = vi.fn().mockResolvedValue(validOutput);
    await verifyReleaseProvenance(options, { run });
    expect(run).toHaveBeenCalledTimes(count);
    const expectedSubjects =
      kind === 'app'
        ? [`axv_${version}_linux_amd64.tar.gz`, 'SHA256SUMS']
        : [
            `axv_guest_${version}_firecracker_amd64.tar.gz`,
            `axv_guest_${version}_firecracker_amd64.ext4`,
            'candidate.json',
            'SHA256SUMS',
          ];
    expect(run.mock.calls).toEqual(
      expectedSubjects.map((subject) => [
        'gh',
        [
          'attestation',
          'verify',
          join(options.directory, subject),
          '--repo',
          'cofy-x/axv',
          '--signer-workflow',
          `cofy-x/axv/.github/workflows/${workflow}`,
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
          policy.bundle,
          '--format',
          'json',
        ],
        { timeout: 300_000 },
      ]),
    );
  },
);

test.each([
  { kind: 'other' },
  { version: undefined },
  { version: '../8.3.2' },
  { version: '8.3.2\n' },
  { version: '08.3.2' },
  { version: '8.3.2-rc.01' },
  { version: '8.3.2;echo injected' },
  { source: 'main' },
  { source: `${source}\n` },
  { source: source.toUpperCase() },
  { directory: '' },
])('rejects invalid selection %j before executing gh', async (changes) => {
  const { options } = await fixture();
  const run = vi.fn();
  await expect(
    verifyReleaseProvenance({ ...options, ...changes }, { run }),
  ).rejects.toThrow();
  expect(run).not.toHaveBeenCalled();
});

test.each(['app', 'guest'])(
  'requires every %s subject and its bundle before any command',
  async (kind) => {
    const { options, policy } = await fixture(kind);
    const run = vi.fn();
    for (const name of [...policy.subjects, 'provenance.json']) {
      const path = join(options.directory, name);
      await rm(path);
      await expect(verifyReleaseProvenance(options, { run })).rejects.toThrow();
      await writeFile(path, '');
      await expect(verifyReleaseProvenance(options, { run })).rejects.toThrow(
        /nonempty regular files/,
      );
      await rm(path);
      await mkdir(path);
      await expect(verifyReleaseProvenance(options, { run })).rejects.toThrow(
        /nonempty regular files/,
      );
      await rm(path, { recursive: true });
      await symlink(join(options.directory, policy.subjects[0]), path);
      await expect(verifyReleaseProvenance(options, { run })).rejects.toThrow(
        /nonempty regular files/,
      );
      await rm(path);
      await writeFile(path, 'test input');
    }
    expect(run).not.toHaveBeenCalled();
  },
);

test('rejects a symlinked download directory', async () => {
  const { options } = await fixture();
  const link = `${options.directory}-link`;
  directories.push(link);
  await symlink(options.directory, link);
  const run = vi.fn();
  await expect(
    verifyReleaseProvenance({ ...options, directory: link }, { run }),
  ).rejects.toThrow(/real download directory/);
  expect(run).not.toHaveBeenCalled();
});

test('propagates verification failures without accepting remaining subjects', async () => {
  const { options } = await fixture('guest');
  const error = new Error('gh rejected the signed provenance policy');
  const run = vi
    .fn()
    .mockResolvedValueOnce(validOutput)
    .mockRejectedValueOnce(error);
  await expect(verifyReleaseProvenance(options, { run })).rejects.toBe(error);
  expect(run).toHaveBeenCalledTimes(2);
});

test.each([
  '',
  'not JSON',
  'null',
  '{}',
  '[]',
  JSON.stringify([{}]),
  JSON.stringify([{ sourceRepositoryIdentifier: '1424477361' }]),
  JSON.stringify([
    {
      verificationResult: {
        signature: {
          certificate: { sourceRepositoryIdentifier: '9999999999' },
        },
      },
    },
  ]),
  JSON.stringify([
    {
      verificationResult: {
        signature: {
          certificate: { sourceRepositoryIdentifier: 1424477361 },
        },
      },
    },
  ]),
])(
  'rejects absent, malformed or mismatched verified repository identity: %s',
  async (stdout) => {
    const { options } = await fixture();
    const run = vi.fn().mockResolvedValue({ stdout });
    await expect(verifyReleaseProvenance(options, { run })).rejects.toThrow();
    expect(run).toHaveBeenCalledTimes(1);
  },
);

test('requires a verified matching repository certificate for each subject', async () => {
  const { options } = await fixture();
  const run = vi
    .fn()
    .mockResolvedValueOnce(validOutput)
    .mockResolvedValueOnce({ stdout: '[]' });
  await expect(verifyReleaseProvenance(options, { run })).rejects.toThrow(
    /SHA256SUMS must identify repository ID/,
  );
  expect(run).toHaveBeenCalledTimes(2);
});

test('accepts a matching verified certificate among multiple verified attestations', async () => {
  const { options } = await fixture();
  const run = vi.fn().mockResolvedValue({
    stdout: JSON.stringify([{}, verifiedResult, null]),
  });
  await expect(
    verifyReleaseProvenance(options, { run }),
  ).resolves.toBeUndefined();
  expect(run).toHaveBeenCalledTimes(2);
});

test('CLI rejects unknown policy options and malformed selectors', async () => {
  for (const args of [
    [],
    ['--repo', 'unexpected/repository'],
    ['--kind', 'app', '--source', 'main'],
    ['--kind', 'app', 'unexpected-positional-input'],
  ]) {
    const result = await captureCommand(
      process.execPath,
      [join(root, 'scripts/verify-release-provenance.mjs'), ...args],
      { allowFailure: true },
    );
    expect(result.code).toBe(1);
    expect(result.stderr).not.toBe('');
    expect(result.stdout).not.toContain('Verified provenance');
  }
});
