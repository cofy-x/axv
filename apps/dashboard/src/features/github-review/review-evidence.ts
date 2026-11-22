import type { RepositoryPolicy, ReviewJob } from './api';

export interface ReviewStageState {
  state: string;
  label?: string;
  disposition: 'passed' | 'attention' | 'active' | 'optional';
}

export function latestTest(job: ReviewJob) {
  return job.evidence?.tests?.at(-1);
}

function status(state: string, label?: string): ReviewStageState {
  const disposition = ['passed', 'completed'].includes(state)
    ? 'passed'
    : ['queued', 'pending', 'running', 'reviewing', 'publishing'].includes(
          state,
        )
      ? 'active'
      : ['empty', 'not_applicable', 'not_configured', 'not_run'].includes(state)
        ? 'optional'
        : 'attention';
  return { state, label, disposition };
}

function hasQuickTest(policy?: RepositoryPolicy) {
  return !!(policy?.testProfiles?.length || policy?.testCommand?.length);
}

function codexState(job: ReviewJob) {
  if (job.status === 'stale') return status('stale', 'Earlier revision');
  const review = job.evidence?.review;
  if (review?.status) return status(review.status);
  if (['queued', 'reviewing', 'failed', 'interrupted'].includes(job.status))
    return status(job.status);
  return status('missing', 'No review record');
}

function checksState(job: ReviewJob) {
  const checks = job.evidence?.checks;
  if (job.status === 'stale' && checks?.status)
    return status('stale', 'Earlier revision');
  if (checks?.refreshErrorAt) return status('error', 'Refresh failed');
  if (checks?.status) return status(checks.status);
  if (['queued', 'reviewing'].includes(job.status))
    return status('pending', 'Waiting for review');
  return status('missing', 'No evidence');
}

function quickTestState(job: ReviewJob, configuredPolicy?: RepositoryPolicy) {
  const test = latestTest(job);
  if (!test) {
    const expected =
      hasQuickTest(job.evidence?.policy) || hasQuickTest(configuredPolicy);
    if (!expected)
      return status(
        'not_configured',
        job.evidence ? 'Not configured' : 'No evidence',
      );
    if (['queued', 'reviewing'].includes(job.status))
      return status('pending', 'Waiting for review');
    return status('missing', 'No test result');
  }
  if (
    job.status === 'stale' ||
    (test.selectionBaseSha && test.selectionBaseSha !== job.baseSha)
  )
    return status('stale', 'Earlier comparison');
  if (test.status === 'passed' && test.checkoutSha !== job.headSha)
    return test.checkoutSha
      ? status('error', 'Unexpected checkout')
      : status('missing', 'Checkout unverified');
  return status(test.status);
}

function comparisonsState(job: ReviewJob) {
  const runs = [...(job.candidates ?? []), ...(job.followups ?? [])].flatMap(
    (candidate) => candidate.comparisons?.slice(-1) ?? [],
  );
  if (!runs.length) return status('not_run', 'Not run');
  if (job.status === 'stale') return status('stale', 'Earlier revision');
  if (runs.some((run) => run.status === 'queued' || run.status === 'running'))
    return status('running');
  if (
    runs.some(
      (run) => run.status !== 'completed' || run.outcome === 'inconclusive',
    )
  )
    return status('neutral', 'Inconclusive');
  return status('completed');
}

export function reviewStageStates(
  job: ReviewJob,
  configuredPolicy?: RepositoryPolicy,
) {
  return {
    codex: codexState(job),
    checks: checksState(job),
    quickTest: quickTestState(job, configuredPolicy),
    comparisons: comparisonsState(job),
  };
}
