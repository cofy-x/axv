export type RemoteState =
  | 'disabled'
  | 'starting'
  | 'needs-login'
  | 'needs-https'
  | 'ready'
  | 'error'
  | 'stopped';

export interface RemoteInfo {
  enabled: boolean;
  supported: boolean;
  state: RemoteState;
  url?: string;
  message?: string;
}

export interface ConnectionInfo {
  local: boolean;
  remote: RemoteInfo;
}

export interface ConnectionSnapshot {
  info?: ConnectionInfo;
  loading: boolean;
  error?: string;
  updatedAt?: number;
}
