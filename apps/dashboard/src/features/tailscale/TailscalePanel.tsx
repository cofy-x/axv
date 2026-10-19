import { useEffect, useState } from 'react';
import { IconButton } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { canAuthorize, connectionAPI, readyURL } from './api';
import { useConnectionStatus } from './useConnectionStatus';
import { connectionPresentation } from './presentation';
import type { ConnectionSnapshot } from './types';

interface Authorization {
  loading?: boolean;
  url?: string;
  error?: string;
}

function useAuthorization(
  local: boolean,
  eligible: boolean,
  revision: number,
  updatedAt?: number,
) {
  const [authorization, setAuthorization] = useState<Authorization>({});
  useEffect(() => {
    if (!local || !eligible) {
      setAuthorization({});
      return;
    }
    const controller = new AbortController();
    const signal = AbortSignal.any([
      controller.signal,
      AbortSignal.timeout(8000),
    ]);
    setAuthorization((previous) => ({
      ...previous,
      loading: true,
      error: undefined,
    }));
    void connectionAPI
      .authorizationURL({ local, remote: { state: 'needs-login' } }, signal)
      .then((url) => {
        if (!controller.signal.aborted) {
          signal.throwIfAborted();
          setAuthorization({ url });
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setAuthorization({
            error:
              error instanceof Error
                ? error.message
                : 'Could not load authorization. Refresh to retry.',
          });
      });
    return () => {
      controller.abort();
    };
  }, [local, eligible, revision, updatedAt]);
  // Ineligibility hides the capability immediately, before its clearing effect.
  return local && eligible ? authorization : {};
}

function Address({
  label,
  value,
  onCopy,
}: {
  label: string;
  value: string;
  onCopy: (label: string, value: string) => void;
}) {
  return (
    <div className="tailscale-address">
      <code>{value}</code>
      <IconButton
        icon="copy"
        label={`Copy ${label}`}
        onClick={() => onCopy(label, value)}
      />
    </div>
  );
}

export function TailscaleDetails({
  snapshot,
  authorization = {},
  onCopy,
}: {
  snapshot: ConnectionSnapshot;
  authorization?: Authorization;
  onCopy: (label: string, value: string) => void;
}) {
  const { info, error } = snapshot;
  const status = connectionPresentation(snapshot);
  const url = !error ? readyURL(info) : undefined;
  const authorize = !error && canAuthorize(info);
  if (!info)
    return (
      <p>
        {snapshot.loading
          ? 'Checking connections…'
          : 'Connection status is unavailable. Refresh to retry.'}
      </p>
    );
  return (
    <>
      <section
        aria-labelledby="tailscale-axv-title"
        className="tailscale-section"
      >
        <h2 id="tailscale-axv-title">AXV Remote Access</h2>
        <p className="tailscale-state">
          <span
            className={`tailscale-status-dot is-${status.remoteTone}`}
            aria-hidden="true"
          />
          {status.remote}
        </p>
        {info.remote.message && <p>{info.remote.message}</p>}
        {info.remote.state === 'disabled' && (
          <p>
            Enable remote access in the AXV configuration and restart AXV to use
            its private desktop address.
          </p>
        )}
        {info.remote.state === 'starting' && (
          <p>AXV is connecting its private Tailscale node.</p>
        )}
        {info.remote.state === 'stopped' && (
          <p>
            The AXV remote connection has stopped. Restart AXV to reconnect.
          </p>
        )}
        {info.remote.state === 'needs-https' && (
          <p>
            Enable MagicDNS and HTTPS certificates in{' '}
            <a
              href="https://login.tailscale.com/admin/dns"
              target="_blank"
              rel="noopener noreferrer"
            >
              Tailscale DNS settings
            </a>
            . AXV will continue when they are available.
          </p>
        )}
        {info.remote.state === 'needs-login' && (
          <>
            <p>
              {info.local
                ? 'Authorize the AXV node with your Tailscale account.'
                : 'Complete AXV authorization on the computer running AXV.'}
            </p>
            {authorize && authorization.loading && (
              <p role="status">Loading authorization…</p>
            )}
            {authorize && authorization.error && (
              <p className="app-error" role="alert">
                {authorization.error}
              </p>
            )}
            {authorize && authorization.url && (
              <a
                className="ns-button tailscale-authorize"
                href={authorization.url}
                target="_blank"
                rel="noopener noreferrer"
                referrerPolicy="no-referrer"
              >
                Authorize
              </a>
            )}
          </>
        )}
        {url && (
          <dl className="info-list">
            <dt>HTTPS URL</dt>
            <dd>
              <div className="tailscale-url">
                <a href={url} target="_blank" rel="noopener noreferrer">
                  {url}
                </a>
                <IconButton
                  icon="copy"
                  label="Copy HTTPS URL"
                  onClick={() => onCopy('HTTPS URL', url)}
                />
              </div>
            </dd>
            <dt>Hostname</dt>
            <dd>
              <code>{new URL(url).hostname}</code>
            </dd>
          </dl>
        )}
        <p className="fine-print">
          AXV uses its own Tailscale node. The system Tailscale connection is
          independent.
        </p>
      </section>
      <section
        aria-labelledby="tailscale-host-title"
        className="tailscale-section"
      >
        <h2 id="tailscale-host-title">System Tailscale</h2>
        <p className="tailscale-state">
          <span
            className={`tailscale-status-dot is-${status.hostTone}`}
            aria-hidden="true"
          />
          {status.host}
        </p>
        {info.host.message && <p>{info.host.message}</p>}
        <dl className="info-list">
          {info.host.hostname && (
            <>
              <dt>Hostname</dt>
              <dd>
                <code>{info.host.hostname}</code>
              </dd>
            </>
          )}
          {info.host.backendState && (
            <>
              <dt>Daemon state</dt>
              <dd>{info.host.backendState}</dd>
            </>
          )}
          {info.host.ips?.map((ip) => (
            <AddressRow key={ip} ip={ip} onCopy={onCopy} />
          ))}
        </dl>
        <p className="fine-print">
          Detected only. Manage the system service with your operating system or
          the Tailscale app.
        </p>
      </section>
    </>
  );
}

function AddressRow({
  ip,
  onCopy,
}: {
  ip: string;
  onCopy: (label: string, value: string) => void;
}) {
  return (
    <>
      <dt>IP address</dt>
      <dd>
        <Address label="IP address" value={ip} onCopy={onCopy} />
      </dd>
    </>
  );
}

export function TailscalePanel({
  interactive = true,
  window,
  desktop,
}: AppWindowProps) {
  const { setTitle } = desktop;
  useEffect(() => setTitle(window.id, 'Tailscale'), [setTitle, window.id]);
  const snapshot = useConnectionStatus(interactive);
  const [revision, setRevision] = useState(0);
  const [copyStatus, setCopyStatus] = useState({ message: '', error: false });
  const eligible =
    interactive && !snapshot.error && canAuthorize(snapshot.info);
  const authorization = useAuthorization(
    snapshot.info?.local === true,
    eligible,
    revision,
    snapshot.updatedAt,
  );
  const copy = async (label: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      setCopyStatus({ message: `${label} copied.`, error: false });
    } catch {
      setCopyStatus({
        message: `Could not copy ${label}. Select the address and copy it manually.`,
        error: true,
      });
    }
  };
  return (
    <div className="tailscale-app">
      <div className="app-toolbar">
        <span className="toolbar-caption" role="status">
          {snapshot.loading
            ? 'Checking…'
            : snapshot.error
              ? 'Check failed'
              : 'Connection status'}
        </span>
        <IconButton
          icon="refresh"
          label="Refresh"
          disabled={snapshot.loading}
          onClick={() => {
            snapshot.refresh();
            setRevision((value) => value + 1);
          }}
        />
      </div>
      <div className="app-pad tailscale-content" aria-busy={snapshot.loading}>
        {snapshot.error && (
          <p className="app-error" role="alert">
            {snapshot.error}
          </p>
        )}
        {snapshot.error && snapshot.info && (
          <p>
            Showing details from the last successful check. Current connection
            states are unknown.
          </p>
        )}
        {copyStatus.message && (
          <p
            className={copyStatus.error ? 'app-error' : 'tailscale-copy-status'}
            role={copyStatus.error ? 'alert' : 'status'}
          >
            {copyStatus.message}
          </p>
        )}
        <TailscaleDetails
          snapshot={snapshot}
          authorization={authorization}
          onCopy={(label, value) => void copy(label, value)}
        />
      </div>
    </div>
  );
}
