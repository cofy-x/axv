import { useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Button, PixelIcon, Select } from '@cofy-x/axv-nextstep';
import { reviewCheckURL, reviewPRURL } from './api';
import type {
  RepositoryPolicy,
  ReviewChecks,
  ReviewJob,
  ReviewTest,
} from './api';

import { CandidateDecisions } from './CandidateDecisions';
import type { AssessmentControls } from './CandidateDecisions';

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
  error: 'Unavailable',
  neutral: 'Inconclusive',
  not_applicable: 'Not applicable',
  canceled: 'Canceled',
};

function Commit({ sha }: { sha: string }) {
  return <code title={sha}>{sha.slice(0, 12)}</code>;
}

function Status({ value, label }: { value: string; label?: string }) {
  return (
    <span className="github-review-state" data-state={value}>
      {label ?? (Object.hasOwn(labels, value) ? labels[value] : value)}
    </span>
  );
}

function pendingDecisions(job: ReviewJob) {
  return [...(job.candidates ?? []), ...(job.followups ?? [])].filter(
    (item) => item.status === 'pending',
  ).length;
}

function comparingCandidate(job: ReviewJob) {
  return [...(job.candidates ?? []), ...(job.followups ?? [])].some(
    (candidate) =>
      candidate.comparisons?.some(
        (run) => run.status === 'queued' || run.status === 'running',
      ),
  );
}

function RevisionStatus({ job }: { job: ReviewJob }) {
  if (job.status !== 'stale' && comparingCandidate(job))
    return <Status value="running" label="Comparing revisions" />;
  const quickState = quickTestState(job);
  if (['analyzed', 'clean'].includes(job.status)) {
    const stages = [
      ['Quick test', quickState],
      ['Checks', job.evidence?.checks?.status ?? ''],
    ];
    const attention =
      stages.find(([, state]) =>
        ['failed', 'error', 'blocked', 'canceled', 'interrupted'].includes(
          state,
        ),
      ) ||
      stages.find(([, state]) =>
        ['queued', 'running', 'pending'].includes(state),
      );
    if (attention) {
      const [name, state] = attention;
      return (
        <Status
          value={state}
          label={`${name} ${labels[state].toLowerCase()}`}
        />
      );
    }
  }
  if (job.status !== 'stale' && pendingDecisions(job)) {
    return (
      <Status value="pending" label={`${pendingDecisions(job)} to verify`} />
    );
  }
  if (
    ['clean', 'analyzed'].includes(job.status) &&
    quickState === 'not_applicable'
  )
    return <Status value="not_applicable" label="No applicable quick test" />;
  if (['clean', 'analyzed'].includes(job.status) && quickState === 'stale')
    return <Status value="stale" label="Quick test from earlier comparison" />;
  return <Status value={job.status} />;
}

function jobKey(job: ReviewJob) {
  return [
    job.repository.toLowerCase(),
    job.number,
    job.baseSha,
    job.headSha,
  ].join('/');
}

function latestTest(job: ReviewJob) {
  return job.evidence?.tests?.at(-1);
}

function quickTestState(job: ReviewJob) {
  const test = latestTest(job);
  return test?.selectionBaseSha && test.selectionBaseSha !== job.baseSha
    ? 'stale'
    : test?.status || '';
}

function isCurrentWork(job: ReviewJob) {
  if (job.status === 'stale') return false;
  return (
    comparingCandidate(job) ||
    ['queued', 'reviewing', 'publishing'].includes(job.status) ||
    ['queued', 'running', 'pending'].includes(
      job.evidence?.checks?.status ?? '',
    ) ||
    ['queued', 'running'].includes(quickTestState(job))
  );
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
  state,
  label,
  children,
}: {
  title: string;
  state: string;
  label?: string;
  children?: ReactNode;
}) {
  return (
    <section className="github-review-stage" aria-label={title}>
      <header className="github-review-row">
        <h4>{title}</h4>
        <Status value={state} label={label} />
      </header>
      {children}
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
}: {
  test: ReviewTest;
  headSha: string;
  baseSha: string;
}) {
  const fields = [
    ['VM', test.vmId],
    ['Project', test.projectId],
    ['Task', test.taskId],
    ['Attempt ID', test.attemptId],
    ['Task exit code', test.exitCode?.toString()],
  ].filter(([, value]) => value);
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
      {test.log && (
        <pre
          className="github-review-log"
          tabIndex={0}
          aria-label={`Test output for attempt ${test.attempt}`}
        >
          {test.log}
        </pre>
      )}
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

function QuickTestStage({
  job,
  policy,
  disabled,
  onRun,
}: {
  job: ReviewJob;
  policy?: RepositoryPolicy;
  disabled: boolean;
  onRun: (job: ReviewJob, attempt: number, testId?: string) => void;
}) {
  const [selectedId, setSelectedId] = useState('');
  const { evidence } = job;
  const test = latestTest(job);
  const state = quickTestState(job);
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
  return (
    <Stage
      title="Local quick test"
      state={state || 'unrecorded'}
      label={
        state === 'stale'
          ? 'Earlier comparison'
          : test
            ? undefined
            : !evidence
              ? 'No evidence'
              : original?.testProfiles?.length || originalCommand?.length
                ? 'Waiting'
                : 'Not configured'
      }
    >
      {test && (
        <div className="github-review-attempt-heading">
          <strong>
            Attempt {test.attempt}
            {state === 'stale' && ` · ${labels[test.status] || test.status}`}
          </strong>
        </div>
      )}
      {test && (
        <TestReceipt test={test} headSha={job.headSha} baseSha={job.baseSha} />
      )}
      <TestCommand
        command={attemptCommand}
        label="Test command"
        collapsible={!!test?.selection}
      />
      {canRun && (
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
      )}
      {(evidence?.tests ?? [])
        .slice(0, -1)
        .reverse()
        .map((previous) => (
          <details className="github-review-previous" key={previous.attempt}>
            <summary>
              Attempt {previous.attempt} ·{' '}
              {labels[previous.status] || previous.status}
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
  assessments,
  retryDisabled,
  testDisabled,
  savedPolicy,
  onRetry,
  onRetryTest,
  onRefreshChecks,
  checksDisabled,
}: {
  job: ReviewJob;
  assessments: AssessmentControls;
  retryDisabled: boolean;
  testDisabled: boolean;
  savedPolicy?: RepositoryPolicy;
  onRetry: (job: ReviewJob) => void;
  onRetryTest: (job: ReviewJob, attempt: number, testId?: string) => void;
  onRefreshChecks: (job: ReviewJob) => void;
  checksDisabled: boolean;
  latestControl: ReactNode;
}) {
  const { evidence } = job;
  const stale = job.status === 'stale';
  const target = `${job.repository} #${job.number}`;
  const completed = ['analyzed', 'clean', 'published', 'publishing'].includes(
    job.status,
  );
  const review = evidence?.review;
  const reviewStatus = review?.status || (completed ? 'completed' : job.status);
  const reviewProblem = review?.error || (!review && !stale && job.error);
  const checks = evidence?.checks;
  return (
    <article className="github-review-sheet" aria-label={`Review of ${target}`}>
      <header className="github-review-pr-header">
        <div className="github-review-row">
          <span className="github-review-pr-id">{target}</span>
          <RevisionStatus job={job} />
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
      <CandidateDecisions job={job} controls={assessments} />
      <details className="github-review-identities">
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
      <section className="github-review-stages" aria-label="Review stages">
        <Stage title="Codex review" state={reviewStatus}>
          {latestControl}
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
          state={checks?.status || 'unrecorded'}
          label={checks?.status ? undefined : 'No evidence'}
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
          {checks?.error && (
            <p className="github-review-warning" role="status">
              {checks.refreshErrorAt && (
                <>
                  Refresh failed <Updated time={checks.refreshErrorAt} />.{' '}
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
              Observed <Updated time={checks.updatedAt} />
            </p>
          )}
        </Stage>
        <QuickTestStage
          key={jobKey(job)}
          job={job}
          policy={savedPolicy}
          disabled={testDisabled}
          onRun={onRetryTest}
        />
      </section>
    </article>
  );
}

export function ReviewHistory({
  jobs,
  assessments,
  retryDisabled,
  savedPolicy,
  testDisabled,
  onRetry,
  onRetryTest,
  onRefreshChecks,
  checksDisabled,
  onLatest,
  latestDisabled,
  interactive,
  queueNotice,
}: {
  onLatest: (job: ReviewJob, select: (job: ReviewJob) => void) => void;
  latestDisabled: boolean;
  queueNotice?: ReactNode;
  jobs: ReviewJob[];
  assessments: AssessmentControls;
  retryDisabled: boolean;
  savedPolicy: (job: ReviewJob) => RepositoryPolicy | undefined;
  testDisabled: boolean;
  onRetry: (job: ReviewJob) => void;
  onRetryTest: (job: ReviewJob, attempt: number, testId?: string) => void;
  onRefreshChecks: (job: ReviewJob) => void;
  checksDisabled: boolean;
  interactive: boolean;
}) {
  const [selection, setSelection] = useState('');
  const container = useRef<HTMLDivElement>(null);
  const selected = jobs.find((job) => jobKey(job) === selection);
  const prURL = selected && reviewPRURL(selected.repository, selected.number);
  const groups = [
    {
      title: 'Needs verification',
      jobs: jobs.filter(
        (job) => job.status !== 'stale' && pendingDecisions(job) > 0,
      ),
    },
    {
      title: 'In progress',
      jobs: jobs.filter((job) => isCurrentWork(job) && !pendingDecisions(job)),
    },
    {
      title: 'Recent reviews',
      jobs: jobs.filter(
        (job) =>
          job.status !== 'stale' &&
          !isCurrentWork(job) &&
          !pendingDecisions(job),
      ),
    },
    {
      title: 'Earlier revisions',
      jobs: jobs.filter((job) => job.status === 'stale'),
    },
  ];

  function returnToList() {
    setSelection('');
    requestAnimationFrame(() => {
      const buttons =
        container.current?.querySelectorAll<HTMLButtonElement>(
          '[data-review-key]',
        );
      Array.from(buttons ?? [])
        .find((button) => button.dataset.reviewKey === selection)
        ?.focus();
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
            {prURL && (
              <a href={prURL} target="_blank" rel="noreferrer">
                Open on GitHub ↗
              </a>
            )}
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
                          current === selection ? jobKey(next) : current,
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
              key={selection}
              job={selected}
              assessments={assessments}
              retryDisabled={retryDisabled}
              testDisabled={testDisabled}
              savedPolicy={savedPolicy(selected)}
              onRetry={onRetry}
              onRetryTest={onRetryTest}
              onRefreshChecks={onRefreshChecks}
              checksDisabled={checksDisabled}
            />
          ) : (
            <p>
              The selected revision is no longer in this account’s recent
              reviews.
            </p>
          )}
        </>
      ) : (
        <>
          {queueNotice}
          {jobs.length === 0 && (
            <div className="github-review-empty">
              <PixelIcon name="code-review" size={40} />
              <div>
                <h2>No reviews yet</h2>
                <p>
                  Watched PRs and direct review requests appear here after a
                  scan.
                </p>
              </div>
            </div>
          )}
          {groups
            .filter((group) => group.jobs.length)
            .map((group) => (
              <section
                key={group.title}
                aria-label={group.title}
                className="github-review-group"
              >
                <h2>
                  {group.title}{' '}
                  <span className="github-review-count">
                    {group.jobs.length}
                  </span>
                </h2>
                <ul>
                  {group.jobs.map((job) => {
                    const key = jobKey(job);
                    const test = latestTest(job);
                    const summary = [
                      job.evidence?.checks?.status &&
                        `Checks: ${labels[job.evidence.checks.status] || job.evidence.checks.status}`,
                      test &&
                        `Quick test: ${labels[test.status] || test.status}`,
                    ]
                      .filter(Boolean)
                      .join(' · ');
                    return (
                      <li key={key}>
                        <button
                          type="button"
                          data-review-key={key}
                          className="github-review-inbox-item"
                          disabled={!interactive}
                          aria-label={`Open ${job.repository} #${job.number} at ${job.headSha.slice(0, 12)}`}
                          onClick={() => {
                            setSelection(key);
                            requestAnimationFrame(() =>
                              container.current
                                ?.querySelector<HTMLButtonElement>(
                                  '[data-return-reviews]',
                                )
                                ?.focus(),
                            );
                          }}
                        >
                          <span className="github-review-inbox-content">
                            <span className="github-review-pr-id">
                              {job.repository} #{job.number}
                            </span>
                            <strong>
                              {job.title || `Pull request #${job.number}`}
                            </strong>
                            <span className="github-review-meta">
                              <code>{job.headSha.slice(0, 8)}</code>
                              <span>
                                {job.source === 'requested'
                                  ? 'Requested from you'
                                  : 'Watched'}
                              </span>
                              {summary && <span>{summary}</span>}
                              {!!job.findings?.length && (
                                <span>
                                  {job.findings.length}{' '}
                                  {job.findings.length === 1
                                    ? 'finding'
                                    : 'findings'}
                                </span>
                              )}
                            </span>
                          </span>
                          <span className="github-review-item-aside">
                            <RevisionStatus job={job} />
                            <span aria-hidden="true">›</span>
                          </span>
                        </button>
                      </li>
                    );
                  })}
                </ul>
              </section>
            ))}
        </>
      )}
    </div>
  );
}
