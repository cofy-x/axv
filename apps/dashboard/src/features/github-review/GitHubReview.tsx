import { useEffect, useRef, useState } from 'react';
import {
  Button,
  Checkbox,
  Field,
  IconButton,
  Select,
  StatusBar,
  Tabs,
  TextArea,
} from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { SerialPoller } from '../../shared/api/serial-poller';
import {
  githubReviewAPI,
  reviewError,
  reviewLoginURL,
  reviewPRURL,
} from './api';
import type {
  ComparisonRuntime,
  ReviewConfig,
  ReviewConnection,
  ReviewJob,
  ReviewProvider,
  ReviewSnapshot,
} from './api';
import { ReviewHistory } from './ReviewHistory';
import { ComparisonRuntimes } from './ComparisonRuntimes';
import {
  QuickTestProfiles,
  testProfileDraft,
  testProfileConfig,
} from './QuickTestProfiles';
import type { TestProfileDraft } from './QuickTestProfiles';
import type { AssessmentControls, DecisionDraft } from './CandidateDecisions';
import type { ComparisonDraft } from './CandidateComparisons';
import { shellComparisonRuntime } from './api';
import './github-review.css';

interface Draft {
  repositories: string;
  policies: Record<
    string,
    {
      rules: string;
      command: string;
      profiles: TestProfileDraft[];
      runtimes: ComparisonRuntime[];
    }
  >;
  intervalMinutes: string;
  dailyLimit: string;
  enabled: boolean;
  revision: number;
}

function configDraft(config: ReviewConfig): Draft {
  return {
    repositories: (config.repositories ?? []).join('\n'),
    policies: Object.fromEntries(
      Object.entries(config.repositoryPolicies ?? {}).map(([repo, policy]) => [
        repo.toLowerCase(),
        {
          rules: policy.rules ?? '',
          command: policy.testCommand?.join('\n') ?? '',
          profiles: (policy.testProfiles ?? []).map(testProfileDraft),
          runtimes: policy.comparisonRuntimes ?? [],
        },
      ]),
    ),
    intervalMinutes: String(config.intervalMinutes),
    dailyLimit: String(config.dailyLimit),
    enabled: config.enabled,
    revision: config.revision,
  };
}

function repositoryLines(value: string) {
  const repositories = new Map<string, string>();
  for (const line of value.split('\n')) {
    const name = line.trim();
    const key = name.toLowerCase();
    if (name && !repositories.has(key)) repositories.set(key, name);
  }
  return [...repositories.values()];
}

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

const providers = {
  github: 'GitHub',
  codex: 'Codex',
} satisfies Record<ReviewProvider, string>;

function Connection({
  provider,
  connection,
  disabled,
  onConnect,
  onDisconnect,
}: {
  provider: ReviewProvider;
  connection: ReviewConnection;
  disabled: boolean;
  onConnect: () => void;
  onDisconnect: () => void;
}) {
  const label = providers[provider];
  const pending = connection.attempt?.status === 'pending';
  const url = pending ? reviewLoginURL(provider, connection.attempt?.url) : '';
  const problem = connection.attempt?.error || connection.error;
  return (
    <section className="github-review-connection" aria-label={label}>
      <div className="github-review-row">
        <div>
          <strong>{label}</strong>
          <span className="github-review-detail">
            {connection.connected
              ? connection.account || 'Connected'
              : pending
                ? 'Waiting for authorization'
                : connection.available
                  ? 'Not connected'
                  : 'Unavailable'}
          </span>
        </div>
        {connection.connected || pending ? (
          <Button
            aria-label={
              pending ? `Cancel ${label} connection` : `Disconnect ${label}`
            }
            disabled={disabled}
            onClick={onDisconnect}
          >
            {pending ? 'Cancel' : 'Disconnect'}
          </Button>
        ) : (
          <Button
            aria-label={`Connect ${label}`}
            disabled={disabled || !connection.available}
            onClick={onConnect}
          >
            Connect
          </Button>
        )}
      </div>
      {pending && connection.attempt?.code && url && (
        <div className="github-review-auth" role="status">
          Open{' '}
          <a href={url} target="_blank" rel="noreferrer">
            {label} authorization
          </a>{' '}
          and enter <code>{connection.attempt.code}</code>.
        </div>
      )}
      {problem && (
        <p className="github-review-problem" role="alert">
          {problem}
        </p>
      )}
    </section>
  );
}

export function GitHubReview({
  window,
  desktop,
  interactive = true,
}: AppWindowProps) {
  const [snapshot, setSnapshot] = useState<ReviewSnapshot>();
  const [draft, setDraft] = useState<Draft>();
  const [busy, setBusy] = useState('');
  const [readError, setReadError] = useState('');
  const [actionError, setActionError] = useState('');
  const [notice, setNotice] = useState('');
  const [editingRepo, setEditingRepo] = useState('');
  const [assessmentDrafts, setAssessmentDrafts] = useState<
    Record<string, Record<string, DecisionDraft>>
  >({});
  const [comparisonDrafts, setComparisonDrafts] = useState<
    Record<string, ComparisonDraft>
  >({});
  const operation = useRef<AbortController>(null);
  const poller = useRef<SerialPoller>(null);
  const tab =
    window.viewState.githubReviewTab === 'settings' ||
    window.viewState.githubReviewTab === 'history'
      ? window.viewState.githubReviewTab
      : snapshot &&
          !snapshot.config.repositories?.length &&
          !snapshot.jobs?.length
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
        if (dirtyDecisions || dirtyComparisons) {
          const answer = await desktop.dialog({
            title: 'Close GitHub Review?',
            message: dirtyComparisons
              ? 'Closing will discard your unsaved GitHub Review edits, including comparison assertions.'
              : draft
                ? 'Closing will discard your unsaved assessments and review settings.'
                : 'Closing will discard your unsaved assessments.',
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
    [desktop, window.id, draft, assessmentDrafts, comparisonDrafts, snapshot],
  );

  function edit(change: Partial<Omit<Draft, 'revision'>>) {
    if (!settings) return;
    const next = { ...settings, ...change };
    const unchanged =
      next.revision === saved?.revision &&
      next.repositories === saved.repositories &&
      next.intervalMinutes === saved.intervalMinutes &&
      next.dailyLimit === saved.dailyLimit &&
      next.enabled === saved.enabled &&
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

  async function change(
    label: string,
    request: (signal: AbortSignal) => Promise<ReviewSnapshot>,
    onSuccess?: (value: ReviewSnapshot) => void,
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
      onSuccess?.(value);
    } catch (error) {
      if (!controller.signal.aborted) setActionError(reviewError(error));
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
    const repositories = repositoryLines(draft.repositories);
    for (const repo of repositories) {
      const policy = draft.policies[repo.toLowerCase()];
      if (
        policy?.runtimes.some(
          (runtime) => !runtime.name.trim() || !runtime.command[0]?.trim(),
        )
      ) {
        setActionError(
          `Enter a name and a launcher for each comparison runtime in ${repo}.`,
        );
        setEditingRepo(repo.toLowerCase());
        return;
      }
      const incomplete = policy?.profiles.findIndex(
        (profile) =>
          !profile.name.trim() ||
          !profile.entryPath.trim() ||
          !profile.command.trim(),
      );
      if (incomplete !== undefined && incomplete >= 0) {
        setActionError(
          `Complete the name, entry file and command for test ${incomplete + 1} in ${repo}.`,
        );
        setEditingRepo(repo.toLowerCase());
        return;
      }
    }
    const repositoryPolicies = Object.fromEntries(
      repositories.flatMap((repo) => {
        const policy = draft.policies[repo.toLowerCase()];
        if (
          !policy ||
          (!policy.rules.trim() &&
            !policy.command.trim() &&
            !policy.profiles.length &&
            !policy.runtimes.length)
        )
          return [];
        return [
          [
            repo,
            {
              rules: policy.rules,
              ...(policy.profiles.length
                ? { testProfiles: policy.profiles.map(testProfileConfig) }
                : {}),
              ...(policy.runtimes.length
                ? {
                    comparisonRuntimes: policy.runtimes.map((runtime) => ({
                      ...runtime,
                      name: runtime.name.trim(),
                      command: runtime.command.filter((arg) => arg.length > 0),
                    })),
                  }
                : {}),
              testCommand: policy.command
                .split('\n')
                .filter((arg) => arg.length > 0),
            },
          ],
        ];
      }),
    );
    const config: ReviewConfig = {
      enabled: draft.enabled,
      revision: draft.revision,
      repositoryPolicies,
      repositories,
      publishRepositories: [],
      intervalMinutes: Number(draft.intervalMinutes),
      dailyLimit: Number(draft.dailyLimit),
    };
    void change(
      'Saving settings…',
      (signal) => githubReviewAPI.save(config, signal),
      () => {
        setDraft(undefined);
        setNotice('Settings saved');
      },
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
    comparisons: {
      drafts: comparisonDrafts,
      ready: !!snapshot?.comparisonReady,
      disabled: disabled || !snapshot?.github.connected || !owner,
      runtimes: comparisonRuntimes,
      onEdit: editComparison,
      onRun: (job, candidateId, edit) => {
        if (
          !owner ||
          !snapshot?.comparisonReady ||
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
    onFollowup: (job, previousCandidateId) => {
      if (!owner || !snapshot?.github.connected || job.status === 'stale')
        return;
      const { repository, number, baseSha, headSha } = job;
      void change(
        'Opening local follow-up…',
        (signal) =>
          githubReviewAPI.followup(
            {
              repository,
              number,
              baseSha,
              headSha,
              account: owner,
              previousCandidateId,
            },
            signal,
          ),
        () => setNotice('Reviewer follow-up opened'),
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

  const repoOptions = repositoryLines(settings?.repositories ?? '').filter(
    (repo) => reviewPRURL(repo, 1),
  );
  const repoKey = (
    repoOptions.find((repo) => repo.toLowerCase() === editingRepo) ||
    repoOptions[0] ||
    ''
  ).toLowerCase();
  const repoPolicy = settings?.policies[repoKey] || {
    rules: '',
    command: '',
    profiles: [] as TestProfileDraft[],
    runtimes: [] as ComparisonRuntime[],
  };

  function editPolicy(change: Partial<typeof repoPolicy>) {
    if (!settings || !repoKey) return;
    const policy = { ...repoPolicy, ...change };
    const policies = { ...settings.policies };
    if (
      policy.rules ||
      policy.command ||
      policy.profiles.length ||
      policy.runtimes.length
    )
      policies[repoKey] = policy;
    else delete policies[repoKey];
    edit({ policies });
  }

  const limitReached =
    snapshot && snapshot.budget.used >= snapshot.config.dailyLimit;
  const resetsAt = snapshot && (
    <time dateTime={snapshot.budget.resetsAt}>
      {new Date(snapshot.budget.resetsAt).toLocaleString(undefined, {
        dateStyle: 'short',
        timeStyle: 'short',
      })}
    </time>
  );

  return (
    <div className="github-review-app">
      <div className="app-toolbar">
        <Button disabled={disabled || !scanEnabled} onClick={triggerScan}>
          Scan now
        </Button>
        <IconButton
          icon="refresh"
          label="Refresh review status"
          disabled={!interactive || !!busy}
          onClick={() => poller.current?.refresh()}
        />
        <span className="toolbar-caption">
          {snapshot
            ? !snapshot.config.enabled
              ? 'Automatic reviews paused'
              : scanEnabled
                ? `Checks every ${snapshot.config.intervalMinutes} min`
                : 'Connect both accounts to start'
            : ''}
        </span>
      </div>
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
        {snapshot?.error && (
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
            <div
              className="app-pad github-review-settings"
              hidden={tab !== 'settings'}
              inert={!interactive || tab !== 'settings'}
            >
              <fieldset className="github-review-connections">
                <legend>Connections on this computer</legend>
                {(['github', 'codex'] as const).map((provider) => (
                  <Connection
                    key={provider}
                    provider={provider}
                    connection={snapshot[provider]}
                    disabled={disabled}
                    onConnect={() =>
                      void change(
                        `Connecting ${providers[provider]}…`,
                        (signal) =>
                          githubReviewAPI.connection(
                            'connect',
                            provider,
                            signal,
                          ),
                      )
                    }
                    onDisconnect={() =>
                      void change(
                        `Disconnecting ${providers[provider]}…`,
                        (signal) =>
                          githubReviewAPI.connection(
                            'disconnect',
                            provider,
                            signal,
                          ),
                      )
                    }
                  />
                ))}
              </fieldset>
              <form
                onSubmit={(event) => {
                  event.preventDefault();
                  saveSettings();
                }}
              >
                <fieldset disabled={disabled || stale}>
                  <legend>Watched repositories</legend>
                  <TextArea
                    label="Repositories (one owner/repo per line)"
                    value={settings.repositories}
                    onChange={(event) =>
                      edit({ repositories: event.target.value })
                    }
                    rows={3}
                    spellCheck={false}
                  />

                  {repoOptions.length > 0 && (
                    <div className="github-review-policy">
                      <Select
                        label="Repository policy"
                        value={repoKey}
                        onChange={(event) => setEditingRepo(event.target.value)}
                      >
                        {repoOptions.map((repo) => (
                          <option
                            value={repo.toLowerCase()}
                            key={repo.toLowerCase()}
                          >
                            {repo}
                          </option>
                        ))}
                      </Select>
                      <TextArea
                        label="Review rules"
                        placeholder="What should Codex check in this repository?"
                        rows={3}
                        value={repoPolicy.rules}
                        onChange={(event) =>
                          editPolicy({ rules: event.target.value })
                        }
                      />
                      <QuickTestProfiles
                        key={repoKey}
                        profiles={repoPolicy.profiles}
                        singleCommand={repoPolicy.command}
                        onProfiles={(profiles) => editPolicy({ profiles })}
                        onCommand={(command) => editPolicy({ command })}
                      />
                      <ComparisonRuntimes
                        runtimes={repoPolicy.runtimes}
                        onChange={(runtimes) => editPolicy({ runtimes })}
                      />
                    </div>
                  )}
                </fieldset>
                <fieldset disabled={disabled || stale}>
                  <legend>Review schedule</legend>
                  <div className="github-review-settings-numbers">
                    <Field
                      label="Poll interval in minutes"
                      type="number"
                      value={settings.intervalMinutes}
                      onChange={(event) =>
                        edit({ intervalMinutes: event.target.value })
                      }
                      min={1}
                      max={1440}
                      step={1}
                      required
                    />
                    <Field
                      label="Daily review limit"
                      type="number"
                      value={settings.dailyLimit}
                      onChange={(event) =>
                        edit({ dailyLimit: event.target.value })
                      }
                      min={1}
                      max={1000}
                      step={1}
                      required
                    />
                  </div>
                  <p className="github-review-detail">
                    {snapshot.budget.used} of {snapshot.config.dailyLimit}{' '}
                    review starts used today. Resets {resetsAt}.
                  </p>
                  <Checkbox
                    label="Enable automatic reviews"
                    checked={settings.enabled}
                    onChange={(event) =>
                      edit({ enabled: event.target.checked })
                    }
                  />
                  <p className="fine-print">
                    AXV reviews ready PRs in watched repositories and direct
                    review requests. Candidate comments stay in AXV.
                  </p>
                  <Button type="submit" primary disabled={!draft}>
                    Save settings
                  </Button>
                </fieldset>
                {stale && (
                  <div className="app-error" role="alert">
                    Saved settings changed. Reload settings before editing
                    again.
                    <div className="app-actions">
                      <Button
                        disabled={disabled}
                        onClick={() => void reloadSettings()}
                      >
                        Reload settings
                      </Button>
                    </div>
                  </div>
                )}
              </form>
            </div>
            <div
              className="app-pad"
              hidden={tab !== 'history'}
              inert={!interactive || tab !== 'history'}
            >
              <ReviewHistory
                queueNotice={
                  limitReached && (
                    <p className="github-review-queue-note">
                      Daily review limit reached. New reviews wait until{' '}
                      {resetsAt}.
                    </p>
                  )
                }
                jobs={snapshot.jobs ?? []}
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
                key={snapshot.github.account || ''}
                interactive={interactive && tab === 'history'}
                savedPolicy={savedPolicy}
                testDisabled={disabled}
                onRetry={retryReview}
                onRetryTest={retryTest}
                onRefreshChecks={refreshChecks}
                checksDisabled={disabled || !snapshot.github.connected}
              />
            </div>
          </>
        )}
      </Tabs>
      <StatusBar>
        {busy ||
          notice ||
          (snapshot?.lastPollAt
            ? `Last scan ${new Date(snapshot.lastPollAt).toLocaleString()}`
            : 'No scans this session')}
      </StatusBar>
    </div>
  );
}
