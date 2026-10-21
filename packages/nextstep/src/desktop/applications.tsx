import { PixelIcon } from '../controls';
import { validAppId } from '../model';
import type { AppDefinition, DesktopAPI } from '../types';

export const missingApplicationMessage =
  'This application is not installed or is currently disabled.';

/** A launcher restores existing windows before considering whether a new one is available. */
export function launchApplication(
  apps: AppDefinition[],
  api: DesktopAPI,
  appId: string,
): void {
  if (!validAppId(appId)) return;
  const existing = [...api.windows]
    .reverse()
    .find((win) => win.appId === appId);
  if (existing) {
    api.focusWindow(existing.id);
    return;
  }
  const app = apps.find((candidate) => candidate.id === appId);
  if (!app || app.unavailable !== undefined || app.launchable === false) {
    void api.dialog({
      title: 'Application unavailable',
      message:
        app?.unavailable ??
        'This application is not currently available from a launcher.',
      buttons: [{ label: 'OK', value: 'ok', default: true }],
    });
    return;
  }
  if (app.launch) {
    Promise.resolve()
      .then(() => app.launch?.(api))
      .catch((error: unknown) => {
        void api.dialog({
          title: `Cannot open ${app.title}`,
          message:
            error instanceof Error
              ? error.message
              : 'The application could not be opened.',
          buttons: [{ label: 'OK', value: 'ok', default: true }],
        });
      });
  } else api.openApp(appId);
}

export function UnavailableApplication({ message }: { message: string }) {
  return (
    <div className="ns-unavailable-application" role="status">
      <PixelIcon name="document" size={42} />
      <strong>Application unavailable</strong>
      <p>{message}</p>
      <p>Your window and its saved state are preserved.</p>
    </div>
  );
}

/** Missing definitions are presentation fallbacks, never executable registry entries. */
export function resolveApplication(
  apps: AppDefinition[],
  appId: string,
): AppDefinition {
  return (
    apps.find((app) => app.id === appId) ?? {
      id: appId,
      title: appId,
      icon: 'document',
      launchable: false,
      unavailable: missingApplicationMessage,
      component: () => null,
    }
  );
}
