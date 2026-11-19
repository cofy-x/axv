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

export interface ComparisonDraft {
  runtime: ComparisonRuntime;
  assertion: string;
  requestId: string;
}

export interface ComparisonControls {
  drafts: Record<string, ComparisonDraft>;
  runtimes: (job: ReviewJob) => ComparisonRuntime[];
  ready: boolean;
  disabled: boolean;
  onEdit: (id: string, draft?: ComparisonDraft) => void;
  onRun: (job: ReviewJob, candidateId: string, draft: ComparisonDraft) => void;
}

const outcomes = {
  head_only_failed: 'Only PR head failed',
  baseline_only_failed: 'Only common ancestor failed',
  both_failed: 'Both failed',
  both_passed: 'Both passed',
  inconclusive: 'Inconclusive',
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

function resultLabel(run: CandidateComparison) {
  return (
    (run.outcome && outcomes[run.outcome]) || states[run.status] || run.status
  );
}

function ComparisonSide({ title, leg }: { title: string; leg: ComparisonLeg }) {
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
    <section className="github-review-comparison-side" aria-label={title}>
      <div className="github-review-row">
        <strong>{title}</strong>
        <span className="github-review-state" data-state={leg.status}>
          {states[leg.status] || leg.status}
        </span>
      </div>
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

function useComparisonDetail(run?: CandidateComparison) {
  const id = run?.id;
  const updatedAt = run?.updatedAt;
  const [attempt, setAttempt] = useState(0);
  const [result, setResult] = useState<{
    id: string;
    data?: CandidateComparison;
    error?: string;
  }>();
  useEffect(() => {
    if (!id) return;
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
  }, [id, updatedAt, attempt]);
  return {
    data: result?.id === id ? result?.data : undefined,
    error: result?.id === id ? result?.error : undefined,
    retry: () => setAttempt((n) => n + 1),
  };
}

type ComparisonDetail = ReturnType<typeof useComparisonDetail>;

function ComparisonResult({
  run,
  detail,
}: {
  run: CandidateComparison;
  detail: ComparisonDetail;
}) {
  const data = detail.data;
  const current =
    data && Date.parse(data.updatedAt) >= Date.parse(run.updatedAt)
      ? data
      : run;
  return (
    <>
      <div className="github-review-row github-review-comparison-summary">
        <strong>{resultLabel(current)}</strong>
        <span>{run.runtime.name}</span>
      </div>
      {current.error && (
        <p className="github-review-problem">{current.error}</p>
      )}
      {detail.error ? (
        <div className="github-review-row" role="alert">
          <span className="github-review-problem">{detail.error}</span>
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
        <ComparisonSide
          title="Common ancestor"
          leg={{ ...current.baseline, log: data?.baseline.log }}
        />
        <ComparisonSide
          title="PR head"
          leg={{ ...current.head, log: data?.head.log }}
        />
      </div>
      <details className="github-review-comparison-inputs">
        <summary>Recorded assertion and runtime</summary>
        <p>
          <code>
            {run.runtime.command.map((arg) => JSON.stringify(arg)).join(' ')}
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
  const detail = useComparisonDetail(open && expanded ? run : undefined);
  return (
    <details onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary>
        {run.runtime.name} · {resultLabel(run)} ·{' '}
        {new Date(run.createdAt).toLocaleString()}
      </summary>
      {open && expanded && <ComparisonResult run={run} detail={detail} />}
    </details>
  );
}

function orderedComparisons(runs?: CandidateComparison[]) {
  return [...(runs ?? [])].sort(
    (a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt),
  );
}

export function ComparisonEvidence({ runs }: { runs?: CandidateComparison[] }) {
  const ordered = orderedComparisons(runs);
  const detail = useComparisonDetail(ordered[0]);
  return <ComparisonHistory ordered={ordered} detail={detail} />;
}

function ComparisonHistory({
  ordered,
  detail,
}: {
  ordered: CandidateComparison[];
  detail: ComparisonDetail;
}) {
  const [expanded, setExpanded] = useState(false);
  const [latest, ...earlier] = ordered;
  if (!latest) return null;
  return (
    <section
      className="github-review-comparison-evidence"
      aria-label="Comparison evidence"
    >
      <h5>Comparison evidence</h5>
      <ComparisonResult run={latest} detail={detail} />
      {earlier.length > 0 && (
        <details
          className="github-review-earlier-comparisons"
          onToggle={(e) => setExpanded(e.currentTarget.open)}
        >
          <summary>Earlier comparisons · {earlier.length}</summary>
          {earlier.map((run) => (
            <EarlierComparison key={run.id} run={run} expanded={expanded} />
          ))}
        </details>
      )}
    </section>
  );
}

export function CandidateComparisons({
  job,
  candidate,
  controls,
}: {
  job: ReviewJob;
  candidate: Candidate;
  controls: ComparisonControls;
}) {
  const runtimes = controls.runtimes(job);
  const draft = controls.drafts[candidate.id];
  const runs = orderedComparisons(candidate.comparisons);
  const last = runs[0];
  const detail = useComparisonDetail(last);
  const active = runs.some((run) => ['queued', 'running'].includes(run.status));
  const complete = ['analyzed', 'clean', 'published'].includes(job.status);
  const canOpen = complete && !!job.mergeBaseSha && runtimes.length > 0;
  const current =
    draft &&
    runtimes.find(
      (item) =>
        item.id === draft.runtime.id &&
        JSON.stringify(item.command) === JSON.stringify(draft.runtime.command),
    );
  const tooLong =
    !!draft && new TextEncoder().encode(draft.assertion).byteLength > 8192;

  const canSubmit =
    !!current &&
    !controls.disabled &&
    controls.ready &&
    canOpen &&
    !active &&
    !tooLong &&
    !!draft?.assertion.trim();

  function start() {
    const previous =
      last &&
      runtimes.find(
        (r) =>
          r.id === last.runtime.id &&
          JSON.stringify(r.command) === JSON.stringify(last.runtime.command),
      );
    controls.onEdit(candidate.id, {
      runtime: previous ?? runtimes[0],
      assertion: previous ? (detail.data?.assertion ?? '') : '',
      requestId: crypto.randomUUID(),
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
      {draft && !runs.some((run) => run.id === draft.requestId) ? (
        <form
          className="github-review-comparison-form"
          aria-label="Compare revisions"
          onSubmit={(e) => {
            e.preventDefault();
            if (canSubmit) controls.onRun(job, candidate.id, draft);
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
            value={draft.assertion}
            onChange={(e) => update({ assertion: e.target.value })}
          />
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
          {!controls.ready && (
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
        <Button
          disabled={
            controls.disabled || (!!last && !detail.data && !detail.error)
          }
          onClick={start}
        >
          Compare revisions
        </Button>
      ) : null}
      <ComparisonHistory ordered={runs} detail={detail} />
    </>
  );
}
