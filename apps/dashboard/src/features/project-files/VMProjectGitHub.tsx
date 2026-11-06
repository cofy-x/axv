import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Field } from '@cofy-x/axv-nextstep';
import type { VMProjectBinding } from './vm-api';
import { vmFileBindingKey } from './vm-file-observer';
import {
  githubChecksLabel,
  githubLink,
  githubRepositoryAllowed,
  verifyGitHubResult,
  vmGitHubAPI,
} from './vm-github-api';
import type {
  GitHubAssociationState,
  GitHubCheckSet,
  GitHubGitState,
  GitHubRequest,
  GitHubResult,
} from './vm-github-api';

const describe = (error: unknown) =>
  error instanceof Error ? error.message : String(error);

function ObjectLink({
  url,
  children,
}: {
  url?: string;
  children: React.ReactNode;
}) {
  const href = githubLink(url);
  return href ? (
    <a href={href} target="_blank" rel="noreferrer">
      {children}
    </a>
  ) : (
    <>{children}</>
  );
}

function Checks({
  title,
  checks,
  repositoryId,
  sha,
}: {
  title: string;
  checks?: GitHubCheckSet;
  repositoryId: string;
  sha: string;
}) {
  const matched =
    !!checks && checks.repository.id === repositoryId && checks.sha === sha;
  return (
    <section className="vm-github-checks" aria-label={title}>
      <h4>{title}</h4>
      <p>{githubChecksLabel(checks, repositoryId, sha)}</p>
      {checks && (
        <p>
          <code>{checks.repository.nameWithOwner}</code> · SHA{' '}
          <code>{checks.sha}</code>
        </p>
      )}
      {matched && !!checks.checks.length && (
        <ul>
          {checks.checks.map((check, index) => (
            <li key={`${check.source}:${check.name}:${index}`}>
              <ObjectLink url={check.url}>{check.name}</ObjectLink> ·{' '}
              {check.conclusion || check.status || 'Unknown'}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function GitState({ git }: { git: GitHubGitState }) {
  return (
    <dl className="info-list vm-github-git" aria-label="Observed local Git">
      <dt>Local branch</dt>
      <dd>
        {!git.repository
          ? 'No Git repository at this project root'
          : git.detached
            ? 'Detached HEAD'
            : git.unborn
              ? `${git.branch || 'Unborn branch'} · no commit yet`
              : git.branch || 'Unknown'}
      </dd>
      <dt>Local HEAD</dt>
      <dd>
        <code>{git.headSha || 'No commit'}</code>
      </dd>
    </dl>
  );
}

export function VMProjectGitHub({
  projectId,
  hostId,
  binding,
  active,
  interactive,
  onNoticeChange,
}: {
  projectId: string;
  hostId: string;
  binding: VMProjectBinding | null;
  active: boolean;
  interactive: boolean;
  onNoticeChange: (notice: '' | 'error' | 'working') => void;
}) {
  const [state, setState] = useState<GitHubAssociationState | null>(null);
  const [repository, setRepository] = useState('');
  const [git, setGit] = useState<GitHubGitState | null>(null);
  const [review, setReview] = useState<GitHubResult | null>(null);
  const [snapshot, setSnapshot] = useState<GitHubResult | null>(null);
  const [stale, setStale] = useState(false);
  const [error, setError] = useState('');
  const [feedback, setFeedback] = useState('');
  const [working, setWorking] = useState('');
  const mounted = useRef(false);
  const generation = useRef(0);
  const pending = useRef<AbortController | null>(null);
  const context = JSON.stringify([
    hostId,
    projectId,
    binding ? vmFileBindingKey(binding) : '',
  ]);
  const latest = useRef({ context, active, interactive, state });
  latest.current = { context, active, interactive, state };
  const association = state?.association;
  const notice = error ? 'error' : working ? 'working' : '';

  useEffect(() => {
    onNoticeChange(notice);
  }, [notice, onNoticeChange]);

  const invalidate = useCallback(() => {
    generation.current++;
    pending.current?.abort();
    pending.current = null;
    setWorking('');
  }, []);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      pending.current?.abort();
    };
  }, []);

  useEffect(() => {
    invalidate();
    setReview(null);
    setGit(null);
    setStale(true);
  }, [context, invalidate]);

  useEffect(() => {
    if (!active) invalidate();
  }, [active, invalidate]);

  const loadAssociation = useCallback(async () => {
    invalidate();
    const currentGeneration = generation.current;
    const controller = new AbortController();
    pending.current = controller;
    setWorking('Loading association…');
    try {
      const result = await vmGitHubAPI.get(
        projectId,
        hostId,
        controller.signal,
      );
      if (
        !mounted.current ||
        controller.signal.aborted ||
        generation.current !== currentGeneration
      )
        return;
      if (!Number.isSafeInteger(result.revision) || result.revision < 0)
        throw new Error('GitHub returned an invalid association revision.');
      if (latest.current.state?.revision !== result.revision) {
        setReview(null);
        setStale(true);
      }
      setState(result);
      setError('');
    } catch (error) {
      if (mounted.current && generation.current === currentGeneration)
        setError(describe(error));
    } finally {
      if (mounted.current && generation.current === currentGeneration) {
        pending.current = null;
        setWorking('');
      }
    }
  }, [hostId, projectId, invalidate]);

  useEffect(() => {
    if (active) void loadAssociation();
  }, [active, loadAssociation]);

  async function run(
    action: 'git' | 'review' | 'associate' | 'clear' | 'refresh',
  ) {
    if (pending.current || !binding || !state || !latest.current.interactive)
      return;
    const captured: GitHubRequest = {
      binding: { ...binding },
      revision: state.revision,
    };
    if (action === 'review') captured.repository = repository;
    if (action === 'associate') {
      if (!review) return;
      captured.repository = review.observation.repository.nameWithOwner;
      captured.repositoryId = review.observation.repository.id;
      captured.accountId = review.observation.account.id;
    }
    if (action === 'refresh') {
      if (!association) return;
      captured.repositoryId = association.repository.id;
      captured.accountId = association.account.id;
    }
    const capturedContext = context;
    const currentGeneration = ++generation.current;
    const controller = new AbortController();
    pending.current = controller;
    const current = () =>
      mounted.current &&
      !controller.signal.aborted &&
      generation.current === currentGeneration &&
      latest.current.context === capturedContext &&
      latest.current.active &&
      latest.current.state?.revision === captured.revision;
    setWorking(
      action === 'refresh'
        ? 'Refreshing GitHub…'
        : action === 'git'
          ? 'Reading Git metadata…'
          : action === 'review'
            ? 'Reviewing repository…'
            : 'Saving association…',
    );
    setError('');
    setFeedback('');
    if (action === 'review') setReview(null);
    if (action === 'refresh') setStale(true);
    try {
      if (action === 'associate' || action === 'clear') {
        const result = await vmGitHubAPI[action](
          projectId,
          captured,
          controller.signal,
        );
        if (!current()) return;
        if (
          result.revision !== captured.revision + 1 ||
          (action === 'clear' && result.association) ||
          (action === 'associate' &&
            (result.association?.repository.id !== captured.repositoryId ||
              result.association?.account.id !== captured.accountId ||
              result.association?.machineId !== captured.binding.machineId ||
              result.association?.profile !== 'axv'))
        )
          throw new Error(
            'The association receipt does not match the reviewed target. Reload the association before continuing.',
          );
        setState(result);
        setReview(null);
        setSnapshot(null);
        setFeedback(
          action === 'associate'
            ? 'Repository associated. Refresh to read PRs and checks.'
            : 'Repository association cleared.',
        );
      } else {
        const result = await vmGitHubAPI[action](
          projectId,
          captured,
          controller.signal,
        );
        if (!current()) return;
        verifyGitHubResult(result, captured);
        setGit(result.observation.git);
        if (
          action !== 'refresh' &&
          snapshot &&
          (snapshot.observation.git.headSha !==
            result.observation.git.headSha ||
            snapshot.observation.git.branch !== result.observation.git.branch)
        )
          setStale(true);
        if (action === 'review') {
          if (
            !result.observation.repository.id ||
            !result.observation.account.id ||
            !githubRepositoryAllowed(
              result.observation.repository.nameWithOwner,
            ) ||
            !githubLink(result.observation.repository.url)
          )
            throw new Error(
              'GitHub did not return a reviewable repository and account.',
            );
          setReview(result);
        }
        if (action === 'refresh') {
          setSnapshot(result);
          setStale(false);
        }
      }
    } catch (error) {
      if (current()) {
        setError(describe(error));
        setStale(true);
      }
    } finally {
      if (mounted.current && generation.current === currentGeneration) {
        pending.current = null;
        setWorking('');
      }
    }
  }

  const disabled = !interactive || !!working || !binding || !state;
  const observation = snapshot?.observation;
  const localGit = git || observation?.git;
  const differentMachine =
    !!association && association.machineId !== binding?.machineId;
  const observedStale =
    stale ||
    snapshot?.revision !== state?.revision ||
    (!!snapshot &&
      (!binding ||
        vmFileBindingKey(snapshot.binding) !== vmFileBindingKey(binding)));

  return (
    <div className="vm-project-github">
      <h2 className="vm-project-workflow-title">GitHub project review</h2>
      <p>
        Review this project’s repository and account explicitly. Reads use the
        dedicated AXV profile in this VM; refresh never creates a project task.
      </p>
      {error && (
        <p className="project-error" role="alert">
          {error}
        </p>
      )}
      {!binding && (
        <p role="status">
          The VM project is unavailable. Reconnect the project explicitly.
        </p>
      )}
      <div className="app-actions vm-github-actions">
        <Button
          disabled={!interactive || !!working}
          onClick={() => void loadAssociation()}
        >
          Reload association
        </Button>
        <Button disabled={disabled} onClick={() => void run('git')}>
          Read Git metadata
        </Button>
        <Button
          primary
          disabled={disabled || !association || differentMachine}
          onClick={() => void run('refresh')}
        >
          Refresh GitHub
        </Button>
      </div>
      {(working || feedback) && <p role="status">{working || feedback}</p>}
      {association ? (
        <section aria-label="Reviewed GitHub association">
          <h3>Associated repository</h3>
          <p>
            <ObjectLink url={association.repository.url}>
              {association.repository.nameWithOwner}
            </ObjectLink>
          </p>
          <p>
            Account: {association.account.login} · AXV profile · VM{' '}
            <code>{association.machineId}</code>
          </p>
          {differentMachine && (
            <p role="status">
              This association was reviewed on another VM. Review and associate
              the repository again for this VM.
            </p>
          )}
          <Button disabled={disabled} onClick={() => void run('clear')}>
            Clear association
          </Button>
        </section>
      ) : (
        <p>No GitHub repository has been associated with this project.</p>
      )}
      {localGit && <GitState git={localGit} />}
      {observation && (
        <section aria-label="GitHub observation">
          <h3>
            {observedStale ? 'Stale GitHub observation' : 'GitHub observation'}
          </h3>
          <p>
            Observed {new Date(observation.observedAt).toLocaleString()} ·{' '}
            {observation.repository.nameWithOwner}
          </p>
          <p>
            Checks describe only the repository and SHA shown. Merge checks
            describe a synthetic merge commit.
          </p>
          <Checks
            title="Local HEAD checks"
            checks={observation.localChecks}
            repositoryId={observation.repository.id}
            sha={localGit?.headSha || ''}
          />
          {!observation.pullRequestsComplete && (
            <p>Pull request results are incomplete.</p>
          )}
          {!observation.pullRequests.length && (
            <p>
              No associated pull requests observed for this commit or branch.
            </p>
          )}
          <ul className="vm-github-prs">
            {observation.pullRequests.map((pr) => {
              const localMatch =
                pr.headRepository.id === observation.repository.id &&
                pr.headSha === localGit?.headSha;
              const baseMatch =
                pr.baseRepository.id === observation.repository.id;
              return (
                <li key={pr.id}>
                  <h4>
                    <ObjectLink url={pr.url}>
                      #{pr.number} {pr.title}
                    </ObjectLink>{' '}
                    · {pr.state}
                  </h4>
                  <p>
                    PR head: <code>{pr.headRepository.nameWithOwner}</code> ·{' '}
                    {pr.headBranch} · <code>{pr.headSha}</code>
                  </p>
                  <p>
                    {localMatch
                      ? 'PR head matches local HEAD.'
                      : 'PR head does not match local HEAD in the associated repository.'}
                    {!baseMatch &&
                      ' PR base repository differs from the association.'}
                  </p>
                  <Checks
                    title={`PR #${pr.number} head checks`}
                    checks={pr.headChecks}
                    repositoryId={pr.headRepository.id}
                    sha={pr.headSha}
                  />
                  {pr.mergeChecks && (
                    <Checks
                      title={`PR #${pr.number} merge checks · separate SHA`}
                      checks={pr.mergeChecks}
                      repositoryId={pr.baseRepository.id}
                      sha={pr.mergeChecks.sha}
                    />
                  )}
                </li>
              );
            })}
          </ul>
        </section>
      )}
      <section aria-label="GitHub repository association">
        <h3>Review repository</h3>
        <Field
          label="GitHub repository"
          value={repository}
          placeholder="owner/repository"
          disabled={!interactive || !!working}
          onChange={(event) => {
            invalidate();
            setRepository(event.target.value);
            setReview(null);
          }}
        />
        <div className="app-actions vm-github-actions">
          <Button
            disabled={disabled || !githubRepositoryAllowed(repository)}
            onClick={() => void run('review')}
          >
            Review repository and account
          </Button>
          <Button
            primary
            disabled={disabled || !review}
            onClick={() => void run('associate')}
          >
            Associate reviewed repository
          </Button>
        </div>
        {git?.remotes.length ? (
          <div aria-label="Repository candidates">
            <p>Remote candidates require review:</p>
            <ul>
              {git.remotes.map((remote, index) => (
                <li key={`${remote.name}:${index}`}>
                  <Button
                    disabled={disabled}
                    onClick={() => {
                      invalidate();
                      setRepository(remote.repository);
                      setReview(null);
                    }}
                  >
                    {remote.name} · {remote.repository}
                  </Button>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
        {git?.remotesPartial && <p>Remote candidates are incomplete.</p>}
        {review && (
          <section className="vm-github-review" aria-label="Repository review">
            <p>
              Repository:{' '}
              <ObjectLink url={review.observation.repository.url}>
                {review.observation.repository.nameWithOwner}
              </ObjectLink>{' '}
              · ID <code>{review.observation.repository.id}</code>
            </p>
            <p>
              Account: {review.observation.account.login} · ID{' '}
              <code>{review.observation.account.id}</code>
            </p>
            <p>
              Guest AXV profile · VM <code>{review.binding.machineId}</code>.
              Associate to retain this exact repository and account.
            </p>
          </section>
        )}
      </section>
      <details className="vm-github-setup">
        <summary>Set up guest GitHub access</summary>
        <p>
          Install the official GitHub CLI as <code>~/.local/bin/gh</code> in
          this VM. Authenticate explicitly in Guest Terminal using the dedicated
          guest root-disk profile:
        </p>
        <pre>{`umask 077
mkdir -p "$HOME/.config/axv/github"
chmod 700 "$HOME/.config/axv/github"
GH_CONFIG_DIR="$HOME/.config/axv/github" "$HOME/.local/bin/gh" auth login --hostname github.com --git-protocol https --web
chmod 600 "$HOME/.config/axv/github/hosts.yml"`}</pre>
        <p>
          The credential can have broader authority; this panel runs fixed reads
          only. AXV does not copy host credentials or fall back to the guest’s
          default GitHub profile. Select an account with access to the reviewed
          repository, then review the repository and account above.
        </p>
        <p>
          <ObjectLink url="https://github.com/cli/cli#installation">
            Official GitHub CLI installation
          </ObjectLink>
        </p>
      </details>
    </div>
  );
}
