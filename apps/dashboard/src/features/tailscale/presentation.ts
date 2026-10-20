import type { ConnectionSnapshot, RemoteState } from './types';

export type ConnectionTone = 'off' | 'ready' | 'pending' | 'error' | 'unknown';

const remoteLabels: Record<RemoteState, string> = {
  disabled: 'Off',
  starting: 'Starting',
  'needs-login': 'Sign in',
  'needs-https': 'HTTPS required',
  ready: 'Ready',
  error: 'Needs attention',
  stopped: 'Stopped',
};

export function connectionPresentation(snapshot: ConnectionSnapshot) {
  const { info, error } = snapshot;
  const remote = error
    ? 'Unknown'
    : info
      ? (remoteLabels[info.remote.state] ?? 'Unknown')
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
  return { remote, remoteTone };
}
