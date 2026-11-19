import { useState } from 'react';
import {
  CandidateComparisons,
  ComparisonEvidence,
} from './CandidateComparisons';
import type { ComparisonControls } from './CandidateComparisons';
import { Button, Select, TextArea } from '@cofy-x/axv-nextstep';
import { reviewCheckURL, reviewCommentURL } from './api';
import type {
  Candidate,
  CandidateOccurrence,
  EvidenceItem,
  FindingStatus,
  ReviewJob,
} from './api';

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
  drafts: Record<string, DecisionDraft>;
  disabled: boolean;
  onEdit: (id: string, draft?: DecisionDraft) => void;
  onFollowup: (job: ReviewJob, previousCandidateId: string) => void;
  onSave: (job: ReviewJob, id: string, draft: DecisionDraft) => void;
}

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

function DecisionBadge({ status }: { status: FindingStatus }) {
  return (
    <span className="github-review-decision-state" data-decision={status}>
      {decisions[status]}
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
            <DecisionBadge status={item.status} />
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
    <div className="github-review-decision-editor">
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

export function CandidateDecisions({
  job,
  controls,
}: {
  job: ReviewJob;
  controls: AssessmentControls;
}) {
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
  ];
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
      {!findings.length && (
        <p>
          {complete
            ? 'No candidate comments for this commit.'
            : 'No candidate comments recorded.'}
        </p>
      )}
      {rows.length > 0 && (
        <ol>
          {rows.map(
            ({ finding, candidate, originHeadSha, originBaseSha }, index) => {
              const storedDraft = candidate && controls.drafts[candidate.id];
              const draft =
                storedDraft && candidate && !storedDraft.changed
                  ? startDraft(candidate)
                  : storedDraft;
              return (
                <li
                  className="github-review-finding"
                  key={candidate?.id ?? `${finding.path}/${index}`}
                >
                  <div className="github-review-row github-review-finding-title">
                    <h4>
                      {originHeadSha && (
                        <small className="github-review-followup-source">
                          Reviewer follow-up
                        </small>
                      )}
                      {finding.title}
                    </h4>
                    {candidate && <DecisionBadge status={candidate.status} />}
                  </div>
                  <div className="github-review-meta">
                    <span className="github-review-severity">
                      {finding.severity}
                    </span>
                    {originHeadSha && (
                      <span
                        title={`Head ${originHeadSha} · base ${originBaseSha}`}
                      >
                        Location on original head{' '}
                        <code>{originHeadSha.slice(0, 12)}</code>
                      </span>
                    )}
                    {finding.path && (
                      <code>
                        {finding.path}
                        {finding.line > 0 && `:${finding.line}`}
                        {finding.side === 'base' && ' (base)'}
                      </code>
                    )}
                  </div>
                  <p className="github-review-preserve">{finding.body}</p>
                  {candidate && (
                    <>
                      {draft ? (
                        <DecisionEditor
                          controls={controls}
                          candidate={candidate}
                          job={job}
                          draft={draft}
                        />
                      ) : (
                        <>
                          {candidate.note && (
                            <p className="github-review-assessment-note">
                              {candidate.note}
                            </p>
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
                                    item={
                                      known
                                        ? { ...item, label: known.label }
                                        : item
                                    }
                                  />
                                );
                              })}
                            </div>
                          )}
                          {job.status !== 'stale' && (
                            <Button
                              disabled={controls.disabled || !complete}
                              onClick={() =>
                                controls.onEdit(
                                  candidate.id,
                                  startDraft(candidate),
                                )
                              }
                            >
                              {candidate.status !== 'pending' ||
                              candidate.note ||
                              candidate.evidence?.length
                                ? 'Edit assessment'
                                : 'Assess candidate'}
                            </Button>
                          )}
                        </>
                      )}
                      <CandidateComparisons
                        job={job}
                        candidate={candidate}
                        controls={controls.comparisons}
                      />
                      <PreviousDecisions history={candidate.history ?? []} />
                    </>
                  )}
                </li>
              );
            },
          )}
        </ol>
      )}
      <PreviousDecisions
        unmatched
        history={unmatched}
        disabled={controls.disabled}
        onFollowup={complete ? (id) => controls.onFollowup(job, id) : undefined}
      />
    </section>
  );
}
