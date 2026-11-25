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
import { PreferencesContext } from '../features/appearance/PreferencesContext';
import { createWindowCommands } from '../shared/desktop/window-commands';
import { WindowCommandsContext } from '../shared/desktop/WindowCommandsContext';
import { createAppRegistry } from './registry';
import { createDesktopIcons } from './desktop-items';
import { useDesktopSession } from './useDesktopSession';
import { DraftNamespaceContext } from '../shared/drafts/context';
import { clientIdentityKey } from './client-identity';
import { useBootstrap } from './useBootstrap';
import type { ReadyDesktop } from './useBootstrap';
import { computeStore } from '../features/machines/compute-store';
import {
  inactiveSubscription,
  usePageVisibility,
} from '../shared/desktop/usePageVisibility';
import {
  closeMissingMachineInspectors,
  createMachineDesktopIcons,
  createMachineDesktopMenu,
} from '../features/machines/desktop-icons';
import { CodexProvider } from '../features/codex/CodexContext';
import { ApplicationCatalogContext } from '../features/extensions/catalog';
import { createExtensionDefinitions } from '../features/extensions/ExtensionWindow';
import { extensionStore } from '../features/extensions/store';
import { composeApplicationDefinitions } from '../features/extensions/registry';
import { takePendingPushOpen } from '../features/notifications/webpush';
import { notificationFromPushMessage } from '../features/notifications/webpush-message';
import {
  notificationLink,
  openNotification,
} from '../features/notifications/definition';

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
  const notificationOpened = useRef(false);
  const pendingPushReference = useRef<{
    storeId: string;
    notificationId: string;
  } | null>(null);
  const getDesktop = useCallback(() => desktopRef.current, []);
  useEffect(() => {
    if (!('serviceWorker' in navigator)) return;
    let mounted = true;
    const open = async (event: MessageEvent) => {
      try {
        const reference = await notificationFromPushMessage(event);
        const desktop = desktopRef.current;
        if (mounted && reference) {
          if (desktop) {
            openNotification(desktop, reference);
            await takePendingPushOpen(reference);
          } else pendingPushReference.current = reference;
        }
      } catch {
        // A worker identity that cannot be verified cannot open a target.
      }
    };
    const received = (event: MessageEvent) => {
      void open(event);
    };
    navigator.serviceWorker.addEventListener('message', received);
    return () => {
      mounted = false;
      navigator.serviceWorker.removeEventListener('message', received);
    };
  }, []);
  const [commands] = useState(createWindowCommands);
  useSyncExternalStore(commands.subscribe, commands.getSnapshot);
  const pageVisible = usePageVisibility();
  const compute = useSyncExternalStore(
    pageVisible ? computeStore.subscribeDesktop : inactiveSubscription,
    computeStore.getDesktopSnapshot,
  );
  useEffect(() => {
    if (pageVisible && desktopRef.current)
      closeMissingMachineInspectors(compute, desktopRef.current);
  }, [compute, pageVisible]);
  const builtins = useMemo(() => createAppRegistry(commands), [commands]);
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
  const machineIcons = useMemo(
    () => createMachineDesktopIcons(compute, getDesktop),
    [compute, getDesktop],
  );
  const icons = useMemo(
    () => [...createDesktopIcons(getDesktop), ...machineIcons],
    [getDesktop, machineIcons],
  );

  return (
    <DraftNamespaceContext.Provider
      value={{ databaseId: bootstrap.databaseId, clientId }}
    >
      <PreferencesContext.Provider value={appearance}>
        <WindowCommandsContext.Provider value={commands}>
          <ApplicationCatalogContext.Provider value={applicationCatalog}>
            <Desktop
              apps={apps}
              icons={icons}
              initialLayout={bootstrap.layout || undefined}
              initialLayoutError={bootstrap.layoutError}
              initialDesktopApplications={{
                terminal: false,
                'project-files': false,
                'remote-access': false,
                appearance: false,
                codex: false,
                machines: false,
                gallery: false,
                applications: false,
                cloudflare: false,
                notifications: false,
              }}
              initialDockOrder={[
                'codex',
                'machines',
                'project-files',
                'terminal',
              ]}
              initialWindows={[{ appId: 'welcome' }]}
              workspaceMenuIcon={
                <img src="/favicon.svg" alt="" width={17} height={17} />
              }
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
                          <Button onClick={() => void retryAuthentication()}>
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
                if (pageVisible)
                  closeMissingMachineInspectors(
                    computeStore.getDesktopSnapshot(),
                    desktop,
                  );
                if (pendingPushReference.current) {
                  openNotification(desktop, pendingPushReference.current);
                  void takePendingPushOpen(pendingPushReference.current).catch(
                    () => {},
                  );
                  pendingPushReference.current = null;
                }
                void takePendingPushOpen()
                  .then((reference) => {
                    if (reference && desktopRef.current === desktop)
                      openNotification(desktop, reference);
                  })
                  .catch(() => {
                    // A retained reference that cannot be verified stays unavailable.
                  });
                if (!notificationOpened.current) {
                  notificationOpened.current = true;
                  const reference = notificationLink(
                    globalThis.location.search,
                  );
                  if (reference) openNotification(desktop, reference);
                }
              }}
              onLayoutChange={onLayoutChange}
              onDesktopMenu={(ids) =>
                createMachineDesktopMenu(machineIcons, ids) ?? []
              }
              onAppearance={() => getDesktop()?.openApp('appearance')}
              onAbout={() => getDesktop()?.openApp('about')}
              onHelp={() => getDesktop()?.openApp('welcome')}
              helpLabel="Welcome"
            />
          </ApplicationCatalogContext.Provider>
        </WindowCommandsContext.Provider>
      </PreferencesContext.Provider>
    </DraftNamespaceContext.Provider>
  );
}
