import type { DesktopAPI } from '@cofy-x/axv-nextstep';

// The stable opener scopes transient navigation to one desktop instance.
const remoteAccessRequests = new WeakMap<DesktopAPI['openApp'], string>();

export function openRemoteAccessPreferences(desktop: DesktopAPI) {
  const windowId = desktop.openApp('appearance');
  if (windowId) remoteAccessRequests.set(desktop.openApp, windowId);
}

export function consumeRemoteAccessRequest(
  openApp: DesktopAPI['openApp'],
  windowId: string,
) {
  if (remoteAccessRequests.get(openApp) !== windowId) return false;
  remoteAccessRequests.delete(openApp);
  return true;
}
