import { request } from '../../shared/api/http';
import type { ConnectionInfo } from './types';

export function canAuthorize(info?: Pick<ConnectionInfo, 'local' | 'remote'>) {
  return info?.local === true && info.remote.state === 'needs-login';
}

export function readyURL(info?: ConnectionInfo) {
  if (info?.remote.state !== 'ready' || !info.remote.url) return undefined;
  try {
    const url = new URL(info.remote.url);
    if (
      url.protocol !== 'https:' ||
      !url.hostname.endsWith('.ts.net') ||
      url.port ||
      url.username ||
      url.password ||
      url.search ||
      url.hash ||
      url.pathname !== '/' ||
      url.href !== `${url.origin}/`
    )
      return undefined;
    return url.origin;
  } catch {
    return undefined;
  }
}

export const connectionAPI = {
  status: (signal?: AbortSignal) =>
    request<ConnectionInfo>('/connection', { signal, cache: 'no-store' }),
  async authorizationURL(
    info: Pick<ConnectionInfo, 'local' | 'remote'>,
    signal?: AbortSignal,
  ) {
    // Remote viewers must never request an enrollment capability.
    if (!canAuthorize(info)) return undefined;
    const status = await request<{ state: string; authUrl?: string }>(
      '/remote/status',
      { signal, cache: 'no-store' },
    );
    if (status.state !== 'needs-login' || !status.authUrl) return undefined;
    let url: URL;
    try {
      url = new URL(status.authUrl);
    } catch {
      throw new Error(
        'The authorization address is unavailable. Refresh to retry.',
      );
    }
    if (
      url.protocol !== 'https:' ||
      url.hostname !== 'login.tailscale.com' ||
      url.port ||
      url.username ||
      url.password ||
      !/^https:\/\/login\.tailscale\.com\/a\/[A-Za-z0-9]+$/.test(status.authUrl)
    )
      throw new Error(
        'The authorization address is unavailable. Refresh to retry.',
      );
    return url.href;
  },
};
