import { useEffect, useRef, useState } from 'react';
import { Button, Field, IconButton, Select } from '@cofy-x/axv-nextstep';
import type { PreparationPlan, PreparationRequirement } from './api';
import {
  preparationDraft,
  preparationDraftError,
  preparationRequest,
  preparationSize,
} from './preparation-form';
import type { PreparationDraft } from './preparation-form';
import { preparationRunning } from './preparation-store';
import { operationError } from './operation-requests';
import { humanize } from './presentation';
import type { useCompute } from './useCompute';

function Checks({ checks }: { checks: PreparationRequirement[] }) {
  function list(items: PreparationRequirement[]) {
    return (
      <ul
        className="machines-environment-checks"
        aria-label="Environment checks"
      >
        {items.map((check) => (
          <li key={check.id} data-state={check.status}>
            <span className="machines-check-state">
              {check.status === 'ready'
                ? 'Ready'
                : check.status === 'needed'
                  ? 'Needed'
                  : 'Needs attention'}
            </span>
            <div>
              <strong>{check.label}</strong>
              <p>{check.detail}</p>
            </div>
          </li>
        ))}
      </ul>
    );
  }

  const blocked = checks.filter((check) => check.status === 'blocked');
  const other = checks.filter((check) => check.status !== 'blocked');

  return (
    <>
      {!!blocked.length && list(blocked)}
      {!!other.length && (
        <details className="machines-technical">
          <summary>Environment checks ({other.length})</summary>
          {list(other)}
        </details>
      )}
    </>
  );
}

export function MachineEnvironment({
  data,
  onShowMachines,
}: {
  data: ReturnType<typeof useCompute>;
  onShowMachines: () => void;
}) {
  const { environment, info, preparation, prepare } = data;
  const { check, job } = preparation;
  const [draft, setDraft] = useState<PreparationDraft>(() =>
    preparationDraft({}),
  );
  const draftSource = useRef('');
  const [plan, setPlan] = useState<PreparationPlan>();
  const [reviewing, setReviewing] = useState(false);
  const [error, setError] = useState('');
  const [reviewedJob, setReviewedJob] = useState('');
  const [stage, setStage] = useState<'settings' | 'review' | 'progress'>(
    'settings',
  );
  const reviewRequest = useRef<AbortController | null>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const platform = check?.platform || environment?.platform;
  const configured =
    check?.configured ?? environment?.configured ?? info?.configured ?? false;
  const running = preparationRunning(job);
  const failed = job?.status === 'failed' || job?.status === 'interrupted';
  const busy = !!preparation.mutation || !!preparation.pending || running;
  const ready =
    configured &&
    !data.error &&
    environment?.runtime?.state !== 'failed' &&
    environment?.runtime?.state !== 'starting' &&
    !!info?.providers?.some((provider) => provider.available);
  const jobVersion = job ? `${job.id}:${job.revision}` : '';
  const showJob =
    !!job &&
    (running || (failed && reviewedJob !== jobVersion) || stage === 'progress');
  const showReview = !!plan && stage === 'review' && !showJob;
  const draftError = preparationDraftError(draft, platform || '', configured);
  const expired = !!plan && Date.parse(plan.expiresAt) <= Date.now();
  const connectsService =
    platform === 'linux' || environment?.runtime?.mode === 'external';
  const startLabel = connectsService
    ? 'Connect'
    : configured
      ? 'Start Service'
      : 'Prepare';
  const title = !check
    ? preparation.loading
      ? 'Checking VM availability'
      : 'VM environment unavailable'
    : running
      ? 'Preparing virtual machines'
      : failed && showJob
        ? job.status === 'interrupted'
          ? 'Preparation interrupted'
          : 'Preparation failed'
        : job?.status === 'cancelled' && showJob
          ? 'Preparation cancelled'
          : ready
            ? 'VM environment ready'
            : environment?.runtime?.state === 'starting'
              ? 'Starting VM service'
              : configured
                ? 'VM service needs attention'
                : showReview
                  ? 'Review VM preparation'
                  : 'Enable virtual machines';

  useEffect(() => {
    if (!check) return;
    const source = check.platform;
    if (draftSource.current === source) return;
    draftSource.current = source;
    setDraft(preparationDraft(check.defaults));
    setPlan(undefined);
    setStage('settings');
  }, [check]);

  useEffect(() => {
    if (running || (plan && job?.planId === plan.id)) setStage('progress');
  }, [running, job?.planId, plan]);

  useEffect(() => () => reviewRequest.current?.abort(), []);

  function change<Key extends keyof PreparationDraft>(
    key: Key,
    value: PreparationDraft[Key],
  ) {
    setDraft((current) => ({ ...current, [key]: value }));
    setPlan(undefined);
    setError('');
  }

  async function review() {
    if (!check || draftError || busy || preparation.connectionError) return;
    reviewRequest.current?.abort();
    const controller = new AbortController();
    reviewRequest.current = controller;
    setReviewing(true);
    setError('');
    try {
      const result = await prepare.review(
        preparationRequest(draft, check.platform, configured, check.defaults),
        controller.signal,
      );
      if (controller.signal.aborted) return;
      setPlan(result);
      setStage('review');
      heading.current?.focus();
    } catch (failure) {
      if (!controller.signal.aborted) setError(operationError(failure));
    } finally {
      if (reviewRequest.current === controller) {
        reviewRequest.current = null;
        setReviewing(false);
      }
    }
  }

  async function start() {
    if (!plan?.canStart || expired || busy || reviewing) return;
    await prepare.start(plan);
    if (prepare.getSnapshot().job?.planId === plan.id) setStage('progress');
  }

  return (
    <section className="machines-environment" aria-label="VM setup">
      <header className="machines-section-heading">
        <span className="machines-eyebrow">
          {platform === 'darwin'
            ? 'macOS'
            : platform === 'linux'
              ? 'Linux'
              : humanize(platform || 'Local host')}
          {check?.arch && ` · ${check.arch}`}
        </span>
        <h2 ref={heading} tabIndex={-1}>
          {title}
        </h2>
      </header>
      {environment?.loggingError && (
        <p className="machines-error" role="alert">
          Desktop logging needs attention. {environment.loggingError}{' '}
          {environment.logPath && <code>{environment.logPath}</code>} Check the
          profile’s disk space and permissions.
        </p>
      )}
      {!check ? (
        <>
          <p role="status">
            {preparation.loading
              ? 'Checking the VM environment…'
              : 'Environment checks could not be completed.'}
          </p>
          {preparation.connectionError && (
            <p className="machines-error" role="alert">
              {preparation.connectionError}
            </p>
          )}
        </>
      ) : (
        <>
          {preparation.connectionError && (
            <p className="machines-error" role="alert">
              Connection interrupted: {preparation.connectionError} The
              preparation job remains on the AXV machine.
            </p>
          )}
          {check.lockedReason && (
            <p className="machines-warning">{check.lockedReason}</p>
          )}
          {(error || preparation.error) && (
            <p className="machines-error" role="alert">
              {error || preparation.error}
            </p>
          )}
          {preparation.pending && (
            <div className="machines-preparation-notice" role="status">
              <p>
                The response to “{preparation.pending.label}” was interrupted.
                Resolve that exact request before submitting another action.
              </p>
              <Button
                disabled={!!preparation.mutation}
                onClick={() => void prepare.resolve()}
              >
                Retry Original Request
              </Button>
            </div>
          )}
          {showJob && job ? (
            <>
              <ol
                className="machines-preparation-stages"
                aria-label="Setup steps"
              >
                <li>1. Settings</li>
                <li>2. Review</li>
                <li aria-current="step">3. Prepare</li>
              </ol>
              <p className="machines-preparation-phase" role="status">
                {humanize(job.status)} · {humanize(job.phase)} · Attempt{' '}
                {job.attempt}
              </p>
              <ol
                className="machines-preparation-progress"
                aria-label="Preparation progress"
              >
                {job.steps.map((step) => (
                  <li key={step.id} data-state={step.status}>
                    <div className="machines-step-heading">
                      <strong>{step.label}</strong>
                      <span>{humanize(step.status)}</span>
                    </div>
                    {step.detail && <p>{step.detail}</p>}
                    {step.status === 'running' && (
                      <progress
                        aria-label={`${step.label} progress`}
                        value={
                          step.bytesTotal ? step.bytesDone || 0 : undefined
                        }
                        max={step.bytesTotal || undefined}
                      />
                    )}
                    {!!step.bytesDone && (
                      <p>
                        {preparationSize(step.bytesDone)}
                        {step.bytesTotal
                          ? ` of ${preparationSize(step.bytesTotal)}`
                          : ' transferred'}
                      </p>
                    )}
                  </li>
                ))}
              </ol>
              {job.error && (
                <p className="machines-error" role="alert">
                  {job.error.message}
                </p>
              )}
              <p className="machines-form-help">
                {job.committed
                  ? 'Configuration saved. Remaining work starts and verifies the VM service.'
                  : running
                    ? 'Preparation continues after closing this window. Cancel is available until configuration is saved.'
                    : job.status === 'cancelled'
                      ? 'Preparation cancelled. Verified downloads are kept for the next attempt.'
                      : 'Completed files are kept. Retry continues this job with its reviewed settings.'}
              </p>
              <div className="machines-form-actions">
                {job.canRetry && (
                  <Button
                    disabled={busy || !!preparation.connectionError}
                    onClick={() => void prepare.retry(job)}
                  >
                    Retry
                  </Button>
                )}
                {running && (
                  <Button
                    disabled={
                      !!preparation.mutation ||
                      !!preparation.pending ||
                      !job.canCancel ||
                      job.committed
                    }
                    onClick={() => void prepare.cancel(job)}
                  >
                    Cancel
                  </Button>
                )}
                {(job.status === 'cancelled' || failed) && (
                  <Button
                    disabled={busy}
                    onClick={() => {
                      setStage('settings');
                      setPlan(undefined);
                      setReviewedJob(jobVersion);
                    }}
                  >
                    Settings
                  </Button>
                )}
                {job.status === 'succeeded' && configured && (
                  <Button onClick={onShowMachines}>Show VMs</Button>
                )}
              </div>
              {job.status === 'succeeded' && !ready && (
                <p className="machines-form-help">
                  Preparation completed. Checking VM service readiness…
                </p>
              )}
              <details className="machines-technical">
                <summary>Reviewed preparation</summary>
                <dl className="info-list machines-details">
                  <dt>Job</dt>
                  <dd>
                    <code>{job.id}</code>
                  </dd>
                  <dt>Configuration</dt>
                  <dd>
                    <code>{job.plan.configPath}</code>
                  </dd>
                  {job.plan.platform === 'darwin' &&
                    job.plan.request.storageRoot && (
                      <>
                        <dt>Storage</dt>
                        <dd>
                          <code>{job.plan.request.storageRoot}</code>
                        </dd>
                      </>
                    )}
                </dl>
                <ol>
                  {job.plan.actions.map((action, index) => (
                    <li key={`${index}:${action}`}>{action}</li>
                  ))}
                </ol>
              </details>
            </>
          ) : showReview && plan ? (
            <>
              <ol
                className="machines-preparation-stages"
                aria-label="Setup steps"
              >
                <li>1. Settings</li>
                <li aria-current="step">2. Review</li>
                <li>3. Prepare</li>
              </ol>
              <p>Confirm the changes to this host before continuing.</p>
              <Checks checks={plan.checks} />
              <dl className="info-list machines-details">
                <dt>Provider</dt>
                <dd>
                  {plan.provider} · {plan.arch}
                </dd>
                <dt>Configuration</dt>
                <dd>
                  <code>{plan.configPath}</code>
                </dd>
                {plan.platform === 'darwin' && plan.request.storageRoot && (
                  <>
                    <dt>VM storage</dt>
                    <dd>
                      <code>{plan.request.storageRoot}</code>
                    </dd>
                  </>
                )}
                {plan.platform === 'darwin' && plan.request.limaHome && (
                  <>
                    <dt>Lima state</dt>
                    <dd>
                      <code>{plan.request.limaHome}</code>
                    </dd>
                  </>
                )}
                {plan.platform === 'darwin' && !!plan.request.cpus && (
                  <>
                    <dt>Resource limits</dt>
                    <dd>
                      {plan.request.cpus} CPU · {plan.request.memoryMiB} MiB
                      memory · {plan.request.diskGiB} GiB storage ·{' '}
                      {plan.request.machines} VMs
                    </dd>
                  </>
                )}
                {(plan.request.hostConfig || plan.request.socket) && (
                  <>
                    <dt>Local service</dt>
                    <dd>
                      <code>
                        {plan.request.hostConfig || plan.request.socket}
                      </code>
                    </dd>
                  </>
                )}
              </dl>
              <h3>Actions</h3>
              <ol className="machines-preparation-actions">
                {plan.actions.map((action, index) => (
                  <li key={`${index}:${action}`}>{action}</li>
                ))}
              </ol>
              <h3>Downloads</h3>
              {plan.downloads.length ? (
                <ul className="machines-preparation-downloads">
                  {plan.downloads.map((download) => (
                    <li key={download.id}>
                      <strong>{download.name}</strong>
                      <span>
                        {download.cached
                          ? 'Verified cached download'
                          : preparationSize(download.bytes)}
                      </span>
                      <a href={download.url} target="_blank" rel="noreferrer">
                        {download.url}
                      </a>
                      <details>
                        <summary>SHA-256</summary>
                        <code>{download.sha256}</code>
                      </details>
                    </li>
                  ))}
                </ul>
              ) : (
                <p>No downloads are required for this plan.</p>
              )}
              {expired && (
                <p className="machines-warning">
                  Review expired. Refresh before preparing.
                </p>
              )}
              <div className="machines-form-actions">
                <Button
                  disabled={
                    !plan.canStart ||
                    busy ||
                    reviewing ||
                    expired ||
                    !!preparation.connectionError
                  }
                  onClick={() => void start()}
                >
                  {startLabel}
                </Button>
                <IconButton
                  icon="refresh"
                  label="Refresh Review"
                  disabled={busy || reviewing}
                  onClick={() => void review()}
                />
                <IconButton
                  icon="back"
                  label="Back"
                  disabled={busy || reviewing}
                  onClick={() => setStage('settings')}
                />
              </div>
            </>
          ) : ready ? (
            <>
              <Checks checks={check.checks} />
              {info?.capacity && (
                <p className="machines-host-capacity">
                  {info.capacity.cpus} CPU · {info.capacity.memoryMiB} MiB
                  memory · {info.capacity.diskGiB} GiB storage ·{' '}
                  {info.capacity.machines} VMs
                </p>
              )}
              {info?.providers
                ?.filter((provider) => !provider.available)
                .map((provider) => (
                  <p className="machines-warning" key={provider.provider}>
                    {provider.reason || `${provider.provider} is unavailable.`}
                  </p>
                ))}
            </>
          ) : (
            <>
              <Checks checks={check.checks} />
              {environment?.runtime?.error && (
                <p className="machines-error">{environment.runtime.error}</p>
              )}
              {info?.providers
                ?.filter((provider) => !provider.available)
                .map((provider) => (
                  <p className="machines-warning" key={provider.provider}>
                    {provider.reason || `${provider.provider} is unavailable.`}
                  </p>
                ))}
              {platform === 'darwin' || platform === 'linux' ? (
                <form
                  className="machines-form"
                  onSubmit={(event) => {
                    event.preventDefault();
                    void review();
                  }}
                >
                  {configured ? (
                    <p className="machines-form-help">
                      The existing configuration and storage will be preserved.
                      Review how to restore the VM connection.
                    </p>
                  ) : (
                    <ol
                      className="machines-preparation-stages"
                      aria-label="Setup steps"
                    >
                      <li aria-current="step">1. Settings</li>
                      <li>2. Review</li>
                      <li>3. Prepare</li>
                    </ol>
                  )}
                  {!configured && platform === 'darwin' && (
                    <fieldset disabled={busy || reviewing}>
                      <legend>Storage and resource limits</legend>
                      <Select
                        label="Image source"
                        value={draft.imageSource}
                        onChange={(event) =>
                          change(
                            'imageSource',
                            event.target
                              .value as PreparationDraft['imageSource'],
                          )
                        }
                      >
                        <option value="official">Official AXV image</option>
                        <option value="bundle">
                          Advanced: local custom bundle
                        </option>
                        <option value="ubuntu-base">
                          Ubuntu base (default; basic VM)
                        </option>
                      </Select>
                      {draft.imageSource === 'bundle' && (
                        <Field
                          label="Custom bundle manifest"
                          value={draft.imageBundle}
                          required
                          spellCheck={false}
                          onChange={(event) =>
                            change('imageBundle', event.target.value)
                          }
                        />
                      )}
                      <p className="machines-form-help">
                        {!check.officialImageAvailable &&
                          'Official AXV image publication is pending. '}
                        The default Ubuntu base provides basic VM management
                        without guest enhancements.
                      </p>
                      <Field
                        label="VM storage directory"
                        value={draft.storageRoot}
                        required
                        spellCheck={false}
                        onChange={(event) =>
                          change('storageRoot', event.target.value)
                        }
                      />
                      <Field
                        label="Lima state directory"
                        value={draft.limaHome}
                        required
                        spellCheck={false}
                        onChange={(event) =>
                          change('limaHome', event.target.value)
                        }
                      />
                      <p className="machines-form-help">
                        Use absolute paths on this host.
                      </p>
                      <div className="machines-resource-fields">
                        <Field
                          label="CPU budget"
                          type="number"
                          min={1}
                          max={4096}
                          value={draft.cpus}
                          required
                          onChange={(event) =>
                            change('cpus', event.target.value)
                          }
                        />
                        <Field
                          label="Memory budget (MiB)"
                          type="number"
                          min={256}
                          value={draft.memoryMiB}
                          required
                          onChange={(event) =>
                            change('memoryMiB', event.target.value)
                          }
                        />
                        <Field
                          label="Disk budget (GiB)"
                          type="number"
                          min={1}
                          value={draft.diskGiB}
                          required
                          onChange={(event) =>
                            change('diskGiB', event.target.value)
                          }
                        />
                        <Field
                          label="VM limit"
                          type="number"
                          min={1}
                          max={100}
                          value={draft.machines}
                          required
                          onChange={(event) =>
                            change('machines', event.target.value)
                          }
                        />
                      </div>
                      <p className="machines-form-help">
                        Allocation limits; leave capacity for the host and image
                        copies.
                      </p>
                    </fieldset>
                  )}
                  {!configured && platform === 'linux' && (
                    <fieldset disabled={busy || reviewing}>
                      <legend>Local Firecracker service</legend>
                      <p className="machines-form-help">
                        Connect to a local Firecracker/KVM service set up by an
                        administrator. Socket access is required.
                      </p>
                      <Select
                        label="Read connection from"
                        value={draft.connection}
                        onChange={(event) =>
                          change(
                            'connection',
                            event.target.value === 'socket'
                              ? 'socket'
                              : 'configuration',
                          )
                        }
                      >
                        <option value="configuration">
                          Local configuration file
                        </option>
                        <option value="socket">Local Unix socket</option>
                      </Select>
                      {draft.connection === 'configuration' ? (
                        <Field
                          label="Local service configuration"
                          value={draft.hostConfig}
                          required
                          spellCheck={false}
                          onChange={(event) =>
                            change('hostConfig', event.target.value)
                          }
                        />
                      ) : (
                        <Field
                          label="Local service socket"
                          value={draft.socket}
                          required
                          spellCheck={false}
                          onChange={(event) =>
                            change('socket', event.target.value)
                          }
                        />
                      )}
                    </fieldset>
                  )}
                  {draftError && (
                    <p className="machines-warning">{draftError}</p>
                  )}
                  {failed && (
                    <p className="machines-form-help">
                      Previous preparation is kept. Review creates a new plan
                      for the current environment.
                    </p>
                  )}
                  {job?.status === 'cancelled' && (
                    <p className="machines-form-help">
                      Preparation cancelled. Verified files are kept for the
                      next plan.
                    </p>
                  )}
                  <div className="machines-form-actions">
                    <Button
                      type="submit"
                      disabled={
                        busy ||
                        reviewing ||
                        !!draftError ||
                        !!preparation.connectionError
                      }
                    >
                      {reviewing ? 'Checking plan…' : 'Review'}
                    </Button>
                  </div>
                </form>
              ) : (
                <p>VM hosting requires macOS or Linux.</p>
              )}
            </>
          )}
          <details className="machines-technical">
            <summary>Technical details</summary>
            <dl className="info-list machines-details">
              <dt>Configuration source</dt>
              <dd>{check.configurationSource || 'Not configured'}</dd>
              <dt>Shared download cache</dt>
              <dd>
                <code>{check.cacheHome}</code>
              </dd>
              <dt>Host identity</dt>
              <dd>
                <code>
                  {info?.hostId ||
                    environment?.runtime?.hostId ||
                    'Unavailable'}
                </code>
              </dd>
              {environment?.logPath && (
                <>
                  <dt>Desktop log</dt>
                  <dd>
                    <code>{environment.logPath}</code>
                  </dd>
                  <dt>Recent service logs</dt>
                  <dd>
                    <code>axv daemon logs</code>
                  </dd>
                </>
              )}
              <dt>Profile</dt>
              <dd>
                <code>{environment?.profileHome || 'Unavailable'}</code>
              </dd>
              <dt>Configuration</dt>
              <dd>
                <code>{environment?.configPath || 'Unavailable'}</code>
              </dd>
              {platform === 'darwin' && (
                <>
                  <dt>Required Lima</dt>
                  <dd>{check.requiredLimaVersion}</dd>
                </>
              )}
              {info?.providers?.map((provider) => (
                <div
                  key={provider.provider}
                  className="machines-provider-detail"
                >
                  <dt>{provider.provider}</dt>
                  <dd>
                    {provider.available ? 'Available' : 'Unavailable'}
                    {provider.version && ` · ${provider.version}`}
                  </dd>
                </div>
              ))}
              <dt>Runtime</dt>
              <dd>
                {environment?.runtime
                  ? humanize(environment.runtime.state)
                  : configured
                    ? 'Configured'
                    : 'Not configured'}
              </dd>
            </dl>
          </details>
        </>
      )}
    </section>
  );
}
