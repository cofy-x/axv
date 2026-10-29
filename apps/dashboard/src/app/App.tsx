import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';
import { Button, Desktop } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { FilesystemContext } from '../filesystem/context';
import { trashEntries } from '../filesystem/commands';
import { PreferencesContext } from '../features/appearance/PreferencesContext';
import { runCommand } from '../shared/desktop/dialogs';
import { createWindowCommands } from '../shared/desktop/window-commands';
import { WindowCommandsContext } from '../shared/desktop/WindowCommandsContext';
import { createAppRegistry } from './registry';
import { createDesktopIcons, createDesktopMenu } from './desktop-items';
import { useDesktopSession } from './useDesktopSession';
import { DraftNamespaceContext } from '../shared/drafts/context';
import { clientIdentityKey } from './client-identity';
import { useBootstrap } from './useBootstrap';
import type { ReadyDesktop } from './useBootstrap';
import { computeStore } from '../features/machines/compute-store';
import {
  createMachineDesktopIcons,
  createMachineDesktopMenu,
} from '../features/machines/desktop-icons';
import { CodexProvider } from '../features/codex/CodexContext';
import { ApplicationCatalogContext } from '../features/extensions/catalog';
import { createExtensionDefinitions } from '../features/extensions/ExtensionWindow';
import { extensionStore } from '../features/extensions/store';
import { composeApplicationDefinitions } from '../features/extensions/registry';

export function App() {
  const { ready, bootError, boot } = useBootstrap();
  if (!ready)
    return (
      <main className="startup-screen">
        <section
          className="startup-panel"
          aria-label="AXV startup"
          data-failed={bootError ? true : undefined}
        >
          <div className="startup-title">AXV</div>
          <div className="startup-content">
            <div className="startup-computer" aria-hidden="true">
              <img
                src="/startup-computer.svg"
                width="120"
                height="100"
                alt=""
              />
              <span className="startup-cursor" />
              <span className="startup-power" />
            </div>
            <div className="startup-message">
              <p className="startup-status" role="status">
                {bootError ? 'Unable to open workspace' : 'Opening workspace…'}
              </p>
              <p className="startup-description">Personal cloud workstation</p>
            </div>
          </div>
          {bootError && (
            <div className="startup-error">
              <p role="alert">{bootError}</p>
              <Button primary onClick={() => void boot()}>
                Retry
              </Button>
            </div>
          )}
        </section>
      </main>
    );
  return (
    <CodexProvider>
      <DesktopApp {...ready} />
    </CodexProvider>
  );
}

function DesktopApp({ bootstrap, clientId }: ReadyDesktop) {
  const {
    filesystem,
    appearance,
    connection,
    persistenceError,
    onLayoutChange,
    flushLayout,
    databaseError,
    authenticationError,
    retryAuthentication,
  } = useDesktopSession({ bootstrap, clientId });
  const [identityError, setIdentityError] = useState('');
  useEffect(() => {
    const check = () => {
      try {
        if (localStorage.getItem(clientIdentityKey) === clientId) return;
      } catch {
        // The current page retains its captured identity and in-memory work.
      }
      setIdentityError(
        'Browser storage changed or is unavailable. This page keeps your current work. Save or download drafts before closing.',
      );
    };
    const onStorage = (event: StorageEvent) => {
      if (event.key === clientIdentityKey || event.key === null) check();
    };
    globalThis.addEventListener('storage', onStorage);
    globalThis.addEventListener('focus', check);
    check();
    return () => {
      globalThis.removeEventListener('storage', onStorage);
      globalThis.removeEventListener('focus', check);
    };
  }, [clientId]);
  const { preferences } = appearance;
  const desktopRef = useRef<DesktopAPI | null>(null);
  const getDesktop = useCallback(() => desktopRef.current, []);
  const [commands] = useState(createWindowCommands);
  useSyncExternalStore(commands.subscribe, commands.getSnapshot);
  const compute = useSyncExternalStore(
    computeStore.subscribe,
    computeStore.getSnapshot,
  );
  const builtins = useMemo(
    () => createAppRegistry(filesystem, commands),
    [filesystem, commands],
  );
  const extensions = useSyncExternalStore(
    extensionStore.subscribe,
    extensionStore.getSnapshot,
  );
  const apps = useMemo(
    () =>
      composeApplicationDefinitions(
        builtins,
        createExtensionDefinitions(extensions.definitionApps),
      ),
    [builtins, extensions.definitionApps],
  );
  const applicationCatalog = useMemo(
    () => ({
      clientId,
      builtins: builtins.map((app) => ({
        id: app.id,
        title: app.title,
        icon: app.icon,
        launchable: app.launchable !== false,
        kind: app.kind || 'application',
      })),
    }),
    [builtins, clientId],
  );
  const machineIcons = createMachineDesktopIcons(compute, getDesktop);
  const icons = [
    ...createDesktopIcons(filesystem, getDesktop),
    ...machineIcons,
  ];

  return (
    <DraftNamespaceContext.Provider
      value={{ databaseId: bootstrap.databaseId, clientId }}
    >
      <FilesystemContext.Provider value={filesystem}>
        <PreferencesContext.Provider value={appearance}>
          <WindowCommandsContext.Provider value={commands}>
            <ApplicationCatalogContext.Provider value={applicationCatalog}>
              <Desktop
                apps={apps}
                isShortcutAvailable={(shortcut) => {
                  const id =
                    shortcut.params.entryId || shortcut.params.folderId;
                  return (
                    !id ||
                    filesystem.entries.some(
                      (entry) => entry.id === id && !entry.trashedAt,
                    )
                  );
                }}
                icons={icons}
                initialLayout={bootstrap.layout || undefined}
                initialLayoutError={bootstrap.layoutError}
                initialDesktopApplications={{
                  notes: false,
                  terminal: false,
                  'project-files': false,
                  'remote-access': false,
                  appearance: false,
                  codex: false,
                  machines: false,
                  gallery: false,
                  applications: false,
                  cloudflare: false,
                }}
                initialDockOrder={[
                  'codex',
                  'machines',
                  'project-files',
                  'terminal',
                  'files',
                  'notes',
                ]}
                initialWindows={[{ appId: 'welcome' }]}
                wallpaper={preferences.wallpaper}
                clock24Hour={preferences.clock24Hour}
                iconSnap={preferences.iconSnap}
                connectionStatus={connection}
                statusOverlay={
                  databaseError ||
                  identityError ||
                  persistenceError ||
                  authenticationError ? (
                    <div className="desktop-warnings">
                      {databaseError && (
                        <div className="persistence-warning" role="alert">
                          <span>
                            {databaseError.code === 'database_changed'
                              ? 'The AXV desktop database changed.'
                              : 'This page can no longer verify its AXV desktop database.'}{' '}
                            Keep this page open to preserve unsaved work.
                          </span>
                          <a
                            className="ns-button"
                            href="/"
                            target="_blank"
                            rel="noopener"
                            title="Open in a new window"
                          >
                            Open current AXV
                          </a>
                        </div>
                      )}
                      {identityError && (
                        <div className="persistence-warning" role="alert">
                          <span>{identityError}</span>
                        </div>
                      )}
                      {authenticationError && !databaseError && (
                        <div className="persistence-warning" role="alert">
                          <span>
                            Authorization is required. Restore access for this
                            browser or installed app, then retry. Keep this page
                            open to preserve drafts.
                          </span>
                          <div className="desktop-warning-actions">
                            <a
                              className="ns-button"
                              href="/"
                              target="_blank"
                              rel="noopener"
                              title="Open in a new window"
                            >
                              Open AXV
                            </a>
                            <Button
                              onClick={() =>
                                void retryAuthentication().catch(() => {})
                              }
                            >
                              Retry
                            </Button>
                          </div>
                        </div>
                      )}
                      {persistenceError &&
                        !databaseError &&
                        !authenticationError && (
                          <div className="persistence-warning" role="alert">
                            <span>{persistenceError}</span>
                            <Button onClick={() => void flushLayout()}>
                              Retry
                            </Button>
                          </div>
                        )}
                    </div>
                  ) : null
                }
                onReady={(desktop) => {
                  desktopRef.current = desktop;
                }}
                onLayoutChange={onLayoutChange}
                onTrashIcons={(ids) => {
                  const desktop = getDesktop();
                  if (desktop)
                    void runCommand(desktop, () =>
                      trashEntries(filesystem, desktop, ids),
                    );
                }}
                onDesktopMenu={(ids, desktop) =>
                  createMachineDesktopMenu(machineIcons, ids) ||
                  createDesktopMenu(filesystem, ids, desktop, icons)
                }
                onAppearance={() => getDesktop()?.openApp('appearance')}
                onAbout={() => getDesktop()?.openApp('about')}
                onHelp={() => getDesktop()?.openApp('welcome')}
                helpLabel="Welcome"
              />
            </ApplicationCatalogContext.Provider>
          </WindowCommandsContext.Provider>
        </PreferencesContext.Provider>
      </FilesystemContext.Provider>
    </DraftNamespaceContext.Provider>
  );
}
