import {
  useCallback,
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
import { getClientId } from './api';
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
  return (
    <CodexProvider>
      <DesktopApp />
    </CodexProvider>
  );
}

function DesktopApp() {
  const session = useDesktopSession();
  const {
    bootstrap,
    bootError,
    boot,
    filesystem,
    appearance,
    connection,
    persistenceError,
    onLayoutChange,
    flushLayout,
  } = session;
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
      clientId: getClientId(),
      builtins: builtins.map((app) => ({
        id: app.id,
        title: app.title,
        icon: app.icon,
        launchable: app.launchable !== false,
        kind: app.kind || 'application',
      })),
    }),
    [builtins],
  );
  const machineIcons = createMachineDesktopIcons(compute, getDesktop);
  const icons = [
    ...createDesktopIcons(filesystem, getDesktop),
    ...machineIcons,
  ];

  if (!bootstrap)
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
              <p>Check that AXV is running, then retry.</p>
              <Button primary onClick={() => void boot()}>
                Retry
              </Button>
            </div>
          )}
        </section>
      </main>
    );

  return (
    <DraftNamespaceContext.Provider
      value={{ databaseId: bootstrap.databaseId, clientId: getClientId() }}
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
              {persistenceError && (
                <div className="persistence-warning" role="alert">
                  <span>{persistenceError}</span>
                  <Button onClick={() => void flushLayout()}>Retry</Button>
                </div>
              )}
            </ApplicationCatalogContext.Provider>
          </WindowCommandsContext.Provider>
        </PreferencesContext.Provider>
      </FilesystemContext.Provider>
    </DraftNamespaceContext.Provider>
  );
}
