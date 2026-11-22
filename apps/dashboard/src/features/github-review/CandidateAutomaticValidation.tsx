import { Button } from '@cofy-x/axv-nextstep';
import type { ReactNode, Ref } from 'react';
import type { CandidateAutoValidation } from './api';
import './github-review-automation.css';

const states: Record<CandidateAutoValidation['state'], string> = {
  needs_review: 'Needs your review',
  queued: 'Automatic verification queued',
  running: 'Running automatic comparison',
  verified: 'Difference reproduced',
  stale: 'Earlier revision',
};

export function CandidateAutomaticValidation({
  validation,
  title,
  historical,
  onSuggestion,
  suggestionButton,
  onViewComparison,
  disabled,
  children,
}: {
  validation: CandidateAutoValidation;
  title: string;
  historical?: boolean;
  onSuggestion?: () => void;
  suggestionButton?: Ref<HTMLDivElement>;
  onViewComparison?: () => void;
  disabled?: boolean;
  children?: ReactNode;
}) {
  const state = historical ? 'stale' : validation.state;
  const suggested = state === 'verified' && validation.suggestion;
  return (
    <section
      className="github-review-automatic-validation"
      aria-label="Automatic verification"
      data-verification={state}
    >
      <div className="github-review-row" role="status">
        <strong>{states[state]}</strong>
        {state !== 'stale' && validation.reused && (
          <span className="github-review-detail">Reused comparison</span>
        )}
      </div>
      {validation.scenarioName && validation.scenarioName !== title && (
        <p className="github-review-detail">{validation.scenarioName}</p>
      )}
      <p>
        {historical && validation.state !== 'stale'
          ? 'This result belongs to an earlier review revision.'
          : validation.reason}
      </p>
      {suggested && <strong>Suggested decision · Confirmed</strong>}
      {((suggested && onSuggestion) || onViewComparison) && (
        <div className="app-actions" ref={suggestionButton}>
          {suggested && onSuggestion && (
            <Button primary onClick={onSuggestion} disabled={disabled}>
              Review suggestion
            </Button>
          )}
          {onViewComparison && (
            <Button onClick={onViewComparison}>View comparison</Button>
          )}
        </div>
      )}
      {children}
    </section>
  );
}
