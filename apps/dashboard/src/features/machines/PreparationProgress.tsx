import { Button } from '@cofy-x/axv-nextstep';
import type { PreparationJob } from './api';
import { preparationSize } from './preparation-form';
import { humanize } from './presentation';

export interface PreparationProgressProps {
  job: PreparationJob;
  linux: boolean;
  running: boolean;
  failed: boolean;
  busy: boolean;
  ready: boolean;
  configured: boolean;
  hasMutation: boolean;
  hasPending: boolean;
  hasConnectionError: boolean;
  onRetry: () => void;
  onCancel: () => void;
  onSettings: () => void;
  onShowMachines: () => void;
}

export function PreparationProgress({
  job,
  linux,
  running,
  failed,
  busy,
  ready,
  configured,
  hasMutation,
  hasPending,
  hasConnectionError,
  onRetry,
  onCancel,
  onSettings,
  onShowMachines,
}: PreparationProgressProps) {
  return (
    <>
      {!linux && (
        <ol className="machines-preparation-stages" aria-label="Setup steps">
          <li>1. Settings</li>
          <li>2. Review</li>
          <li aria-current="step">3. Prepare</li>
        </ol>
      )}
      <p className="machines-preparation-phase" role="status">
        {humanize(job.status)} · {humanize(job.phase)} · Attempt {job.attempt}
      </p>
      <ol
        className="machines-preparation-progress"
        aria-label={linux ? 'Connection progress' : 'Preparation progress'}
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
                value={step.bytesTotal ? step.bytesDone || 0 : undefined}
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
          <Button disabled={busy || hasConnectionError} onClick={onRetry}>
            Retry
          </Button>
        )}
        {running && (
          <Button
            disabled={
              hasMutation || hasPending || !job.canCancel || job.committed
            }
            onClick={onCancel}
          >
            Cancel
          </Button>
        )}
        {(job.status === 'cancelled' || failed) && (
          <Button disabled={busy} onClick={onSettings}>
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
          {job.plan.platform === 'darwin' && job.plan.request.storageRoot && (
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
  );
}
