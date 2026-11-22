import { Button, Select, TextArea } from '@cofy-x/axv-nextstep';
import { useEffect, useRef, useState } from 'react';
import { APIError } from '../../shared/api/http';
import { githubReviewAPI, reviewError } from './api';
import type {
  Candidate,
  CandidateComparison,
  ComparisonLeg,
  ComparisonRuntime,
  ReviewJob,
} from './api';
import './candidate-comparisons.css';

export interface ComparisonDraft {
  runtime: ComparisonRuntime;
  assertion: string;
  requestId: string;
  reuseComparisonId?: string;
}

export interface ComparisonControls {
  drafts: Record<string, ComparisonDraft>;
  runtimes: (job: ReviewJob) => ComparisonRuntime[];
  ready: (job: ReviewJob) => boolean;
  disabled: boolean;
  onEdit: (id: string, draft?: ComparisonDraft) => void;
  onRun: (job: ReviewJob, candidateId: string, draft: ComparisonDraft) => void;
}

const outcomes = {
  head_only_failed: 'Assertion failed only on PR head',
  baseline_only_failed: 'Assertion failed only on the common ancestor',
  both_failed: 'Assertion failed on both revisions',
  both_passed: 'Assertion passed on both revisions',
  inconclusive: 'Comparison inconclusive',
} satisfies Record<NonNullable<CandidateComparison['outcome']>, string>;

const states: Record<string, string> = {
  queued: 'Queued',
  pending: 'Pending',
  running: 'Running',
  completed: 'Completed',
  error: 'Unavailable',
  canceled: 'Canceled',
  passed: 'Passed',
  failed: 'Failed',
  blocked: 'Blocked',
};

export function comparisonResultLabel(run: CandidateComparison) {
  if (run.status === 'queued') return 'Comparison queued';
  if (run.status === 'running') return 'Comparing revisions';
  return (
    (run.outcome && outcomes[run.outcome]) || states[run.status] || run.status
  );
}

function activeComparison(run: CandidateComparison) {
  return run.status === 'queued' || run.status === 'running';
}

export function completedComparison(candidate: Candidate, job: ReviewJob) {
  if (job.status === 'stale' || candidate.status !== 'pending') return;
  const latest = orderedComparisons(candidate.comparisons)[0];
  if (
    latest?.status === 'completed' &&
    latest.outcome &&
    latest.outcome !== 'inconclusive' &&
    latest.baseSha === job.baseSha &&
    latest.headSha === job.headSha &&
    latest.mergeBaseSha === job.mergeBaseSha &&
    latest.baseline.checkoutSha === job.mergeBaseSha &&
    latest.head.checkoutSha === job.headSha
  ) {
    return latest;
  }
}

function ComparisonSide({ title, leg }: { title: string; leg: ComparisonLeg }) {
  const checkoutPending = ['pending', 'queued', 'running'].includes(leg.status);
  return (
    <section className="github-review-comparison-side" aria-label={title}>
      <div className="github-review-row" aria-live="polite">
        <strong>{title}</strong>
        <span className="github-review-state" data-state={leg.status}>
          {leg.status === 'passed' || leg.status === 'failed'
            ? `Assertion ${leg.status}`
            : states[leg.status] || leg.status}
        </span>
      </div>
      <div className="github-review-meta github-review-comparison-checkout">
        {leg.checkoutSha ? (
          <span>
            Checkout{' '}
            <code title={leg.checkoutSha}>{leg.checkoutSha.slice(0, 12)}</code>
          </span>
        ) : (
          <span>Checkout {checkoutPending ? 'pending' : 'unverified'}</span>
        )}
        {leg.assertionExitCode !== undefined && (
          <span>Assertion exit {leg.assertionExitCode}</span>
        )}
      </div>
    </section>
  );
}

function ComparisonSideDetails({
  title,
  leg,
}: {
  title: string;
  leg: ComparisonLeg;
}) {
  const output = useRef<HTMLPreElement>(null);
  const terminal = !['running', 'queued', 'pending'].includes(leg.status);
  useEffect(() => {
    if (terminal && output.current)
      output.current.scrollTop = output.current.scrollHeight;
  }, [terminal, leg.log]);
  const receipt = [
    ['VM', leg.binding?.machineId || leg.vmId],
    ['Boot', leg.binding?.bootId],
    ['Task', leg.taskId],
    ['Attempt', leg.attemptId],
    ['Task exit code', leg.exitCode?.toString()],
  ].filter(([, value]) => value);
  return (
    <section
      className="github-review-comparison-side"
      aria-label={`${title} execution details`}
    >
      <strong>{title}</strong>
      <dl className="github-review-comparison-sha">
        <div>
          <dt>Target</dt>
          <dd>
            <code>{leg.targetSha}</code>
          </dd>
        </div>
        <div>
          <dt>Actual checkout</dt>
          <dd>
            {leg.checkoutSha ? <code>{leg.checkoutSha}</code> : 'Unverified'}
          </dd>
        </div>
        {leg.assertionExitCode !== undefined && (
          <div>
            <dt>Assertion exit code</dt>
            <dd>{leg.assertionExitCode}</dd>
          </div>
        )}
      </dl>
      {leg.error && <p className="github-review-problem">{leg.error}</p>}
      {leg.log && (
        <pre
          className="github-review-log"
          tabIndex={0}
          ref={output}
          aria-label={title + ' output'}
        >
          {leg.log}
        </pre>
      )}
      {leg.truncated && (
        <p className="github-review-detail">Showing partial task output.</p>
      )}
      {receipt.length > 0 && (
        <details className="github-review-comparison-receipt">
          <summary>Execution receipt</summary>
          <dl>
            {receipt.map(([label, value]) => (
              <div key={label}>
                <dt>{label}</dt>
                <dd>
                  <code>{value}</code>
                </dd>
              </div>
            ))}
          </dl>
        </details>
      )}
    </section>
  );
}

function useComparisonDetail(run?: CandidateComparison, enabled = true) {
  const id = run?.id;
  const updatedAt = run?.updatedAt;
  const [attempt, setAttempt] = useState(0);
  const [result, setResult] = useState<{
    id: string;
    data?: CandidateComparison;
    error?: string;
  }>();
  useEffect(() => {
    if (!id || !enabled) return;
    const request = new AbortController();
    void githubReviewAPI.comparison(id, request.signal).then(
      (data) => {
        if (!request.signal.aborted) setResult({ id, data });
      },
      (error: unknown) => {
        if (request.signal.aborted) return;
        const forbidden =
          error instanceof APIError && [401, 403, 404].includes(error.status);
        setResult((previous) => ({
          id,
          data: !forbidden && previous?.id === id ? previous.data : undefined,
          error: reviewError(error),
        }));
      },
    );
    return () => request.abort();
  }, [id, updatedAt, attempt, enabled]);
  return {
    data: result?.id === id ? result?.data : undefined,
    error: result?.id === id ? result?.error : undefined,
    retry: () => setAttempt((n) => n + 1),
  };
}

function ComparisonResult({ run }: { run: CandidateComparison }) {
  const [open, setOpen] = useState(false);
  const detail = useComparisonDetail(run, open || activeComparison(run));
  const data = detail.data;
  const current =
    data && Date.parse(data.updatedAt) >= Date.parse(run.updatedAt)
      ? data
      : run;
  return (
    <>
      <div
        className="github-review-comparison-result-summary"
        data-logs-open={open || undefined}
      >
        <div
          className="github-review-row github-review-comparison-summary"
          role="status"
        >
          <strong>{comparisonResultLabel(current)}</strong>
          <span>{run.runtime.name}</span>
        </div>
        <div className="github-review-comparison-pair">
          <ComparisonSide title="Common ancestor" leg={current.baseline} />
          <ComparisonSide title="PR head" leg={current.head} />
        </div>
      </div>
      {current.error && (
        <p className="github-review-problem">{current.error}</p>
      )}
      <details
        className="github-review-comparison-inputs github-review-comparison-details"
        onToggle={(e) => setOpen(e.currentTarget.open)}
      >
        <summary>Logs and execution details</summary>
        {open && (
          <>
            {detail.error ? (
              <div className="github-review-row">
                <span className="github-review-problem" role="alert">
                  {detail.error}
                </span>
                <Button onClick={detail.retry}>Retry evidence</Button>
              </div>
            ) : (
              !data && (
                <p className="github-review-detail" role="status">
                  Loading comparison evidence…
                </p>
              )
            )}
            <div className="github-review-comparison-pair">
              <ComparisonSideDetails
                title="Common ancestor"
                leg={{ ...current.baseline, log: data?.baseline.log }}
              />
              <ComparisonSideDetails
                title="PR head"
                leg={{ ...current.head, log: data?.head.log }}
              />
            </div>
            <details className="github-review-comparison-inputs">
              <summary>Recorded assertion and runtime</summary>
              <p>
                <code>
                  {run.runtime.command
                    .map((arg) => JSON.stringify(arg))
                    .join(' ')}
                </code>
              </p>
              {data?.assertion !== undefined && (
                <pre className="github-review-log" tabIndex={0}>
                  {data.assertion}
                </pre>
              )}
              <time className="github-review-detail" dateTime={run.createdAt}>
                {new Date(run.createdAt).toLocaleString()}
              </time>
            </details>
          </>
        )}
      </details>
    </>
  );
}

function EarlierComparison({
  run,
  expanded,
}: {
  run: CandidateComparison;
  expanded: boolean;
}) {
  const [open, setOpen] = useState(false);
  return (
    <details onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary>
        {run.runtime.name} · {comparisonResultLabel(run)} ·{' '}
        {new Date(run.createdAt).toLocaleString()}
      </summary>
      {open && expanded && <ComparisonResult run={run} />}
    </details>
  );
}

function orderedComparisons(runs?: CandidateComparison[]) {
  return [...(runs ?? [])].sort(
    (a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt),
  );
}

export function ComparisonEvidence({
  runs,
  comparisonId,
}: {
  runs?: CandidateComparison[];
  comparisonId?: string;
}) {
  return (
    <ComparisonHistory
      ordered={orderedComparisons(runs)}
      evidenceId={comparisonId}
    />
  );
}

function ComparisonHistory({
  ordered,
  evidenceId,
}: {
  ordered: CandidateComparison[];
  evidenceId?: string;
}) {
  const [expanded, setExpanded] = useState(false);
  const current = ordered.find((run) => run.id === evidenceId) ?? ordered[0];
  if (!current) return null;
  const earlier = ordered.filter((run) => run.id !== current.id);
  return (
    <section
      className="github-review-comparison-evidence"
      aria-label="Comparison evidence"
      tabIndex={-1}
    >
      <h5>Comparison evidence</h5>
      <ComparisonResult key={current.id} run={current} />
      {earlier.length > 0 && (
        <details
          className="github-review-earlier-comparisons"
          onToggle={(e) => setExpanded(e.currentTarget.open)}
        >
          <summary>
            {current.id === ordered[0].id ? 'Earlier' : 'Other'} comparisons ·{' '}
            {earlier.length}
          </summary>
          {earlier.map((run) => (
            <EarlierComparison key={run.id} run={run} expanded={expanded} />
          ))}
        </details>
      )}
    </section>
  );
}

export function CandidateComparisonEditor({
  job,
  candidate,
  controls,
  inline = false,
}: {
  job: ReviewJob;
  candidate: Candidate;
  controls: ComparisonControls;
  inline?: boolean;
}) {
  const runtimes = controls.runtimes(job);
  const draft = controls.drafts[candidate.id];
  const runs = orderedComparisons(candidate.comparisons);
  const last = runs[0];
  const replay = runs.find((run) => run.id === draft?.reuseComparisonId);
  const replayDetail = useComparisonDetail(replay);
  const form = useRef<HTMLFormElement>(null);
  const focusForm = useRef(false);
  const autoState = candidate.autoValidation?.state;
  const active =
    runs.some(activeComparison) ||
    autoState === 'queued' ||
    autoState === 'running';
  const complete = ['analyzed', 'clean', 'published'].includes(job.status);
  const canOpen = complete && !!job.mergeBaseSha && runtimes.length > 0;
  const editing = draft && !runs.some((run) => run.id === draft.requestId);
  const ready = controls.ready(job);
  const showVMNotice = canOpen && !active && !ready;
  const current =
    draft &&
    runtimes.find(
      (item) =>
        item.id === draft.runtime.id &&
        JSON.stringify(item.command) === JSON.stringify(draft.runtime.command),
    );
  const assertion = draft?.reuseComparisonId
    ? (replayDetail.data?.assertion ?? '')
    : (draft?.assertion ?? '');
  const tooLong = new TextEncoder().encode(assertion).byteLength > 8192;

  const canSubmit =
    !!current &&
    !controls.disabled &&
    ready &&
    canOpen &&
    !active &&
    !tooLong &&
    !!assertion.trim();

  useEffect(() => {
    if (editing && focusForm.current) {
      focusForm.current = false;
      form.current
        ?.querySelector<HTMLSelectElement>('select:not(:disabled)')
        ?.focus();
    }
  }, [editing]);

  function start() {
    const previous =
      last &&
      runtimes.find(
        (r) =>
          r.id === last.runtime.id &&
          JSON.stringify(r.command) === JSON.stringify(last.runtime.command),
      );
    focusForm.current = true;
    controls.onEdit(candidate.id, {
      runtime: previous ?? runtimes[0],
      assertion: '',
      requestId: crypto.randomUUID(),
      reuseComparisonId: previous ? last.id : undefined,
    });
  }

  function update(change: Partial<ComparisonDraft>) {
    if (draft)
      controls.onEdit(candidate.id, {
        ...draft,
        ...change,
        requestId: crypto.randomUUID(),
      });
  }

  return (
    <>
      {editing ? (
        <form
          ref={form}
          className="github-review-comparison-form"
          aria-label="Compare revisions"
          onSubmit={(e) => {
            e.preventDefault();
            if (canSubmit)
              controls.onRun(job, candidate.id, { ...draft, assertion });
          }}
        >
          <strong>Compare revisions</strong>
          <p className="github-review-detail">
            Run the assertion at the common ancestor and PR head on the same
            dedicated VM. Exit 0 means the assertion passed.
          </p>
          <Select
            label="Runtime"
            aria-label="Runtime"
            value={current?.id || ''}
            disabled={controls.disabled || !canOpen || active}
            onChange={(e) => {
              const runtime = runtimes.find((r) => r.id === e.target.value);
              if (runtime) update({ runtime });
            }}
          >
            {!current && <option value="">Choose a runtime…</option>}
            {runtimes.map((r) => (
              <option value={r.id} key={r.id}>
                {r.name}
              </option>
            ))}
          </Select>
          {current ? (
            <code className="github-review-detail github-review-runtime-command">
              {current.command.map((arg) => JSON.stringify(arg)).join(' ')}
            </code>
          ) : (
            canOpen && (
              <p role="alert">
                The saved runtime changed. Choose a runtime for this assertion.
              </p>
            )
          )}
          <TextArea
            label="Assertion"
            aria-label="Assertion"
            rows={5}
            required
            spellCheck={false}
            disabled={controls.disabled || !canOpen || active}
            value={assertion}
            onChange={(e) =>
              update({
                assertion: e.target.value,
                reuseComparisonId: undefined,
              })
            }
          />
          {draft.reuseComparisonId &&
            (replayDetail.error ? (
              <div className="github-review-row">
                <span className="github-review-problem" role="alert">
                  {replayDetail.error}
                </span>
                <Button
                  disabled={controls.disabled}
                  onClick={replayDetail.retry}
                >
                  Retry loading assertion
                </Button>
              </div>
            ) : !replay ? (
              <p className="github-review-problem" role="alert">
                The earlier assertion is unavailable.
              </p>
            ) : !replayDetail.data ? (
              <p className="github-review-detail" role="status">
                Loading the earlier assertion…
              </p>
            ) : null)}
          {tooLong && (
            <p className="github-review-problem" role="alert">
              The assertion exceeds 8 KiB.
            </p>
          )}
          {!canOpen && (
            <p role="alert">
              This revision is unavailable for a new comparison.
            </p>
          )}
          {!ready && (
            <p className="github-review-detail">
              Configure the dedicated review VM to run a comparison.
            </p>
          )}
          <div className="app-actions">
            <Button type="submit" primary disabled={!canSubmit}>
              Run comparison
            </Button>
            <Button
              disabled={controls.disabled}
              onClick={() => controls.onEdit(candidate.id)}
            >
              Cancel
            </Button>
          </div>
        </form>
      ) : canOpen && !active ? (
        <>
          <Button
            primary={inline}
            disabled={controls.disabled || showVMNotice}
            onClick={start}
          >
            Compare revisions
          </Button>
          {showVMNotice && (
            <p className="github-review-detail">
              Configure the dedicated review VM to run a comparison.
            </p>
          )}
        </>
      ) : null}
    </>
  );
}
