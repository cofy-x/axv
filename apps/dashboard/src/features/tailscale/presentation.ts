import type {
  ConnectionSnapshot,
  HostTailscaleState,
  RemoteState,
} from './types';

export type ConnectionTone = 'off' | 'ready' | 'pending' | 'error' | 'unknown';

const remoteLabels: Record<RemoteState, string> = {
  disabled: 'Off',
  starting: 'Starting',
  'needs-login': 'Sign in',
  'needs-https': 'Setup required',
  ready: 'Ready',
  error: 'Error',
  stopped: 'Stopped',
};

const hostLabels: Record<HostTailscaleState, string> = {
  'not-installed': 'Not detected',
  unavailable: 'Unavailable',
  connected: 'Connected',
  disconnected: 'Disconnected',
  'needs-login': 'Sign in',
};

export function connectionPresentation(snapshot: ConnectionSnapshot) {
  const { info, error } = snapshot;
  const remote = error
    ? 'Unknown'
    : info
      ? (remoteLabels[info.remote.state] ?? 'Unknown')
      : 'Checking…';
  const host = error
    ? 'Unknown'
    : info
      ? (hostLabels[info.host.state] ?? 'Unknown')
      : 'Checking…';
  const remoteTone: ConnectionTone = error
    ? 'unknown'
    : info?.remote.state === 'ready'
      ? info.remote.message
        ? 'pending'
        : 'ready'
      : info?.remote.state === 'error'
        ? 'error'
        : info?.remote.state === 'disabled' || info?.remote.state === 'stopped'
          ? 'off'
          : 'pending';
  const hostTone: ConnectionTone = error
    ? 'unknown'
    : info?.host.state === 'connected'
      ? 'ready'
      : info?.host.state === 'unavailable'
        ? 'unknown'
        : info?.host.state === 'needs-login'
          ? 'pending'
          : 'off';
  return { remote, host, remoteTone, hostTone };
}
