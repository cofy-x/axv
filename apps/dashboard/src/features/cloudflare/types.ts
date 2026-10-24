import type { VMProjectBinding } from '../project-files/vm-api';

export type CloudflareState =
  'disabled' | 'starting' | 'listening' | 'error' | 'stopped';

export interface CloudflareInfo {
  enabled: boolean;
  configured: boolean;
  state: CloudflareState;
  url?: string;
  message?: string;
}

export interface CloudflareConnection {
  local: boolean;
  cloudflare: CloudflareInfo;
}

export interface CloudflareStatus extends CloudflareInfo {
  instanceId: string;
  listen?: string;
  error?: string;
}

export interface CloudflareSnapshot {
  info?: CloudflareConnection;
  loading: boolean;
  error?: string;
  updatedAt?: number;
}

export interface CloudflareCapabilities {
  configure: boolean;
  publish: boolean;
  traffic: boolean;
  health: boolean;
}

export interface CloudflareAccessSettings {
  listen: string;
  origin: string;
  issuer: string;
  audience: string;
  ownerSubject: string;
}

export interface CloudflareAccountSettings {
  accountId: string;
  zoneId: string;
  tunnelId: string;
  domain: string;
  listen: string;
  audience: string;
}

export interface CloudflareConfig {
  instanceId: string;
  revision: string;
  access: CloudflareAccessSettings & { enabled: boolean };
  account: CloudflareAccountSettings & {
    configured: boolean;
    hasToken: boolean;
  };
}

export interface CloudflareConfigWrite {
  instanceId: string;
  expectedRevision: string;
  access: CloudflareAccessSettings;
  account: CloudflareAccountSettings;
  tokenAction: 'keep' | 'replace' | 'clear';
  token?: string;
}

export type CloudflareWizardStep =
  'access' | 'token' | 'account' | 'zone' | 'tunnel' | 'domain';

export interface CloudflareChoice {
  id: string;
  name: string;
}

export interface CloudflareWizardResult {
  ok: boolean;
  message: string;
  accounts?: CloudflareChoice[];
  zones?: CloudflareChoice[];
  tunnels?: (CloudflareChoice & { status: string; remoteConfig: boolean })[];
  zoneName?: string;
  warnings?: string[];
}

export interface CloudflareWizardRequest {
  step: CloudflareWizardStep;
  token?: string;
  accountId?: string;
  zoneId?: string;
  tunnelId?: string;
  domain?: string;
  access?: CloudflareAccessSettings;
}

export interface CloudflareHealth {
  state: 'unconfigured' | 'healthy' | 'degraded' | 'error';
  checkedAt: string;
  message: string;
  tunnelName?: string;
  tunnelStatus?: string;
}

export interface CloudflareTraffic {
  supported: boolean;
  reason?: string;
  since: string;
  until: string;
  requests: number;
  bytes: number;
  cachedRequests: number;
  series: {
    time: string;
    requests: number;
    bytes: number;
    cachedRequests: number;
  }[];
}

export interface CloudflarePublishTarget {
  binding: VMProjectBinding;
  port: number;
}

export interface CloudflarePublishRequest extends CloudflarePublishTarget {
  instanceId: string;
  requestId: string;
  name: string;
  domain: string;
  visibility: 'owner' | 'public';
}

export interface CloudflarePublication {
  id: string;
  name: string;
  generation: string;
  domain: string;
  hostname: string;
  port: number;
  binding: VMProjectBinding;
  visibility: 'owner' | 'public';
  state:
    | 'provisioning'
    | 'published'
    | 'unavailable'
    | 'removing'
    | 'error'
    | 'removed';
  url?: string;
  error?: string;
  requestId: string;
  phase: string;
  cleanupPending: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface CloudflarePublications {
  instanceId: string;
  domain?: string;
  isolation?: {
    mode: 'dedicated-site' | 'public-suffix';
    domain: string;
    maxOwnerSessionSeconds: number;
    uniqueHostnames: boolean;
    retiredSitesReusable: boolean;
  };
  supported: boolean;
  reason?: string;
  publications: CloudflarePublication[];
}
