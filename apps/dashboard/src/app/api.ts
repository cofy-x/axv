import type { DesktopLayout } from '@cofy-x/axv-nextstep';
import type { Entry } from '../filesystem/types';
import type { Preferences } from '../features/appearance/preferences';
import { request } from '../shared/api/http';
import { requestDesktop } from '../shared/api/desktop';

export interface Bootstrap {
  databaseId: string;
  layoutError?: string;
  entries: Entry[];
  preferences: Preferences;
  layout: DesktopLayout | null;
}

export const desktopApi = {
  bootstrap: (clientId: string) =>
    request<Bootstrap>(`/bootstrap?clientId=${encodeURIComponent(clientId)}`),
  health: () => request('/health'),
  layout: (clientId: string, layout: DesktopLayout) => {
    const body = JSON.stringify({ clientId, layout });
    // Browsers cap the shared keepalive request budget at 64 KiB.
    return requestDesktop<{ ok: boolean }>('/layout', {
      method: 'PUT',
      body,
      keepalive: new TextEncoder().encode(body).byteLength <= 60_000,
    });
  },
};
