import type { Route } from '@playwright/test';
import type {
  Candidate,
  ReviewJob,
  ReviewSnapshot,
} from '../apps/dashboard/src/features/github-review/api';
import { reviewStageStates } from '../apps/dashboard/src/features/github-review/review-evidence';

type InboxGroup = 'action' | 'active' | 'recent' | 'earlier';

function candidates(job: ReviewJob) {
  return [...(job.candidates ?? []), ...(job.followups ?? [])];
}

function validating(candidate: Candidate) {
  return (
    candidate.autoValidation?.state === 'queued' ||
    candidate.autoValidation?.state === 'running'
  );
}

function fixtureGroups(snapshot: ReviewSnapshot, view: string) {
  const current = new Set<string>();
  for (const job of snapshot.jobs)
    if (job.status !== 'stale')
      current.add(`${job.repository.toLowerCase()}#${job.number}`);
  return [...snapshot.jobs]
    .sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt))
    .flatMap((job): (ReviewJob & { inboxGroup: InboxGroup })[] => {
      const stages = Object.entries(
        reviewStageStates(
          job,
          Object.entries(snapshot.config.repositoryPolicies ?? {}).find(
            ([repo]) => repo.toLowerCase() === job.repository.toLowerCase(),
          )?.[1],
        ),
      );
      const entries = candidates(job);
      if (job.status === 'stale') {
        const key = `${job.repository.toLowerCase()}#${job.number}`;
        const unrepresented = !current.has(key);
        current.add(key);
        const inboxGroup =
          unrepresented && job.observedBaseSha ? 'action' : 'earlier';
        return view === (inboxGroup === 'earlier' ? 'history' : 'action')
          ? [{ ...job, inboxGroup }]
          : [];
      }
      const needsEvidence =
        !entries.length ||
        entries.some(
          (candidate) =>
            candidate.status === 'pending' &&
            !validating(candidate) &&
            !candidate.autoValidation?.suggestion,
        );
      const action =
        entries.some(
          (candidate) =>
            (candidate.status === 'pending' && !validating(candidate)) ||
            (candidate.commentDraft &&
              !candidate.commentDraft.stale &&
              !candidate.commentDraft.reviewedAt),
        ) ||
        stages.some(
          ([key, stage]) =>
            stage.disposition === 'attention' &&
            (stage.state !== 'missing' ||
              (needsEvidence && (!!job.evidence || key === 'codex'))),
        );
      const active =
        entries.some(validating) ||
        stages.some(([, stage]) => stage.disposition === 'active');
      if (view === 'action' && action)
        return [{ ...job, inboxGroup: 'action' }];
      if (view === 'active' && active)
        return [{ ...job, inboxGroup: 'active' }];
      return view === 'history' && !action && !active
        ? [{ ...job, inboxGroup: 'recent' }]
        : [];
    });
}

export async function answerReviewRead(route: Route, snapshot: ReviewSnapshot) {
  if (route.request().method() !== 'GET') return false;
  const { pathname, searchParams } = new URL(route.request().url());
  const account = snapshot.github.account ?? '';
  if (pathname === '/api/github-review' && searchParams.has('summary')) {
    await route.fulfill({
      json: { ...snapshot, hasJobs: snapshot.jobs.length > 0, jobs: [] },
    });
    return true;
  }
  if (pathname === '/api/github-review/inbox') {
    const view = searchParams.get('view') ?? '';
    await route.fulfill({
      json: { account, jobs: fixtureGroups(snapshot, view) },
    });
    return true;
  }
  if (pathname === '/api/github-review/job') {
    const job = snapshot.jobs.find(
      (job) =>
        job.repository === searchParams.get('repository') &&
        job.number === Number(searchParams.get('number')) &&
        job.headSha === searchParams.get('headSha') &&
        job.baseSha === searchParams.get('baseSha'),
    );
    if (job) await route.fulfill({ json: { account, job } });
    else
      await route.fulfill({
        status: 404,
        json: { error: 'Review not found.' },
      });
    return true;
  }
  return false;
}
