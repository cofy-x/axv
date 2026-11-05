import type { AppDefinition, DesktopAPI } from '@cofy-x/axv-nextstep';
import { deferWindow } from '../../shared/desktop/defer-window';
import type { NotificationReference } from './types';

const NotificationsPanel = deferWindow(() =>
  import('./NotificationsPanel').then((module) => module.NotificationsPanel),
);
const NotificationWork = deferWindow(() =>
  import('./NotificationWork').then((module) => module.NotificationWork),
);

export const notificationsApp: AppDefinition = {
  id: 'notifications',
  title: 'Notifications',
  icon: 'info',
  kind: 'panel',
  launchable: true,
  defaultPinned: false,
  defaultDesktop: false,
  windowKey: () => 'main',
  component: NotificationsPanel,
  defaultSize: { width: 570, height: 580 },
  minSize: { width: 310, height: 300 },
};

export const notificationWorkApp: AppDefinition = {
  id: 'notification-work',
  title: 'Original work',
  icon: 'info',
  kind: 'panel',
  launchable: false,
  windowKey: (params) => `${params.storeId}:${params.notificationId}`,
  component: NotificationWork,
  defaultSize: { width: 600, height: 480 },
  minSize: { width: 310, height: 280 },
};

export function notificationLink(
  search: string,
): NotificationReference | undefined {
  const raw = new URLSearchParams(search).get('notification');
  if (!raw || raw.length > 240) return undefined;
  const separator = raw.indexOf(':');
  if (separator < 0) return undefined;
  const storeId = raw.slice(0, separator);
  const notificationId = raw.slice(separator + 1);
  if (
    !/^[A-Za-z0-9._-]{1,128}$/.test(storeId) ||
    !/^[A-Za-z0-9._-]{1,160}$/.test(notificationId)
  )
    return undefined;
  return { storeId, notificationId };
}

export function openNotification(
  desktop: DesktopAPI,
  reference: NotificationReference,
) {
  const id = desktop.openApp('notifications');
  desktop.updateWindowState(id, (state) => ({
    ...state,
    notificationTab: 'history',
    notificationReference: reference,
  }));
}
