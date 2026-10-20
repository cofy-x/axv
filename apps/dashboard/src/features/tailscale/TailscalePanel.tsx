import { useEffect, useRef, useState } from 'react';
import { Button, IconButton } from '@cofy-x/axv-nextstep';
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
      .authorizationURL(
        {
          local,
          remote: { enabled: true, supported: true, state: 'needs-login' },
        },
        signal,
      )
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
    return () => controller.abort();
  }, [local, eligible, revision, updatedAt]);
  // Hide enrollment capabilities immediately when the panel loses eligibility.
  return local && eligible ? authorization : {};
}

function useLocalError(eligible: boolean, updatedAt?: number) {
  const [detail, setDetail] = useState('');
  useEffect(() => {
    if (!eligible) {
      setDetail('');
      return;
    }
    const controller = new AbortController();
    const signal = AbortSignal.any([
      controller.signal,
      AbortSignal.timeout(8000),
    ]);
    void connectionAPI
      .localError(signal)
      .then((error) => {
        if (!signal.aborted) setDetail(error ?? '');
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setDetail(
            error instanceof Error
              ? error.message
              : 'Could not load the error details. Refresh to retry.',
          );
      });
    return () => controller.abort();
  }, [eligible, updatedAt]);
  return eligible ? detail : '';
}

export function TailscaleDetails({
  snapshot,
  authorization = {},
  changing = false,
  onSetEnabled,
  onCopy,
}: {
  snapshot: ConnectionSnapshot;
  authorization?: Authorization;
  changing?: boolean;
  onSetEnabled?: (enabled: boolean) => void;
  onCopy: (label: string, value: string) => void;
}) {
  const { info, error } = snapshot;
  const status = connectionPresentation(snapshot);
  const url = !error ? readyURL(info) : undefined;
  const authorize = !error && !changing && canAuthorize(info);
  if (!info)
    return (
      <p>
        {snapshot.loading
          ? 'Checking connection…'
          : 'Connection status is unavailable. Refresh to retry.'}
      </p>
    );
  const { remote } = info;
  return (
    <section
      aria-labelledby="tailscale-axv-title"
      className="tailscale-section"
    >
      <h2 id="tailscale-axv-title">AXV Remote Access</h2>
      <p>Open this workstation privately from your other devices.</p>
      <p className="tailscale-state">
        <span
          className={`tailscale-status-dot is-${status.remoteTone}`}
          aria-hidden="true"
        />
        {status.remote}
      </p>
      {remote.message && <p>{remote.message}</p>}
      {!remote.supported ? (
        <p>Remote access is available on AXV servers running macOS or Linux.</p>
      ) : remote.state === 'disabled' ? (
        <p>
          Enable to connect AXV, then authorize it with your Tailscale account.
        </p>
      ) : remote.state === 'starting' ? (
        <p>AXV is connecting its private Tailscale node.</p>
      ) : remote.state === 'stopped' || remote.state === 'error' ? (
        <p>Retry to connect AXV again. Existing authorization is kept.</p>
      ) : null}
      {remote.state === 'needs-https' && (
        <>
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
          <p className="fine-print">
            Issuing a certificate publishes this node’s name in public
            certificate transparency logs. Access stays private to Tailscale.
          </p>
        </>
      )}
      {remote.state === 'needs-login' && (
        <>
          <p>
            {info.local
              ? 'Authorize the AXV node with your Tailscale account, then return here.'
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
        </dl>
      )}
      {info.local && remote.supported && onSetEnabled && (
        <>
          <div className="tailscale-actions">
            {remote.enabled &&
              (remote.state === 'error' || remote.state === 'stopped') && (
                <Button
                  primary
                  disabled={!!error || changing}
                  onClick={() => onSetEnabled(true)}
                >
                  Retry
                </Button>
              )}
            <Button
              primary={!remote.enabled}
              disabled={!!error || changing}
              onClick={() => onSetEnabled(!remote.enabled)}
            >
              {remote.enabled ? 'Disable' : 'Enable'}
            </Button>
            {changing && <span role="status">Saving…</span>}
          </div>
          <p className="fine-print">
            {remote.enabled
              ? 'Enabled for this AXV service and future launches. Disabling keeps your node authorization.'
              : 'Enabling is saved for future AXV service launches. You can disable it here at any time.'}
          </p>
        </>
      )}
      {!info.local && (
        <p className="fine-print">
          Manage remote access on the computer running AXV.
        </p>
      )}
      <p className="fine-print">
        Tailscale is built into AXV. No system installation is needed on the AXV
        server; its DNS, routes and firewall stay unchanged.
      </p>
      <details className="tailscale-help">
        <summary>Connect another device</summary>
        <ol>
          <li>
            <a
              href="https://tailscale.com/download"
              target="_blank"
              rel="noopener noreferrer"
            >
              Install Tailscale
            </a>{' '}
            on the device you want to connect from.
          </li>
          <li>Connect with the same Tailscale account that authorized AXV.</li>
          <li>When AXV is ready, open its HTTPS URL on that device.</li>
        </ol>
        <p>The desktop uses the same host files, agents and VMs.</p>
      </details>
    </section>
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
  const [changing, setChanging] = useState(false);
  const [changeError, setChangeError] = useState('');
  const [copyStatus, setCopyStatus] = useState({ message: '', error: false });
  const mutation = useRef<AbortController | null>(null);
  const actionPending = useRef(false);
  const latest = useRef({ interactive, snapshot });
  latest.current = { interactive, snapshot };
  useEffect(() => () => mutation.current?.abort(), []);
  const eligible =
    interactive && !changing && !snapshot.error && canAuthorize(snapshot.info);
  const authorization = useAuthorization(
    snapshot.info?.local === true,
    eligible,
    revision,
    snapshot.updatedAt,
  );
  const localError = useLocalError(
    interactive &&
      !snapshot.error &&
      snapshot.info?.local === true &&
      (snapshot.info.remote.state === 'error' ||
        (snapshot.info.remote.state === 'ready' &&
          !!snapshot.info.remote.message)),
    snapshot.updatedAt,
  );
  const setEnabled = async (enabled: boolean) => {
    if (actionPending.current) return;
    actionPending.current = true;
    try {
      if (!enabled && snapshot.info?.remote.state === 'ready') {
        const answer = await desktop.dialog({
          title: 'Disable remote access?',
          message:
            'Remote browsers will disconnect and their host shells will end. Codex views will detach; Codex runs and VMs will continue. Local work stays open, and node authorization is kept.',
          buttons: [
            { label: 'Cancel', value: 'cancel', cancel: true, default: true },
            { label: 'Disable', value: 'disable' },
          ],
        });
        if (answer?.button !== 'disable') return;
      }
      const current = latest.current;
      const info = current.snapshot.info;
      if (
        !current.interactive ||
        current.snapshot.error ||
        !info?.local ||
        !info.remote.supported
      )
        return;
      const controller = new AbortController();
      mutation.current = controller;
      setChanging(true);
      setChangeError('');
      setCopyStatus({ message: '', error: false });
      const signal = AbortSignal.any([
        controller.signal,
        AbortSignal.timeout(30000),
      ]);
      try {
        await connectionAPI.setEnabled(info, enabled, signal);
        signal.throwIfAborted();
      } catch (error) {
        if (!controller.signal.aborted)
          setChangeError(
            signal.aborted
              ? 'Could not confirm the change in time. Refresh to check the current state.'
              : error instanceof Error
                ? error.message
                : 'Could not save remote access. Refresh to retry.',
          );
      } finally {
        mutation.current = null;
        if (!controller.signal.aborted) {
          setChanging(false);
          latest.current.snapshot.refresh();
        }
      }
    } finally {
      actionPending.current = false;
    }
  };
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
          disabled={snapshot.loading || changing}
          onClick={() => {
            snapshot.refresh();
            setRevision((value) => value + 1);
          }}
        />
      </div>
      <div className="app-pad tailscale-content" aria-busy={changing}>
        {snapshot.error && (
          <p className="app-error" role="alert">
            {snapshot.error}
          </p>
        )}
        {snapshot.error && snapshot.info && (
          <p>
            Showing details from the last successful check. Current connection
            state is unknown.
          </p>
        )}
        {(changeError || localError) && (
          <p className="app-error" role="alert">
            {changeError || localError}
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
          changing={changing}
          onSetEnabled={(enabled) => void setEnabled(enabled)}
          onCopy={(label, value) => void copy(label, value)}
        />
      </div>
    </div>
  );
}
