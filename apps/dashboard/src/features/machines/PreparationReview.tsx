import { Button, IconButton } from '@cofy-x/axv-nextstep';
import type { PreparationPlan } from './api';
import { preparationSize } from './preparation-form';
import { EnvironmentChecks } from './HostOverview';

export interface PreparationReviewProps {
  plan: PreparationPlan;
  expired: boolean;
  busy: boolean;
  reviewing: boolean;
  hasConnectionError: boolean;
  startLabel: string;
  onStart: () => void;
  onRefresh: () => void;
  onBack: () => void;
}

export function PreparationReview({
  plan,
  expired,
  busy,
  reviewing,
  hasConnectionError,
  startLabel,
  onStart,
  onRefresh,
  onBack,
}: PreparationReviewProps) {
  return (
    <>
      <ol className="machines-preparation-stages" aria-label="Setup steps">
        <li>1. Settings</li>
        <li aria-current="step">2. Review</li>
        <li>3. Prepare</li>
      </ol>
      <EnvironmentChecks checks={plan.checks} />
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
              {plan.request.cpus} CPU · {plan.request.memoryMiB} MiB memory ·{' '}
              {plan.request.diskGiB} GiB storage · {plan.request.machines} VMs
            </dd>
          </>
        )}
        {(plan.request.hostConfig || plan.request.socket) && (
          <>
            <dt>Local service</dt>
            <dd>
              <code>{plan.request.hostConfig || plan.request.socket}</code>
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
            !plan.canStart || busy || reviewing || expired || hasConnectionError
          }
          onClick={onStart}
        >
          {startLabel}
        </Button>
        <IconButton
          icon="refresh"
          label="Refresh Review"
          disabled={busy || reviewing}
          onClick={onRefresh}
        />
        <IconButton
          icon="back"
          label="Back"
          disabled={busy || reviewing}
          onClick={onBack}
        />
      </div>
    </>
  );
}
