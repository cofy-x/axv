import { Button } from '@cofy-x/axv-nextstep';
import type { RepositoryPolicy, ReviewJob } from './api';
import type { CandidateFocus } from './CandidateDecisions';
import { latestTest, reviewStageStates } from './review-evidence';
import {
  Commit,
  RevisionStatus,
  Status,
  automationInProgress,
  candidateProgress,
  jobKey,
  pendingDecisions,
  queueLabel,
  reviewCandidates,
  statusText,
} from './ReviewPrimitives';

export type ReviewFocus = CandidateFocus | { kind: 'evidence' };

export const inboxViews = {
  action: 'Needs action',
  active: 'In progress',
  history: 'History',
} as const;

export type InboxGroup = 'action' | 'active' | 'recent' | 'earlier';

export type InboxAction = {
  focus: ReviewFocus;
  count: number;
  noun: string;
  label: string;
};

export type InboxItem = {
  job: ReviewJob;
  summary: string;
  actions: InboxAction[];
  blocker?: string;
  activity?: string;
};

function inboxActions(job: ReviewJob, blockers: number) {
  const candidates = job.status === 'stale' ? [] : reviewCandidates(job);
  const drafts = candidates.filter(
    ({ commentDraft }) =>
      commentDraft && !commentDraft.stale && !commentDraft.reviewedAt,
  );
  const suggested = candidates.filter(
    ({ status, autoValidation }) =>
      status === 'pending' &&
      autoValidation?.state === 'verified' &&
      autoValidation.suggestion,
  );
  const suggestionsWithoutDrafts = suggested.filter(
    (candidate) => !drafts.includes(candidate),
  );
  const manual = candidates.filter(
    (candidate) =>
      candidate.status === 'pending' &&
      candidate.autoValidation?.state !== 'queued' &&
      candidate.autoValidation?.state !== 'running' &&
      !suggested.includes(candidate),
  );
  const actions: InboxAction[] = [];
  if (drafts.length > 0) {
    actions.push({
      focus: { kind: 'draft', candidateId: drafts[0].id },
      count: drafts.length,
      noun: drafts.length === 1 ? 'draft to review' : 'drafts to review',
      label: 'Review draft',
    });
  }
  if (manual.length > 0) {
    actions.push({
      focus: { kind: 'candidate', candidateId: manual[0].id },
      count: manual.length,
      noun: 'to assess',
      label: 'Review candidate',
    });
  }
  if (suggestionsWithoutDrafts.length > 0) {
    actions.push({
      focus: { kind: 'candidate', candidateId: suggestionsWithoutDrafts[0].id },
      count: suggestionsWithoutDrafts.length,
      noun:
        suggestionsWithoutDrafts.length === 1
          ? 'suggestion to review'
          : 'suggestions to review',
      label: 'Review suggestion',
    });
  }
  if (blockers > 0) {
    actions.push({
      focus: { kind: 'evidence' },
      count: blockers,
      noun: blockers === 1 ? 'blocker' : 'blockers',
      label: blockers === 1 ? 'View blocker' : 'View blockers',
    });
  }
  return actions;
}

export function groupInbox(
  jobs: ReviewJob[],
  savedPolicy: (job: ReviewJob) => RepositoryPolicy | undefined,
  dailyLimitReached: boolean,
) {
  const groups: Record<InboxGroup, InboxItem[]> = {
    action: [],
    active: [],
    recent: [],
    earlier: [],
  };
  for (const job of jobs) {
    const { codex, checks, quickTest, comparisons } = reviewStageStates(
      job,
      savedPolicy(job),
    );
    const summary = [
      job.evidence?.checks?.status &&
        `Checks: ${checks.label ?? statusText(checks.state)}`,
      latestTest(job) &&
        `Quick test: ${quickTest.label ?? statusText(quickTest.state)}`,
    ]
      .filter(Boolean)
      .join(' · ');
    if (job.status === 'stale') {
      if (job.inboxGroup === 'action') {
        groups.action.push({
          job,
          summary,
          actions: inboxActions(job, 1),
          blocker: 'Target branch changed',
        });
      } else {
        groups.earlier.push({ job, summary, actions: [] });
      }
      continue;
    }
    const stages = [
      { name: 'Codex review', ...codex },
      { name: 'Upstream CI', ...checks },
      { name: 'Quick test', ...quickTest },
      { name: 'Candidate comparison', ...comparisons },
    ];
    const hasCandidates = reviewCandidates(job).length > 0;
    const needsEvidence = !hasCandidates || pendingDecisions(job) > 0;
    const blockers = stages.filter(
      (stage) =>
        stage.disposition === 'attention' &&
        (stage.state !== 'missing' ||
          (needsEvidence && (!!job.evidence || stage.name === 'Codex review'))),
    );
    const activeStage = stages.find((stage) => stage.disposition === 'active');
    const activity =
      queueLabel(job, dailyLimitReached) ??
      (automationInProgress(job)
        ? 'Automatic verification'
        : activeStage &&
          `${activeStage.name} · ${activeStage.label ?? statusText(activeStage.state)}`);
    const actions = inboxActions(job, blockers.length);
    const firstBlocker = blockers[0];
    const item = {
      job,
      summary,
      actions,
      activity,
      blocker:
        firstBlocker &&
        `${firstBlocker.name} · ${firstBlocker.label ?? statusText(firstBlocker.state)}`,
    };
    if (job.inboxGroup) groups[job.inboxGroup].push(item);
  }
  return groups;
}

const inboxSections = {
  action: 'Needs action',
  active: 'In progress',
  recent: 'Completed',
  earlier: 'Superseded',
} satisfies Record<InboxGroup, string>;

export function InboxSection({
  group,
  items,
  interactive,
  onOpen,
}: {
  group: InboxGroup;
  items: InboxItem[];
  interactive: boolean;
  onOpen: (job: ReviewJob, focus?: ReviewFocus, action?: string) => void;
}) {
  const title = inboxSections[group];
  return (
    <section aria-label={title} className="github-review-group">
      {(group === 'recent' || group === 'earlier') && <h2>{title}</h2>}
      <ul>
        {items.map(({ job, summary, actions, blocker, activity }) => {
          const key = jobKey(job);
          const target = `${job.repository} #${job.number}`;
          const progress = group === 'action' ? '' : candidateProgress(job);
          return (
            <li
              key={key}
              className="github-review-inbox-row"
              data-review-key={key}
            >
              <button
                type="button"
                data-review-open=""
                className="github-review-inbox-item"
                disabled={!interactive}
                aria-label={`Open ${target} at ${job.headSha.slice(0, 12)}`}
                aria-describedby={`review-inbox-meta-${key}`}
                onClick={() => onOpen(job)}
              >
                <span className="github-review-inbox-content">
                  <span className="github-review-pr-id">{target}</span>
                  <strong>{job.title || `Pull request #${job.number}`}</strong>
                  <span
                    className="github-review-meta"
                    id={`review-inbox-meta-${key}`}
                  >
                    {job.source === 'requested' && (
                      <span>Requested from you</span>
                    )}
                    {group !== 'action' && (
                      <>
                        <code>{job.headSha.slice(0, 8)}</code>
                        {(group === 'recent' || group === 'earlier') && (
                          <span>
                            Base <Commit sha={job.baseSha} />
                          </span>
                        )}
                        {summary && <span>{summary}</span>}
                        {progress && <strong>{progress}</strong>}
                      </>
                    )}
                    {actions.length > 0 && (
                      <strong>
                        {actions
                          .map(({ count, noun }) => `${count} ${noun}`)
                          .join(' · ')}
                      </strong>
                    )}
                    {group === 'action' && blocker && <span>{blocker}</span>}
                  </span>
                </span>
                <span className="github-review-item-aside">
                  {group === 'active' && (
                    <Status
                      value={job.status === 'queued' ? 'queued' : 'running'}
                      label={activity}
                    />
                  )}
                  {(group === 'recent' || group === 'earlier') && (
                    <RevisionStatus job={job} />
                  )}
                  <span aria-hidden="true">›</span>
                </span>
              </button>
              {group === 'action' && actions.length > 0 && (
                <div className="github-review-inbox-actions">
                  {actions.map(({ focus, label }, index) => (
                    <Button
                      key={label}
                      data-review-action={label}
                      aria-label={`${label} for ${target}`}
                      primary={index === 0}
                      disabled={!interactive}
                      onClick={() => onOpen(job, focus, label)}
                    >
                      {label}
                    </Button>
                  ))}
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
