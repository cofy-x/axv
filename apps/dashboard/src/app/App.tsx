import {
  useCallback,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';
import { Button, Desktop, PixelIcon } from '@cofy-x/axv-nextstep';
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
import {
  CodexProvider,
  useCodexAvailability,
} from '../features/codex/CodexContext';

export function App() {
  return (
    <CodexProvider>
      <DesktopApp />
    </CodexProvider>
  );
}

function DesktopApp() {
  const codex = useCodexAvailability();
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
  const apps = useMemo(
    () =>
      createAppRegistry(filesystem, commands, codex.info?.installed === true),
    [filesystem, commands, codex.info?.installed],
  );
  const machineIcons = createMachineDesktopIcons(compute, getDesktop);
  const icons = [
    ...createDesktopIcons(
      filesystem,
      getDesktop,
      codex.info?.installed === true,
    ),
    ...machineIcons,
  ];

  if (!bootstrap)
    return (
      <div className="startup-screen">
        <div className="startup-panel">
          <PixelIcon name="computer" size={64} />
          <strong>{document.title}</strong>
          {bootError ? (
            <>
              <p role="alert">{bootError}</p>
              <p>Check that AXV is running, then retry.</p>
              <Button primary onClick={() => void boot()}>
                Retry
              </Button>
            </>
          ) : (
            <p>Starting AXV…</p>
          )}
        </div>
      </div>
    );

  return (
    <DraftNamespaceContext.Provider
      value={{ databaseId: bootstrap.databaseId, clientId: getClientId() }}
    >
      <FilesystemContext.Provider value={filesystem}>
        <PreferencesContext.Provider value={appearance}>
          <WindowCommandsContext.Provider value={commands}>
            <Desktop
              apps={apps}
              isShortcutAvailable={(shortcut) => {
                const id = shortcut.params.entryId || shortcut.params.folderId;
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
              initialWindows={[
                { appId: 'welcome' },
                {
                  appId: 'files',
                  params: { folderId: 'documents' },
                  title: 'Documents',
                },
              ]}
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
          </WindowCommandsContext.Provider>
        </PreferencesContext.Provider>
      </FilesystemContext.Provider>
    </DraftNamespaceContext.Provider>
  );
}
