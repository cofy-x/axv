import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';
import { Button, Tabs } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { cloudflareAPI } from './api';
import { CloudflareDetails } from './CloudflareAccessCard';
import type { AccessChange, CopyStatus } from './CloudflareAccessCard';
import { CloudflareDiagnostics } from './CloudflareDiagnostics';
import { CloudflareOverviewHelp } from './CloudflareOverviewHelp';
import { CloudflarePublishing } from './CloudflarePublishing';
import { CloudflareSetup } from './CloudflareSetup';
import { CloudflareTraffic } from './CloudflareTraffic';
import { CloudflareTunnelCard } from './CloudflareTunnelCard';
import { CloudflareDiagnosticsStore } from './diagnostics-store';
import { requestedPublication } from './publishing-context';
import { CloudflareStore } from './status-store';
import { useResource } from './useResource';

export { CloudflareDetails } from './CloudflareAccessCard';

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
            <p className="cloudflare-stale-note">
              Showing previous details. Current connection state is unknown.
            </p>
          )}
          {changeError && (
            <p className="app-error" role="alert">
              {changeError}
            </p>
          )}
          {snapshot.info?.local && (snapshot.error || changeError) && (
            <div className="cloudflare-inline-action">
              <Button
                disabled={!interactive || !!changing}
                onClick={openDiagnostics}
                aria-controls="cloudflare-diagnostics"
              >
                View diagnostics
              </Button>
            </div>
          )}
          <div className="cloudflare-overview-stack">
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
            <CloudflareTunnelCard
              interactive={interactive}
              canPublish={allowed?.publish === true}
              canHealth={allowed?.health === true}
              healthValue={health.value}
              healthError={health.error}
              desktopListening={snapshot.info?.cloudflare.state === 'listening'}
              onPublish={() => selectTab('publishing')}
            />
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
                  <div className="cloudflare-disclosure-body">
                    <CloudflareDiagnostics
                      status={detail.status}
                      error={detail.error}
                      unavailable={!!snapshot.error}
                    />
                  </div>
                )}
              </details>
            )}
            <CloudflareOverviewHelp />
          </div>
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
