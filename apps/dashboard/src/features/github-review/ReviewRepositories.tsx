import { useEffect, useId, useRef, useState } from 'react';
import { Button, Checkbox, Field, TextArea } from '@cofy-x/axv-nextstep';
import { ComparisonRuntimes } from './ComparisonRuntimes';
import { QuickTestProfiles } from './QuickTestProfiles';
import {
  emptyPolicyDraft,
  isWatchedRepositoryName,
  watchedRepositoryLimit,
} from './review-settings';
import type { PolicyDraft, RepositorySettingsIssue } from './review-settings';
import { githubReviewAPI, reviewError } from './api';
import type { RepositoryReviewEnvironment, ReviewVMReadiness } from './api';
import './review-repositories.css';

type AddMode = 'single' | 'batch';

function candidatesStatus(policy: PolicyDraft) {
  const count = policy.comparisonScenarios.length;
  const scenarios = `${count} ${count === 1 ? 'scenario' : 'scenarios'}`;
  if (policy.autoCandidateValidation && !count) return 'Waiting for a scenario';
  return count
    ? `${policy.autoCandidateValidation ? 'On' : 'Off'} · ${scenarios}`
    : 'Off';
}

function quickTestCount(policy: PolicyDraft) {
  return policy.profiles.length || Number(Boolean(policy.command.trim()));
}

export function ReviewRepositoryList({
  repositories,
  policies,
  disabled,
  returnFocus,
  onAdd,
  onConfigure,
}: {
  repositories: string[];
  policies: Record<string, PolicyDraft>;
  disabled: boolean;
  returnFocus?: string;
  onAdd: (newRepositories: string[]) => void;
  onConfigure: (repository: string) => void;
}) {
  const titleId = useId();
  const errorId = useId();
  const panel = useRef<HTMLElement>(null);
  const addPanel = useRef<HTMLDivElement>(null);
  const addButton = useRef<HTMLDivElement>(null);
  const pendingFocus = useRef('');
  const restoredFocus = useRef('');
  const previousMode = useRef<AddMode>(undefined);
  const [mode, setMode] = useState<AddMode>();
  const [single, setSingle] = useState('');
  const [batch, setBatch] = useState('');
  const [error, setError] = useState('');

  useEffect(() => {
    if (mode)
      addPanel.current?.querySelector<HTMLElement>('input, textarea')?.focus();
    else if (previousMode.current && !pendingFocus.current)
      addButton.current?.querySelector('button')?.focus();
    previousMode.current = mode;
  }, [mode]);

  useEffect(() => {
    const repository = pendingFocus.current || returnFocus?.toLowerCase();
    if (
      !repository ||
      (!pendingFocus.current && restoredFocus.current === repository)
    )
      return;
    const trigger = Array.from(
      panel.current?.querySelectorAll<HTMLButtonElement>(
        'button[data-repository]',
      ) ?? [],
    ).find((item) => item.dataset.repository === repository);
    if (!trigger && pendingFocus.current) return;
    (trigger ?? addButton.current?.querySelector('button'))?.focus();
    pendingFocus.current = '';
    restoredFocus.current = repository;
  }, [repositories, returnFocus]);

  function changeMode(next?: AddMode) {
    if (next === 'batch' && !batch) setBatch(single);
    setMode(next);
    setError('');
  }

  function addRepositories() {
    if (disabled) return;
    const requested = (mode === 'batch' ? batch.split(/\r?\n/) : [single])
      .map((name) => name.trim())
      .filter(Boolean);
    if (!requested.length) {
      setError('Enter a repository as owner/repo.');
      return;
    }
    const invalid = requested.find((name) => !isWatchedRepositoryName(name));
    if (invalid) {
      setError(
        mode === 'batch'
          ? `Enter a valid owner/repo on line ${batch.split(/\r?\n/).findIndex((line) => line.trim() === invalid) + 1}.`
          : 'Enter a valid owner/repo.',
      );
      return;
    }
    const names = new Set(repositories.map((name) => name.toLowerCase()));
    const additions = requested.filter((name) => {
      const key = name.toLowerCase();
      if (names.has(key)) return false;
      names.add(key);
      return true;
    });
    if (!additions.length) {
      setError(
        mode === 'batch'
          ? 'These repositories are already in the list.'
          : 'This repository is already in the list.',
      );
      return;
    }
    if (names.size > watchedRepositoryLimit) {
      setError(
        `The watch list can contain up to ${watchedRepositoryLimit} repositories.`,
      );
      return;
    }
    pendingFocus.current = additions[0].toLowerCase();
    onAdd(additions);
    setSingle('');
    setBatch('');
    changeMode();
  }

  function cancelAdd() {
    setSingle('');
    setBatch('');
    changeMode();
  }

  return (
    <section
      aria-labelledby={titleId}
      className="github-review-repository-list"
      ref={panel}
    >
      <header className="github-review-row">
        <h2 id={titleId}>Repositories</h2>
        {!mode && (
          <div ref={addButton}>
            <Button
              primary
              disabled={disabled}
              onClick={() => changeMode('single')}
            >
              Add repository
            </Button>
          </div>
        )}
      </header>
      {mode && (
        <div
          aria-label="Add repositories"
          className="github-review-repository-add"
          ref={addPanel}
          role="group"
        >
          {mode === 'single' ? (
            <Field
              label="Repository name"
              placeholder="owner/repo"
              autoComplete="off"
              spellCheck={false}
              value={single}
              disabled={disabled}
              aria-invalid={!!error || undefined}
              aria-describedby={error ? errorId : undefined}
              onChange={(event) => {
                setSingle(event.target.value);
                setError('');
              }}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
                  event.preventDefault();
                  addRepositories();
                }
              }}
            />
          ) : (
            <TextArea
              label="Repository names (one owner/repo per line)"
              rows={4}
              spellCheck={false}
              value={batch}
              disabled={disabled}
              aria-invalid={!!error || undefined}
              aria-describedby={error ? errorId : undefined}
              onChange={(event) => {
                setBatch(event.target.value);
                setError('');
              }}
            />
          )}
          {error && (
            <p className="github-review-problem" id={errorId} role="alert">
              {error}
            </p>
          )}
          <div className="app-actions">
            <Button primary disabled={disabled} onClick={addRepositories}>
              Add to list
            </Button>
            <Button disabled={disabled} onClick={cancelAdd}>
              Cancel
            </Button>
            <Button
              disabled={disabled}
              onClick={() => changeMode(mode === 'single' ? 'batch' : 'single')}
            >
              {mode === 'single'
                ? 'Paste multiple repositories'
                : 'Add one repository'}
            </Button>
          </div>
        </div>
      )}
      {repositories.length ? (
        <ul>
          {repositories.map((repository) => {
            const key = repository.toLowerCase();
            const policy = policies[key] ?? emptyPolicyDraft;
            const quickTests = quickTestCount(policy);
            return (
              <li key={key} className="github-review-row">
                <div className="github-review-repository-description">
                  <strong>{repository}</strong>
                  <div className="github-review-meta">
                    <span>
                      {policy.rules.trim()
                        ? 'Custom review rules'
                        : 'Default review rules'}
                    </span>
                    <span>
                      {quickTests}{' '}
                      {quickTests === 1 ? 'quick test' : 'quick tests'}
                    </span>
                    <span>
                      Automatic verification{' '}
                      {candidatesStatus(policy).toLowerCase()}
                    </span>
                  </div>
                </div>
                <Button
                  aria-label={`Configure ${repository}`}
                  data-repository={key}
                  disabled={disabled}
                  onClick={() => onConfigure(repository)}
                >
                  Configure
                </Button>
              </li>
            );
          })}
        </ul>
      ) : (
        <p className="github-review-detail">
          Add repositories to watch for new pull requests.
        </p>
      )}
    </section>
  );
}

export function ReviewRepositoryEditor({
  repository,
  policy,
  disabled,
  environment,
  issue,
  onBack,
  onChange,
  onStopWatching,
}: {
  repository: string;
  policy: PolicyDraft;
  disabled: boolean;
  environment: RepositoryReviewEnvironment;
  issue?: RepositorySettingsIssue;
  onBack: () => void;
  onChange: (patch: Partial<PolicyDraft>) => void;
  onStopWatching: () => void;
}) {
  const titleId = useId();
  const errorId = useId();
  const title = useRef<HTMLHeadingElement>(null);
  const rules = useRef<HTMLDetailsElement>(null);
  const vmTests = useRef<HTMLDetailsElement>(null);
  const verification = useRef<HTMLDetailsElement>(null);
  const readinessRequest = useRef<AbortController>(null);
  const [readiness, setReadiness] = useState<ReviewVMReadiness>();
  const [checkingVM, setCheckingVM] = useState(false);

  useEffect(() => {
    title.current?.focus();
  }, [repository]);

  useEffect(() => () => readinessRequest.current?.abort(), []);

  async function checkVM() {
    if (readinessRequest.current) return;
    const controller = new AbortController();
    readinessRequest.current = controller;
    setCheckingVM(true);
    setReadiness(undefined);
    try {
      const result = await githubReviewAPI.vmReadiness(
        repository,
        AbortSignal.any([controller.signal, AbortSignal.timeout(12000)]),
      );
      if (!controller.signal.aborted) setReadiness(result);
    } catch (error) {
      if (!controller.signal.aborted)
        setReadiness({ status: 'error', message: reviewError(error) });
    } finally {
      readinessRequest.current = null;
      if (!controller.signal.aborted) setCheckingVM(false);
    }
  }

  useEffect(() => {
    if (!issue) return;
    const section =
      issue.kind === 'rules'
        ? rules.current
        : issue.kind === 'verification'
          ? verification.current
          : vmTests.current;
    if (!section) return;
    section.open = true;
    const runtimeGroup =
      issue.kind === 'runtime'
        ? section.querySelector<HTMLDetailsElement>('[data-review-runtimes]')
        : null;
    if (runtimeGroup) runtimeGroup.open = true;
    const id = issue.kind === 'quick-test' ? issue.profileId : issue.runtimeId;
    const editor = id
      ? Array.from(
          (runtimeGroup ?? section).querySelectorAll<HTMLElement>(
            '[data-review-settings-item]',
          ),
        ).find((item) => item.dataset.reviewSettingsItem === id)
      : section;
    const field =
      issue.kind === 'quick-test' && !issue.profileId
        ? section.querySelector<HTMLElement>('[data-review-legacy-command]')
        : editor?.querySelector<HTMLElement>('[aria-invalid="true"]');
    if (field) {
      const group = field.closest('details');
      if (group) group.open = true;
    }
    (field ?? section.querySelector('summary'))?.focus();
  }, [issue]);

  return (
    <section
      aria-labelledby={titleId}
      className="github-review-repository-editor"
    >
      <Button className="github-review-repository-back" onClick={onBack}>
        Back to repositories
      </Button>
      <h2 id={titleId} ref={title} tabIndex={-1}>
        {repository}
      </h2>
      <fieldset disabled={disabled} aria-label="Repository policy">
        <details ref={rules} className="github-review-repository-group">
          <summary>
            Review rules{' '}
            <span>{policy.rules.trim() ? 'Custom' : 'Defaults'}</span>
          </summary>
          <div className="github-review-repository-fields">
            {issue?.kind === 'rules' && (
              <p id={errorId} className="github-review-problem" role="alert">
                {issue.message}
              </p>
            )}
            <TextArea
              label="Review rules"
              placeholder="What should Codex check in this repository?"
              rows={4}
              value={policy.rules}
              aria-invalid={issue?.kind === 'rules' || undefined}
              aria-describedby={issue?.kind === 'rules' ? errorId : undefined}
              onChange={(event) => onChange({ rules: event.target.value })}
            />
          </div>
        </details>
        <details
          ref={vmTests}
          className="github-review-repository-group"
          onToggle={(event) => {
            if (
              event.currentTarget.open &&
              environment.configured &&
              !readiness
            )
              void checkVM();
          }}
        >
          <summary>
            VM tests{' '}
            <span>
              {environment.configured
                ? environment.name
                : 'Review VM needs setup'}
            </span>
          </summary>
          <div className="github-review-repository-fields">
            {(issue?.kind === 'quick-test' || issue?.kind === 'runtime') && (
              <p id={errorId} className="github-review-problem" role="alert">
                {issue.message}
              </p>
            )}
            {environment.configured ? (
              <div>
                {(checkingVM || readiness) && (
                  <p
                    role="status"
                    className={
                      !readiness || readiness.status === 'reachable'
                        ? 'github-review-detail'
                        : 'github-review-problem'
                    }
                  >
                    {checkingVM
                      ? 'Checking the review VM…'
                      : readiness?.message}
                  </p>
                )}
                {!checkingVM && (
                  <Button onClick={() => void checkVM()}>
                    {readiness ? 'Recheck VM' : 'Check VM'}
                  </Button>
                )}
              </div>
            ) : (
              <p className="github-review-detail">
                Set up the review VM on this host before running local tests or
                candidate comparisons.
              </p>
            )}
            <QuickTestProfiles
              profiles={policy.profiles}
              singleCommand={policy.command}
              onProfiles={(profiles) => onChange({ profiles })}
              onCommand={(command) => onChange({ command })}
              issue={issue?.kind === 'quick-test' ? issue : undefined}
              errorId={errorId}
            />
            <ComparisonRuntimes
              runtimes={policy.runtimes}
              onChange={(runtimes) => onChange({ runtimes })}
              issue={issue?.kind === 'runtime' ? issue : undefined}
              errorId={errorId}
            />
          </div>
        </details>
        <details ref={verification} className="github-review-repository-group">
          <summary>
            Automatic candidate verification{' '}
            <span>{candidatesStatus(policy)}</span>
          </summary>
          <div className="github-review-repository-fields">
            {issue?.kind === 'verification' && (
              <p id={errorId} className="github-review-problem" role="alert">
                {issue.message}
              </p>
            )}
            <Checkbox
              label="Enable automatic candidate verification"
              checked={policy.autoCandidateValidation}
              disabled={
                !policy.autoCandidateValidation &&
                !policy.comparisonScenarios.length
              }
              aria-invalid={issue?.kind === 'verification' || undefined}
              aria-describedby={
                issue?.kind === 'verification' ? errorId : undefined
              }
              onChange={(event) =>
                onChange({ autoCandidateValidation: event.target.checked })
              }
            />
            <p className="github-review-detail">
              {policy.comparisonScenarios.length
                ? 'AXV runs approved comparisons in the review VM. Candidates without a match still need manual verification.'
                : 'Confirm a reproduced regression and approve its comparison to enable verification for this repository.'}
            </p>
            {policy.comparisonScenarios.length > 0 && (
              <ul
                aria-label="Approved comparison scenarios"
                className="github-review-repository-scenarios"
              >
                {policy.comparisonScenarios.map((scenario) => (
                  <li key={scenario.id}>
                    <strong>{scenario.name}</strong>
                    <span className="github-review-detail">
                      <code>{scenario.path}</code> ·{' '}
                      {scenario.side === 'base' ? 'Diff base' : 'PR head'}
                      {scenario.title !== scenario.name &&
                        ` · ${scenario.title}`}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </details>
      </fieldset>
      <div className="github-review-repository-stop">
        <Button disabled={disabled} onClick={onStopWatching}>
          Stop watching
        </Button>
      </div>
    </section>
  );
}
