import { request } from '../../shared/api/http';
import type {
  CloudflareConnection,
  CloudflareInfo,
  CloudflareStatus,
  CloudflareCapabilities,
  CloudflareConfig,
  CloudflareConfigWrite,
  CloudflareWizardRequest,
  CloudflareWizardResult,
  CloudflareHealth,
  CloudflareTraffic,
  CloudflarePublications,
  CloudflarePublication,
  CloudflarePublishRequest,
} from './types';

export function cloudflareURL(info?: CloudflareInfo) {
  if (info?.state !== 'listening' || !info.url) return undefined;
  try {
    const url = new URL(info.url);
    if (
      url.protocol !== 'https:' ||
      url.username ||
      url.password ||
      url.port ||
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

function requireLocal(info: Pick<CloudflareConnection, 'local'>) {
  if (!info.local)
    throw new Error('Manage Cloudflare on the computer running AXV.');
}

export const cloudflareAPI = {
  capabilities: (signal?: AbortSignal) =>
    request<CloudflareCapabilities>('/cloudflare/capabilities', {
      signal,
      cache: 'no-store',
    }),
  config(info: Pick<CloudflareConnection, 'local'>, signal?: AbortSignal) {
    requireLocal(info);
    return request<CloudflareConfig>('/cloudflare/config', {
      signal,
      cache: 'no-store',
    });
  },
  saveConfig(
    info: Pick<CloudflareConnection, 'local'>,
    value: CloudflareConfigWrite,
    signal?: AbortSignal,
  ) {
    requireLocal(info);
    return request<CloudflareConfig>('/cloudflare/config', {
      method: 'PUT',
      body: JSON.stringify(value),
      signal,
    });
  },
  rotateToken(
    info: Pick<CloudflareConnection, 'local'>,
    value: { instanceId: string; expectedRevision: string; token: string },
    signal?: AbortSignal,
  ) {
    requireLocal(info);
    return request<CloudflareConfig>('/cloudflare/token', {
      method: 'PUT',
      body: JSON.stringify(value),
      signal,
    });
  },
  validate(
    info: Pick<CloudflareConnection, 'local'>,
    value: CloudflareWizardRequest,
    signal?: AbortSignal,
  ) {
    requireLocal(info);
    return request<CloudflareWizardResult>('/cloudflare/wizard', {
      method: 'POST',
      body: JSON.stringify(value),
      signal,
    });
  },
  health: (signal?: AbortSignal, force = false) =>
    request<CloudflareHealth>(
      `/cloudflare/health${force ? '?force=true' : ''}`,
      {
        signal,
        cache: 'no-store',
      },
    ),
  traffic: (hours: number, signal?: AbortSignal) =>
    request<CloudflareTraffic>(`/cloudflare/traffic?hours=${hours}`, {
      signal,
      cache: 'no-store',
    }),
  publications: (signal?: AbortSignal) =>
    request<CloudflarePublications>('/cloudflare-publications', {
      signal,
      cache: 'no-store',
    }),
  publish: (value: CloudflarePublishRequest, signal?: AbortSignal) =>
    request<CloudflarePublication>('/cloudflare-publications', {
      method: 'POST',
      headers: { 'X-AXV-Host-ID': value.binding.hostId },
      body: JSON.stringify(value),
      signal,
    }),
  unpublish: (record: CloudflarePublication, signal?: AbortSignal) =>
    request<CloudflarePublication>(
      `/cloudflare-publications/${encodeURIComponent(record.id)}/unpublish`,
      {
        method: 'POST',
        headers: { 'X-AXV-Host-ID': record.binding.hostId },
        body: '{}',
        signal,
      },
    ),
  retry: (record: CloudflarePublication, signal?: AbortSignal) =>
    request<CloudflarePublication>(
      `/cloudflare-publications/${encodeURIComponent(record.id)}/retry`,
      {
        method: 'POST',
        headers: { 'X-AXV-Host-ID': record.binding.hostId },
        body: '{}',
        signal,
      },
    ),
  async status(signal?: AbortSignal) {
    const info = await request<CloudflareConnection>('/connection', {
      signal,
      cache: 'no-store',
    });
    if (!info.cloudflare)
      throw new Error('Cloudflare status is unavailable. Refresh to retry.');
    return info;
  },
  localStatus(info: Pick<CloudflareConnection, 'local'>, signal?: AbortSignal) {
    requireLocal(info);
    return request<CloudflareStatus>('/cloudflare/status', {
      signal,
      cache: 'no-store',
    });
  },
  setEnabled(
    info: CloudflareConnection,
    enabled: boolean,
    signal?: AbortSignal,
  ) {
    requireLocal(info);
    return request<CloudflareInfo>('/cloudflare', {
      method: 'PUT',
      body: JSON.stringify({ enabled }),
      signal,
    });
  },
};
