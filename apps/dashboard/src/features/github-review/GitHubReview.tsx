import { useEffect, useRef, useState } from 'react';
import { Button, IconButton, StatusBar, Tabs } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { SerialPoller } from '../../shared/api/serial-poller';
import { githubReviewAPI, reviewEnvironment, reviewError } from './api';
import type {
  ReviewJob,
  ReviewProvider,
  ReviewSnapshot,
  TestRecommendation,
} from './api';
import { ReviewHistory } from './ReviewHistory';
import { ReviewAutomationStatus } from './ReviewAutomationStatus';
import { ReviewIntake } from './ReviewIntake';
import { ReviewSettings } from './ReviewSettings';
import type { ReviewSettingsDestination } from './ReviewSettings';
import { reviewProviderNames } from './ReviewGlobalSettings';
import { configDraft, configFromDraft } from './review-settings';
import type { SettingsDraft, SettingsPatch } from './review-settings';
import type { AssessmentControls, DecisionDraft } from './CandidateDecisions';
import type { ComparisonDraft } from './CandidateComparisons';
import type { CommentDraftEdit } from './CommentDraft';
import { shellComparisonRuntime } from './api';
import './github-review.css';

function confirmDiscard(desktop: AppWindowProps['desktop']) {
  return desktop.dialog({
    title: 'Discard review settings?',
    message: 'Unsaved review settings will be lost.',
    buttons: [
      { label: 'Cancel', value: 'cancel', cancel: true, default: true },
      { label: 'Discard settings', value: 'discard' },
    ],
  });
}

export function GitHubReview({
  window,
  desktop,
  interactive = true,
}: AppWindowProps) {
  const [snapshot, setSnapshot] = useState<ReviewSnapshot>();
  const [draft, setDraft] = useState<SettingsDraft>();
  const [settingsNavigation, setSettingsNavigation] = useState<{
    section: ReviewSettingsDestination;
  }>();
  const [busy, setBusy] = useState('');
  const [readError, setReadError] = useState('');
  const [actionError, setActionError] = useState('');
  const [notice, setNotice] = useState('');
  const [reviewRefresh, setReviewRefresh] = useState(0);
  const [assessmentDrafts, setAssessmentDrafts] = useState<
    Record<string, Record<string, DecisionDraft>>
  >({});
  const [comparisonDrafts, setComparisonDrafts] = useState<
    Record<string, ComparisonDraft>
  >({});
  const [commentEdits, setCommentEdits] = useState<
    Record<string, Record<string, CommentDraftEdit>>
  >({});
  const operation = useRef<AbortController>(null);
  const poller = useRef<SerialPoller>(null);
  const hasReviews = snapshot?.hasJobs ?? Boolean(snapshot?.jobs?.length);
  const tab =
    window.viewState.githubReviewTab === 'settings' ||
    window.viewState.githubReviewTab === 'history'
      ? window.viewState.githubReviewTab
      : snapshot && !snapshot.config.repositories?.length && !hasReviews
        ? 'settings'
        : 'history';
  const saved = snapshot && configDraft(snapshot.config);
  const settings = draft ?? saved;
  const stale = !!(draft && saved && draft.revision !== saved.revision);
  const disabled = !interactive || !!busy || !!readError;

  useEffect(() => {
    if (!interactive) return;
    const current = new SerialPoller({
      timeoutMs: 15000,
      intervalMs: 2500,
      read: async ({ signal, isCurrent }) => {
        const value = await githubReviewAPI.snapshot(signal);
        if (isCurrent() && !signal.aborted) {
          setSnapshot(value);
          setReadError('');
        }
      },
      onError: (error) => setReadError(reviewError(error)),
    });
    poller.current = current;
    if (!operation.current) current.start();
    return () => {
      current.stop();
      poller.current = null;
    };
  }, [interactive]);

  useEffect(() => () => operation.current?.abort(), []);
  useEffect(
    () =>
      desktop.beforeClose(window.id, async () => {
        const dirtyDecisions = Object.values(assessmentDrafts).some((byId) =>
          Object.values(byId).some((d) => d.changed),
        );
        const recordedComparisons = new Set(
          snapshot?.jobs.flatMap((job) =>
            [...(job.candidates ?? []), ...(job.followups ?? [])].flatMap(
              (candidate) => candidate.comparisons?.map((run) => run.id) ?? [],
            ),
          ),
        );
        const dirtyComparisons = Object.values(comparisonDrafts).some(
          (d) => !!d.assertion.trim() && !recordedComparisons.has(d.requestId),
        );
        const dirtyComments = Object.values(commentEdits).some((byId) =>
          Object.values(byId).some((edit) => edit.body !== edit.savedBody),
        );
        if (dirtyDecisions || dirtyComparisons || dirtyComments) {
          const answer = await desktop.dialog({
            title: 'Close GitHub Review?',
            message: 'Closing will discard your unsaved GitHub Review edits.',
            buttons: [
              {
                label: 'Keep editing',
                value: 'keep',
                cancel: true,
                default: true,
              },
              { label: 'Discard edits', value: 'discard' },
            ],
          });
          return answer?.button === 'discard';
        }
        if (!draft) return true;
        return (await confirmDiscard(desktop))?.button === 'discard';
      }),
    [
      desktop,
      window.id,
      draft,
      assessmentDrafts,
      comparisonDrafts,
      commentEdits,
      snapshot,
    ],
  );

  function edit(change: SettingsPatch) {
    if (!settings) return;
    const next = { ...settings, ...change };
    const unchanged =
      next.revision === saved?.revision &&
      JSON.stringify(next.repositories) ===
        JSON.stringify(saved.repositories) &&
      next.intervalMinutes === saved.intervalMinutes &&
      next.dailyLimit === saved.dailyLimit &&
      next.enabled === saved.enabled &&
      next.watchOnly === saved.watchOnly &&
      JSON.stringify(next.policies) === JSON.stringify(saved.policies);
    setDraft(unchanged ? undefined : next);
    setNotice('');
    setActionError('');
  }

  async function reloadSettings() {
    if ((await confirmDiscard(desktop))?.button === 'discard') {
      setDraft(undefined);
      setActionError('');
      poller.current?.refresh();
    }
  }

  async function change<Result extends ReviewSnapshot>(
    label: string,
    request: (signal: AbortSignal) => Promise<Result>,
    onSuccess?: (value: Result) => void,
    onError: (message: string) => void = setActionError,
  ) {
    if (disabled || operation.current) return;
    const controller = new AbortController();
    operation.current = controller;
    poller.current?.stop();
    setBusy(label);
    setNotice('');
    setActionError('');
    try {
      const signal = AbortSignal.any([
        controller.signal,
        AbortSignal.timeout(15000),
      ]);
      const value = await request(signal);
      signal.throwIfAborted();
      setSnapshot(value);
      setReviewRefresh((previous) => previous + 1);
      onSuccess?.(value);
    } catch (error) {
      if (!controller.signal.aborted) onError(reviewError(error));
    } finally {
      operation.current = null;
      if (!controller.signal.aborted) {
        setBusy('');
        poller.current?.start();
      }
    }
  }

  function saveSettings() {
    if (!draft || stale) return;
    const config = configFromDraft(draft);
    void change(
      'Saving settings…',
      (signal) => githubReviewAPI.save(config, signal),
      () => {
        setDraft(undefined);
        setNotice('Settings saved');
      },
    );
  }

  function connection(
    action: 'connect' | 'disconnect',
    provider: ReviewProvider,
  ) {
    void change(
      `${action === 'connect' ? 'Connecting' : 'Disconnecting'} ${reviewProviderNames[provider]}…`,
      (signal) => githubReviewAPI.connection(action, provider, signal),
    );
  }

  function triggerScan() {
    void change('Requesting scan…', githubReviewAPI.run, () =>
      setNotice('Scan requested'),
    );
  }

  function retryReview({ repository, number, baseSha, headSha }: ReviewJob) {
    if (!snapshot || !scanEnabled) return;
    const revision = snapshot.config.revision;
    void change(
      'Requesting retry…',
      (signal) =>
        githubReviewAPI.retry(
          { repository, number, baseSha, headSha, revision },
          signal,
        ),
      () => setNotice('Retry queued'),
    );
  }

  const scanEnabled =
    snapshot?.config.enabled &&
    snapshot.github.connected &&
    snapshot.codex.connected;

  function refreshChecks({
    repository,
    number,
    baseSha,
    headSha,
    status,
  }: ReviewJob) {
    if (!snapshot?.github.connected || status === 'stale') return;
    void change(
      'Refreshing GitHub Checks…',
      (signal) =>
        githubReviewAPI.refreshChecks(
          {
            repository,
            number,
            baseSha,
            headSha,
            revision: snapshot.config.revision,
          },
          signal,
        ),
      () => setNotice('Check refresh requested'),
    );
  }

  function retryTest(job: ReviewJob, attempt: number, testId?: string) {
    const policy = savedPolicy(job);
    if (
      !snapshot ||
      disabled ||
      !job.evidence ||
      (!policy?.testCommand?.length && !policy?.testProfiles?.length) ||
      (testId &&
        !policy.testProfiles?.some((profile) => profile.id === testId)) ||
      (job.evidence.tests.at(-1)?.attempt ?? 0) !== attempt
    )
      return;
    const { repository, number, baseSha, headSha } = job;
    const revision = snapshot.config.revision;
    void change(
      'Requesting VM test…',
      (signal) =>
        githubReviewAPI.retryTest(
          {
            repository,
            number,
            baseSha,
            headSha,
            revision,
            attempt,
            ...(testId ? { testId } : {}),
          },
          signal,
        ),
      () => setNotice('Test retry queued'),
    );
  }

  function confirmTestRecommendation(
    job: ReviewJob,
    recommendation: TestRecommendation,
  ) {
    const { repository, number, baseSha, headSha } = job;
    if (
      !snapshot?.github.connected ||
      job.status === 'stale' ||
      recommendation.status !== 'run' ||
      recommendation.baseSha !== baseSha ||
      recommendation.headSha !== headSha
    )
      return;
    void change(
      'Confirming the recommended VM test…',
      (signal) =>
        githubReviewAPI.confirmTestRecommendation(
          {
            repository,
            number,
            baseSha,
            headSha,
            revision: snapshot.config.revision,
            recommendationId: recommendation.id,
          },
          signal,
        ),
      () => setNotice('Recommended test queued'),
    );
  }

  function savedPolicy(job: ReviewJob) {
    if (
      !snapshot?.github.connected ||
      job.status === 'stale' ||
      !snapshot.config.repositories?.some(
        (repo) => repo.toLowerCase() === job.repository.toLowerCase(),
      )
    )
      return;

    const current = Object.entries(
      snapshot?.config.repositoryPolicies ?? {},
    ).find(
      ([repo]) => repo.toLowerCase() === job.repository.toLowerCase(),
    )?.[1];
    return current;
  }

  const owner = snapshot?.github.account ?? '';

  function editAssessment(id: string, next?: DecisionDraft) {
    setAssessmentDrafts((all) => {
      const drafts = { ...all[owner] };
      if (next) drafts[id] = next;
      else delete drafts[id];
      return { ...all, [owner]: drafts };
    });
  }

  function editComment(id: string, edit?: CommentDraftEdit) {
    setCommentEdits((all) => {
      const edits = { ...all[owner] };
      if (edit) edits[id] = edit;
      else delete edits[id];
      return { ...all, [owner]: edits };
    });
  }

  function comparisonRuntimes(job: ReviewJob) {
    if (
      !snapshot?.github.connected ||
      !snapshot.config.repositories.some(
        (repo) => repo.toLowerCase() === job.repository.toLowerCase(),
      )
    )
      return [];
    const entries = savedPolicy(job)?.comparisonRuntimes;
    return entries?.length ? entries : [shellComparisonRuntime];
  }

  function editComparison(id: string, draft?: ComparisonDraft) {
    setComparisonDrafts((all) => {
      const next = { ...all };
      if (draft) next[id] = draft;
      else delete next[id];
      return next;
    });
  }

  const assessments: AssessmentControls = {
    automation: {
      scenarios: (job) => savedPolicy(job)?.comparisonScenarios ?? [],
      onApprove: (job, candidate, comparison, name) => {
        if (!owner || !snapshot?.github.connected || job.status === 'stale')
          return;
        const { repository, number, baseSha, headSha } = job;
        void change(
          'Approving the recorded assertion…',
          (signal) =>
            githubReviewAPI.approveComparisonScenario(
              {
                account: owner,
                repository,
                number,
                baseSha,
                headSha,
                revision: snapshot.config.revision,
                candidateId: candidate.id,
                comparisonId: comparison.id,
                name,
                enable: true,
              },
              signal,
            ),
          () => setNotice('Automatic verification enabled for this scenario'),
        );
      },
    },
    comments: {
      edits: commentEdits[owner] ?? {},
      onEdit: editComment,
      onSave: (job, candidateId, edit) => {
        if (!owner || !snapshot?.github.connected || job.status === 'stale')
          return;
        const { repository, number, baseSha, headSha } = job;
        void change(
          'Saving local comment draft…',
          (signal) =>
            githubReviewAPI.saveCommentDraft(
              {
                repository,
                number,
                baseSha,
                headSha,
                candidateId,
                account: owner,
                body: edit.body,
                revision: edit.revision,
              },
              signal,
            ),
          () => {
            editComment(candidateId);
            setNotice('Local comment draft saved');
          },
        );
      },
      onReviewed: (job, candidateId, revision, reviewed) => {
        if (!owner || !snapshot?.github.connected || job.status === 'stale')
          return;
        const { repository, number, baseSha, headSha } = job;
        void change(
          'Updating draft status…',
          (signal) =>
            githubReviewAPI.reviewCommentDraft(
              {
                repository,
                number,
                baseSha,
                headSha,
                candidateId,
                account: owner,
                revision,
                reviewed,
              },
              signal,
            ),
          () =>
            setNotice(
              reviewed ? 'Draft marked as reviewed' : 'Draft marked as unread',
            ),
        );
      },
    },
    comparisons: {
      drafts: comparisonDrafts,
      ready: (job) => reviewEnvironment(snapshot, job.repository).configured,
      disabled: disabled || !snapshot?.github.connected || !owner,
      runtimes: comparisonRuntimes,
      onEdit: editComparison,
      onRun: (job, candidateId, edit) => {
        if (
          !owner ||
          !snapshot ||
          !reviewEnvironment(snapshot, job.repository).configured ||
          job.status === 'stale' ||
          !job.mergeBaseSha
        )
          return;
        const { repository, number, baseSha, headSha } = job;
        void change(
          'Queuing comparison…',
          (signal) =>
            githubReviewAPI.compare(
              {
                account: owner,
                repository,
                number,
                baseSha,
                headSha,
                revision: snapshot.config.revision,
                candidateId,
                requestId: edit.requestId,
                runtimeId: edit.runtime.id,
                assertion: edit.assertion,
              },
              signal,
            ),
          () => {
            editComparison(candidateId);
            setNotice('Comparison queued');
          },
        );
      },
    },
    drafts: assessmentDrafts[owner] ?? {},
    disabled: disabled || !snapshot?.github.connected || !owner,
    onEdit: editAssessment,
    onFollowup: (job, previousCandidateId, onOpened) => {
      if (!owner || !snapshot?.github.connected || job.status === 'stale')
        return;
      const { repository, number, baseSha, headSha } = job;
      void change(
        'Opening local follow-up…',
        async (signal) => {
          const target = { repository, number, baseSha, headSha };
          const updated = await githubReviewAPI.followup(
            {
              ...target,
              account: owner,
              previousCandidateId,
            },
            signal,
          );
          if (
            updated.jobs.some(
              (item) =>
                item.repository.toLowerCase() === repository.toLowerCase() &&
                item.number === number &&
                item.baseSha === baseSha &&
                item.headSha === headSha,
            )
          )
            return updated;
          const result = await githubReviewAPI.job(target, signal);
          return result.account.toLowerCase() === owner.toLowerCase()
            ? { ...updated, jobs: [...updated.jobs, result.job] }
            : updated;
        },
        (updated) => {
          const revision = updated.jobs.find(
            (j) =>
              j.repository.toLowerCase() === repository.toLowerCase() &&
              j.number === number &&
              j.baseSha === baseSha &&
              j.headSha === headSha,
          );
          const followup = revision?.followups?.find(
            (item) => item.originId === previousCandidateId,
          );
          if (followup) onOpened(followup.id);
          setNotice('Reviewer follow-up opened');
        },
      );
    },
    onSave: (job, candidateId, decision) => {
      if (!owner || !snapshot?.github.connected || job.status === 'stale')
        return;
      const { repository, number, baseSha, headSha } = job;
      void change(
        'Saving local assessment…',
        (signal) =>
          githubReviewAPI.decide(
            {
              account: owner,
              repository,
              number,
              baseSha,
              headSha,
              candidateId,
              revision: decision.revision,
              status: decision.status,
              note: decision.note,
              previousCandidateId: decision.previousCandidateId,
              evidenceIds: decision.evidence.map((item) => item.id),
            },
            signal,
          ),
        () => {
          editAssessment(candidateId);
          setNotice('Local assessment saved');
        },
      );
    },
  };

  const refreshStatus = (
    <IconButton
      icon="refresh"
      label="Refresh review status"
      disabled={!interactive || !!busy}
      onClick={() => {
        poller.current?.refresh();
        setReviewRefresh((previous) => previous + 1);
      }}
    />
  );

  return (
    <div className="github-review-app">
      <Tabs
        label="GitHub Review sections"
        value={tab}
        tabs={[
          { id: 'history', label: 'Pull requests', disabled: !interactive },
          { id: 'settings', label: 'Settings', disabled: !interactive },
        ]}
        onChange={(next) =>
          desktop.updateWindowState(window.id, (state) => ({
            ...state,
            githubReviewTab: next,
          }))
        }
      >
        {readError && (
          <p className="app-error" role="alert">
            {readError} {snapshot && 'Shown results may be stale.'}
          </p>
        )}
        {actionError && (
          <p className="app-error" role="alert">
            {actionError}
          </p>
        )}
        {snapshot?.error && snapshot.error !== snapshot.discovery?.error && (
          <p className="app-error" role="alert">
            {snapshot.error}
          </p>
        )}
        {!snapshot || !settings ? (
          !readError && (
            <p className="app-pad" role="status">
              Loading GitHub Review…
            </p>
          )
        ) : (
          <>
            <ReviewSettings
              active={tab === 'settings'}
              interactive={interactive}
              snapshot={snapshot}
              values={settings}
              navigation={settingsNavigation}
              dirty={Boolean(draft)}
              stale={stale}
              disabled={disabled}
              desktop={desktop}
              onEdit={edit}
              onSave={saveSettings}
              onReload={() => void reloadSettings()}
              onConnect={(provider) => connection('connect', provider)}
              onDisconnect={(provider) => connection('disconnect', provider)}
            />
            <div
              className="app-pad"
              hidden={tab !== 'history'}
              inert={!interactive || tab !== 'history'}
            >
              <ReviewHistory
                automation={(openReview) => (
                  <div className="github-review-automation-panel">
                    <ReviewAutomationStatus
                      snapshot={snapshot}
                      onOpenSettings={(section) => {
                        setSettingsNavigation({ section });
                        desktop.updateWindowState(window.id, (state) => ({
                          ...state,
                          githubReviewTab: 'settings',
                        }));
                      }}
                    />
                    <ReviewIntake
                      disabled={disabled}
                      pending={busy === 'Checking the pull request…'}
                      connected={
                        snapshot.github.connected && snapshot.codex.connected
                      }
                      onRequest={(target, close, onError) => {
                        void change(
                          'Checking the pull request…',
                          (signal) =>
                            githubReviewAPI.reviewPR(
                              { ...target, revision: snapshot.config.revision },
                              signal,
                            ),
                          ({ selectedReview }) => {
                            close();
                            openReview(selectedReview);
                            setNotice(
                              selectedReview.status === 'queued'
                                ? 'Review queued'
                                : selectedReview.status === 'reviewing'
                                  ? 'Review in progress'
                                  : 'Existing review opened',
                            );
                          },
                          onError,
                        );
                      }}
                    >
                      <Button
                        disabled={disabled || !scanEnabled}
                        onClick={triggerScan}
                      >
                        Scan now
                      </Button>
                      {refreshStatus}
                    </ReviewIntake>
                  </div>
                )}
                dailyLimitReached={
                  snapshot.budget.used >= snapshot.config.dailyLimit
                }
                refreshStatus={refreshStatus}
                account={
                  snapshot.reviewAccount ?? snapshot.github.account ?? ''
                }
                hasJobs={hasReviews}
                refreshKey={reviewRefresh}
                assessments={assessments}
                latestDisabled={
                  disabled ||
                  !snapshot.github.connected ||
                  !snapshot.codex.connected
                }
                onLatest={(job, select) => {
                  const { repository, number, baseSha, headSha } = job;
                  void change(
                    'Checking the latest commit…',
                    (signal) =>
                      githubReviewAPI.reviewLatest(
                        {
                          repository,
                          number,
                          baseSha,
                          headSha,
                          revision: snapshot.config.revision,
                        },
                        signal,
                      ),
                    (value) => {
                      const latest = value.jobs.find(
                        (j) =>
                          j.repository.toLowerCase() ===
                            repository.toLowerCase() &&
                          j.number === number &&
                          j.status !== 'stale',
                      );
                      if (latest) select(latest);
                      setNotice('Latest PR comparison checked');
                    },
                  );
                }}
                retryDisabled={disabled || !scanEnabled}
                key={snapshot.reviewAccount ?? snapshot.github.account ?? ''}
                interactive={interactive && tab === 'history'}
                savedPolicy={savedPolicy}
                testDisabled={disabled}
                onRetry={retryReview}
                onRetryTest={retryTest}
                onConfirmRecommendation={confirmTestRecommendation}
                onRefreshChecks={refreshChecks}
                checksDisabled={disabled || !snapshot.github.connected}
              />
            </div>
          </>
        )}
      </Tabs>
      <StatusBar>{busy || notice}</StatusBar>
    </div>
  );
}
