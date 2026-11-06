import { describe, expect, it } from 'vitest';
import {
  githubChecksLabel,
  githubLink,
  githubRepositoryAllowed,
  verifyGitHubResult,
} from './vm-github-api';
import type {
  GitHubCheckSet,
  GitHubRequest,
  GitHubResult,
} from './vm-github-api';

const binding = {
  hostId: 'host',
  projectId: 'project',
  projectRevision: 1,
  volumeId: 'volume',
  volumeRevision: 2,
  machineId: 'machine',
  bootId: 'boot',
  instanceId: 'instance',
};
const repository = {
  id: 'repository',
  nameWithOwner: 'owner/repository',
  url: 'https://github.com/owner/repository',
};
const captured: GitHubRequest = {
  binding,
  revision: 3,
  repositoryId: repository.id,
  accountId: 'account',
};
const result: GitHubResult = {
  binding,
  revision: 3,
  observation: {
    git: {
      repository: true,
      headSha: 'local',
      branch: 'feature',
      detached: false,
      unborn: false,
      remotes: [],
      remotesPartial: false,
    },
    repository,
    account: { id: 'account', login: 'reviewer' },
    pullRequests: [],
    pullRequestsComplete: true,
    observedAt: '2026-11-06T00:00:00Z',
  },
};

describe('GitHub review identity', () => {
  it('accepts only the captured environment, association revision, account and repository', () => {
    expect(verifyGitHubResult(result, captured)).toBe(result);
    for (const key of Object.keys(binding) as (keyof typeof binding)[]) {
      const value = binding[key];
      expect(() =>
        verifyGitHubResult(
          {
            ...result,
            binding: {
              ...binding,
              [key]:
                typeof value === 'number' ? value + 1 : `${value}-replacement`,
            },
          },
          captured,
        ),
      ).toThrow('another project');
    }
    expect(() =>
      verifyGitHubResult({ ...result, revision: 4 }, captured),
    ).toThrow();
    expect(() =>
      verifyGitHubResult(
        {
          ...result,
          observation: {
            ...result.observation,
            repository: { ...repository, id: 'other' },
          },
        },
        captured,
      ),
    ).toThrow();
    expect(() =>
      verifyGitHubResult(
        {
          ...result,
          observation: {
            ...result.observation,
            account: { id: 'other', login: 'reviewer' },
          },
        },
        captured,
      ),
    ).toThrow();
  });

  it('accepts reviewed slugs and links without treating arbitrary URLs as authority', () => {
    expect(githubRepositoryAllowed('owner/repo.name')).toBe(true);
    for (const value of [
      'https://github.com/owner/repo',
      'owner/repo?token=secret',
      '-owner/repo',
      'owner/repo\n',
      'owner/repo/extra',
    ])
      expect(githubRepositoryAllowed(value)).toBe(false);
    expect(githubLink(repository.url)).toBe(repository.url);
    for (const value of [
      'javascript:alert(1)',
      'https://github.com.evil.test/owner/repo',
      'https://token@github.com/owner/repo',
      'http://github.com/owner/repo',
    ])
      expect(githubLink(value)).toBeUndefined();
  });
});

describe('GitHub check labels', () => {
  const checks: GitHubCheckSet = {
    repository,
    sha: 'local',
    state: 'success',
    complete: true,
    checks: [
      {
        name: 'Test',
        source: 'check-run',
        status: 'completed',
        conclusion: 'success',
      },
    ],
  };

  it('keeps merge, fork and obsolete PR results separate from the reviewed local SHA', () => {
    expect(githubChecksLabel(checks, repository.id, 'local')).toBe(
      'Checks completed',
    );
    expect(githubChecksLabel(checks, repository.id, 'new-head')).toBe(
      'Unmatched repository or commit',
    );
    expect(
      githubChecksLabel(
        { ...checks, sha: 'synthetic-merge' },
        repository.id,
        'local',
      ),
    ).toBe('Unmatched repository or commit');
    expect(githubChecksLabel(checks, 'fork-repository', 'local')).toBe(
      'Unmatched repository or commit',
    );
  });

  it('never reports completion for empty, incomplete, pending or failed checks', () => {
    expect(githubChecksLabel(undefined, repository.id, 'local')).toBe(
      'No checks observed',
    );
    expect(
      githubChecksLabel({ ...checks, checks: [] }, repository.id, 'local'),
    ).toBe('No checks reported');
    expect(
      githubChecksLabel({ ...checks, complete: false }, repository.id, 'local'),
    ).toBe('Incomplete checks');
    expect(
      githubChecksLabel(
        {
          ...checks,
          checks: [
            { ...checks.checks[0], status: 'in_progress', conclusion: '' },
          ],
        },
        repository.id,
        'local',
      ),
    ).toBe('Checks pending');
    expect(
      githubChecksLabel(
        { ...checks, checks: [{ ...checks.checks[0], conclusion: 'failure' }] },
        repository.id,
        'local',
      ),
    ).toBe('Checks require attention');
  });

  it('retains non-success rollup states when every returned row succeeded', () => {
    for (const state of ['failure', 'error'])
      expect(
        githubChecksLabel({ ...checks, state }, repository.id, 'local'),
      ).toBe('Checks require attention');
    for (const state of ['pending', 'expected'])
      expect(
        githubChecksLabel({ ...checks, state }, repository.id, 'local'),
      ).toBe('Checks pending');
    for (const state of ['unknown', 'unavailable', 'none', ''])
      expect(
        githubChecksLabel({ ...checks, state }, repository.id, 'local'),
      ).toBe('Check state unknown');
  });
});
