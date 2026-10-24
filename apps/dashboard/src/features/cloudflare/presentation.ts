import type { CloudflareSnapshot, CloudflareState } from './types';

const labels: Record<CloudflareState, string> = {
  disabled: 'Off',
  starting: 'Starting',
  listening: 'Listening locally',
  error: 'Needs attention',
  stopped: 'Stopped',
};

export function cloudflarePresentation(snapshot: CloudflareSnapshot) {
  if (snapshot.error) return { label: 'Unknown', tone: 'unknown' };
  const status = snapshot.info?.cloudflare;
  if (!status) return { label: 'Checking…', tone: 'unknown' };
  if (!status.configured && status.state !== 'error')
    return { label: 'Setup required', tone: 'off' };
  return {
    label: labels[status.state] ?? 'Unknown',
    tone:
      status.state === 'error'
        ? 'error'
        : status.state === 'disabled' || status.state === 'stopped'
          ? 'off'
          : 'pending',
  };
}
