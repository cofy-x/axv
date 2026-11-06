import { request } from '../../shared/api/http';
import type { VMProjectBinding } from './vm-api';
import { sameProjectBinding } from './vm-init-api';

export interface GitHubRepository {
  id: string;
  nameWithOwner: string;
  url: string;
}

export interface GitHubAccount {
  id: string;
  login: string;
}

export interface GitHubGitState {
  repository: boolean;
  headSha?: string;
  branch?: string;
  detached: boolean;
  unborn: boolean;
  remotes: { name: string; repository: string }[];
  remotesPartial: boolean;
}

export interface GitHubCheckSet {
  repository: GitHubRepository;
  sha: string;
  state: string;
  complete: boolean;
  checks: {
    name: string;
    source: string;
    status: string;
    conclusion: string;
    url?: string;
  }[];
}

export interface GitHubPullRequest {
  id: string;
  number: number;
  title: string;
  url: string;
  state: string;
  baseRepository: GitHubRepository;
  headRepository: GitHubRepository;
  headBranch: string;
  headSha: string;
  headChecks?: GitHubCheckSet;
  mergeChecks?: GitHubCheckSet;
}

export interface GitHubObservation {
  git: GitHubGitState;
  account: GitHubAccount;
  repository: GitHubRepository;
  localChecks?: GitHubCheckSet;
  pullRequests: GitHubPullRequest[];
  pullRequestsComplete: boolean;
  observedAt: string;
}

export interface GitHubAssociation {
  repository: GitHubRepository;
  account: GitHubAccount;
  machineId: string;
  profile: 'axv';
  updatedAt: string;
}

export interface GitHubAssociationState {
  revision: number;
  association?: GitHubAssociation;
}

export interface GitHubResult {
  binding: VMProjectBinding;
  revision: number;
  observation: GitHubObservation;
}

export interface GitHubRequest {
  binding: VMProjectBinding;
  revision: number;
  repository?: string;
  repositoryId?: string;
  accountId?: string;
}

export function githubRepositoryAllowed(value: string) {
  return /^[A-Za-z0-9][A-Za-z0-9-]{0,38}\/[A-Za-z0-9_.-]{1,100}$/u.test(value);
}

export function githubLink(value: string | undefined) {
  try {
    const url = new URL(value || '');
    return url.protocol === 'https:' &&
      url.hostname === 'github.com' &&
      !url.username &&
      !url.password &&
      !url.port
      ? url.href
      : undefined;
  } catch {
    return undefined;
  }
}

export function verifyGitHubResult(
  result: GitHubResult,
  captured: GitHubRequest,
) {
  if (
    !sameProjectBinding(result.binding, captured.binding) ||
    result.revision !== captured.revision ||
    !result.observation?.git ||
    (captured.repositoryId &&
      result.observation.repository?.id !== captured.repositoryId) ||
    (captured.accountId &&
      result.observation.account?.id !== captured.accountId)
  )
    throw new Error(
      'GitHub returned another project, VM, repository or account. The response was ignored.',
    );
  return result;
}

export function githubChecksLabel(
  checks: GitHubCheckSet | undefined,
  repositoryId: string,
  sha: string,
) {
  if (!checks) return 'No checks observed';
  if (checks.repository.id !== repositoryId || checks.sha !== sha)
    return 'Unmatched repository or commit';
  if (!checks.complete) return 'Incomplete checks';
  if (!checks.checks.length) return 'No checks reported';
  if (
    ['failure', 'error'].includes(checks.state) ||
    checks.checks.some(
      (check) =>
        [
          'failure',
          'failed',
          'error',
          'timed_out',
          'cancelled',
          'action_required',
          'startup_failure',
          'stale',
        ].includes(check.conclusion) || check.status === 'error',
    )
  )
    return 'Checks require attention';
  if (['pending', 'expected'].includes(checks.state)) return 'Checks pending';
  if (checks.state !== 'success') return 'Check state unknown';
  if (
    checks.checks.every(
      (check) =>
        check.status === 'completed' &&
        ['success', 'neutral', 'skipped'].includes(check.conclusion),
    )
  )
    return 'Checks completed';
  return 'Checks pending';
}

const path = (projectId: string) =>
  `/compute/projects/${encodeURIComponent(projectId)}/github`;

function operation<T>(
  projectId: string,
  action: string,
  values: GitHubRequest,
  signal: AbortSignal,
) {
  return request<T>(`${path(projectId)}/${action}`, {
    method: 'POST',
    headers: { 'X-AXV-Host-ID': values.binding.hostId },
    body: JSON.stringify(values),
    signal: AbortSignal.any([signal, AbortSignal.timeout(60000)]),
  });
}

export const vmGitHubAPI = {
  get: (projectId: string, hostId: string, signal: AbortSignal) =>
    request<GitHubAssociationState>(path(projectId), {
      headers: { 'X-AXV-Host-ID': hostId },
      signal: AbortSignal.any([signal, AbortSignal.timeout(30000)]),
    }),
  git: (id: string, values: GitHubRequest, signal: AbortSignal) =>
    operation<GitHubResult>(id, 'git', values, signal),
  review: (id: string, values: GitHubRequest, signal: AbortSignal) =>
    operation<GitHubResult>(id, 'review', values, signal),
  associate: (id: string, values: GitHubRequest, signal: AbortSignal) =>
    operation<GitHubAssociationState>(id, 'associate', values, signal),
  clear: (id: string, values: GitHubRequest, signal: AbortSignal) =>
    operation<GitHubAssociationState>(id, 'clear', values, signal),
  refresh: (id: string, values: GitHubRequest, signal: AbortSignal) =>
    operation<GitHubResult>(
      id,
      'refresh',
      { binding: values.binding, revision: values.revision },
      signal,
    ),
};
