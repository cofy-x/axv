import { useState } from 'react';
import { Button, Select } from '@cofy-x/axv-nextstep';
import { reviewCheckURL } from './api';
import type {
  RepositoryPolicy,
  ReviewChecks,
  ReviewJob,
  ReviewTest,
  TestRecommendation,
} from './api';
import type { CandidateFocus } from './CandidateDecisions';
import { latestTest } from './review-evidence';
import type { ReviewStageState } from './review-evidence';
import {
  Commit,
  Stage,
  Status,
  Updated,
  reviewCandidates,
  statusText,
} from './ReviewPrimitives';

type CheckRun = NonNullable<ReviewChecks['runs']>[number];

export type ConfirmTestRecommendation = (
  job: ReviewJob,
  recommendation: TestRecommendation,
) => void;

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

export function checkoutsHint(checks: ReviewChecks | undefined) {
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

export function ComparisonOverview({
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

export function CheckRuns({
  runs,
  headSha,
}: {
  runs: CheckRun[];
  headSha: string;
}) {
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
      {test.reusedFrom && (
        <p className="github-review-detail">
          Reused test attempt {test.reusedFrom.attempt} from base{' '}
          <Commit sha={test.reusedFrom.baseSha} />.
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

export function QuickTestStage({
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
