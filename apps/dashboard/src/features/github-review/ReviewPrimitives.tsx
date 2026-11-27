import { useId, useState } from 'react';
import type { ReactNode } from 'react';
import type { ReviewJob, ReviewTarget } from './api';
import { reviewStageStates } from './review-evidence';
import type { ReviewStageState } from './review-evidence';

const labels: Record<string, string> = {
  interrupted: 'Interrupted',
  queued: 'Queued',
  reviewing: 'Reviewing',
  analyzed: 'Reviewed locally',
  publishing: 'Publishing',
  published: 'Comment posted',
  clean: 'No findings',
  completed: 'Completed',
  stale: 'Superseded',
  failed: 'Failed',
  blocked: 'Blocked',
  running: 'Running',
  pending: 'Pending',
  passed: 'Passed',
  empty: 'No checks',
  missing: 'No evidence',
  not_configured: 'Not configured',
  not_run: 'Not run',
  error: 'Unavailable',
  neutral: 'Inconclusive',
  not_applicable: 'Not applicable',
  canceled: 'Canceled',
};

export function statusText(value: string) {
  return Object.hasOwn(labels, value) ? labels[value] : value;
}

export function Commit({ sha }: { sha: string }) {
  return <code title={sha}>{sha.slice(0, 12)}</code>;
}

export function Status({ value, label }: { value: string; label?: string }) {
  return (
    <span className="github-review-state" data-state={value}>
      {label ?? statusText(value)}
    </span>
  );
}

export function queueLabel(job: ReviewJob, dailyLimitReached: boolean) {
  if (job.queueReason) return job.queueReason;
  if (job.status === 'queued' && dailyLimitReached)
    return 'Waiting for daily reset';
}

export function reviewCandidates(job: ReviewJob) {
  return [...(job.candidates ?? []), ...(job.followups ?? [])];
}

export function automationInProgress(job: ReviewJob) {
  return (
    job.status !== 'stale' &&
    reviewCandidates(job).some(
      (candidate) =>
        candidate.autoValidation?.state === 'queued' ||
        candidate.autoValidation?.state === 'running',
    )
  );
}

export function pendingSuggestions(job: ReviewJob) {
  if (job.status === 'stale') return 0;
  return reviewCandidates(job).filter(
    (candidate) =>
      candidate.status === 'pending' &&
      candidate.autoValidation?.state === 'verified' &&
      candidate.autoValidation.suggestion,
  ).length;
}

export function pendingDecisions(job: ReviewJob) {
  return reviewCandidates(job).filter(
    (item) =>
      item.status === 'pending' &&
      item.autoValidation?.state !== 'queued' &&
      item.autoValidation?.state !== 'running' &&
      !(
        item.autoValidation?.state === 'verified' &&
        item.autoValidation.suggestion
      ),
  ).length;
}

export function RevisionStatus({ job }: { job: ReviewJob }) {
  const stages = reviewStageStates(job);
  if (automationInProgress(job))
    return <Status value="running" label="Automatic verification" />;
  if (stages.comparisons.disposition === 'active')
    return <Status value="running" label="Comparing revisions" />;
  if (['analyzed', 'clean'].includes(job.status)) {
    const sources = [
      { name: 'Quick test', ...stages.quickTest },
      { name: 'Checks', ...stages.checks },
    ];
    const stage =
      sources.find(({ disposition }) => disposition === 'attention') ??
      sources.find(({ disposition }) => disposition === 'active');
    if (stage)
      return (
        <Status
          value={stage.state}
          label={`${stage.name} · ${stage.label ?? statusText(stage.state)}`}
        />
      );
  }
  if (job.status !== 'stale' && pendingDecisions(job)) {
    return (
      <Status value="pending" label={`${pendingDecisions(job)} to assess`} />
    );
  }
  if (stages.quickTest.state === 'not_applicable')
    return <Status value="not_applicable" label="No applicable quick test" />;
  return <Status value={job.status} />;
}

export function jobKey(job: ReviewTarget) {
  return [
    job.repository.toLowerCase(),
    job.number,
    job.baseSha,
    job.headSha,
  ].join('/');
}

export function candidateProgress(job: ReviewJob) {
  const candidates = reviewCandidates(job);
  const count = job.findings?.length ?? 0;
  const confirmed = candidates.filter(
    (candidate) => candidate.status === 'confirmed',
  ).length;
  const pending = pendingDecisions(job);
  const suggested = pendingSuggestions(job);
  const dismissed = candidates.filter(
    (candidate) => candidate.status === 'false_positive',
  ).length;
  const resolved = candidates.filter(
    (candidate) => candidate.status === 'resolved',
  ).length;
  const localDrafts = candidates.filter(
    (candidate) => candidate.commentDraft && !candidate.commentDraft.stale,
  ).length;
  const outdatedDrafts = candidates.filter(
    (candidate) => candidate.commentDraft?.stale,
  ).length;
  return [
    confirmed > 0 && `${confirmed} confirmed`,
    pending > 0 && `${pending} to assess`,
    suggested > 0 &&
      `${suggested} suggested ${suggested === 1 ? 'decision' : 'decisions'}`,
    dismissed > 0 &&
      `${dismissed} false ${dismissed === 1 ? 'positive' : 'positives'}`,
    resolved > 0 && `${resolved} resolved`,
    localDrafts > 0 &&
      `${localDrafts} local ${localDrafts === 1 ? 'draft' : 'drafts'}`,
    outdatedDrafts > 0 &&
      `${outdatedDrafts} outdated ${outdatedDrafts === 1 ? 'draft' : 'drafts'}`,
    !candidates.length &&
      count > 0 &&
      `${count} ${count === 1 ? 'finding' : 'findings'}`,
  ]
    .filter(Boolean)
    .join(' · ');
}

export function Updated({
  time,
  seconds,
}: {
  time?: string;
  seconds?: boolean;
}) {
  if (!time) return null;
  return (
    <time dateTime={time}>
      {new Date(time).toLocaleString(undefined, {
        dateStyle: 'short',
        timeStyle: seconds ? 'medium' : 'short',
      })}
    </time>
  );
}

export function Stage({
  title,
  stage,
  hint,
  children,
}: {
  title: string;
  stage: ReviewStageState;
  hint?: ReactNode;
  children?: ReactNode;
}) {
  const contentId = useId();
  const statusId = `${contentId}-status`;
  const [expanded, setExpanded] = useState(false);
  const attention = stage.disposition === 'attention';
  const visible = attention || expanded;
  const description = (
    <span className="github-review-stage-status" id={statusId}>
      <Status value={stage.state} label={stage.label} />
      {hint && <span className="github-review-stage-hint">{hint}</span>}
    </span>
  );
  return (
    <section
      className="github-review-stage"
      aria-label={title}
      data-disposition={stage.disposition}
    >
      {attention ? (
        <header className="github-review-stage-summary">
          <h4>{title}</h4>
          {description}
        </header>
      ) : (
        <button
          type="button"
          className="github-review-stage-summary github-review-stage-toggle"
          aria-label={`${title} details`}
          aria-describedby={statusId}
          aria-expanded={visible}
          aria-controls={contentId}
          onClick={() => setExpanded(!expanded)}
        >
          <strong>{title}</strong>
          {description}
          <span className="github-review-stage-disclosure" aria-hidden="true">
            {visible ? '▾' : '▸'}
          </span>
        </button>
      )}
      <div
        className="github-review-stage-content"
        id={contentId}
        hidden={!visible}
      >
        {children}
      </div>
    </section>
  );
}
