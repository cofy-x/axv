import { useEffect, useId, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Button, PixelIcon, Select } from '@cofy-x/axv-nextstep';
import { SerialPoller } from '../../shared/api/serial-poller';
import {
  githubReviewAPI,
  reviewCheckURL,
  reviewError,
  reviewPRURL,
} from './api';
import type {
  RepositoryPolicy,
  ReviewChecks,
  ReviewInboxPage,
  ReviewInboxView,
  ReviewJob,
  ReviewTarget,
  ReviewTest,
  TestRecommendation,
} from './api';

import { CandidateDecisions } from './CandidateDecisions';
import type { AssessmentControls, CandidateFocus } from './CandidateDecisions';
import { latestTest, reviewStageStates } from './review-evidence';
import type { ReviewStageState } from './review-evidence';
import './github-review-overview.css';
import './github-review-inbox.css';

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

function statusText(value: string) {
  return Object.hasOwn(labels, value) ? labels[value] : value;
}

function Commit({ sha }: { sha: string }) {
  return <code title={sha}>{sha.slice(0, 12)}</code>;
}

function Status({ value, label }: { value: string; label?: string }) {
  return (
    <span className="github-review-state" data-state={value}>
      {label ?? statusText(value)}
    </span>
  );
}

function queueLabel(job: ReviewJob, dailyLimitReached: boolean) {
  if (job.queueReason) return job.queueReason;
  if (job.status === 'queued' && dailyLimitReached)
    return 'Waiting for daily reset';
}

function reviewCandidates(job: ReviewJob) {
  return [...(job.candidates ?? []), ...(job.followups ?? [])];
}

function automationInProgress(job: ReviewJob) {
  return (
    job.status !== 'stale' &&
    reviewCandidates(job).some(
      (candidate) =>
        candidate.autoValidation?.state === 'queued' ||
        candidate.autoValidation?.state === 'running',
    )
  );
}

function pendingSuggestions(job: ReviewJob) {
  if (job.status === 'stale') return 0;
  return reviewCandidates(job).filter(
    (candidate) =>
      candidate.status === 'pending' &&
      candidate.autoValidation?.state === 'verified' &&
      candidate.autoValidation.suggestion,
  ).length;
}

function pendingDecisions(job: ReviewJob) {
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

function RevisionStatus({ job }: { job: ReviewJob }) {
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

function jobKey(job: ReviewTarget) {
  return [
    job.repository.toLowerCase(),
    job.number,
    job.baseSha,
    job.headSha,
  ].join('/');
}

function candidateProgress(job: ReviewJob) {
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

function Updated({ time, seconds }: { time?: string; seconds?: boolean }) {
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

function Stage({
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

type CheckRun = NonNullable<ReviewChecks['runs']>[number];

function checkOutcome(run: CheckRun) {
  if (run.status !== 'completed') return 'pending';
  switch (run.conclusion) {
    case 'success':
      return 'passed';
    case 'skipped':
      return 'skipped';
    case 'neutral':
      return 'neutral';
    case 'timed_out':
    case 'cancelled':
    case 'action_required':
    case 'failure':
    case 'startup_failure':
    case 'stale':
      return 'failed';
    default:
      return 'pending';
  }
}

function checkoutKind(run: CheckRun, headSha: string) {
  if (run.executionSha === headSha) return 'PR head';
  if (/^refs\/(?:remotes\/)?pull\/\d+\/merge$/.test(run.executionRef ?? '')) {
    return 'merge commit';
  }
  return run.executionRef;
}

function checkoutsHint(checks: ReviewChecks | undefined) {
  const applicable = (checks?.runs ?? []).filter(
    (run) => checkOutcome(run) !== 'skipped',
  );
  if (!applicable.length) return;
  const unknown = applicable.filter((run) => !run.executionSha).length;
  if (unknown === applicable.length) return 'Checkout unreported';
  if (unknown) return `${unknown} without checkout`;
  const checkouts = new Set(applicable.map((run) => run.executionSha));
  if (checkouts.size > 1) return `${checkouts.size} checkouts`;
  const sha = applicable[0].executionSha;
  if (sha)
    return (
      <>
        Checkout <Commit sha={sha} />
      </>
    );
}

function ComparisonOverview({
  job,
  stage,
  onOpen,
}: {
  job: ReviewJob;
  stage: ReviewStageState;
  onOpen: (focus: CandidateFocus) => void;
}) {
  const results = reviewCandidates(job).flatMap((candidate) => {
    const run = candidate.comparisons?.at(-1);
    return run ? [{ run, candidate }] : [];
  });
  if (!results.length) return null;
  const differences = results.filter(
    ({ run }) =>
      run.outcome === 'head_only_failed' ||
      run.outcome === 'baseline_only_failed',
  ).length;
  const inconclusive = results.filter(
    ({ run }) =>
      ['error', 'canceled'].includes(run.status) ||
      run.outcome === 'inconclusive',
  ).length;
  const description = [
    differences > 0 &&
      `${differences} assertion ${differences === 1 ? 'difference' : 'differences'}`,
    inconclusive > 0 && `${inconclusive} inconclusive`,
  ]
    .filter(Boolean)
    .join(' · ');
  const open = ({ candidate, run }: (typeof results)[number]) =>
    onOpen({
      kind: 'comparison',
      candidateId: candidate.id,
      comparisonId: run.id,
    });
  return (
    <div className="github-review-comparison-overview">
      <strong>Candidate comparisons</strong>
      <span className="github-review-stage-status">
        <Status value={stage.state} label={stage.label} />
        {description && (
          <span className="github-review-stage-hint">{description}</span>
        )}
      </span>
      {results.length === 1 ? (
        <Button onClick={() => open(results[0])}>View comparison</Button>
      ) : (
        <details className="github-review-comparison-links">
          <summary>View comparisons</summary>
          <ul>
            {results.map((result) => {
              const { candidate, run } = result;
              const label =
                'title' in candidate && typeof candidate.title === 'string'
                  ? candidate.title
                  : (job.findings?.[candidate.index]?.title ??
                    'Candidate comparison');
              return (
                <li key={run.id}>
                  <Button onClick={() => open(result)}>{label}</Button>
                </li>
              );
            })}
          </ul>
        </details>
      )}
    </div>
  );
}

function CheckList({ runs, headSha }: { runs: CheckRun[]; headSha: string }) {
  if (!runs.length) return null;
  return (
    <ul className="github-review-checks">
      {runs.map((run, index) => {
        const url = reviewCheckURL(run.url);
        const provenance = reviewCheckURL(run.provenanceUrl);
        const kind = checkoutKind(run, headSha);
        return (
          <li key={`${index}/${run.name}`}>
            <div className="github-review-row">
              {url ? (
                <a href={url} target="_blank" rel="noreferrer">
                  {run.name}
                </a>
              ) : (
                <span>{run.name}</span>
              )}
              <span>{(run.conclusion || run.status).replaceAll('_', ' ')}</span>
            </div>
            {run.scope && (
              <p className="github-review-check-scope">{run.scope}</p>
            )}
            {run.conclusion !== 'skipped' && (
              <div className="github-review-meta">
                <span>
                  Checkout{' '}
                  {run.executionSha ? (
                    <>
                      <Commit sha={run.executionSha} />
                      {kind && ` · ${kind}`}
                    </>
                  ) : (
                    'unknown'
                  )}
                </span>
                {provenance && (
                  <a href={provenance} target="_blank" rel="noreferrer">
                    Checkout evidence
                  </a>
                )}
              </div>
            )}
          </li>
        );
      })}
    </ul>
  );
}

function CheckRuns({ runs, headSha }: { runs: CheckRun[]; headSha: string }) {
  const groups: Record<ReturnType<typeof checkOutcome>, CheckRun[]> = {
    failed: [],
    pending: [],
    passed: [],
    skipped: [],
    neutral: [],
  };
  for (const run of runs) groups[checkOutcome(run)].push(run);
  const ordered = Object.values(groups).flat();
  if (runs.length <= 5) return <CheckList runs={ordered} headSha={headSha} />;
  const summary = Object.entries(groups)
    .filter(([, items]) => items.length)
    .map(([label, items]) => `${items.length} ${label}`)
    .join(' · ');
  const visibleCount = Math.min(
    3,
    groups.failed.length + groups.pending.length,
  );
  const checkouts = new Map<string, { run: CheckRun; count: number }>();
  for (const run of ordered) {
    if (!run.executionSha || run.conclusion === 'skipped') continue;
    const key = JSON.stringify([run.executionSha, checkoutKind(run, headSha)]);
    const existing = checkouts.get(key);
    if (existing) existing.count++;
    else checkouts.set(key, { run, count: 1 });
  }
  const unverified = ordered.filter(
    (run) => !run.executionSha && run.conclusion !== 'skipped',
  ).length;
  return (
    <>
      <p className="github-review-check-totals">{summary}</p>
      {Array.from(checkouts, ([key, { run, count }]) => {
        const kind = checkoutKind(run, headSha);
        return (
          <div className="github-review-meta" key={key}>
            <span>
              Checkout <Commit sha={run.executionSha!} />
              {kind && ` · ${kind}`} · {count}{' '}
              {count === 1 ? 'check' : 'checks'}
            </span>
          </div>
        );
      })}
      {unverified > 0 && (
        <div className="github-review-meta">
          Checkout unknown for {unverified}{' '}
          {unverified === 1 ? 'check' : 'checks'}
        </div>
      )}
      <CheckList runs={ordered.slice(0, visibleCount)} headSha={headSha} />
      <details className="github-review-identities">
        <summary>
          {visibleCount
            ? `${runs.length - visibleCount} more check runs`
            : `Show ${runs.length} check runs`}
        </summary>
        <CheckList runs={ordered.slice(visibleCount)} headSha={headSha} />
      </details>
    </>
  );
}

function TestCommand({
  command,
  label,
  collapsible = false,
}: {
  command?: string[];
  label: string;
  collapsible?: boolean;
}) {
  if (!command?.length) return null;
  const argumentsList = (
    <div role="group" className="github-review-command" aria-label={label}>
      {command.map((arg, index) => (
        <code key={index}>{arg}</code>
      ))}
    </div>
  );
  return collapsible ? (
    <details className="github-review-identities">
      <summary>{label}</summary>
      {argumentsList}
    </details>
  ) : (
    argumentsList
  );
}

function TestReceipt({
  test,
  headSha,
  baseSha,
  foldOutput = false,
}: {
  test: ReviewTest;
  headSha: string;
  baseSha: string;
  foldOutput?: boolean;
}) {
  const fields = [
    ['VM', test.vmId],
    ['Project', test.projectId],
    ['Task', test.taskId],
    ['Attempt ID', test.attemptId],
    ['Task exit code', test.exitCode?.toString()],
  ].filter(([, value]) => value);
  const output = test.log && (
    <pre
      className="github-review-log"
      tabIndex={0}
      aria-label={`Test output for attempt ${test.attempt}`}
    >
      {test.log}
    </pre>
  );
  return (
    <>
      {test.selection && (
        <div className="github-review-selection">
          <strong>{test.testName || 'Test selection'}</strong>
          <span className="github-review-detail">
            {test.selection === 'manual'
              ? 'Selected for this PR'
              : 'Automatic selection'}
            {test.entryPath && (
              <>
                {' '}
                · <code>{test.entryPath}</code>
              </>
            )}
          </span>
          {test.selectionReason && <p>{test.selectionReason}</p>}
        </div>
      )}
      <div className="github-review-meta">
        <span>
          {test.checkoutSha ? (
            <>
              Checkout <Commit sha={test.checkoutSha} /> ·{' '}
              {test.checkoutSha === headSha ? 'PR head' : 'unexpected commit'}
            </>
          ) : (
            <>
              Target <Commit sha={headSha} /> · PR head
            </>
          )}
        </span>
        {!test.checkoutSha && test.taskId && <span>Checkout unconfirmed</span>}
        {test.selectionBaseSha === baseSha && (
          <span>
            Selection base <Commit sha={baseSha} />
          </span>
        )}
      </div>
      {test.selectionBaseSha && test.selectionBaseSha !== baseSha && (
        <p className="github-review-warning">
          Selected against <Commit sha={test.selectionBaseSha} />. Current
          review base <Commit sha={baseSha} />.
        </p>
      )}
      {test.error && <p className="github-review-problem">{test.error}</p>}
      {test.status === 'failed' && test.exitCode !== undefined && (
        <p className="github-review-detail">
          Test exited with code {test.exitCode}.
        </p>
      )}
      {output &&
        (foldOutput ? (
          <details className="github-review-identities">
            <summary>Test output for attempt {test.attempt}</summary>
            {output}
          </details>
        ) : (
          output
        ))}
      {test.truncated && (
        <p className="github-review-detail">
          Showing partial output from this VM task.
        </p>
      )}
      {(fields.length > 0 || test.updatedAt) && (
        <details className="github-review-identities">
          <summary>Execution receipt</summary>
          <dl>
            {fields.map(([label, value]) => (
              <div key={label}>
                <dt>{label}</dt>
                <dd>
                  <code>{value}</code>
                </dd>
              </div>
            ))}
            {test.updatedAt && (
              <div>
                <dt>Updated</dt>
                <dd>
                  <Updated time={test.updatedAt} />
                </dd>
              </div>
            )}
          </dl>
        </details>
      )}
    </>
  );
}

const finishedTests = [
  'passed',
  'failed',
  'error',
  'canceled',
  'not_applicable',
];

type ConfirmTestRecommendation = (
  job: ReviewJob,
  recommendation: TestRecommendation,
) => void;

function RecommendedQuickTest({
  recommendation,
  disabled,
  onRun,
}: {
  recommendation: TestRecommendation;
  disabled: boolean;
  onRun: () => void;
}) {
  return (
    <section
      className="github-review-recommendation"
      aria-label="Recommended quick test"
    >
      <div className="github-review-row">
        <strong>{recommendation.testName}</strong>
        <Button primary disabled={disabled} onClick={onRun}>
          Run recommended test
        </Button>
      </div>
      <p>{recommendation.reason}</p>
      <div className="github-review-meta">
        <span>
          Run on PR head <Commit sha={recommendation.headSha} />
        </span>
        <span>
          Base <Commit sha={recommendation.baseSha} />
        </span>
      </div>
      <TestCommand
        command={recommendation.command}
        label="Recommended test command"
        collapsible
      />
    </section>
  );
}

function QuickTestStage({
  job,
  stage,
  policy,
  disabled,
  onRun,
  onConfirmRecommendation,
}: {
  job: ReviewJob;
  stage: ReviewStageState;
  policy?: RepositoryPolicy;
  disabled: boolean;
  onRun: (job: ReviewJob, attempt: number, testId?: string) => void;
  onConfirmRecommendation: ConfirmTestRecommendation;
}) {
  const [selectedId, setSelectedId] = useState('');
  const { evidence } = job;
  const test = latestTest(job);
  const original = evidence?.policy;
  const originalCommand = original?.testProfiles?.length
    ? undefined
    : original?.testCommand;
  const attemptCommand =
    test?.command ?? (test?.selection ? undefined : originalCommand);
  const profiles = policy?.testProfiles ?? [];
  const selected = profiles.find((item) => item.id === selectedId);
  const single = profiles.length ? undefined : policy?.testCommand;
  const canRun =
    !!evidence &&
    job.status !== 'stale' &&
    (!test || finishedTests.includes(test.status)) &&
    !!(profiles.length || single?.length);
  const commandChanged =
    !!test &&
    !!single?.length &&
    JSON.stringify(single) !== JSON.stringify(attemptCommand);
  const offer = job.quickTestRecommendation;
  const currentOffer =
    job.status !== 'stale' &&
    offer?.headSha === job.headSha &&
    offer.baseSha === job.baseSha &&
    offer.latestAttempt === (test?.attempt ?? 0)
      ? offer
      : undefined;
  const reusable =
    currentOffer?.status === 'reuse'
      ? evidence?.tests.find(
          (receipt) =>
            receipt.attempt === currentOffer.reusableAttempt &&
            receipt.checkoutSha === job.headSha &&
            (receipt.status === 'passed' || receipt.status === 'failed') &&
            (!currentOffer.reusableStatus ||
              currentOffer.reusableStatus === receipt.status),
        )
      : undefined;
  const proposal =
    currentOffer?.status === 'run' ? (
      <RecommendedQuickTest
        recommendation={currentOffer}
        disabled={disabled || !canRun}
        onRun={() => onConfirmRecommendation(job, currentOffer)}
      />
    ) : null;
  const options = canRun && (
    <div className="github-review-next-test">
      {profiles.length > 0 && (
        <Select
          label="Next test for this PR"
          value={selected?.id ?? ''}
          disabled={disabled}
          onChange={(event) => setSelectedId(event.target.value)}
        >
          <option value="">Automatic selection</option>
          {profiles.map((profile) => (
            <option key={profile.id} value={profile.id}>
              {profile.name}
            </option>
          ))}
        </Select>
      )}
      {selected && (
        <div className="github-review-detail">
          Required entry file · <code>{selected.entryPath}</code>
        </div>
      )}
      {commandChanged && (
        <p className="github-review-detail">
          Saved command for the next attempt
        </p>
      )}
      <TestCommand
        command={selected?.command ?? (commandChanged ? single : undefined)}
        label="Saved command for the next attempt"
        collapsible={!!selected}
      />
      <Button
        disabled={disabled}
        onClick={() => onRun(job, test?.attempt ?? 0, selected?.id)}
      >
        {selected
          ? 'Run selected test'
          : profiles.length
            ? 'Run automatic selection'
            : commandChanged
              ? 'Run updated test'
              : test
                ? 'Retry test'
                : 'Run test'}
      </Button>
    </div>
  );
  return (
    <Stage
      title="Local quick test"
      stage={stage}
      hint={
        test && (
          <>
            {test.testName}
            {test.testName && test.checkoutSha && ' · '}
            {test.checkoutSha && (
              <>
                Checkout <Commit sha={test.checkoutSha} />
              </>
            )}
            {reusable && (
              <>
                {(test.testName || test.checkoutSha) && ' · '}
                {reusable.attempt === test.attempt
                  ? 'Receipt available'
                  : `Receipt available from attempt ${reusable.attempt}`}
              </>
            )}
          </>
        )
      }
    >
      {proposal}
      {test && (
        <div className="github-review-attempt-heading">
          <strong>
            Attempt {test.attempt}
            {stage.state !== test.status &&
              ` · Recorded ${statusText(test.status).toLowerCase()}`}
          </strong>
        </div>
      )}
      {test && (
        <TestReceipt
          test={test}
          headSha={job.headSha}
          baseSha={job.baseSha}
          foldOutput={
            test.status === 'passed' && stage.disposition === 'attention'
          }
        />
      )}
      <TestCommand
        command={attemptCommand}
        label="Test command"
        collapsible={!!test?.selection}
      />
      {options && (proposal || reusable) ? (
        <details className="github-review-identities">
          <summary>Other test options</summary>
          {options}
        </details>
      ) : (
        options
      )}
      {(evidence?.tests ?? [])
        .slice(0, -1)
        .reverse()
        .map((previous) => (
          <details className="github-review-previous" key={previous.attempt}>
            <summary>
              Attempt {previous.attempt} · {statusText(previous.status)}
              {previous.testName && ` · ${previous.testName}`}
              {previous.attempt === reusable?.attempt && ' · Reusable receipt'}
              {previous.selectionBaseSha &&
                previous.selectionBaseSha !== job.baseSha &&
                ' · Earlier comparison'}
            </summary>
            <TestCommand
              command={
                previous.command ??
                (previous.selection ? undefined : originalCommand)
              }
              label={`Command for attempt ${previous.attempt}`}
              collapsible={!!previous.selection}
            />
            <TestReceipt
              test={previous}
              headSha={job.headSha}
              baseSha={job.baseSha}
            />
          </details>
        ))}
    </Stage>
  );
}

function ReviewDetail({
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

export type ReviewFocus = CandidateFocus | { kind: 'evidence' };

const inboxViews = {
  action: 'Needs action',
  active: 'In progress',
  history: 'History',
} as const;

type InboxGroup = 'action' | 'active' | 'recent' | 'earlier';

type InboxAction = {
  focus: ReviewFocus;
  count: number;
  noun: string;
  label: string;
};

type InboxItem = {
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

function groupInbox(
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

function InboxSection({
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

export function ReviewHistory({
  account,
  hasJobs,
  refreshKey,
  assessments,
  retryDisabled,
  savedPolicy,
  testDisabled,
  onRetry,
  onRetryTest,
  onConfirmRecommendation,
  onRefreshChecks,
  checksDisabled,
  onLatest,
  latestDisabled,
  interactive,
  automation,
  refreshStatus,
  dailyLimitReached,
}: {
  onLatest: (job: ReviewJob, select: (target: ReviewTarget) => void) => void;
  latestDisabled: boolean;
  automation: (open: (target: ReviewTarget) => void) => ReactNode;
  dailyLimitReached: boolean;
  refreshStatus?: ReactNode;
  account: string;
  hasJobs: boolean;
  refreshKey: number;
  assessments: AssessmentControls;
  retryDisabled: boolean;
  savedPolicy: (job: ReviewJob) => RepositoryPolicy | undefined;
  testDisabled: boolean;
  onRetry: (job: ReviewJob) => void;
  onRetryTest: (job: ReviewJob, attempt: number, testId?: string) => void;
  onConfirmRecommendation: ConfirmTestRecommendation;
  onRefreshChecks: (job: ReviewJob) => void;
  checksDisabled: boolean;
  interactive: boolean;
}) {
  const [view, setView] = useState<ReviewInboxView>('action');
  const [cursors, setCursors] = useState(['']);
  const [listing, setListing] = useState<{
    key: string;
    page: ReviewInboxPage;
  }>();
  const [detail, setDetail] = useState<{ key: string; job: ReviewJob }>();
  const [readError, setReadError] = useState<{
    key: string;
    message: string;
  }>();
  const [selection, setSelection] = useState<{
    target: ReviewTarget;
    focus?: ReviewFocus;
    action?: string;
  }>();
  const container = useRef<HTMLDivElement>(null);
  const cursor = cursors.at(-1) ?? '';
  const listKey = JSON.stringify([view, cursor]);
  const target = selection?.target;
  const selectedKey = target ? jobKey(target) : '';
  const resourceKey = selectedKey || listKey;
  const error = readError?.key === resourceKey ? readError.message : '';
  const page = listing?.key === listKey ? listing.page : undefined;
  const selected = detail?.key === selectedKey ? detail.job : undefined;
  const prURL = target && reviewPRURL(target.repository, target.number);
  const groups = groupInbox(page?.jobs ?? [], savedPolicy, dailyLimitReached);
  const visibleGroups: InboxGroup[] =
    view === 'history' ? ['recent', 'earlier'] : [view];

  useEffect(() => {
    if (!interactive) return;
    const poll = new SerialPoller({
      timeoutMs: 15000,
      intervalMs: 2500,
      read: async ({ signal, isCurrent }) => {
        const result = target
          ? await githubReviewAPI.job(target, signal)
          : await githubReviewAPI.inbox(view, cursor, signal);
        if (!isCurrent() || signal.aborted) return;
        if (result.account.toLowerCase() !== account.toLowerCase()) {
          setReadError({
            key: resourceKey,
            message:
              'The review account changed. Refresh to load the current account.',
          });
          return;
        }
        if ('job' in result) {
          if (jobKey(result.job) !== selectedKey) return;
          setDetail({ key: selectedKey, job: result.job });
        } else {
          setListing({ key: listKey, page: result });
        }
        setReadError(undefined);
      },
      onError: (failure) =>
        setReadError({ key: resourceKey, message: reviewError(failure) }),
    });
    poll.start();
    return () => poll.stop();
  }, [
    account,
    cursor,
    interactive,
    listKey,
    refreshKey,
    resourceKey,
    selectedKey,
    target,
    view,
  ]);

  function open(target: ReviewTarget, focus?: ReviewFocus, action?: string) {
    const { repository, number, headSha, baseSha } = target;
    setDetail(undefined);
    setSelection({
      target: { repository, number, headSha, baseSha },
      focus,
      action,
    });
    if (!focus) {
      requestAnimationFrame(() => {
        container.current
          ?.querySelector<HTMLButtonElement>('[data-return-reviews]')
          ?.focus();
      });
    }
  }

  function returnToList() {
    setSelection(undefined);
    requestAnimationFrame(() => {
      const rows =
        container.current?.querySelectorAll<HTMLElement>('[data-review-key]');
      const row = Array.from(rows ?? []).find(
        (element) => element.dataset.reviewKey === selectedKey,
      );
      const actions = row?.querySelectorAll<HTMLButtonElement>(
        '[data-review-action]',
      );
      const previouslyFocused = Array.from(actions ?? []).find(
        (button) => button.dataset.reviewAction === selection?.action,
      );
      const target =
        previouslyFocused ??
        row?.querySelector<HTMLButtonElement>('[data-review-open]') ??
        container.current?.querySelector<HTMLButtonElement>(
          '.github-review-inbox-views [aria-pressed="true"]',
        );
      target?.focus();
    });
  }

  return (
    <div ref={container} className="github-review-inbox">
      {selection ? (
        <>
          <div className="github-review-row github-review-detail-nav">
            <Button
              data-return-reviews=""
              onClick={returnToList}
              disabled={!interactive}
            >
              ← Pull requests
            </Button>
            <div className="github-review-detail-actions">
              {prURL && (
                <a href={prURL} target="_blank" rel="noreferrer">
                  Open on GitHub ↗
                </a>
              )}
              {refreshStatus}
            </div>
          </div>
          {selected ? (
            <ReviewDetail
              latestControl={
                <div className="github-review-review-latest">
                  <Button
                    disabled={latestDisabled}
                    onClick={() =>
                      onLatest(selected, (next) =>
                        setSelection((current) =>
                          current === selection
                            ? {
                                target: {
                                  repository: next.repository,
                                  number: next.number,
                                  headSha: next.headSha,
                                  baseSha: next.baseSha,
                                },
                              }
                            : current,
                        ),
                      )
                    }
                  >
                    Review latest commit
                  </Button>
                  <span className="github-review-detail">
                    A new comparison uses one daily review. At the limit it
                    waits until reset.
                  </span>
                </div>
              }
              key={selectedKey}
              job={selected}
              dailyLimitReached={dailyLimitReached}
              focus={selection.focus}
              assessments={assessments}
              retryDisabled={retryDisabled}
              testDisabled={testDisabled}
              savedPolicy={savedPolicy(selected)}
              onRetry={onRetry}
              onRetryTest={onRetryTest}
              onConfirmRecommendation={onConfirmRecommendation}
              onRefreshChecks={onRefreshChecks}
              checksDisabled={checksDisabled}
            />
          ) : !error ? (
            <p role="status">Loading the selected revision…</p>
          ) : null}
          {error && (
            <p className="app-error" role="alert">
              {error}
            </p>
          )}
        </>
      ) : (
        <>
          {automation(open)}
          {(hasJobs || !!page?.jobs.length) && (
            <nav className="github-review-inbox-views" aria-label="Inbox views">
              {(Object.keys(inboxViews) as ReviewInboxView[]).map((id) => {
                const count =
                  view === id && id !== 'history' && !cursor
                    ? groups[id].length
                    : 0;
                return (
                  <button
                    key={id}
                    type="button"
                    className="github-review-inbox-filter"
                    aria-pressed={id === view}
                    disabled={!interactive}
                    onClick={() => {
                      if (id !== view) {
                        setView(id);
                        setCursors(['']);
                      }
                    }}
                  >
                    {inboxViews[id]}
                    {count > 0 && (
                      <span className="github-review-count" aria-hidden="true">
                        {count}
                        {page?.nextCursor && '+'}
                      </span>
                    )}
                  </button>
                );
              })}
            </nav>
          )}
          {error && (
            <p className="app-error" role="alert">
              {error}
            </p>
          )}
          {!page ? (
            !error && <p role="status">Loading pull requests…</p>
          ) : !hasJobs && !page.jobs.length ? (
            <div className="github-review-empty">
              <PixelIcon name="code-review" size={40} />
              <div>
                <h2>No reviews yet</h2>
                <p>
                  Queue a PR by link or scan for watched pull requests and
                  review requests.
                </p>
              </div>
            </div>
          ) : visibleGroups.some((id) => groups[id].length > 0) ? (
            visibleGroups
              .filter((id) => groups[id].length > 0)
              .map((id) => (
                <InboxSection
                  key={id}
                  group={id}
                  items={groups[id]}
                  interactive={interactive}
                  onOpen={open}
                />
              ))
          ) : (
            <div className="github-review-empty">
              <div>
                <h2>
                  {page.nextCursor
                    ? 'No matching reviews on this page'
                    : cursor
                      ? 'No reviews remain on this page'
                      : view === 'action'
                        ? 'Nothing needs your attention'
                        : view === 'active'
                          ? 'Nothing in progress'
                          : 'No completed reviews'}
                </h2>
              </div>
            </div>
          )}
          {(cursor || page?.nextCursor) && (
            <nav
              className="github-review-page-nav"
              aria-label="Pull request pages"
            >
              <Button
                disabled={!interactive || !cursor}
                onClick={() => setCursors(cursors.slice(0, -1))}
              >
                Previous page
              </Button>
              <span aria-live="polite">Page {cursors.length}</span>
              <Button
                disabled={!interactive || !page?.nextCursor}
                onClick={() => {
                  if (page?.nextCursor)
                    setCursors([...cursors, page.nextCursor]);
                }}
              >
                Next page
              </Button>
            </nav>
          )}
        </>
      )}
    </div>
  );
}
