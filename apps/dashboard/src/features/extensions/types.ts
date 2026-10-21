import type { IconName } from '@cofy-x/axv-nextstep';

export type ExtensionIcon = 'document' | 'checklist';

export interface ExtensionManifest {
  schemaVersion: 1;
  id: string;
  title: string;
  icon?: ExtensionIcon;
  version: string;
  apiVersion: 1;
  capabilities: ['storage'];
}

export interface ExtensionPackage {
  manifest: ExtensionManifest;
  html: string;
}

export interface ExtensionRecord {
  id: string;
  title: string;
  icon?: ExtensionIcon;
  version: string;
  apiVersion: 1;
  capabilities: string[];
  digest: string;
  generation: number;
  enabled: boolean;
  installed: boolean;
}

export interface ExtensionData {
  value: unknown;
  revision: number;
}

export interface ExtensionCatalog {
  storeId: string;
  apps: ExtensionRecord[];
}

export interface ExtensionDraft {
  value: unknown;
  expectedRevision: number;
  digest: string;
}

export interface BuiltinApplication {
  id: string;
  title: string;
  icon: IconName;
  launchable: boolean;
  kind: string;
}

export const extensionAppId = (id: string) => `extension.${id}`;
