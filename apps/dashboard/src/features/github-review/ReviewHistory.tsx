import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Button, PixelIcon } from '@cofy-x/axv-nextstep';
import { SerialPoller } from '../../shared/api/serial-poller';
import { githubReviewAPI, reviewError, reviewPRURL } from './api';
import type {
  RepositoryPolicy,
  ReviewInboxPage,
  ReviewInboxView,
  ReviewJob,
  ReviewTarget,
} from './api';
import type { AssessmentControls } from './CandidateDecisions';
import { ReviewDetail } from './ReviewDetail';
import type { ConfirmTestRecommendation } from './ReviewEvidenceStages';
import { InboxSection, groupInbox, inboxViews } from './ReviewInboxList';
import type { InboxGroup, ReviewFocus } from './ReviewInboxList';
import { jobKey } from './ReviewPrimitives';
import './github-review-overview.css';
import './github-review-inbox.css';

export type { ReviewFocus } from './ReviewInboxList';

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
  inboxControls,
  refreshStatus,
  dailyLimitReached,
}: {
  onLatest: (job: ReviewJob, select: (target: ReviewTarget) => void) => void;
  latestDisabled: boolean;
  inboxControls: (open: (target: ReviewTarget) => void) => ReactNode;
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
      const focusTarget =
        previouslyFocused ??
        row?.querySelector<HTMLButtonElement>('[data-review-open]') ??
        container.current?.querySelector<HTMLButtonElement>(
          '.github-review-inbox-views [aria-pressed="true"]',
        );
      focusTarget?.focus();
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
          {inboxControls(open)}
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
