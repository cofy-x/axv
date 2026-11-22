import { useEffect, useId, useRef, useState } from 'react';
import {
  CandidateComparisonEditor,
  comparisonResultLabel,
  completedComparison,
  ComparisonEvidence,
} from './CandidateComparisons';
import type { ComparisonControls } from './CandidateComparisons';
import { CandidateAutomaticValidation } from './CandidateAutomaticValidation';
import { Button, Select, TextArea } from '@cofy-x/axv-nextstep';
import { CommentDraft, HistoricalCommentDraft } from './CommentDraft';
import type { CommentDraftEdit } from './CommentDraft';
import { reviewCheckURL, reviewCommentURL } from './api';
import type {
  Candidate,
  CandidateComparison,
  CandidateOccurrence,
  ComparisonScenario,
  EvidenceItem,
  Finding,
  FindingStatus,
  ReviewJob,
} from './api';
import './github-review-candidates.css';

const decisions = {
  pending: 'Needs verification',
  confirmed: 'Confirmed',
  false_positive: 'False positive',
  resolved: 'Resolved on this revision',
} satisfies Record<FindingStatus, string>;

const sources = {
  check: 'Upstream CI',
  test: 'Local quick test',
} satisfies Record<EvidenceItem['kind'], string>;

export interface DecisionDraft {
  revision: number;
  status: FindingStatus;
  note: string;
  evidence: EvidenceItem[];
  previousCandidateId: string;
  changed: boolean;
}

export interface AssessmentControls {
  comparisons: ComparisonControls;
  automation: {
    scenarios: (job: ReviewJob) => ComparisonScenario[];
    onApprove: (
      job: ReviewJob,
      candidate: Candidate,
      comparison: CandidateComparison,
      name: string,
    ) => void;
  };
  comments: {
    edits: Record<string, CommentDraftEdit>;
    onEdit: (id: string, edit?: CommentDraftEdit) => void;
    onSave: (job: ReviewJob, id: string, edit: CommentDraftEdit) => void;
    onReviewed: (
      job: ReviewJob,
      id: string,
      revision: number,
      reviewed: boolean,
    ) => void;
  };
  drafts: Record<string, DecisionDraft>;
  disabled: boolean;
  onEdit: (id: string, draft?: DecisionDraft) => void;
  onFollowup: (
    job: ReviewJob,
    previousCandidateId: string,
    onOpened: (candidateId: string) => void,
  ) => void;
  onSave: (job: ReviewJob, id: string, draft: DecisionDraft) => void;
}

export type CandidateFocus =
  | { kind: 'candidate' | 'draft'; candidateId: string }
  | { kind: 'comparison'; candidateId: string; comparisonId?: string };

function startDraft(candidate: Candidate): DecisionDraft {
  return {
    status: candidate.status,
    note: candidate.note,
    evidence: candidate.evidence ?? [],
    revision: candidate.revision,
    previousCandidateId: '',
    changed: false,
  };
}

function DecisionBadge({
  candidate,
  comparison,
}: {
  candidate: Pick<Candidate, 'status' | 'autoValidation' | 'commentDraft'>;
  comparison?: CandidateComparison;
}) {
  const { status, autoValidation, commentDraft } = candidate;
  const ready =
    status === 'pending' &&
    autoValidation?.state === 'verified' &&
    !!autoValidation.suggestion;
  const label = ready
    ? commentDraft && !commentDraft.stale && !commentDraft.reviewedAt
      ? 'Draft to review'
      : 'Suggestion ready'
    : comparison
      ? 'Needs assessment'
      : decisions[status];
  return (
    <span
      className="github-review-decision-state"
      data-decision={ready ? 'suggested' : status}
    >
      {label}
    </span>
  );
}

function LinkedEvidence({
  item,
  unlink,
}: {
  item: EvidenceItem;
  unlink?: () => void;
}) {
  const url = reviewCheckURL(item.url);
  return (
    <div className="github-review-linked-evidence">
      <details>
        <summary>
          <span>{item.label}</span>
          <span className="github-review-detail">
            {[sources[item.kind], item.status].filter(Boolean).join(' · ')}
          </span>
        </summary>
        {item.sha && (
          <div className="github-review-detail">
            Commit <code>{item.sha}</code>
          </div>
        )}
        {item.detail && (
          <pre className="github-review-log" tabIndex={0}>
            {item.detail}
          </pre>
        )}
        {url && (
          <a href={url} target="_blank" rel="noreferrer">
            Open GitHub evidence
          </a>
        )}
      </details>
      {unlink && (
        <Button aria-label={`Unlink ${item.label}`} onClick={unlink}>
          Unlink
        </Button>
      )}
    </div>
  );
}

function PreviousDecisions({
  history,
  unmatched = false,
  onFollowup,
  disabled,
}: {
  history: CandidateOccurrence[];
  unmatched?: boolean;
  onFollowup?: (id: string) => void;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  if (!history.length) return null;
  return (
    <details
      className="github-review-history"
      onToggle={(e) => setOpen(e.currentTarget.open)}
    >
      <summary>
        {unmatched ? 'Earlier findings on this PR' : 'Issue history'} ·{' '}
        {history.length}{' '}
        {history.length === 1 ? 'earlier occurrence' : 'earlier occurrences'}
      </summary>
      {unmatched && (
        <p className="github-review-detail">
          These assessments belong to earlier revisions. Select an issue to
          reconsider against this revision.
        </p>
      )}
      {history.map((item) => (
        <div className="github-review-occurrence" key={item.id}>
          <div className="github-review-row">
            <strong>{item.title}</strong>
            <DecisionBadge candidate={item} />
          </div>
          <div className="github-review-meta">
            <code>{item.path}</code>
            <span>
              Head <code title={item.headSha}>{item.headSha.slice(0, 12)}</code>
            </span>
            <span>
              Base <code title={item.baseSha}>{item.baseSha.slice(0, 12)}</code>
            </span>
          </div>
          {item.note && <p className="github-review-preserve">{item.note}</p>}
          {open && item.autoValidation && (
            <CandidateAutomaticValidation
              validation={item.autoValidation}
              title={item.title}
              historical
            />
          )}
          {open && item.commentDraft && (
            <HistoricalCommentDraft draft={item.commentDraft} />
          )}
          {onFollowup && (
            <Button disabled={disabled} onClick={() => onFollowup(item.id)}>
              Follow up on this revision
            </Button>
          )}
          {open && <ComparisonEvidence runs={item.comparisons} />}
          {item.evidence?.map((e) => (
            <LinkedEvidence item={e} key={e.id} />
          ))}
        </div>
      ))}
    </details>
  );
}

function DecisionEditor({
  job,
  candidate,
  draft,
  controls,
}: {
  job: ReviewJob;
  candidate: Candidate;
  draft: DecisionDraft;
  controls: AssessmentControls;
}) {
  const editor = useRef<HTMLDivElement>(null);
  useEffect(() => {
    editor.current
      ?.querySelector<HTMLSelectElement>('select:not(:disabled)')
      ?.focus();
  }, []);
  const change = (update: Partial<DecisionDraft>) =>
    controls.onEdit(candidate.id, { ...draft, ...update, changed: true });
  const conflict = draft.revision !== candidate.revision;
  const historical = job.status === 'stale';
  const used = new Set(draft.evidence.map((item) => item.id));
  const available = (job.evidenceOptions ?? []).filter(
    (item) => !used.has(item.id),
  );
  const occurrences = (job.previousCandidates ?? []).filter(
    (i) =>
      i.id !== candidate.id && !candidate.history?.some((h) => i.id === h.id),
  );
  return (
    <div className="github-review-decision-editor" ref={editor}>
      {conflict && (
        <div className="github-review-warning" role="alert">
          <strong>
            The saved assessment changed. Your edits are still here.
          </strong>
          <p>
            Latest decision · {decisions[candidate.status]}
            {candidate.note && ` · ${candidate.note}`}
          </p>
          {candidate.evidence?.map((item) => (
            <LinkedEvidence key={item.id} item={item} />
          ))}
          <Button
            disabled={controls.disabled || historical}
            onClick={() => change({ revision: candidate.revision })}
          >
            Keep my edits on this revision
          </Button>
        </div>
      )}
      <fieldset disabled={controls.disabled || historical}>
        <legend>Local assessment</legend>
        <Select
          label="Decision"
          value={draft.status}
          onChange={(event) =>
            change({ status: event.target.value as FindingStatus })
          }
        >
          {Object.entries(decisions)
            .filter(([id]) => id !== 'resolved' || candidate.index === -1)
            .map(([id, label]) => (
              <option value={id} key={id}>
                {label}
              </option>
            ))}
        </Select>
        <TextArea
          label="Reason or next check"
          rows={2}
          value={draft.note}
          onChange={(e) => change({ note: e.target.value })}
        />
        <Select
          label="Link evidence from this revision"
          value=""
          disabled={!available.length || used.size >= 16}
          onChange={(e) => {
            const item = available.find((i) => i.id === e.target.value);
            if (item) change({ evidence: [...draft.evidence, item] });
          }}
        >
          <option value="">
            {available.length
              ? 'Select evidence…'
              : 'No further evidence available'}
          </option>
          {Object.entries(sources).map(([kind, source]) => {
            const group = available.filter((item) => item.kind === kind);
            return (
              group.length > 0 && (
                <optgroup label={source} key={kind}>
                  {group.map((item) => (
                    <option key={item.id} value={item.id}>
                      {item.label} · {item.status}
                      {item.sha ? ` · ${item.sha.slice(0, 8)}` : ''}
                    </option>
                  ))}
                </optgroup>
              )
            );
          })}
        </Select>
        {draft.evidence.map((item) => (
          <LinkedEvidence
            key={item.id}
            item={item}
            unlink={() =>
              change({
                evidence: draft.evidence.filter(
                  (selected) => selected.id !== item.id,
                ),
              })
            }
          />
        ))}
        {candidate.index !== -1 && occurrences.length > 0 && (
          <Select
            label="Track the same issue across revisions"
            value={draft.previousCandidateId}
            onChange={(e) => change({ previousCandidateId: e.target.value })}
          >
            <option value="">Keep current issue history</option>
            {occurrences.map((item) => (
              <option key={item.id} value={item.id}>
                {item.title} · {item.path} · {item.headSha.slice(0, 8)} /{' '}
                {item.baseSha.slice(0, 8)}
              </option>
            ))}
          </Select>
        )}
      </fieldset>
      <div className="app-actions">
        <Button
          primary
          disabled={
            controls.disabled || conflict || historical || !draft.changed
          }
          onClick={() => controls.onSave(job, candidate.id, draft)}
        >
          Save assessment
        </Button>
        <Button
          disabled={controls.disabled && !historical}
          onClick={() => controls.onEdit(candidate.id)}
        >
          Cancel
        </Button>
      </div>
    </div>
  );
}

function FindingLocation({ finding }: { finding: Finding }) {
  if (!finding.path) return null;
  return (
    <code>
      {finding.path}
      {finding.line > 0 && `:${finding.line}`}
      {finding.side === 'base' && ' (base)'}
    </code>
  );
}

function SelectedCandidate({
  id,
  job,
  finding,
  candidate,
  originHeadSha,
  originBaseSha,
  controls,
  complete,
  comparison,
  focus,
  onViewComparison,
}: {
  id: string;
  job: ReviewJob;
  finding: Finding;
  candidate?: Candidate;
  originHeadSha: string;
  originBaseSha: string;
  controls: AssessmentControls;
  complete: boolean;
  comparison?: CandidateComparison;
  focus?: CandidateFocus;
  onViewComparison: (comparisonId: string) => void;
}) {
  const storedDraft = candidate && controls.drafts[candidate.id];
  const draft =
    storedDraft && candidate && !storedDraft.changed
      ? startDraft(candidate)
      : storedDraft;
  const wasEditing = useRef(false);
  const assessmentTrigger = useRef<HTMLDivElement>(null);
  const title = useRef<HTMLHeadingElement>(null);
  const evidence = useRef<HTMLDetailsElement>(null);
  const editing = Boolean(draft);
  const anchorSha = finding.side === 'base' ? job.mergeBaseSha : job.headSha;
  const stale = job.status === 'stale';
  const canDraft = complete && job.status !== 'publishing';
  const validation = candidate?.autoValidation;
  const hasSuggestion =
    !stale &&
    complete &&
    candidate?.status === 'pending' &&
    validation?.state === 'verified' &&
    !!validation.suggestion;
  const validationComparison = validation?.comparisonId
    ? candidate?.comparisons?.find((run) => run.id === validation.comparisonId)
    : undefined;
  const hasComparisonResult =
    validationComparison?.status === 'completed' &&
    validationComparison.outcome &&
    validationComparison.outcome !== 'inconclusive';
  const showComparisonResult = comparison && !validationComparison;
  const compareInline =
    !stale &&
    candidate?.status === 'pending' &&
    validation?.state === 'needs_review' &&
    !hasComparisonResult;
  const showComparisonWithoutVM =
    !stale &&
    candidate?.status === 'pending' &&
    !validation &&
    !controls.comparisons.ready(job);
  const commentEdit = candidate && controls.comments.edits[candidate.id];
  const scenarioSource =
    !stale &&
    canDraft &&
    !originHeadSha &&
    candidate?.status === 'confirmed' &&
    controls.comparisons.ready(job)
      ? candidate.comparisons
          ?.filter(
            (run) =>
              !run.scenarioId &&
              run.status === 'completed' &&
              run.outcome === 'head_only_failed' &&
              run.baseSha === job.baseSha &&
              run.headSha === job.headSha &&
              run.mergeBaseSha === job.mergeBaseSha &&
              run.baseline.checkoutSha === job.mergeBaseSha &&
              run.head.checkoutSha === job.headSha &&
              run.baseline.assertionExitCode === 0 &&
              run.head.assertionExitCode !== undefined &&
              run.head.assertionExitCode !== 0,
          )
          .at(-1)
      : undefined;
  const canApproveScenario =
    scenarioSource &&
    controls.comparisons
      .runtimes(job)
      .some(
        (runtime) =>
          runtime.id === scenarioSource.runtime.id &&
          runtime.name === scenarioSource.runtime.name &&
          runtime.command.length === scenarioSource.runtime.command.length &&
          runtime.command.every(
            (argument, index) =>
              argument === scenarioSource.runtime.command[index],
          ),
      ) &&
    !controls.automation
      .scenarios(job)
      .some(
        (scenario) =>
          scenario.sourceComparisonId === scenarioSource.id ||
          (scenario.path === finding.path &&
            scenario.title === finding.title &&
            scenario.side === (finding.side ?? 'head')),
      );
  useEffect(() => {
    if (editing) wasEditing.current = true;
    else if (wasEditing.current && !controls.disabled) {
      assessmentTrigger.current
        ?.querySelector<HTMLButtonElement>('button:not(:disabled)')
        ?.focus();
      wasEditing.current = false;
    }
  }, [editing, controls.disabled]);
  useEffect(() => {
    if (focus?.kind === 'candidate') {
      title.current?.focus({ preventScroll: true });
      title.current?.scrollIntoView({ block: 'start' });
    }
    if (focus?.kind === 'comparison' && evidence.current) {
      evidence.current.open = true;
      const comparison = evidence.current.querySelector<HTMLElement>(
        '.github-review-comparison-evidence',
      );
      comparison?.focus({ preventScroll: true });
      comparison?.scrollIntoView({ block: 'start' });
    }
  }, [focus]);
  const comparisonEditor = candidate && (
    <CandidateComparisonEditor
      job={job}
      candidate={candidate}
      controls={controls.comparisons}
      inline={compareInline}
    />
  );
  const comment = candidate &&
    (candidate.commentDraft ||
      commentEdit ||
      (!originHeadSha &&
        anchorSha &&
        finding.path &&
        finding.line > 0 &&
        !stale)) && (
      <CommentDraft
        finding={finding}
        anchorSha={anchorSha ?? ''}
        saved={candidate.commentDraft}
        edit={commentEdit}
        disabled={controls.disabled || !canDraft}
        stale={stale}
        focusInitially={focus?.kind === 'draft'}
        onEdit={(edit) => controls.comments.onEdit(candidate.id, edit)}
        onSave={(edit) => controls.comments.onSave(job, candidate.id, edit)}
        onReviewed={(revision, reviewed) =>
          controls.comments.onReviewed(job, candidate.id, revision, reviewed)
        }
      />
    );
  const hasDraft = !!(candidate?.commentDraft || commentEdit);
  return (
    <li className="github-review-finding" id={id}>
      <div className="github-review-row github-review-finding-title">
        <h4 ref={title} tabIndex={-1}>
          {originHeadSha && (
            <small className="github-review-followup-source">
              Reviewer follow-up
            </small>
          )}
          {finding.title}
        </h4>
        {candidate && (
          <DecisionBadge candidate={candidate} comparison={comparison} />
        )}
      </div>
      <div className="github-review-meta">
        <span className="github-review-severity">{finding.severity}</span>
        {originHeadSha && (
          <span title={`Head ${originHeadSha} · base ${originBaseSha}`}>
            Location on original head <code>{originHeadSha.slice(0, 12)}</code>
          </span>
        )}
        <FindingLocation finding={finding} />
      </div>
      {!candidate && <p className="github-review-preserve">{finding.body}</p>}
      {candidate && (
        <>
          {validation && (
            <CandidateAutomaticValidation
              validation={validation}
              title={finding.title}
              historical={stale}
              disabled={controls.disabled || Boolean(draft)}
              suggestionButton={hasSuggestion ? assessmentTrigger : undefined}
              onSuggestion={
                hasSuggestion
                  ? () =>
                      controls.onEdit(candidate.id, {
                        ...startDraft(candidate),
                        status: 'confirmed',
                        note: candidate.note || validation.reason,
                        changed: true,
                      })
                  : undefined
              }
              onViewComparison={
                validationComparison
                  ? () => onViewComparison(validationComparison.id)
                  : undefined
              }
            >
              {compareInline && comparisonEditor}
            </CandidateAutomaticValidation>
          )}
          {hasDraft && comment}
          {candidate.status === 'pending' && !validation?.suggestion && (
            <p className="github-review-preserve">{finding.body}</p>
          )}
          {showComparisonResult && (
            <div className="github-review-row github-review-candidate-result">
              <strong>{comparisonResultLabel(comparison)}</strong>
              <Button onClick={() => onViewComparison(comparison.id)}>
                View comparison
              </Button>
            </div>
          )}
          {draft ? (
            <DecisionEditor
              controls={controls}
              candidate={candidate}
              job={job}
              draft={draft}
            />
          ) : !stale && !hasSuggestion ? (
            <div ref={assessmentTrigger}>
              <Button
                disabled={controls.disabled || !complete}
                onClick={() =>
                  controls.onEdit(candidate.id, startDraft(candidate))
                }
              >
                {candidate.status !== 'pending' ||
                candidate.note ||
                candidate.evidence?.length
                  ? 'Edit assessment'
                  : 'Assess candidate'}
              </Button>
            </div>
          ) : null}
          {showComparisonWithoutVM && comparisonEditor}
          {!hasDraft && comment}
          <details
            ref={evidence}
            className="github-review-finding-context github-review-candidate-evidence"
          >
            <summary>Candidate evidence</summary>
            {candidate.note && (
              <details className="github-review-finding-context github-review-note-disclosure">
                <summary>Assessment note</summary>
                <p className="github-review-assessment-note">
                  {candidate.note}
                </p>
              </details>
            )}
            {(candidate.status !== 'pending' || validation?.suggestion) && (
              <details className="github-review-finding-context">
                <summary>Original finding</summary>
                <p className="github-review-preserve">{finding.body}</p>
              </details>
            )}
            {!compareInline && !showComparisonWithoutVM && comparisonEditor}
            <ComparisonEvidence
              runs={candidate.comparisons}
              comparisonId={
                focus?.kind === 'comparison' && focus.comparisonId
                  ? focus.comparisonId
                  : validation?.comparisonId
              }
            />
            {scenarioSource && canApproveScenario && (
              <details className="github-review-automatic-scenario">
                <summary>Automatic scenario</summary>
                <p>
                  Approve the recorded assertion for candidates with this title,
                  file, and diff side in this repository.
                </p>
                <Button
                  disabled={controls.disabled}
                  onClick={() =>
                    controls.automation.onApprove(
                      job,
                      candidate,
                      scenarioSource,
                      finding.title,
                    )
                  }
                >
                  Use as automatic scenario
                </Button>
              </details>
            )}
            {!!candidate.evidence?.length && (
              <div
                role="group"
                aria-label="Linked evidence"
                className="github-review-linked"
              >
                <strong>Linked evidence</strong>
                {candidate.evidence.map((item) => {
                  const known = job.evidenceOptions?.find(
                    (option) => option.id === item.id,
                  );
                  return (
                    <LinkedEvidence
                      key={item.id}
                      item={known ? { ...item, label: known.label } : item}
                    />
                  );
                })}
              </div>
            )}
            <PreviousDecisions history={candidate.history ?? []} />
          </details>
        </>
      )}
    </li>
  );
}

export function CandidateDecisions({
  job,
  controls,
  focus,
}: {
  job: ReviewJob;
  controls: AssessmentControls;
  focus?: CandidateFocus;
}) {
  const detailId = useId();
  const [selection, setSelection] = useState({
    external: focus,
    key: focus?.candidateId,
    request: focus,
  });
  if (selection.external !== focus)
    setSelection({ external: focus, key: focus?.candidateId, request: focus });

  function select(candidateId: string, request?: CandidateFocus) {
    setSelection({ external: focus, key: candidateId, request });
  }

  const findings = job.findings ?? [];
  const posted = job.status === 'published' || Boolean(reviewCommentURL(job));
  const complete = ['clean', 'analyzed', 'published', 'publishing'].includes(
    job.status,
  );
  const linkedOccurrences = new Set(
    [...(job.candidates ?? []), ...(job.followups ?? [])]
      .flatMap((c) => c.history?.map((h) => h.id) ?? [])
      .concat((job.followups ?? []).map((f) => f.originId)),
  );
  const unmatched = (job.previousCandidates ?? []).filter(
    (c) => !linkedOccurrences.has(c.id),
  );
  const url = reviewCommentURL(job);
  const rows = [
    ...findings.map((finding, index) => ({
      finding,
      candidate: job.candidates?.find((c) => c.index === index),
      originHeadSha: '',
      originBaseSha: '',
    })),
    ...(job.followups ?? []).map((f) => ({
      finding: f,
      candidate: f,
      originHeadSha: f.originHeadSha,
      originBaseSha: f.originBaseSha,
    })),
  ].map((row, index) => ({
    ...row,
    key: row.candidate?.id ?? `${job.headSha}/${job.baseSha}/${index}`,
    comparison: row.candidate && completedComparison(row.candidate, job),
  }));
  const selected = rows.find((row) => row.key === selection.key) ?? rows[0];
  if (!selected && !unmatched.length && !url) return null;
  return (
    <section
      className="github-review-comments"
      aria-label={posted ? 'Posted findings' : 'Local candidate comments'}
    >
      <header className="github-review-row">
        <h3>{posted ? 'Posted findings' : 'Local candidate comments'}</h3>
        {url && (
          <a href={url} target="_blank" rel="noreferrer">
            View comment on GitHub
          </a>
        )}
      </header>
      {rows.length > 1 && (
        <ul
          className="github-review-candidate-index"
          aria-label="Choose a candidate"
        >
          {rows.map((row, index) => (
            <li key={row.key}>
              <button
                type="button"
                aria-controls={detailId}
                aria-pressed={row.key === selected?.key}
                aria-label={
                  row.key === selected?.key
                    ? `Candidate ${index + 1} of ${rows.length} · ${row.finding.title}`
                    : undefined
                }
                className="github-review-candidate-choice"
                onClick={() => select(row.key)}
              >
                {row.key === selected?.key ? (
                  <strong>
                    Candidate {index + 1} of {rows.length}
                  </strong>
                ) : (
                  <>
                    <span className="github-review-candidate-choice-label">
                      <strong>{row.finding.title}</strong>
                      <FindingLocation finding={row.finding} />
                    </span>
                    {row.candidate && (
                      <DecisionBadge
                        candidate={row.candidate}
                        comparison={row.comparison}
                      />
                    )}
                  </>
                )}
              </button>
            </li>
          ))}
        </ul>
      )}
      {selected && (
        <ol className="github-review-selected-candidate">
          <SelectedCandidate
            key={selected.key}
            id={detailId}
            job={job}
            controls={controls}
            complete={complete}
            comparison={selected.comparison}
            focus={
              selected.key === selection.request?.candidateId
                ? selection.request
                : undefined
            }
            finding={selected.finding}
            candidate={selected.candidate}
            originHeadSha={selected.originHeadSha}
            originBaseSha={selected.originBaseSha}
            onViewComparison={(comparisonId) =>
              select(selected.key, {
                kind: 'comparison',
                candidateId: selected.key,
                comparisonId,
              })
            }
          />
        </ol>
      )}
      <PreviousDecisions
        unmatched
        history={unmatched}
        disabled={controls.disabled}
        onFollowup={
          complete ? (id) => controls.onFollowup(job, id, select) : undefined
        }
      />
    </section>
  );
}
