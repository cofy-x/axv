import { useEffect, useRef, useState } from 'react';
import { Button, Field, IconButton, Select } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { PreparationPlan, PreparationRequirement } from './api';
import {
  defaultFirecrackerSocket,
  preparationDraft,
  preparationDraftError,
  preparationRequest,
  preparationSize,
} from './preparation-form';
import type { PreparationDraft } from './preparation-form';
import { preparationRunning } from './preparation-store';
import { operationError } from './operation-requests';
import { humanize, linuxVMSetupGuide } from './presentation';
import type { useCompute } from './useCompute';
import { openMachineDiagnostics } from './MachineDiagnostics';
import { HostServiceGuidance } from './HostServiceGuidance';

const serviceStatusLabel = {
  missing: 'Firecracker service not found',
  permission_denied: 'Firecracker access denied',
  unavailable: 'Firecracker service unavailable',
  ready: 'Firecracker service detected',
};

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
  desktop,
  onShowMachines,
}: {
  data: ReturnType<typeof useCompute>;
  desktop: DesktopAPI;
  onShowMachines: () => void;
}) {
  const { environment, info, preparation, prepare } = data;
  const { check, job } = preparation;
  const [draft, setDraft] = useState<PreparationDraft>(() =>
    preparationDraft({}),
  );
  const draftSource = useRef('');
  const [connectionSelected, setConnectionSelected] = useState(false);
  const [connectionEdits, setConnectionEdits] = useState<PreparationDraft>();
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
  const runtime = environment?.runtime;
  const launcherConnection =
    check?.configurationSource === 'environment' ||
    environment?.configurationSource === 'environment' ||
    (runtime?.mode === 'external' && runtime.readOnly);
  const launcherReady =
    launcherConnection && runtime?.state === 'ready' && !data.error;
  const configured =
    launcherConnection ||
    (check?.configured ?? environment?.configured ?? info?.configured ?? false);
  const linux = platform === 'linux';
  const linuxSetup = linux && !configured;
  const service = check?.localService;
  const useDetectedSocket = linuxSetup && !connectionSelected;
  const connectionDraft: PreparationDraft = useDetectedSocket
    ? {
        ...draft,
        connection: 'socket',
        socket: service?.socket || check?.defaults.socket || draft.socket,
      }
    : draft;
  const socketConnection = connectionDraft.connection === 'socket';
  const connectionPath = socketConnection
    ? connectionDraft.socket
    : connectionDraft.hostConfig;
  const connectionInput = connectionEdits ?? connectionDraft;
  const serviceCheck =
    socketConnection && service?.socket === connectionDraft.socket
      ? service
      : undefined;
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
  const showHostOverview = launcherConnection
    ? launcherReady
    : !!check && ready && !showJob && !showReview;
  const draftError = preparationDraftError(
    connectionInput,
    platform || '',
    configured,
  );
  const expired = !!plan && Date.parse(plan.expiresAt) <= Date.now();
  const connectionPlan = showReview ? plan : undefined;
  const canConnect = !connectionEdits && !!connectionPlan?.canStart && !expired;
  const directConnect =
    linuxSetup &&
    !connectionEdits &&
    !check?.lockedReason &&
    socketConnection &&
    connectionPath === defaultFirecrackerSocket &&
    serviceCheck?.status === 'ready';
  const connectionFailures =
    connectionPlan?.checks.filter((check) => check.status === 'blocked') ?? [];
  const serviceFailure = connectionFailures.find(
    (check) => check.id === 'local_service',
  );
  const detectedAfterMissingCheck =
    serviceFailure?.code === 'missing' && serviceCheck?.status === 'ready';
  const showHostGuidance =
    !connectionEdits &&
    !directConnect &&
    !preparation.connectionError &&
    !detectedAfterMissingCheck &&
    socketConnection &&
    connectionPath === defaultFirecrackerSocket &&
    (connectionPlan
      ? serviceFailure?.code === 'missing'
      : serviceCheck?.status === 'missing' || !!serviceCheck?.systemdState);
  const remainingFailures =
    showHostGuidance || detectedAfterMissingCheck
      ? connectionFailures.filter((check) => check !== serviceFailure)
      : connectionFailures;
  const simpleConnect =
    directConnect &&
    !canConnect &&
    !remainingFailures.length &&
    !preparation.connectionError;
  const connectsService = linux || environment?.runtime?.mode === 'external';
  const startLabel = connectsService
    ? 'Connect'
    : configured
      ? 'Start Service'
      : 'Prepare';
  const title = launcherConnection
    ? launcherReady
      ? 'VM service connected'
      : !data.error && (!runtime || runtime.state === 'starting')
        ? 'Connecting VM service'
        : 'VM service needs attention'
    : !check
      ? preparation.loading
        ? 'Checking VM availability'
        : 'VM environment unavailable'
      : running
        ? linux
          ? 'Connecting VM service'
          : 'Preparing virtual machines'
        : failed && showJob
          ? linux
            ? 'VM service connection failed'
            : job.status === 'interrupted'
              ? 'Preparation interrupted'
              : 'Preparation failed'
          : job?.status === 'cancelled' && showJob
            ? linux
              ? 'Connection cancelled'
              : 'Preparation cancelled'
            : ready
              ? 'VM service connected'
              : environment?.runtime?.state === 'starting'
                ? 'Starting VM service'
                : configured
                  ? 'VM service needs attention'
                  : linuxSetup
                    ? simpleConnect
                      ? 'Firecracker ready to connect'
                      : 'Connect VM service'
                    : showReview
                      ? 'Review VM preparation'
                      : 'Enable virtual machines';

  useEffect(() => {
    if (!check) return;
    const source = check.platform;
    if (draftSource.current === source) return;
    draftSource.current = source;
    setDraft(preparationDraft(check.defaults));
    setConnectionSelected(false);
    setConnectionEdits(undefined);
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

  async function review(connectAfterCheck = false) {
    if (!check || draftError || busy || preparation.connectionError) return;
    reviewRequest.current?.abort();
    const controller = new AbortController();
    reviewRequest.current = controller;
    setReviewing(true);
    setError('');
    if (connectionEdits || useDetectedSocket) {
      setDraft(connectionInput);
      setConnectionSelected(true);
    }
    if (connectionEdits) {
      setConnectionEdits(undefined);
      setPlan(undefined);
      setStage('settings');
    }
    try {
      const result = await prepare.review(
        preparationRequest(
          connectionInput,
          check.platform,
          configured,
          check.defaults,
        ),
        controller.signal,
      );
      if (controller.signal.aborted) return;
      setPlan(result);
      setStage('review');
      if (connectAfterCheck && result.canStart) {
        const latest = prepare.getSnapshot();
        if (latest.check?.instanceId !== check.instanceId) {
          setPlan(undefined);
          setStage('settings');
          setError(
            'The desktop changed during connection. Check the VM service again.',
          );
          return;
        }
        if (!latest.check.configured && !latest.connectionError)
          await start(result);
      } else {
        heading.current?.focus();
      }
    } catch (failure) {
      if (!controller.signal.aborted) setError(operationError(failure));
    } finally {
      if (reviewRequest.current === controller) {
        reviewRequest.current = null;
        setReviewing(false);
      }
    }
  }

  async function start(selectedPlan = plan) {
    const latest = prepare.getSnapshot();
    if (
      !selectedPlan?.canStart ||
      Date.parse(selectedPlan.expiresAt) <= Date.now() ||
      latest.mutation ||
      latest.pending ||
      preparationRunning(latest.job) ||
      (selectedPlan === plan && reviewing)
    )
      return;
    await prepare.start(selectedPlan);
    if (prepare.getSnapshot().job?.planId === selectedPlan.id)
      setStage('progress');
  }

  function showDiagnostics() {
    const defaults = check?.defaults;
    const connection = launcherConnection
      ? undefined
      : linuxSetup
        ? {
            label: socketConnection ? 'Socket' : 'Host configuration',
            value: connectionPath,
          }
        : defaults?.socket
          ? { label: 'Socket', value: defaults.socket }
          : defaults?.hostConfig
            ? { label: 'Host configuration', value: defaults.hostConfig }
            : undefined;
    const failedCheck = connectionFailures[0];
    const checked = connectionPlan?.canStart && !expired;
    const status =
      linuxSetup && !showJob && !simpleConnect
        ? checked
          ? 'Connection verified'
          : failedCheck?.label ||
            (serviceCheck && serviceStatusLabel[serviceCheck.status]) ||
            title
        : title;
    const detail =
      linuxSetup && !showJob && !checked
        ? failedCheck?.detail || serviceCheck?.detail
        : undefined;
    void openMachineDiagnostics(desktop, data, { status, detail, connection });
  }

  const capacity = info?.capacity;
  const hostCapacity = capacity && (
    <div className="machines-host-limits">
      <h3>Capacity limits</h3>
      <dl className="machines-host-capacity">
        <div>
          <dt>CPUs</dt>
          <dd>{capacity.cpus}</dd>
        </div>
        <div>
          <dt>Memory</dt>
          <dd>
            {capacity.memoryMiB % 1024 === 0
              ? `${capacity.memoryMiB / 1024} GiB`
              : `${capacity.memoryMiB} MiB`}
          </dd>
        </div>
        <div>
          <dt>Storage</dt>
          <dd>{capacity.diskGiB} GiB</dd>
        </div>
        <div>
          <dt>VM slots</dt>
          <dd>
            {data.machines.length} of {capacity.machines} used
          </dd>
        </div>
      </dl>
    </div>
  );
  const providerWarnings = info?.providers
    ?.filter(
      (provider) =>
        !provider.available &&
        (!runtime?.error || provider.reason !== runtime.error),
    )
    .map((provider) => (
      <p className="machines-warning" key={provider.provider}>
        {provider.reason || `${provider.provider} is unavailable.`}
      </p>
    ));

  return (
    <section className="machines-environment" aria-label="VM host">
      <header
        className="machines-section-heading machines-host-heading"
        data-connected={showHostOverview}
      >
        <div>
          {!launcherConnection && !simpleConnect && !ready && (
            <span className="machines-eyebrow">
              {platform === 'darwin'
                ? 'macOS'
                : platform === 'linux'
                  ? 'Linux'
                  : humanize(platform || 'Local host')}
              {check?.arch && ` · ${check.arch}`}
            </span>
          )}
          <h2 ref={heading} tabIndex={-1}>
            {title}
          </h2>
        </div>
        {(!check || !linuxSetup || showJob) && (
          <Button onClick={showDiagnostics}>Diagnostics</Button>
        )}
      </header>
      {environment?.loggingError && (
        <p className="machines-error" role="alert">
          Desktop logging needs attention. {environment.loggingError}{' '}
          {environment.logPath && <code>{environment.logPath}</code>} Check the
          profile’s disk space and permissions.
        </p>
      )}
      {launcherConnection ? (
        <>
          {launcherReady && hostCapacity}
          {runtime?.error && runtime.error !== data.error && (
            <p className="machines-error" role="alert">
              {runtime.error}
            </p>
          )}
          {launcherReady && providerWarnings}
        </>
      ) : !check ? (
        <>
          <p role="status">
            {preparation.loading
              ? 'Checking the VM environment…'
              : 'Environment checks could not be completed.'}
          </p>
          {preparation.connectionError && (
            <>
              <p className="machines-error" role="alert">
                {preparation.connectionError}
              </p>
              <Button disabled={preparation.refreshing} onClick={data.refresh}>
                Check again
              </Button>
            </>
          )}
        </>
      ) : (
        <>
          {preparation.connectionError && (
            <p className="machines-error" role="alert">
              Connection interrupted: {preparation.connectionError}{' '}
              {job && 'The preparation job remains on the AXV machine.'}
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
              {!linux && (
                <ol
                  className="machines-preparation-stages"
                  aria-label="Setup steps"
                >
                  <li>1. Settings</li>
                  <li>2. Review</li>
                  <li aria-current="step">3. Prepare</li>
                </ol>
              )}
              <p className="machines-preparation-phase" role="status">
                {humanize(job.status)} · {humanize(job.phase)} · Attempt{' '}
                {job.attempt}
              </p>
              <ol
                className="machines-preparation-progress"
                aria-label={
                  linux ? 'Connection progress' : 'Preparation progress'
                }
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
              {!linux && (
                <p className="machines-form-help">
                  {job.committed
                    ? 'Configuration saved. Remaining work starts and verifies the VM service.'
                    : running
                      ? 'Preparation continues after closing this window. Cancel is available until configuration is saved.'
                      : job.status === 'cancelled'
                        ? 'Preparation cancelled. Verified downloads are kept for the next attempt.'
                        : 'Completed files are kept. Retry continues this job with its reviewed settings.'}
                </p>
              )}
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
                  {linux
                    ? 'Connection saved. Checking VM service readiness…'
                    : 'Preparation completed. Checking VM service readiness…'}
                </p>
              )}
              <details className="machines-technical">
                <summary>
                  {linux ? 'Connection details' : 'Reviewed preparation'}
                </summary>
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
          ) : linuxSetup ? (
            <form
              className="machines-form"
              onSubmit={(event) => {
                event.preventDefault();
                if (preparation.connectionError) {
                  data.refresh();
                  return;
                }
                void (canConnect ? start() : review(directConnect));
              }}
            >
              {showHostGuidance && (
                <HostServiceGuidance
                  state={serviceCheck?.systemdState}
                  arch={check.arch}
                  executablePath={
                    data.error ? undefined : environment?.executablePath
                  }
                  desktop={desktop}
                />
              )}
              {!simpleConnect && (
                <div className="machines-environment-facts">
                  {connectionEdits ? (
                    <div className="machines-connection-fields">
                      <Select
                        label="Read connection from"
                        value={connectionEdits.connection}
                        disabled={busy || reviewing}
                        autoFocus
                        onChange={(event) =>
                          setConnectionEdits({
                            ...connectionEdits,
                            connection:
                              event.target.value === 'socket'
                                ? 'socket'
                                : 'configuration',
                          })
                        }
                      >
                        <option value="socket">Local Unix socket</option>
                        <option value="configuration">
                          Local configuration file
                        </option>
                      </Select>
                      {connectionEdits.connection === 'socket' ? (
                        <Field
                          label="Local service socket"
                          value={connectionEdits.socket}
                          required
                          spellCheck={false}
                          disabled={busy || reviewing}
                          onChange={(event) =>
                            setConnectionEdits({
                              ...connectionEdits,
                              socket: event.target.value,
                            })
                          }
                        />
                      ) : (
                        <Field
                          label="Local service configuration"
                          value={connectionEdits.hostConfig}
                          required
                          spellCheck={false}
                          disabled={busy || reviewing}
                          onChange={(event) =>
                            setConnectionEdits({
                              ...connectionEdits,
                              hostConfig: event.target.value,
                            })
                          }
                        />
                      )}
                    </div>
                  ) : (
                    <dl className="info-list machines-details">
                      <dt>
                        {socketConnection ? 'Socket' : 'Host configuration'}
                      </dt>
                      <dd>
                        <code>{connectionPath}</code>
                      </dd>
                      {!socketConnection && connectionPlan?.request.socket && (
                        <>
                          <dt>Resolved socket</dt>
                          <dd>
                            <code>{connectionPlan.request.socket}</code>
                          </dd>
                        </>
                      )}
                    </dl>
                  )}
                </div>
              )}
              {!simpleConnect &&
                !connectionEdits &&
                (connectionPlan && !connectionPlan.canStart ? (
                  <div role="status">
                    {showHostGuidance && (
                      <p>Connection checked. The default socket is missing.</p>
                    )}
                    {remainingFailures.map((check) => (
                      <div key={check.id}>
                        <strong>{check.label}</strong>
                        <p className="machines-form-help">{check.detail}</p>
                      </div>
                    ))}
                  </div>
                ) : expired ? (
                  <p className="machines-warning" role="status">
                    Connection check expired. Check again to connect.
                  </p>
                ) : canConnect ? (
                  <p role="status">Connection verified</p>
                ) : null)}
              {!simpleConnect &&
                !connectionEdits &&
                serviceCheck &&
                (showHostGuidance
                  ? serviceCheck.status === 'permission_denied'
                  : !connectionPlan) && (
                  <div role="status">
                    <strong>{serviceStatusLabel[serviceCheck.status]}</strong>
                    <p className="machines-form-help">{serviceCheck.detail}</p>
                  </div>
                )}
              {!showHostGuidance &&
                !connectionEdits &&
                !canConnect &&
                serviceCheck?.status !== 'ready' && (
                  <p className="machines-form-help">
                    <a
                      href={linuxVMSetupGuide}
                      target="_blank"
                      rel="noreferrer"
                    >
                      Linux VM setup guide
                    </a>
                  </p>
                )}
              {draftError && <p className="machines-warning">{draftError}</p>}
              <div className="machines-form-actions">
                <Button
                  type="submit"
                  primary={!showHostGuidance}
                  disabled={
                    busy ||
                    reviewing ||
                    (preparation.connectionError
                      ? preparation.refreshing
                      : !!draftError)
                  }
                >
                  {preparation.connectionError
                    ? 'Check again'
                    : reviewing
                      ? 'Checking connection…'
                      : canConnect || directConnect
                        ? 'Connect'
                        : 'Check connection'}
                </Button>
                <Button
                  disabled={busy || reviewing}
                  onClick={() =>
                    setConnectionEdits(
                      connectionEdits ? undefined : connectionDraft,
                    )
                  }
                >
                  {connectionEdits ? 'Cancel' : 'Edit connection'}
                </Button>
                <Button onClick={showDiagnostics}>Diagnostics</Button>
              </div>
            </form>
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
              {hostCapacity}
              {providerWarnings}
              <Checks checks={check.checks} />
            </>
          ) : (
            <>
              <Checks checks={check.checks} />
              {environment?.runtime?.error && (
                <p className="machines-error">{environment.runtime.error}</p>
              )}
              {providerWarnings}
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
        </>
      )}
    </section>
  );
}
