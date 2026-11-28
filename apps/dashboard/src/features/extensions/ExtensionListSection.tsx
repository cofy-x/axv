import type { ReactNode } from 'react';
import { Button, PixelIcon } from '@cofy-x/axv-nextstep';
import { extensionAppId } from './types';
import type { ExtensionRecord } from './types';

interface ExtensionListSectionProps {
  installed: ExtensionRecord[];
  removed: ExtensionRecord[];
  loading: boolean;
  snapshotError: string;
  disabled: boolean;
  addPanelOpen: boolean;
  onOpenAddPanel: () => void;
  onOpenApplication: (app: ExtensionRecord) => void;
  onToggleEnabled: (app: ExtensionRecord) => void;
  onExportData: (app: ExtensionRecord) => void;
  renderMoreActions: (app: ExtensionRecord) => ReactNode;
  renderDesktopOption: (appId: string, title: string) => ReactNode;
}

export function ExtensionListSection({
  installed,
  removed,
  loading,
  snapshotError,
  disabled,
  addPanelOpen,
  onOpenAddPanel,
  onOpenApplication,
  onToggleEnabled,
  onExportData,
  renderMoreActions,
  renderDesktopOption,
}: ExtensionListSectionProps) {
  return (
    <>
      <section
        className="applications-section"
        aria-labelledby="applications-extensions-heading"
      >
        <div className="applications-heading">
          <h2 id="applications-extensions-heading">Installed extensions</h2>
          {installed.length > 0 && (
            <span className="applications-count">
              {installed.length} installed
            </span>
          )}
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
                        onClick={() => onOpenApplication(app)}
                      >
                        Open
                      </Button>
                    ) : (
                      <Button
                        primary
                        disabled={disabled}
                        onClick={() => onToggleEnabled(app)}
                      >
                        Enable
                      </Button>
                    )}
                    {renderMoreActions(app)}
                  </div>
                </div>
                <div className="applications-row-footer">
                  <span className="applications-state">
                    <span
                      className={`applications-pill${
                        snapshotError
                          ? ' is-muted'
                          : app.enabled
                            ? ' is-enabled'
                            : ' is-disabled'
                      }`}
                    >
                      {snapshotError
                        ? 'Status unavailable'
                        : app.enabled
                          ? 'Enabled'
                          : 'Disabled'}
                    </span>
                    {!snapshotError && ' · Private storage'}
                  </span>
                  {renderDesktopOption(extensionAppId(app.id), app.title)}
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
            <div className="applications-empty-body">
              <p className="applications-empty-title">
                {loading
                  ? 'Loading applications…'
                  : snapshotError
                    ? 'Application list unavailable'
                    : 'No extensions installed'}
              </p>
              {!loading && !snapshotError && (
                <p className="applications-meta">
                  Install sandboxed AXV extension packages from this host. Each
                  extension runs in an isolated origin with private storage.
                </p>
              )}
            </div>
            {!loading && !snapshotError && !addPanelOpen && (
              <div className="applications-actions">
                <Button onClick={onOpenAddPanel}>Open package installer</Button>
              </div>
            )}
          </div>
        )}
      </section>
      {!!removed.length && (
        <section
          className="applications-section"
          aria-labelledby="applications-removed-heading"
        >
          <div className="applications-heading">
            <h2 id="applications-removed-heading">Uninstalled applications</h2>
            <span className="applications-count">
              {removed.length} retained
            </span>
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
                      onClick={() => onExportData(app)}
                    >
                      Export saved data
                    </Button>
                    {renderMoreActions(app)}
                  </div>
                </div>
                <div className="applications-row-footer">
                  <span className="applications-state">
                    <span className="applications-pill is-muted">
                      {snapshotError ? 'Status unavailable' : 'Uninstalled'}
                    </span>
                    {!snapshotError && ' · Saved data kept'}
                  </span>
                  {renderDesktopOption(extensionAppId(app.id), app.title)}
                </div>
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  );
}
