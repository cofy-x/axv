import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { Button } from '@cofy-x/axv-nextstep';
import type { AppDefinition, AppWindowProps } from '@cofy-x/axv-nextstep';
import { getEditorSession } from '../../shared/drafts/session';
import type { EditorSession } from '../../shared/drafts/session';
import { useApplicationCatalog } from './catalog';
import { draftKey, ExtensionRuntime, prepareExtensionClose } from './runtime';
import { extensionStore } from './store';
import { extensionAppId } from './types';
import type { ExtensionRecord } from './types';

function exportText(name: string, text: string) {
  const url = URL.createObjectURL(
    new Blob([text], { type: 'application/json' }),
  );
  const link = document.createElement('a');
  link.href = url;
  link.download = name;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function ExtensionWindow(props: AppWindowProps) {
  const catalog = useSyncExternalStore(
    extensionStore.subscribe,
    extensionStore.getSnapshot,
  );
  const { clientId } = useApplicationCatalog();
  if (!catalog.storeId || !clientId)
    return (
      <div className="app-pad">
        <p role={catalog.error ? 'alert' : 'status'}>
          {catalog.error || 'Loading applications…'}
        </p>
        <Button onClick={() => void extensionStore.refresh()}>Retry</Button>
      </div>
    );
  return (
    <ExtensionDraftSession
      {...props}
      storeId={catalog.storeId}
      clientId={clientId}
    />
  );
}

function ExtensionDraftSession(
  props: AppWindowProps & { storeId: string; clientId: string },
) {
  const [scope] = useState(() => ({
    storeId: props.storeId,
    clientId: props.clientId,
  }));
  const [session, setSession] = useState<EditorSession>();
  const [error, setError] = useState('');
  useEffect(() => {
    let cancelled = false;
    void getEditorSession(
      {
        databaseId: `extensions:${scope.storeId}`,
        clientId: scope.clientId,
      },
      props.window.id,
    )
      .then((next) => {
        if (!cancelled) setSession(next);
      })
      .catch((failure: unknown) => {
        if (!cancelled)
          setError(
            failure instanceof Error ? failure.message : String(failure),
          );
      });
    return () => {
      cancelled = true;
    };
  }, [scope, props.window.id]);
  if (!session)
    return (
      <p className="extension-message" role={error ? 'alert' : 'status'}>
        {error || 'Preparing application recovery…'}
      </p>
    );
  return (
    <ExtensionSessionWindow
      {...props}
      scope={scope}
      draftId={session.draftId}
      recoveryId={session.recoveryId}
    />
  );
}

function ExtensionSessionWindow({
  window: win,
  desktop,
  visible = false,
  interactive = false,
  storeId,
  scope,
  draftId,
  recoveryId,
}: AppWindowProps & {
  storeId: string;
  clientId: string;
  scope: { storeId: string; clientId: string };
  draftId: string;
  recoveryId?: string;
}) {
  const catalog = useSyncExternalStore(
    extensionStore.subscribe,
    extensionStore.getSnapshot,
  );
  const appId = win.appId.slice('extension.'.length);
  const app = catalog.apps.find((candidate) => candidate.id === appId);
  const initialApp = useRef(scope.storeId === storeId ? app : undefined);
  const [runtime] = useState(
    () =>
      new ExtensionRuntime(
        appId,
        win.id,
        scope.storeId,
        scope.clientId,
        draftKey(scope.storeId, scope.clientId, draftId),
        undefined,
        recoveryId
          ? draftKey(scope.storeId, scope.clientId, recoveryId)
          : undefined,
      ),
  );
  const snapshot = useSyncExternalStore(runtime.subscribe, runtime.getSnapshot);
  const [actionError, setActionError] = useState('');
  const changedStore = scope.storeId !== storeId;
  const unavailable = changedStore
    ? 'The application store changed. Export any draft, then close and reopen this window.'
    : !app?.installed
      ? 'This application is uninstalled. Its saved data and window are retained.'
      : !app.enabled
        ? 'This application is disabled. Enable it in Applications to continue.'
        : '';
  const stale =
    !!snapshot.digest &&
    !!app &&
    (snapshot.digest !== app.digest || snapshot.generation !== app.generation);
  const blocked = !!unavailable || stale || snapshot.phase !== 'ready';
  const run = async (work: () => Promise<void> | void) => {
    setActionError('');
    try {
      await work();
    } catch (error) {
      setActionError(error instanceof Error ? error.message : String(error));
    }
  };

  useEffect(() => {
    const unregister = runtime.register();
    let cancelled = false;
    // StrictMode's trial mount must not create a server session.
    queueMicrotask(() => {
      const initial = initialApp.current;
      if (
        !cancelled &&
        initial?.installed &&
        initial.enabled &&
        runtime.getSnapshot().phase === 'initial'
      )
        void runtime.start(initial);
    });
    return () => {
      cancelled = true;
      unregister();
    };
  }, [runtime]);
  useEffect(
    () =>
      desktop.beforeClose(win.id, () =>
        prepareExtensionClose(desktop, runtime),
      ),
    [desktop, runtime, win.id],
  );
  useEffect(() => {
    runtime.setLifecycle(visible && !blocked, interactive && !blocked);
  }, [runtime, visible, interactive, blocked]);
  useEffect(() => {
    if (unavailable || stale) runtime.suspend();
  }, [runtime, unavailable, stale]);
  useEffect(() => {
    if (!runtime.hasUnsaved()) return;
    const onUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    globalThis.addEventListener('beforeunload', onUnload);
    return () => globalThis.removeEventListener('beforeunload', onUnload);
  }, [
    runtime,
    snapshot.draft,
    snapshot.recoveryError,
    snapshot.rejectedDraft,
    snapshot.saving,
  ]);

  const reload = async () => {
    if (!app || unavailable) return;
    const current = runtime.getSnapshot();
    if (current.draft || current.recoveryError || current.rejectedDraft) {
      const answer = await desktop.dialog({
        title: 'Reload application?',
        message: current.rejectedDraft
          ? 'The latest draft was rejected and is still in the application. Cancel to correct it there, or discard it to reload.'
          : 'Reconnect with the draft and its original saved revision, or discard it. Keeping a draft does not overwrite newer saved data.',
        buttons: [
          { label: 'Cancel', value: 'cancel', cancel: true },
          { label: 'Discard draft', value: 'discard' },
          ...(current.draft && !current.rejectedDraft
            ? [{ label: 'Keep draft', value: 'keep', default: true }]
            : []),
        ],
      });
      if (answer?.button === 'keep') {
        await runtime.start(app, true);
        return;
      }
      if (answer?.button !== 'discard') return;
      runtime.discard();
    }
    await runtime.start(app);
  };
  return (
    <div className="extension-window">
      <div className="app-toolbar">
        <Button
          disabled={
            !snapshot.draft ||
            snapshot.saving ||
            !!snapshot.rejectedDraft ||
            blocked
          }
          onClick={() => void run(() => runtime.save())}
        >
          Save
        </Button>
        <Button
          disabled={!!unavailable || snapshot.saving}
          onClick={() => void run(reload)}
        >
          Reload
        </Button>
        {snapshot.draft || snapshot.recoveryError || snapshot.rejectedDraft ? (
          <Button
            onClick={() =>
              void run(() => {
                const raw = runtime.exportDraft();
                if (!raw) throw new Error('No recoverable draft is available.');
                exportText(`${appId}-draft.json`, raw);
              })
            }
          >
            Export draft
          </Button>
        ) : null}
        <span className="toolbar-caption" role="status">
          {snapshot.saving
            ? 'Saving…'
            : snapshot.rejectedDraft
              ? 'Draft not protected'
              : snapshot.draft
                ? 'Unsaved changes'
                : snapshot.phase === 'ready'
                  ? 'Saved'
                  : 'Application'}
        </span>
      </div>
      {(actionError ||
        snapshot.rejectedDraft ||
        snapshot.error ||
        catalog.error) && (
        <p className="app-error extension-message" role="alert">
          {actionError ||
            snapshot.rejectedDraft ||
            snapshot.error ||
            catalog.error}
        </p>
      )}
      {unavailable && (
        <div className="extension-message">
          <p>{unavailable}</p>
          <Button onClick={() => desktop.openApp('applications')}>
            Applications
          </Button>
        </div>
      )}
      {!unavailable && stale && (
        <p className="extension-message" role="status">
          A different application version is active. Reload this window to
          continue; unsaved data is retained.
        </p>
      )}
      {!unavailable && snapshot.phase === 'recovery' && (
        <div className="app-pad extension-recovery">
          <p>
            A draft was recovered for this application. Restore it with its
            original saved revision, or discard it.
          </p>
          {snapshot.draft && snapshot.draft.digest !== app?.digest && (
            <p>
              The installed application has changed since this draft was
              written.
            </p>
          )}
          <div className="app-actions">
            <Button
              disabled={snapshot.recoveryError}
              onClick={() =>
                void run(async () => {
                  if (app) await runtime.start(app, true);
                })
              }
            >
              Restore draft
            </Button>
            <Button
              onClick={() =>
                void run(async () => {
                  runtime.discard();
                  if (app) await runtime.start(app);
                })
              }
            >
              Discard draft
            </Button>
          </div>
        </div>
      )}
      {!unavailable && snapshot.phase === 'initial' && (
        <div className="app-pad">
          <Button onClick={() => void run(reload)}>Open application</Button>
        </div>
      )}
      {snapshot.phase === 'loading' && (
        <p className="extension-message" role="status">
          Opening application…
        </p>
      )}
      {snapshot.contentUrl &&
        !unavailable &&
        !stale &&
        (snapshot.phase === 'loading' || snapshot.phase === 'ready') && (
          <div
            className="extension-frame-container"
            style={{
              visibility:
                blocked && snapshot.phase !== 'loading' ? 'hidden' : undefined,
            }}
          >
            <iframe
              key={snapshot.epoch}
              src={snapshot.contentUrl}
              title={`${app?.title || appId} content`}
              sandbox="allow-scripts"
              referrerPolicy="no-referrer"
              tabIndex={interactive && !blocked ? 0 : -1}
              inert={!interactive || blocked || undefined}
              onLoad={(event) => {
                const frame = event.currentTarget.contentWindow;
                if (frame)
                  runtime.connect(
                    frame,
                    () => desktop.focusWindow(win.id),
                    snapshot.epoch,
                  );
              }}
            />
            {!interactive && (
              <div
                className="extension-focus-capture"
                aria-hidden="true"
                onPointerDown={() => desktop.focusWindow(win.id)}
              />
            )}
          </div>
        )}
      <div className="ns-status">
        {app?.title || appId}
        {snapshot.version ? ` · ${snapshot.version}` : ''} · Private application
        data
      </div>
    </div>
  );
}

export function createExtensionDefinitions(
  apps: Pick<ExtensionRecord, 'id' | 'title' | 'icon' | 'installed'>[],
): AppDefinition[] {
  return apps.map((app) => ({
    id: extensionAppId(app.id),
    title: app.title,
    icon: app.icon || 'document',
    defaultPinned: false,
    defaultDesktop: false,
    launchable: app.installed,
    launch: (desktop) => {
      desktop.openApp(extensionAppId(app.id));
    },
    windowKey: () => 'main',
    defaultSize: { width: 560, height: 470 },
    minSize: { width: 300, height: 280 },
    component: ExtensionWindow,
  }));
}
