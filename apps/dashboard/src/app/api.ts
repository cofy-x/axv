import type { DesktopLayout } from '@cofy-x/axv-nextstep';
import type { Entry } from '../filesystem/types';
import type { Preferences } from '../features/appearance/preferences';
import { request } from '../shared/api/http';

export interface Bootstrap {
  databaseId: string;
  layoutError?: string;
  entries: Entry[];
  preferences: Preferences;
  layout: DesktopLayout | null;
}

let browserId: string | undefined;

export function getClientId(): string {
  if (browserId) return browserId;
  try {
    const key = 'nextstep-browser-id';
    browserId = localStorage.getItem(key) || crypto.randomUUID();
    localStorage.setItem(key, browserId);
  } catch {
    browserId = crypto.randomUUID();
  }
  return browserId;
}

export const desktopApi = {
  bootstrap: () =>
    request<Bootstrap>(
      `/bootstrap?clientId=${encodeURIComponent(getClientId())}`,
    ),
  health: () => request('/health'),
  layout: (layout: DesktopLayout) => {
    const body = JSON.stringify({ clientId: getClientId(), layout });
    // Browsers cap the shared keepalive request budget at 64 KiB.
    return request<{ ok: boolean }>('/layout', {
      method: 'PUT',
      body,
      keepalive: new TextEncoder().encode(body).byteLength <= 60_000,
    });
  },
};
