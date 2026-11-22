import { useEffect, useId, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Button, Field } from '@cofy-x/axv-nextstep';
import { parseReviewPRURL } from './api';
import type { ReviewPR } from './api';

export function ReviewIntake({
  children,
  disabled,
  connected,
  pending,
  onRequest,
}: {
  children: ReactNode;
  disabled: boolean;
  connected: boolean;
  pending: boolean;
  onRequest: (
    target: ReviewPR,
    onSuccess: () => void,
    onError: (message: string) => void,
  ) => void;
}) {
  const id = useId();
  const controls = useRef<HTMLDivElement>(null);
  const form = useRef<HTMLFormElement>(null);
  const [open, setOpen] = useState(false);
  const [url, setURL] = useState('');
  const [error, setError] = useState('');

  useEffect(() => {
    if (open) form.current?.querySelector('input')?.focus();
  }, [open, error]);

  function close(restoreFocus: boolean) {
    setOpen(false);
    setURL('');
    setError('');
    if (restoreFocus) controls.current?.querySelector('button')?.focus();
  }

  return (
    <>
      <div ref={controls} className="github-review-inbox-controls">
        <Button
          primary
          disabled={disabled}
          aria-expanded={open}
          aria-controls={open ? id : undefined}
          onClick={() => {
            if (open) close(true);
            else setOpen(true);
          }}
        >
          Review a PR
        </Button>
        {children}
      </div>
      {open && (
        <form
          id={id}
          ref={form}
          className="github-review-intake"
          aria-label="Review a GitHub pull request"
          aria-busy={pending}
          noValidate
          onKeyDown={(event) => {
            if (event.key === 'Escape' && !pending) {
              event.stopPropagation();
              close(true);
            }
          }}
          onSubmit={(event) => {
            event.preventDefault();
            if (disabled || !connected) return;
            const target = parseReviewPRURL(url);
            if (!target) {
              setError(
                'Use a link like https://github.com/owner/repo/pull/123.',
              );
              form.current?.querySelector('input')?.focus();
              return;
            }
            setError('');
            onRequest(target, () => close(false), setError);
          }}
        >
          <Field
            label="GitHub pull request URL"
            type="url"
            autoComplete="off"
            spellCheck={false}
            disabled={disabled}
            value={url}
            aria-invalid={error ? true : undefined}
            aria-describedby={`${id}-hint${error ? ` ${id}-error` : ''}`}
            placeholder="https://github.com/owner/repo/pull/123"
            onChange={(event) => {
              setURL(event.target.value);
              setError('');
            }}
          />
          <p id={`${id}-hint`} className="github-review-detail">
            {connected
              ? 'Use a watched repository or a PR assigned to you for review. Results stay in AXV.'
              : 'Connect GitHub and Codex in Settings to queue a review.'}
          </p>
          {error && (
            <p
              id={`${id}-error`}
              className="github-review-problem"
              role="alert"
            >
              {error}
            </p>
          )}
          <div className="github-review-intake-actions">
            <Button type="submit" primary disabled={disabled || !connected}>
              {pending ? 'Checking GitHub…' : 'Queue review'}
            </Button>
            <Button disabled={pending} onClick={() => close(true)}>
              Cancel
            </Button>
          </div>
        </form>
      )}
    </>
  );
}
