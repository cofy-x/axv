import { useId, useRef, useState, useSyncExternalStore } from 'react';
import { Button, Checkbox, IconButton, StatusBar } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { AddPackagePanel } from './AddPackagePanel';
import { BuiltinApplicationsSection } from './BuiltinApplicationsSection';
import { ExtensionListSection } from './ExtensionListSection';
import { extensionsApi } from './api';
import { useApplicationCatalog } from './catalog';
import { downloadJSON, readPackage } from './package-utils';
import type { PackageReview } from './package-utils';
import { prepareExtensionChange } from './runtime';
import { extensionStore } from './store';
import { extensionAppId } from './types';
import type { ExtensionPackage, ExtensionRecord } from './types';

export function ApplicationsManager({ desktop }: AppWindowProps) {
  const snapshot = useSyncExternalStore(
    extensionStore.subscribe,
    extensionStore.getSnapshot,
  );
  const { builtins } = useApplicationCatalog();
  const panelId = useId();
  const [addPanelOpen, setAddPanelOpen] = useState(false);
  const [review, setReview] = useState<PackageReview>();
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const locked = useRef(false);
  const installed = snapshot.apps.filter((app) => app.installed);
  const removed = snapshot.apps.filter((app) => !app.installed);
  const unavailable = snapshot.loading || !!snapshot.error || !snapshot.storeId;
  const disabled = !!busy || unavailable;

  function focusAddPackageFromPanel() {
    if (document.getElementById(panelId)?.contains(document.activeElement))
      document.getElementById(`${panelId}-toggle`)?.focus();
  }

  function hideAddPanel() {
    focusAddPackageFromPanel();
    setAddPanelOpen(false);
  }

  function desktopOption(appId: string, title: string) {
    return (
      <Checkbox
        className="applications-desktop-option"
        aria-label={`Show ${title} on desktop`}
        checked={desktop.isApplicationOnDesktop(appId)}
        onChange={(event) =>
          desktop.showDesktopApplication(appId, event.currentTarget.checked)
        }
      >
        Show on desktop
      </Checkbox>
    );
  }

  function refresh() {
    setError('');
    setMessage('');
    void extensionStore.refresh(true);
  }

  function assertStore(storeId: string) {
    const current = extensionStore.getSnapshot();
    if (!storeId || current.storeId !== storeId)
      throw new Error(
        'The application store changed. Refresh and review this action again.',
      );
    if (current.error)
      throw new Error('Refresh the application list before making changes.');
    return current;
  }

  function assertApplication(storeId: string, app: ExtensionRecord) {
    const current = assertStore(storeId);
    if (
      current.apps.find((item) => item.id === app.id)?.generation !==
      app.generation
    )
      throw new Error(
        'The application changed. Refresh and review this action again.',
      );
    return current;
  }

  function openApplication(app: ExtensionRecord) {
    try {
      assertApplication(snapshot.storeId, app);
      desktop.launchApp(extensionAppId(app.id));
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    }
  }

  async function run(
    label: string,
    task: () => Promise<boolean | void>,
    success: string,
    storeId = snapshot.storeId,
  ) {
    if (locked.current) return;
    locked.current = true;
    setBusy(label);
    setError('');
    setMessage('');
    try {
      assertStore(storeId);
      if ((await task()) === false) return;
      await extensionStore.refresh(true);
      if (extensionStore.getSnapshot().error) {
        setError(
          'The action completed, but the application list could not be refreshed. Refresh to check its current status.',
        );
        return;
      }
      assertStore(storeId);
      setMessage(success);
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      locked.current = false;
      setBusy('');
    }
  }

  function stage(pkg: ExtensionPackage) {
    const current = assertStore(snapshot.storeId);
    const existing = current.apps.find((app) => app.id === pkg.manifest.id);
    setReview({
      package: pkg,
      expectedGeneration: existing?.generation ?? 0,
      storeId: current.storeId,
    });
    setMessage('');
    setError('');
  }

  function stageSafely(pkg: ExtensionPackage) {
    try {
      stage(pkg);
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    }
  }

  async function selectPackage(file: File) {
    if (locked.current) return;
    locked.current = true;
    setBusy('Reading package…');
    setError('');
    setMessage('');
    try {
      if (!file.size || file.size > 1024 * 1024)
        throw new Error(
          'Choose a non-empty JSON package no larger than 1 MiB.',
        );
      const text = await file.text();
      stage(readPackage(JSON.parse(text) as unknown));
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      locked.current = false;
      setBusy('');
    }
  }

  function install() {
    if (!review) return;
    const captured = review;
    const pkg = captured.package;
    // Disabled controls can lose focus before the request completes.
    focusAddPackageFromPanel();
    void run(
      'Installing…',
      async () => {
        if (!(await prepareExtensionChange(desktop, pkg.manifest.id)))
          return false;
        assertStore(captured.storeId);
        await extensionsApi.install(
          captured.storeId,
          pkg,
          captured.expectedGeneration || undefined,
        );
        setReview(undefined);
        hideAddPanel();
      },
      `${pkg.manifest.title} installed.`,
      captured.storeId,
    );
  }

  function setEnabled(app: ExtensionRecord, storeId: string) {
    void run(
      app.enabled ? 'Disabling…' : 'Enabling…',
      async () => {
        assertApplication(storeId, app);
        if (!(await prepareExtensionChange(desktop, app.id))) return false;
        assertApplication(storeId, app);
        await extensionsApi.setEnabled(storeId, app, !app.enabled);
      },
      `${app.title} ${app.enabled ? 'disabled' : 'enabled'}.`,
      storeId,
    );
  }

  function uninstall(app: ExtensionRecord, storeId: string) {
    void run(
      'Uninstalling…',
      async () => {
        assertApplication(storeId, app);
        const answer = await desktop.dialog({
          title: `Uninstall ${app.title}?`,
          message:
            'Its windows will become unavailable. Saved data stays here for export or a later reinstall.',
          buttons: [
            { label: 'Cancel', value: 'cancel', cancel: true, default: true },
            { label: 'Uninstall', value: 'uninstall' },
          ],
        });
        if (answer?.button !== 'uninstall') return false;
        assertApplication(storeId, app);
        if (!(await prepareExtensionChange(desktop, app.id))) return false;
        assertApplication(storeId, app);
        await extensionsApi.uninstall(storeId, app);
      },
      `${app.title} uninstalled. Saved data kept.`,
      storeId,
    );
  }

  function exportData(app: ExtensionRecord, storeId: string) {
    void run(
      'Exporting…',
      async () => {
        const data = await extensionsApi.data(storeId, app.id);
        assertStore(storeId);
        downloadJSON(`${app.id}-data.json`, { appId: app.id, ...data });
      },
      `Saved data for ${app.title} exported.`,
      storeId,
    );
  }

  function clearData(app: ExtensionRecord, storeId: string) {
    void run(
      'Clearing data…',
      async () => {
        assertApplication(storeId, app);
        const data = await extensionsApi.data(storeId, app.id);
        assertApplication(storeId, app);
        const answer = await desktop.dialog({
          title: `Delete ${app.title} data?`,
          message:
            'Permanently delete this uninstalled application’s saved data? Export a copy first if you need it. This cannot be undone.',
          buttons: [
            { label: 'Cancel', value: 'cancel', cancel: true, default: true },
            { label: 'Delete data', value: 'delete' },
          ],
        });
        if (answer?.button !== 'delete') return false;
        assertApplication(storeId, app);
        if (!(await prepareExtensionChange(desktop, app.id))) return false;
        const current = assertApplication(storeId, app);
        if (current.apps.find((item) => item.id === app.id)?.installed)
          throw new Error(
            'This application was reinstalled. Refresh its status.',
          );
        await extensionsApi.clearData(storeId, app.id, data.revision);
      },
      `Saved data for ${app.title} deleted.`,
      storeId,
    );
  }

  function moreActions(app: ExtensionRecord) {
    return (
      <IconButton
        icon="more"
        label={`More actions for ${app.title}, ${app.id}`}
        aria-haspopup="menu"
        disabled={disabled}
        onClick={(event) => {
          const rect = event.currentTarget.getBoundingClientRect();
          const storeId = snapshot.storeId;
          desktop.contextMenu(
            app.installed
              ? [
                  {
                    label: app.enabled ? 'Disable' : 'Enable',
                    action: () => setEnabled(app, storeId),
                  },
                  {
                    label: 'Export saved data',
                    action: () => exportData(app, storeId),
                  },
                  { separator: true },
                  {
                    label: 'Uninstall',
                    action: () => uninstall(app, storeId),
                  },
                ]
              : [
                  {
                    label: 'Delete data',
                    action: () => clearData(app, storeId),
                  },
                ],
            { x: rect.left, y: rect.bottom },
            { title: `${app.title} · ${app.id}` },
          );
        }}
      />
    );
  }

  const reviewedApp = review
    ? snapshot.apps.find((app) => app.id === review.package.manifest.id)
    : undefined;
  const reviewStale =
    !!review &&
    (review.storeId !== snapshot.storeId ||
      review.expectedGeneration !== (reviewedApp?.generation ?? 0));

  return (
    <div className="applications-app">
      <div className="app-toolbar applications-toolbar">
        <Button
          primary
          id={`${panelId}-toggle`}
          aria-expanded={addPanelOpen}
          aria-controls={panelId}
          onClick={() => setAddPanelOpen(!addPanelOpen)}
        >
          Add package
        </Button>
        <IconButton
          icon="refresh"
          label={snapshot.loading ? 'Refreshing…' : 'Refresh'}
          disabled={!!busy || snapshot.loading}
          onClick={refresh}
        />
      </div>
      {(error || snapshot.error) && (
        <div className="app-error" role="alert">
          {error || snapshot.error}
          {error && snapshot.error && <p>{snapshot.error}</p>}
          {snapshot.error && (
            <div className="app-actions">
              <Button disabled={!!busy} onClick={refresh}>
                Retry
              </Button>
            </div>
          )}
        </div>
      )}
      <div className="applications-content">
        <AddPackagePanel
          panelId={panelId}
          open={addPanelOpen}
          disabled={disabled}
          busy={!!busy}
          review={review}
          reviewedApp={reviewedApp}
          reviewStale={reviewStale}
          onClose={hideAddPanel}
          onSelectFile={(file) => void selectPackage(file)}
          onStagePackage={stageSafely}
          onCancelReview={() => setReview(undefined)}
          onInstall={install}
        />
        <ExtensionListSection
          installed={installed}
          removed={removed}
          loading={snapshot.loading}
          snapshotError={snapshot.error}
          disabled={disabled}
          addPanelOpen={addPanelOpen}
          onOpenAddPanel={() => setAddPanelOpen(true)}
          onOpenApplication={openApplication}
          onToggleEnabled={(app) => setEnabled(app, snapshot.storeId)}
          onExportData={(app) => exportData(app, snapshot.storeId)}
          renderMoreActions={moreActions}
          renderDesktopOption={desktopOption}
        />
        <BuiltinApplicationsSection
          builtins={builtins}
          onOpenBuiltin={(appId) => desktop.launchApp(appId)}
          renderDesktopOption={desktopOption}
        />
        <p className="applications-message" role="status" aria-live="polite">
          {message}
        </p>
      </div>
      {busy && <StatusBar>{busy}</StatusBar>}
    </div>
  );
}
