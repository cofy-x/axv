import { useId, useRef, useState, useSyncExternalStore } from 'react';
import {
  Button,
  Checkbox,
  Field,
  IconButton,
  PixelIcon,
  StatusBar,
} from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { extensionsApi } from './api';
import { useApplicationCatalog } from './catalog';
import { prepareExtensionChange } from './runtime';
import { checklistPackage } from './sample';
import { extensionStore } from './store';
import { extensionAppId } from './types';
import type { ExtensionPackage, ExtensionRecord } from './types';

interface PackageReview {
  package: ExtensionPackage;
  expectedGeneration: number;
  storeId: string;
}

function downloadJSON(name: string, value: unknown) {
  const url = URL.createObjectURL(
    new Blob([JSON.stringify(value, null, 2) + '\n'], {
      type: 'application/json',
    }),
  );
  const link = document.createElement('a');
  link.href = url;
  link.download = name;
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function readPackage(value: unknown): ExtensionPackage {
  if (!value || typeof value !== 'object')
    throw new Error('Choose an AXV extension package in JSON format.');
  const candidate = value as Partial<ExtensionPackage>;
  const manifest = candidate.manifest;
  if (
    !manifest ||
    manifest.schemaVersion !== 1 ||
    manifest.apiVersion !== 1 ||
    typeof manifest.id !== 'string' ||
    typeof manifest.title !== 'string' ||
    typeof manifest.version !== 'string' ||
    !Array.isArray(manifest.capabilities) ||
    manifest.capabilities.length !== 1 ||
    manifest.capabilities[0] !== 'storage' ||
    typeof candidate.html !== 'string' ||
    !candidate.html.trim()
  )
    throw new Error(
      'This package needs an API 1 manifest, the storage capability and an HTML document.',
    );
  if (new TextEncoder().encode(candidate.html).length > 512 * 1024)
    throw new Error('The HTML document must be no larger than 512 KiB.');
  if (
    manifest.icon !== undefined &&
    manifest.icon !== 'document' &&
    manifest.icon !== 'checklist'
  )
    throw new Error('The application icon must be document or checklist.');
  return candidate as ExtensionPackage;
}

export function ApplicationsManager({ desktop }: AppWindowProps) {
  const snapshot = useSyncExternalStore(
    extensionStore.subscribe,
    extensionStore.getSnapshot,
  );
  const { builtins } = useApplicationCatalog();
  const panelId = useId();
  const [addPanelOpen, setAddPanelOpen] = useState<boolean>();
  const [review, setReview] = useState<PackageReview>();
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const locked = useRef(false);
  const installed = snapshot.apps.filter((app) => app.installed);
  const removed = snapshot.apps.filter((app) => !app.installed);
  const unavailable = snapshot.loading || !!snapshot.error || !snapshot.storeId;
  const disabled = !!busy || unavailable;

  // Resolve the initial empty state once; later catalog polls keep the user's
  // explicit choice, including a closed panel with an unfinished review.
  if (addPanelOpen === undefined && !unavailable)
    setAddPanelOpen(installed.length === 0);

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
        <PixelIcon name="applications" size={32} />
        <strong className="toolbar-caption">Applications</strong>
        <Button
          id={`${panelId}-toggle`}
          aria-expanded={!!addPanelOpen}
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
        <section
          className="applications-add-panel"
          id={panelId}
          hidden={!addPanelOpen}
          aria-labelledby={`${panelId}-heading`}
        >
          <div className="applications-panel-heading">
            <h2 id={`${panelId}-heading`}>Add an extension</h2>
            <Button onClick={hideAddPanel}>Close</Button>
          </div>
          <p>Choose a package to review before installing.</p>
          <Field
            label="Extension package"
            className="applications-package-input"
            type="file"
            accept=".json,application/json"
            disabled={disabled}
            onChange={(event) => {
              const file = event.currentTarget.files?.[0];
              event.currentTarget.value = '';
              if (file) void selectPackage(file);
            }}
          />
          <div className="applications-actions applications-example-actions">
            <Button
              disabled={disabled}
              onClick={() => {
                try {
                  stage(checklistPackage);
                } catch (failure) {
                  setError(
                    failure instanceof Error
                      ? failure.message
                      : String(failure),
                  );
                }
              }}
            >
              Install Checklist
            </Button>
            <Button
              onClick={() =>
                downloadJSON('local.checklist-1.0.0.json', checklistPackage)
              }
            >
              Download example
            </Button>
            <span className="applications-meta">
              Original Checklist example · Private storage
            </span>
          </div>
          <p className="applications-access-note applications-meta">
            Install, enable and remove applications from the computer running
            AXV.
          </p>
          {review && (
            <div className="applications-review">
              <h3>Review {review.package.manifest.title}</h3>
              <dl className="info-list">
                <dt>Application</dt>
                <dd>{review.package.manifest.id}</dd>
                <dt>Version</dt>
                <dd>
                  {reviewedApp?.installed && `${reviewedApp.version} → `}
                  {review.package.manifest.version}
                </dd>
                <dt>Access</dt>
                <dd>Private application storage</dd>
              </dl>
              <p>
                {reviewedApp?.installed
                  ? 'This replaces the installed package and keeps its saved data.'
                  : 'The application will be enabled after installation.'}
              </p>
              {reviewStale && (
                <p role="alert">
                  The application changed since this review. Review again to use
                  its current version.
                </p>
              )}
              <div className="applications-actions">
                <Button disabled={!!busy} onClick={() => setReview(undefined)}>
                  Cancel
                </Button>
                {reviewStale ? (
                  <Button
                    disabled={disabled}
                    onClick={() => {
                      try {
                        stage(review.package);
                      } catch (failure) {
                        setError(String(failure));
                      }
                    }}
                  >
                    Review again
                  </Button>
                ) : (
                  <Button primary disabled={disabled} onClick={install}>
                    {reviewedApp?.installed ? 'Replace package' : 'Install'}
                  </Button>
                )}
              </div>
            </div>
          )}
        </section>
        <section aria-labelledby="applications-extensions-heading">
          <div className="applications-heading">
            <h2 id="applications-extensions-heading">Installed extensions</h2>
            <span>{installed.length} installed</span>
          </div>
          {installed.length ? (
            <ul className="applications-list">
              {installed.map((app) => (
                <li className="applications-row" key={app.id}>
                  <div className="applications-row-head">
                    <div className="applications-identity">
                      <PixelIcon name={app.icon || 'document'} size={32} />
                      <div className="applications-identity-text">
                        <h3>{app.title}</h3>
                        <p className="applications-meta">
                          {app.id} · {app.version}
                        </p>
                      </div>
                    </div>
                    <div
                      className="applications-actions applications-row-actions"
                      aria-label={`${app.title} actions`}
                    >
                      {app.enabled ? (
                        <Button
                          primary
                          disabled={disabled}
                          onClick={() => openApplication(app)}
                        >
                          Open
                        </Button>
                      ) : (
                        <Button
                          primary
                          disabled={disabled}
                          onClick={() => setEnabled(app, snapshot.storeId)}
                        >
                          Enable
                        </Button>
                      )}
                      {moreActions(app)}
                    </div>
                  </div>
                  <div className="applications-row-footer">
                    <span className="applications-state">
                      {snapshot.error
                        ? 'Status unavailable'
                        : app.enabled
                          ? 'Enabled · Private storage'
                          : 'Disabled · Private storage'}
                    </span>
                    {desktopOption(extensionAppId(app.id), app.title)}
                  </div>
                  {!app.enabled && (
                    <p className="applications-meta applications-row-guidance">
                      Enable from the computer running AXV.
                    </p>
                  )}
                </li>
              ))}
            </ul>
          ) : (
            <div className="applications-empty">
              <PixelIcon name="applications" size={40} />
              <strong>
                {snapshot.loading
                  ? 'Loading applications…'
                  : snapshot.error
                    ? 'Application list unavailable'
                    : 'No extensions installed'}
              </strong>
              <p>
                {snapshot.error
                  ? 'Refresh to retry.'
                  : !snapshot.loading && 'Add a package or try Checklist.'}
              </p>
            </div>
          )}
        </section>
        {!!removed.length && (
          <section aria-labelledby="applications-removed-heading">
            <div className="applications-heading">
              <h2 id="applications-removed-heading">
                Uninstalled applications
              </h2>
              <span>{removed.length} retained</span>
            </div>
            <p className="applications-meta">
              Saved data is kept. Export a copy or reinstall to use it again.
            </p>
            <ul className="applications-list">
              {removed.map((app) => (
                <li className="applications-row" key={app.id}>
                  <div className="applications-row-head">
                    <div className="applications-identity">
                      <PixelIcon name={app.icon || 'document'} size={32} />
                      <div className="applications-identity-text">
                        <h3>{app.title}</h3>
                        <p className="applications-meta">{app.id}</p>
                      </div>
                    </div>
                    <div
                      className="applications-actions applications-row-actions"
                      aria-label={`${app.title} data actions`}
                    >
                      <Button
                        primary
                        disabled={disabled}
                        onClick={() => exportData(app, snapshot.storeId)}
                      >
                        Export saved data
                      </Button>
                      {moreActions(app)}
                    </div>
                  </div>
                  <div className="applications-row-footer">
                    <span className="applications-state">
                      {snapshot.error
                        ? 'Status unavailable'
                        : 'Uninstalled · Saved data kept'}
                    </span>
                    {desktopOption(extensionAppId(app.id), app.title)}
                  </div>
                </li>
              ))}
            </ul>
          </section>
        )}
        <details className="applications-builtins">
          <summary>Built-in applications ({builtins.length})</summary>
          <ul className="applications-list">
            {[...builtins]
              .sort((a, b) => a.title.localeCompare(b.title))
              .map((app) => (
                <li
                  className="applications-row applications-builtin"
                  key={app.id}
                >
                  <div className="applications-identity">
                    <PixelIcon name={app.icon} size={28} />
                    <div>
                      <strong>{app.title}</strong>
                      <p className="applications-meta">
                        {app.kind === 'panel' ? 'Utility panel' : 'Application'}
                        {!app.launchable &&
                          ' · Opened from its owning application'}
                      </p>
                    </div>
                  </div>
                  {app.launchable && (
                    <div className="applications-builtin-controls">
                      {desktopOption(app.id, app.title)}
                      <Button
                        aria-label={`Open ${app.title}`}
                        onClick={() => desktop.launchApp(app.id)}
                      >
                        Open
                      </Button>
                    </div>
                  )}
                </li>
              ))}
          </ul>
        </details>
        <p className="applications-message" role="status" aria-live="polite">
          {message}
        </p>
      </div>
      <StatusBar>
        {busy ||
          (snapshot.loading
            ? 'Loading applications…'
            : snapshot.error
              ? 'Application list unavailable'
              : `${installed.length} extensions · ${builtins.length} built-in applications`)}
      </StatusBar>
    </div>
  );
}
