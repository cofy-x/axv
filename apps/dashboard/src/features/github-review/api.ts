import { APIError, request } from '../../shared/api/http';

export type ReviewProvider = 'github' | 'codex';

export interface TestProfile {
  id: string;
  name: string;
  entryPath: string;
  appliesTo?: string[];
  command: string[];
}

export interface ComparisonRuntime {
  id: string;
  name: string;
  command: string[];
}

export const shellComparisonRuntime: ComparisonRuntime = {
  id: 'shell',
  name: 'POSIX shell',
  command: ['/bin/sh', '-ec'],
};

export interface ComparisonLeg {
  targetSha: string;
  checkoutSha?: string;
  status: string;
  assertionExitCode?: number;
  exitCode?: number;
  log?: string;
  truncated?: boolean;
  error?: string;
  taskId?: string;
  attemptId?: string;
  vmId?: string;
  projectId?: string;
  binding?: { hostId?: string; machineId?: string; bootId?: string };
}

export interface CandidateComparison {
  id: string;
  candidateId: string;
  repository: string;
  number: number;
  baseSha: string;
  headSha: string;
  mergeBaseSha: string;
  runtime: ComparisonRuntime;
  assertion?: string;
  status: string;
  outcome?:
    | 'head_only_failed'
    | 'baseline_only_failed'
    | 'both_passed'
    | 'both_failed'
    | 'inconclusive';
  baseline: ComparisonLeg;
  head: ComparisonLeg;
  error?: string;
  createdAt: string;
  updatedAt: string;
}

export interface RepositoryPolicy {
  comparisonRuntimes?: ComparisonRuntime[];
  testProfiles?: TestProfile[];
  rules: string;
  testCommand: string[];
}

export interface ReviewConfig {
  repositoryPolicies?: Record<string, RepositoryPolicy>;
  repositories: string[];
  publishRepositories: string[];
  intervalMinutes: number;
  dailyLimit: number;
  enabled: boolean;
  revision: number;
}

export interface ReviewConnection {
  available: boolean;
  connected: boolean;
  account?: string;
  attempt?: {
    status: 'pending' | 'complete' | 'failed';
    url?: string;
    code?: string;
    error?: string;
  };
  error?: string;
}

export interface ReviewChecks {
  status: string;
  runs?: {
    name: string;
    status: string;
    conclusion?: string;
    url?: string;
    executionSha?: string;
    executionRef?: string;
    scope?: string;
    provenanceUrl?: string;
  }[];
  updatedAt?: string;
  refreshErrorAt?: string;
  error?: string;
}

export interface ReviewTest {
  testId?: string;
  testName?: string;
  entryPath?: string;
  selection?: 'automatic' | 'manual';
  selectionReason?: string;
  selectionBaseSha?: string;
  command?: string[];
  attempt: number;
  status: string;
  taskId?: string;
  attemptId?: string;
  checkoutSha?: string;
  log?: string;
  truncated?: boolean;
  exitCode?: number;
  error?: string;
  updatedAt?: string;
  vmId?: string;
  projectId?: string;
}

export type FindingStatus =
  'pending' | 'confirmed' | 'false_positive' | 'resolved';

export interface Finding {
  path: string;
  line: number;
  side?: 'base' | 'head';
  severity: string;
  title: string;
  body: string;
}

export interface EvidenceItem {
  id: string;
  kind: 'check' | 'test';
  label: string;
  status: string;
  sha?: string;
  detail?: string;
  url?: string;
}

export interface CandidateOccurrence {
  comparisons?: CandidateComparison[];
  id: string;
  headSha: string;
  baseSha: string;
  title: string;
  path: string;
  status: FindingStatus;
  note: string;
  revision: number;
  evidence?: EvidenceItem[];
}

export interface Candidate {
  comparisons?: CandidateComparison[];
  id: string;
  trackId: string;
  index: number;
  status: FindingStatus;
  note: string;
  revision: number;
  evidence?: EvidenceItem[];
  history?: CandidateOccurrence[];
}

export interface Followup extends Candidate, Finding {
  originId: string;
  originHeadSha: string;
  originBaseSha: string;
}

export interface CandidateDecision {
  account: string;
  repository: string;
  number: number;
  baseSha: string;
  headSha: string;
  candidateId: string;
  revision: number;
  status: FindingStatus;
  note: string;
  evidenceIds: string[];
  previousCandidateId: string;
}

export interface ReviewJob {
  evidence?: {
    review?: {
      authorContext?: {
        title: string;
        description: string;
        descriptionTruncated?: boolean;
        titleTruncated?: boolean;
      };
      status: string;
      messages?: { at: string; text: string }[];
      startedAt?: string;
      updatedAt?: string;
      error?: string;
    };
    policy: RepositoryPolicy;
    checks: ReviewChecks;
    tests: ReviewTest[];
  };
  repository: string;
  number: number;
  title?: string;
  baseSha: string;
  headSha: string;
  source: 'repository' | 'requested';
  status: string;
  findings?: Finding[];
  followups?: Followup[];
  candidates?: Candidate[];
  evidenceOptions?: EvidenceItem[];
  previousCandidates?: CandidateOccurrence[];
  mergeBaseSha?: string;
  observedBaseSha?: string;
  commentUrl?: string;
  error?: string;
  updatedAt: string;
}

export interface ReviewSnapshot {
  config: ReviewConfig;
  comparisonReady?: boolean;
  github: ReviewConnection;
  codex: ReviewConnection;
  jobs: ReviewJob[];
  budget: { used: number; resetsAt: string };
  lastPollAt?: string;
  error?: string;
}

export interface ReviewRetry extends Pick<
  ReviewJob,
  'repository' | 'number' | 'baseSha' | 'headSha'
> {
  revision: number;
}

const path = '/github-review';

export const githubReviewAPI = {
  snapshot: (signal: AbortSignal) =>
    request<ReviewSnapshot>(path, { signal, cache: 'no-store' }),
  save: (config: ReviewConfig, signal: AbortSignal) =>
    request<ReviewSnapshot>(`${path}/config`, {
      method: 'PUT',
      body: JSON.stringify(config),
      signal,
    }),
  connection: (
    action: 'connect' | 'disconnect',
    provider: ReviewProvider,
    signal: AbortSignal,
  ) =>
    request<ReviewSnapshot>(`${path}/${action}/${provider}`, {
      method: 'POST',
      signal,
    }),
  run: (signal: AbortSignal) =>
    request<ReviewSnapshot>(`${path}/run`, { method: 'POST', signal }),
  refreshChecks: (retry: ReviewRetry, signal: AbortSignal) =>
    request<ReviewSnapshot>(`${path}/refresh-checks`, {
      method: 'POST',
      body: JSON.stringify(retry),
      signal,
    }),
  comparison: (id: string, signal: AbortSignal) =>
    request<CandidateComparison>(
      `${path}/candidate-comparison/${encodeURIComponent(id)}`,
      { signal, cache: 'no-store' },
    ),
  compare: (
    comparison: ReviewRetry & {
      account: string;
      candidateId: string;
      requestId: string;
      runtimeId: string;
      assertion: string;
    },
    signal: AbortSignal,
  ) =>
    request<ReviewSnapshot>(`${path}/candidate-comparison`, {
      method: 'POST',
      body: JSON.stringify(comparison),
      signal,
    }),
  decide: (decision: CandidateDecision, signal: AbortSignal) =>
    request<ReviewSnapshot>(`${path}/candidate`, {
      method: 'POST',
      body: JSON.stringify(decision),
      signal,
    }),
  followup: (
    body: Pick<
      CandidateDecision,
      | 'account'
      | 'repository'
      | 'number'
      | 'headSha'
      | 'baseSha'
      | 'previousCandidateId'
    >,
    signal: AbortSignal,
  ) =>
    request<ReviewSnapshot>(`${path}/candidate-followup`, {
      method: 'POST',
      body: JSON.stringify(body),
      signal,
    }),
  reviewLatest: (body: ReviewRetry, signal: AbortSignal) =>
    request<ReviewSnapshot>(`${path}/review-latest`, {
      method: 'POST',
      body: JSON.stringify(body),
      signal,
    }),
  retryTest: (
    retry: ReviewRetry & { attempt: number; testId?: string },
    signal: AbortSignal,
  ) =>
    request<ReviewSnapshot>(`${path}/retry-test`, {
      method: 'POST',
      body: JSON.stringify(retry),
      signal,
    }),
  retry: (retry: ReviewRetry, signal: AbortSignal) =>
    request<ReviewSnapshot>(`${path}/retry`, {
      method: 'POST',
      body: JSON.stringify(retry),
      signal,
    }),
};

export function reviewError(error: unknown) {
  if (error instanceof APIError) {
    if (error.status === 401 || error.status === 403)
      return 'Open GitHub Review on the computer running AXV.';
    return error.message;
  }
  return 'Could not reach GitHub Review. Refresh to check its current state.';
}

function httpsURL(value: string) {
  try {
    const url = new URL(value);
    if (
      url.protocol === 'https:' &&
      !url.username &&
      !url.password &&
      !url.port
    )
      return url;
  } catch {
    // An invalid response URL stays plain text.
  }
}

export function reviewLoginURL(provider: ReviewProvider, value?: string) {
  const url = value && httpsURL(value);
  if (!url || url.search || url.hash) return;
  const expected =
    provider === 'github'
      ? 'https://github.com/login/device'
      : 'https://auth.openai.com/codex/device';
  return url.href === expected ? expected : undefined;
}

export function reviewPRURL(repository: string, number: number) {
  const slug = /^[a-z\d][a-z\d-]*\/([a-z\d_.-]+)$/i.exec(repository);
  if (!slug || slug[1] === '.' || slug[1] === '..') return;
  if (!Number.isSafeInteger(number) || number < 1) return;
  return `https://github.com/${repository}/pull/${number}`;
}

export function reviewCommentURL(job: ReviewJob) {
  const url = job.commentUrl && httpsURL(job.commentUrl);
  const prURL = reviewPRURL(job.repository, job.number);
  if (
    url &&
    prURL &&
    `${url.origin}${url.pathname}`.toLowerCase() === prURL.toLowerCase() &&
    !url.search &&
    /^#(?:issuecomment|pullrequestreview|discussion_r)-?\d+$/.test(url.hash)
  )
    return url.href;
}

export function reviewCheckURL(value?: string) {
  const url = value && httpsURL(value);
  return url && url.hostname === 'github.com' ? url.href : undefined;
}
