import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';
import { Button, IconButton, Tabs } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { cloudflareAPI, cloudflareURL } from './api';
import { cloudflarePresentation } from './presentation';
import { CloudflareStore } from './status-store';
import { CloudflareDiagnosticsStore } from './diagnostics-store';
import { CloudflareDiagnostics } from './CloudflareDiagnostics';
import { CloudflareSetup } from './CloudflareSetup';
import { CloudflarePublishing } from './CloudflarePublishing';
import { requestedPublication } from './publishing-context';
import { CloudflareTraffic } from './CloudflareTraffic';
import { useResource } from './useResource';
import type { CloudflareSnapshot } from './types';

function useCloudflareStatus(interactive: boolean) {
  const [store] = useState(() => new CloudflareStore());
  const subscribe = useCallback(
    (listener: () => void) =>
      interactive ? store.subscribe(listener) : () => undefined,
    [store, interactive],
  );
  const snapshot = useSyncExternalStore(subscribe, store.getSnapshot);
  return { ...snapshot, refresh: store.refresh };
}

function useLocalDiagnostics(eligible: boolean) {
  const [store] = useState(() => new CloudflareDiagnosticsStore());
  const subscribe = useCallback(
    (listener: () => void) =>
      eligible ? store.subscribe(listener) : () => undefined,
    [store, eligible],
  );
  const snapshot = useSyncExternalStore(subscribe, store.getSnapshot);
  return { ...(eligible ? snapshot : {}), refresh: store.refresh };
}

interface CopyStatus {
  message: string;
  error: boolean;
}

type AccessChange = 'enable' | 'disable';

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
      <div className="cloudflare-heading">
        <p>
          {snapshot.loading
            ? 'Checking…'
            : 'Status unavailable. Refresh to check.'}
        </p>
        {refreshButton}
      </div>
    );
  const needsSetup = !error && !status.configured && status.state !== 'error';
  const retry =
    status.enabled &&
    status.configured &&
    (status.state === 'error' || status.state === 'stopped');
  return (
    <section aria-labelledby="cloudflare-access-title">
      <div className="cloudflare-heading">
        <h2 id="cloudflare-access-title">Desktop access</h2>
        <div className="cloudflare-status-actions">
          <p className="cloudflare-state" role="status" aria-atomic="true">
            <span
              className={`cloudflare-status-dot is-${changing ? 'pending' : presentation.tone}`}
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
      {info.local &&
        onViewDiagnostics &&
        (status.state === 'error' || status.state === 'stopped') && (
          <Button
            disabled={!interactive || !!changing}
            onClick={onViewDiagnostics}
            aria-controls="cloudflare-diagnostics"
          >
            View diagnostics
          </Button>
        )}
      {url && (
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
        <>
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
        </>
      )}
      {!info.local && (
        <p className="fine-print">
          Manage Cloudflare on the computer running AXV.
        </p>
      )}
    </section>
  );
}

export function CloudflarePanel({
  interactive = true,
  visible = true,
  window,
  desktop,
}: AppWindowProps) {
  const { setTitle } = desktop;
  useEffect(() => setTitle(window.id, 'Cloudflare'), [setTitle, window.id]);
  const snapshot = useCloudflareStatus(interactive);
  const [expanded, setExpanded] = useState(false);
  const tab = ['overview', 'setup', 'publishing', 'traffic'].includes(
    String(window.viewState.cloudflareTab),
  )
    ? String(window.viewState.cloudflareTab)
    : 'overview';
  const selectTab = (next: string) =>
    desktop.updateWindowState(window.id, (previous) => ({
      ...previous,
      cloudflareTab: next,
    }));
  const selectSetup = (section: 'desktop' | 'account') =>
    desktop.updateWindowState(window.id, (previous) => ({
      ...previous,
      cloudflareTab: 'setup',
      cloudflareSetupSection: section,
    }));
  const caps = useResource(cloudflareAPI.capabilities, interactive, 60000);
  const allowed = !caps.error ? caps.value : undefined;
  const canConfigure =
    allowed?.configure === true && snapshot.info?.local === true;
  const [setupDirty, setSetupDirty] = useState(false);
  const [publishDirty, setPublishDirty] = useState(false);
  const forceHealth = useRef(false);
  const readHealth = useCallback((signal: AbortSignal) => {
    const force = forceHealth.current;
    forceHealth.current = false;
    return cloudflareAPI.health(signal, force);
  }, []);
  const health = useResource(
    readHealth,
    interactive && tab === 'overview' && allowed?.health === true,
    30000,
    35000,
  );
  const [changing, setChanging] = useState<AccessChange>();
  const [changeError, setChangeError] = useState('');
  const [copyStatus, setCopyStatus] = useState<CopyStatus>({
    message: '',
    error: false,
  });
  const mutation = useRef<AbortController | null>(null);
  const pending = useRef(false);
  const diagnostics = useRef<HTMLDetailsElement>(null);
  const focusDiagnostics = useRef(false);
  const latest = useRef({ interactive, snapshot, canConfigure });
  latest.current = { interactive, snapshot, canConfigure };
  useEffect(() => () => mutation.current?.abort(), []);
  useEffect(() => {
    if (!expanded || !focusDiagnostics.current) return;
    focusDiagnostics.current = false;
    if (!latest.current.interactive) return;
    diagnostics.current
      ?.querySelector('summary')
      ?.focus({ preventScroll: true });
    diagnostics.current?.scrollIntoView({ block: 'nearest' });
  }, [expanded]);
  const detail = useLocalDiagnostics(
    interactive &&
      tab === 'overview' &&
      expanded &&
      !changing &&
      !snapshot.error &&
      canConfigure,
  );

  const openDiagnostics = () => {
    if (!interactive || snapshot.info?.local !== true) return;
    if (expanded) {
      diagnostics.current
        ?.querySelector('summary')
        ?.focus({ preventScroll: true });
      diagnostics.current?.scrollIntoView({ block: 'nearest' });
      detail.refresh();
    } else {
      focusDiagnostics.current = true;
      setExpanded(true);
    }
  };

  useEffect(
    () =>
      desktop.beforeClose(window.id, async () => {
        if (!setupDirty && !publishDirty) return true;
        const answer = await desktop.dialog({
          title: 'Discard Cloudflare drafts?',
          message:
            'Unsaved settings and publication drafts will be lost. Saved configuration and published services are kept.',
          buttons: [
            { label: 'Cancel', value: 'cancel', cancel: true, default: true },
            { label: 'Discard drafts', value: 'discard' },
          ],
        });
        return answer?.button === 'discard';
      }),
    [desktop, window.id, setupDirty, publishDirty],
  );

  const setEnabled = async (enabled: boolean) => {
    if (pending.current) return;
    pending.current = true;
    try {
      if (!enabled && snapshot.info?.cloudflare.state === 'listening') {
        const answer = await desktop.dialog({
          title: 'Disable Cloudflare access?',
          message:
            'Cloudflare browsers will disconnect and their host shells will end. Codex views will detach; Codex runs and VMs will continue. Local and Tailscale connections stay open. Your Cloudflare setup is kept.',
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
        !current.canConfigure ||
        (enabled && !info.cloudflare.configured)
      )
        return;
      const controller = new AbortController();
      mutation.current = controller;
      setChanging(enabled ? 'enable' : 'disable');
      setChangeError('');
      setCopyStatus({ message: '', error: false });
      const signal = AbortSignal.any([
        controller.signal,
        AbortSignal.timeout(30000),
      ]);
      try {
        await cloudflareAPI.setEnabled(info, enabled, signal);
        signal.throwIfAborted();
      } catch (error) {
        if (!controller.signal.aborted)
          setChangeError(
            signal.aborted
              ? 'Could not confirm the change in time. Refresh to check the current state.'
              : error instanceof Error
                ? error.message
                : 'Could not save Cloudflare access. Refresh to retry.',
          );
      } finally {
        mutation.current = null;
        if (!controller.signal.aborted) {
          setChanging(undefined);
          latest.current.snapshot.refresh();
        }
      }
    } finally {
      pending.current = false;
    }
  };

  const copy = async (url: string) => {
    try {
      await navigator.clipboard.writeText(url);
      setCopyStatus({ message: 'Desktop URL copied.', error: false });
    } catch {
      setCopyStatus({
        message:
          'Could not copy Desktop URL. Select the address and copy it manually.',
        error: true,
      });
    }
  };

  const refreshOverview = () => {
    snapshot.refresh();
    detail.refresh();
    caps.refresh();
    forceHealth.current = true;
    health.refresh();
  };

  return (
    <div className="cloudflare-app">
      {caps.error && (
        <div className="cloudflare-capability-error">
          <p className="app-error" role="alert">
            {caps.error}
          </p>
          <Button
            disabled={!interactive || caps.loading}
            onClick={caps.refresh}
          >
            Retry
          </Button>
        </div>
      )}
      <Tabs
        label="Cloudflare sections"
        value={tab}
        onChange={selectTab}
        tabs={[
          { id: 'overview', label: 'Overview', disabled: !interactive },
          { id: 'setup', label: 'Setup', disabled: !interactive },
          { id: 'publishing', label: 'Publishing', disabled: !interactive },
          { id: 'traffic', label: 'Traffic', disabled: !interactive },
        ]}
      >
        <div
          className="app-pad cloudflare-content"
          hidden={tab !== 'overview'}
          inert={!interactive || tab !== 'overview'}
          aria-busy={!!changing}
        >
          {snapshot.error && (
            <p className="app-error" role="alert">
              {snapshot.error}
            </p>
          )}
          {snapshot.error && snapshot.info && (
            <p>
              Showing previous details. Current connection state is unknown.
            </p>
          )}
          {changeError && (
            <p className="app-error" role="alert">
              {changeError}
            </p>
          )}
          {snapshot.info?.local && (snapshot.error || changeError) && (
            <Button
              disabled={!interactive || !!changing}
              onClick={openDiagnostics}
              aria-controls="cloudflare-diagnostics"
            >
              View diagnostics
            </Button>
          )}
          <CloudflareDetails
            snapshot={snapshot}
            interactive={interactive}
            changing={changing}
            copyStatus={copyStatus}
            onSetEnabled={
              canConfigure ? (enabled) => void setEnabled(enabled) : undefined
            }
            onSetupGuide={
              canConfigure ? () => selectSetup('desktop') : undefined
            }
            onViewDiagnostics={
              snapshot.error || changeError ? undefined : openDiagnostics
            }
            onRefresh={refreshOverview}
            refreshing={
              snapshot.loading || (allowed?.health === true && health.loading)
            }
            onCopy={(url) => void copy(url)}
          />
          <section
            className="cloudflare-cloud-status"
            aria-labelledby="cloudflare-tunnel-title"
          >
            <div className="cloudflare-section-heading">
              <h2 id="cloudflare-tunnel-title">Tunnel</h2>
              <strong>
                {health.error || !allowed?.health
                  ? 'Unknown'
                  : health.value
                    ? {
                        unconfigured: 'Not configured',
                        healthy: 'Healthy',
                        degraded: 'Degraded',
                        error: 'Needs attention',
                      }[health.value.state]
                    : 'Checking…'}
              </strong>
            </div>
            {health.error && (
              <p className="app-error" role="alert">
                {health.error}
              </p>
            )}
            {allowed?.health && health.value && (
              <>
                <p>
                  {health.value.state === 'healthy'
                    ? 'Token and DNS read access checked.'
                    : health.value.message}
                </p>
                {health.value.tunnelName && (
                  <p>
                    Tunnel: {health.value.tunnelName}
                    {health.value.tunnelStatus &&
                    health.value.state !== 'healthy'
                      ? ` · ${health.value.tunnelStatus}`
                      : ''}
                  </p>
                )}
                <p className="fine-print">
                  Checked{' '}
                  <time
                    dateTime={health.value.checkedAt}
                    title={new Date(health.value.checkedAt).toLocaleString()}
                  >
                    {new Date(health.value.checkedAt).toLocaleString(
                      undefined,
                      {
                        dateStyle: 'medium',
                        timeStyle: 'short',
                      },
                    )}
                  </time>
                </p>
                {health.value.state === 'healthy' &&
                  snapshot.info?.cloudflare.state !== 'listening' && (
                    <p className="fine-print">
                      Public access has not been verified.
                    </p>
                  )}
              </>
            )}
            <div className="cloudflare-actions">
              <Button
                disabled={!interactive || !allowed?.publish}
                onClick={() => selectTab('publishing')}
              >
                Publish a service
              </Button>
            </div>
          </section>
          {snapshot.info?.local && (
            <details
              id="cloudflare-diagnostics"
              ref={diagnostics}
              className="cloudflare-disclosure"
              open={expanded}
              onToggle={(event) => setExpanded(event.currentTarget.open)}
            >
              <summary>Diagnostics</summary>
              {expanded && interactive && (
                <CloudflareDiagnostics
                  status={detail.status}
                  error={detail.error}
                  unavailable={!!snapshot.error}
                />
              )}
            </details>
          )}
          <details className="cloudflare-disclosure">
            <summary>Help</summary>
            <p>
              AXV manages its desktop entrypoint and can publish VM services
              using your connected account. Create the Cloudflare Tunnel and
              Access applications separately, and run your tunnel connector on
              the AXV host.
            </p>
            <p>
              Closing this window keeps access enabled. Enable and disable
              choices are saved for future launches.
            </p>
            <p>
              Tunnel health checks verify the token, DNS read access and
              connector status. They do not verify public URL reachability, DNS
              write permissions or WAF protection.
            </p>
            <p>
              When your Access session expires, its connections close: host
              shells end and Codex views detach while Codex runs and VMs
              continue. Use an Access application session of 15 minutes or less.
              Provider logout or policy changes are not detected immediately;
              the original token expiry still limits an open connection. Sign in
              again to reconnect.
            </p>
            <p className="cloudflare-guide-links">
              <a
                href="https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/self-hosted-public-app/"
                target="_blank"
                rel="noopener noreferrer"
              >
                Cloudflare Access guide
              </a>
              <a
                href="https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/local-management/configuration-file/"
                target="_blank"
                rel="noopener noreferrer"
              >
                Cloudflare Tunnel guide
              </a>
            </p>
          </details>
        </div>
        <section
          id="cloudflare-setup-page"
          className="app-pad cloudflare-content"
          hidden={tab !== 'setup'}
          inert={!interactive || tab !== 'setup'}
        >
          <CloudflareSetup
            active={interactive && tab === 'setup'}
            present={visible && tab === 'setup'}
            allowed={canConfigure}
            enabled={snapshot.info?.cloudflare.enabled === true}
            desktop={desktop}
            onSaved={snapshot.refresh}
            onDirtyChange={setSetupDirty}
            section={
              window.viewState.cloudflareSetupSection === 'desktop' ||
              window.viewState.cloudflareSetupSection === 'account'
                ? window.viewState.cloudflareSetupSection
                : undefined
            }
            onSectionChange={selectSetup}
          />
        </section>
        <section
          className="app-pad cloudflare-content"
          hidden={tab !== 'publishing'}
          inert={!interactive || tab !== 'publishing'}
        >
          <CloudflarePublishing
            active={interactive && tab === 'publishing'}
            present={visible && tab === 'publishing'}
            allowed={allowed?.publish === true}
            requested={requestedPublication(
              window.viewState.cloudflarePublishRequest,
            )}
            desktop={desktop}
            onDirtyChange={setPublishDirty}
            onSetup={() => selectSetup('account')}
          />
        </section>
        <section
          className="app-pad cloudflare-content"
          hidden={tab !== 'traffic'}
          inert={!interactive || tab !== 'traffic'}
        >
          <CloudflareTraffic
            active={interactive && tab === 'traffic'}
            allowed={allowed?.traffic === true}
          />
        </section>
      </Tabs>
    </div>
  );
}
