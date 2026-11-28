import { Button, IconButton } from '@cofy-x/axv-nextstep';
import { cloudflareURL } from './api';
import { cloudflarePresentation } from './presentation';
import type { CloudflareSnapshot } from './types';

export interface CopyStatus {
  message: string;
  error: boolean;
}

export type AccessChange = 'enable' | 'disable';

export function CloudflareDetails({
  snapshot,
  interactive = true,
  changing,
  guideExpanded = false,
  copyStatus = { message: '', error: false },
  onSetEnabled,
  onSetupGuide,
  onViewDiagnostics,
  onRefresh,
  refreshing = false,
  onCopy,
}: {
  snapshot: CloudflareSnapshot;
  interactive?: boolean;
  changing?: AccessChange;
  guideExpanded?: boolean;
  copyStatus?: CopyStatus;
  onSetEnabled?: (enabled: boolean) => void;
  onSetupGuide?: (trigger: HTMLButtonElement) => void;
  onViewDiagnostics?: () => void;
  onRefresh?: () => void;
  refreshing?: boolean;
  onCopy: (value: string) => void;
}) {
  const { info, error } = snapshot;
  const presentation = cloudflarePresentation(snapshot);
  const status = info?.cloudflare;
  const url = interactive && !error ? cloudflareURL(status) : undefined;
  const refreshButton = onRefresh && (
    <IconButton
      icon="refresh"
      label="Refresh"
      disabled={!interactive || refreshing || !!changing}
      onClick={onRefresh}
    />
  );
  if (!info || !status)
    return (
      <section className="cloudflare-card">
        <div className="cloudflare-heading">
          <p className="cloudflare-empty-line">
            {snapshot.loading
              ? 'Checking…'
              : 'Status unavailable. Refresh to check.'}
          </p>
          {refreshButton}
        </div>
      </section>
    );
  const needsSetup = !error && !status.configured && status.state !== 'error';
  const retry =
    status.enabled &&
    status.configured &&
    (status.state === 'error' || status.state === 'stopped');
  const dotTone = changing ? 'pending' : presentation.tone;
  return (
    <section
      className="cloudflare-card"
      aria-labelledby="cloudflare-access-title"
    >
      <div className="cloudflare-heading">
        <h2 id="cloudflare-access-title">Desktop access</h2>
        <div className="cloudflare-status-actions">
          <p
            className={`cloudflare-state is-${dotTone}`}
            role="status"
            aria-atomic="true"
          >
            <span
              className={`cloudflare-status-dot is-${dotTone}`}
              aria-hidden="true"
            />
            {changing === 'enable'
              ? 'Enabling…'
              : changing === 'disable'
                ? 'Disabling…'
                : presentation.label}
          </p>
          {refreshButton}
        </div>
      </div>
      <div className="cloudflare-card-body">
        {changing ? (
          <p>
            {changing === 'enable'
              ? 'Starting AXV’s tunnel entrypoint.'
              : 'Stopping AXV’s tunnel entrypoint.'}
          </p>
        ) : error ? (
          <p>Refresh to check the current entrypoint state.</p>
        ) : status.state === 'error' ? (
          <p>
            {info.local
              ? 'AXV’s entrypoint needs attention. View diagnostics for details.'
              : status.message || 'The AXV entrypoint needs attention.'}
          </p>
        ) : needsSetup ? (
          <p>
            Open Setup to connect an existing Access application before enabling
            desktop access.
          </p>
        ) : status.state === 'stopped' ? (
          <p>
            {info.local
              ? 'AXV’s entrypoint has stopped. View diagnostics for details.'
              : status.message || 'The AXV entrypoint has stopped.'}
          </p>
        ) : status.state === 'listening' ? (
          <p>Public access has not been verified.</p>
        ) : status.state === 'starting' ? (
          <p>Starting the AXV entrypoint.</p>
        ) : (
          <p>Cloudflare access is configured but not enabled.</p>
        )}
      </div>
      {info.local &&
        onViewDiagnostics &&
        (status.state === 'error' || status.state === 'stopped') && (
          <div className="cloudflare-inline-action">
            <Button
              disabled={!interactive || !!changing}
              onClick={onViewDiagnostics}
              aria-controls="cloudflare-diagnostics"
            >
              View diagnostics
            </Button>
          </div>
        )}
      {url && (
        <div className="cloudflare-url-card">
          <dl className="info-list">
            <dt>Desktop URL</dt>
            <dd>
              <div className="cloudflare-url">
                <a href={url} target="_blank" rel="noopener noreferrer">
                  {url}
                </a>
                <IconButton
                  icon="copy"
                  label="Copy Desktop URL"
                  onClick={() => onCopy(url)}
                />
              </div>
            </dd>
          </dl>
        </div>
      )}
      {copyStatus.message && (
        <p
          className={`cloudflare-copy-status${copyStatus.error ? ' app-error' : ''}`}
          role={copyStatus.error ? 'alert' : 'status'}
        >
          {copyStatus.message}
        </p>
      )}
      {info.local && onSetEnabled && (
        <div className="cloudflare-card-footer">
          <div className="cloudflare-actions">
            {onSetupGuide && (
              <Button
                primary={needsSetup}
                disabled={!interactive || !!changing}
                aria-expanded={guideExpanded}
                aria-controls="cloudflare-setup-page"
                onClick={(event) => onSetupGuide(event.currentTarget)}
              >
                Open setup
              </Button>
            )}
            {retry && (
              <Button
                primary
                disabled={!interactive || !!error || !!changing}
                onClick={() => onSetEnabled(true)}
              >
                Retry
              </Button>
            )}
            <Button
              primary={!status.enabled && status.configured}
              disabled={
                !interactive ||
                !!error ||
                !!changing ||
                (!status.enabled && !status.configured)
              }
              onClick={() => onSetEnabled(!status.enabled)}
            >
              {status.enabled ? 'Disable' : 'Enable'}
            </Button>
          </div>
          {retry && (
            <p className="fine-print">
              Retry restarts only AXV’s entrypoint, not your tunnel connector.
            </p>
          )}
        </div>
      )}
      {!info.local && (
        <p className="fine-print cloudflare-remote-note">
          Manage Cloudflare on the computer running AXV.
        </p>
      )}
    </section>
  );
}
