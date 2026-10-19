export type RemoteState =
  | 'disabled'
  | 'starting'
  | 'needs-login'
  | 'needs-https'
  | 'ready'
  | 'error'
  | 'stopped';

export type HostTailscaleState =
  | 'not-installed'
  | 'unavailable'
  | 'connected'
  | 'disconnected'
  | 'needs-login';

export interface ConnectionInfo {
  local: boolean;
  remote: { state: RemoteState; url?: string; message?: string };
  host: {
    state: HostTailscaleState;
    installed: boolean;
    daemonAvailable?: boolean;
    backendState?: string;
    hostname?: string;
    ips?: string[];
    message?: string;
  };
}

export interface ConnectionSnapshot {
  info?: ConnectionInfo;
  loading: boolean;
  error?: string;
  updatedAt?: number;
}
