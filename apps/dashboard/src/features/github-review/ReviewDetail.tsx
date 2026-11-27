import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Button } from '@cofy-x/axv-nextstep';
import type { RepositoryPolicy, ReviewJob } from './api';
import { CandidateDecisions } from './CandidateDecisions';
import type { AssessmentControls, CandidateFocus } from './CandidateDecisions';
import { reviewStageStates } from './review-evidence';
import {
  CheckRuns,
  ComparisonOverview,
  QuickTestStage,
  checkoutsHint,
} from './ReviewEvidenceStages';
import type { ConfirmTestRecommendation } from './ReviewEvidenceStages';
import type { ReviewFocus } from './ReviewInboxList';
import {
  Commit,
  Stage,
  Status,
  Updated,
  jobKey,
  queueLabel,
  statusText,
} from './ReviewPrimitives';

export function ReviewDetail({
  latestControl,
  job,
  dailyLimitReached,
  assessments,
  retryDisabled,
  testDisabled,
  savedPolicy,
  onRetry,
  onRetryTest,
  onConfirmRecommendation,
  onRefreshChecks,
  checksDisabled,
  focus,
}: {
  job: ReviewJob;
  dailyLimitReached: boolean;
  assessments: AssessmentControls;
  retryDisabled: boolean;
  testDisabled: boolean;
  savedPolicy?: RepositoryPolicy;
  onRetry: (job: ReviewJob) => void;
  onRetryTest: (job: ReviewJob, attempt: number, testId?: string) => void;
  onConfirmRecommendation: ConfirmTestRecommendation;
  onRefreshChecks: (job: ReviewJob) => void;
  checksDisabled: boolean;
  latestControl: ReactNode;
  focus?: ReviewFocus;
}) {
  const { evidence } = job;
  const stages = reviewStageStates(job, savedPolicy);
  const evidenceDetails = useRef<HTMLDetailsElement>(null);
  const evidenceSummary = useRef<HTMLElement>(null);
  const [candidateFocus, setCandidateFocus] = useState<CandidateFocus>();
  const stale = job.status === 'stale';
  const target = `${job.repository} #${job.number}`;
  const review = evidence?.review;
  const reviewProblem = review?.error || (!review && !stale && job.error);
  const checks = evidence?.checks;
  const evidenceIssues = [
    { title: 'Codex', ...stages.codex },
    { title: 'CI', ...stages.checks },
    { title: 'Quick test', ...stages.quickTest },
    { title: 'Comparison', ...stages.comparisons },
  ].filter((stage) => !stale && stage.disposition === 'attention');
  useEffect(() => {
    if (focus?.kind !== 'evidence' || !evidenceDetails.current) return;
    evidenceDetails.current.open = true;
    evidenceSummary.current?.focus({ preventScroll: true });
    evidenceSummary.current?.scrollIntoView({ block: 'nearest' });
  }, [focus]);
  return (
    <article className="github-review-sheet" aria-label={`Review of ${target}`}>
      <header className="github-review-pr-header">
        <div className="github-review-row">
          <span className="github-review-pr-id">{target}</span>
          {job.status !== 'analyzed' && (
            <Status
              value={job.status}
              label={queueLabel(job, dailyLimitReached)}
            />
          )}
        </div>
        <h2>{job.title || `Pull request #${job.number}`}</h2>
        <div className="github-review-meta">
          <span>
            {job.source === 'requested'
              ? 'Requested from you'
              : 'Watched repository'}
          </span>
          <span>
            PR head <Commit sha={job.headSha} />
          </span>
          <Updated time={job.updatedAt} />
        </div>
      </header>
      {job.queueReason && (
        <div className="github-review-warning">{latestControl}</div>
      )}
      {stale && (
        <p className="github-review-warning">
          {job.observedBaseSha ? (
            <>
              The comparison for this head changed. This review used base{' '}
              <Commit sha={job.baseSha} />. The observed target is{' '}
              <Commit sha={job.observedBaseSha} />.
            </>
          ) : (
            'This revision is superseded. The evidence below belongs to the earlier commit.'
          )}
        </p>
      )}
      {job.error && !stale && job.error !== reviewProblem && (
        <p className="github-review-problem">{job.error}</p>
      )}
      <details ref={evidenceDetails} className="github-review-overview">
        <summary
          ref={evidenceSummary}
          className="github-review-evidence-summary"
        >
          <strong>View evidence</strong>
          {evidenceIssues.length > 0 && (
            <span className="github-review-evidence-issues">
              {evidenceIssues.map(({ title, state, label }) => (
                <Status
                  key={title}
                  value={state}
                  label={`${title} · ${label ?? statusText(state)}`}
                />
              ))}
            </span>
          )}
        </summary>
        <section aria-label="Review overview">
          <section className="github-review-stages" aria-label="Review stages">
            <Stage
              title="Codex review"
              stage={stages.codex}
              hint={stages.codex.disposition === 'passed' && 'Captured PR diff'}
            >
              {!job.queueReason && latestControl}
              {(review?.startedAt || review?.updatedAt) && (
                <div className="github-review-meta">
                  {review.startedAt && (
                    <span>
                      Started <Updated time={review.startedAt} />
                    </span>
                  )}
                  {review.updatedAt && (
                    <span>
                      {review.status === 'completed' ? 'Finished' : 'Updated'}{' '}
                      <Updated time={review.updatedAt} />
                    </span>
                  )}
                </div>
              )}
              {reviewProblem && (
                <p className="github-review-problem">{reviewProblem}</p>
              )}
              {review?.authorContext && (
                <details className="github-review-identities">
                  <summary>Author context used for review</summary>
                  <strong className="github-review-author-title">
                    {review.authorContext.title}
                    {review.authorContext.titleTruncated && '…'}
                  </strong>
                  {review.authorContext.description ? (
                    <p className="github-review-preserve github-review-author-body">
                      {review.authorContext.description}
                    </p>
                  ) : (
                    <p className="github-review-detail">
                      The author supplied no description.
                    </p>
                  )}
                  {review.authorContext.descriptionTruncated && (
                    <p className="github-review-detail">
                      The review used an excerpt of the author’s description.
                    </p>
                  )}
                </details>
              )}
              {!!review?.messages?.length && (
                <details className="github-review-identities">
                  <summary>Activity</summary>
                  <ol className="github-review-messages">
                    {review.messages.map((message, index) => (
                      <li key={index}>
                        <Updated time={message.at} />
                        <span>{message.text}</span>
                      </li>
                    ))}
                  </ol>
                </details>
              )}
              {!evidence && ['analyzed', 'clean'].includes(job.status) && (
                <Button disabled={retryDisabled} onClick={() => onRetry(job)}>
                  Run with current rules
                </Button>
              )}
              {job.observedBaseSha && stale && (
                <Button disabled={retryDisabled} onClick={() => onRetry(job)}>
                  Review updated comparison
                </Button>
              )}
              {job.status === 'failed' && (
                <Button disabled={retryDisabled} onClick={() => onRetry(job)}>
                  Retry review
                </Button>
              )}
            </Stage>
            <Stage
              title="Upstream CI"
              stage={stages.checks}
              hint={checkoutsHint(checks)}
            >
              <div className="github-review-row">
                <span className="github-review-detail">
                  Checks associated with PR head <Commit sha={job.headSha} />
                </span>
                {evidence && !stale && (
                  <Button
                    disabled={checksDisabled || checks?.status === 'running'}
                    onClick={() => onRefreshChecks(job)}
                  >
                    Refresh checks
                  </Button>
                )}
              </div>
              {checks?.status === 'empty' && (
                <p>GitHub reported no check runs for this head commit.</p>
              )}
              {!checks?.status && stages.checks.disposition === 'attention' && (
                <p>No upstream checks have been recorded for this revision.</p>
              )}
              {checks?.error && (
                <p className="github-review-warning" role="status">
                  {checks.refreshErrorAt && (
                    <>
                      Refresh failed <Updated time={checks.refreshErrorAt} />
                      .{' '}
                    </>
                  )}
                  {checks.error}{' '}
                  {checks.updatedAt &&
                    checks.refreshErrorAt &&
                    'Showing the last recorded observation.'}
                </p>
              )}
              {checks?.runs && (
                <CheckRuns runs={checks.runs} headSha={job.headSha} />
              )}
              {checks?.updatedAt && (
                <p className="github-review-detail">
                  Observed {statusText(checks.status)} ·{' '}
                  <Updated time={checks.updatedAt} />
                </p>
              )}
            </Stage>
            <QuickTestStage
              key={jobKey(job)}
              job={job}
              stage={stages.quickTest}
              policy={savedPolicy}
              disabled={testDisabled}
              onRun={onRetryTest}
              onConfirmRecommendation={onConfirmRecommendation}
            />
          </section>
          <ComparisonOverview
            job={job}
            stage={stages.comparisons}
            onOpen={setCandidateFocus}
          />
          <details className="github-review-identities github-review-evidence-identities">
            <summary>Commits and review rules</summary>
            <dl>
              <div>
                <dt>Head</dt>
                <dd>
                  <code>{job.headSha}</code>
                </dd>
              </div>
              <div>
                <dt>Base</dt>
                <dd>
                  <code>{job.baseSha}</code>
                </dd>
              </div>
              {job.mergeBaseSha && (
                <div>
                  <dt>Diff merge base</dt>
                  <dd>
                    <code>{job.mergeBaseSha}</code>
                  </dd>
                </div>
              )}
            </dl>
            {evidence?.policy?.rules && (
              <p className="github-review-preserve">{evidence.policy.rules}</p>
            )}
          </details>
        </section>
      </details>
      <CandidateDecisions
        job={job}
        controls={assessments}
        focus={
          candidateFocus ?? (focus?.kind !== 'evidence' ? focus : undefined)
        }
      />
    </article>
  );
}
