import type { BuiltinApplication, ExtensionPackage } from './types';

export interface PackageReview {
  package: ExtensionPackage;
  expectedGeneration: number;
  storeId: string;
}

export interface GroupedBuiltins {
  workstation: BuiltinApplication[];
  system: BuiltinApplication[];
  internal: BuiltinApplication[];
}

const WORKSTATION_ORDER = [
  'codex',
  'project-files',
  'machines',
  'github-review',
  'terminal',
];

const SYSTEM_ORDER = [
  'remote-access',
  'cloudflare',
  'notifications',
  'applications',
  'appearance',
  'welcome',
  'gallery',
];

const BUILTIN_DESCRIPTIONS: Record<string, string> = {
  codex: 'AI coding sessions across the host and virtual machines',
  'project-files': 'Browse and edit files in host and VM projects',
  machines: 'Create, start and inspect Firecracker virtual machines',
  'github-review': 'Inspect pull requests, review findings and VM runs',
  terminal: 'Interactive shell sessions on this workstation',
  'remote-access': 'Private tailnet HTTPS access via Tailscale',
  cloudflare: 'Zero Trust desktop access and app publishing',
  notifications: 'Session alerts, review findings and task updates',
  applications: 'Manage installed extensions and desktop shortcuts',
  appearance: 'Theme, alerts and workstation preferences',
  welcome: 'Workstation overview and quick launch links',
  gallery: 'NEXTSTEP interface component reference',
};

function orderIndex(order: string[], id: string) {
  const index = order.indexOf(id);
  return index === -1 ? order.length : index;
}

function sortByOrder(apps: BuiltinApplication[], order: string[]) {
  return [...apps].sort((left, right) => {
    const rank = orderIndex(order, left.id) - orderIndex(order, right.id);
    return rank !== 0 ? rank : left.title.localeCompare(right.title);
  });
}

export function describeBuiltin(app: BuiltinApplication): string {
  if (!app.launchable) return 'Opened from its owning application';
  return (
    BUILTIN_DESCRIPTIONS[app.id] ??
    (app.kind === 'panel' ? 'Utility panel' : 'Application')
  );
}

export function groupBuiltins(builtins: BuiltinApplication[]): GroupedBuiltins {
  const workstation: BuiltinApplication[] = [];
  const system: BuiltinApplication[] = [];
  const internal: BuiltinApplication[] = [];
  for (const app of builtins) {
    if (!app.launchable) internal.push(app);
    else if (WORKSTATION_ORDER.includes(app.id)) workstation.push(app);
    else system.push(app);
  }
  return {
    workstation: sortByOrder(workstation, WORKSTATION_ORDER),
    system: sortByOrder(system, SYSTEM_ORDER),
    internal: [...internal].sort((a, b) => a.title.localeCompare(b.title)),
  };
}

export function downloadJSON(name: string, value: unknown) {
  const url = URL.createObjectURL(
    new Blob([JSON.stringify(value, null, 2) + '\n'], {
      type: 'application/json',
    }),
  );
  const link = document.createElement('a');
  link.href = url;
  link.download = name;
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function readPackage(value: unknown): ExtensionPackage {
  if (!value || typeof value !== 'object')
    throw new Error('Choose an AXV extension package in JSON format.');
  const candidate = value as Partial<ExtensionPackage>;
  const manifest = candidate.manifest;
  if (
    !manifest ||
    manifest.schemaVersion !== 1 ||
    manifest.apiVersion !== 1 ||
    typeof manifest.id !== 'string' ||
    typeof manifest.title !== 'string' ||
    typeof manifest.version !== 'string' ||
    !Array.isArray(manifest.capabilities) ||
    manifest.capabilities.length !== 1 ||
    manifest.capabilities[0] !== 'storage' ||
    typeof candidate.html !== 'string' ||
    !candidate.html.trim()
  )
    throw new Error(
      'This package needs an API 1 manifest, the storage capability and an HTML document.',
    );
  if (new TextEncoder().encode(candidate.html).length > 512 * 1024)
    throw new Error('The HTML document must be no larger than 512 KiB.');
  if (
    manifest.icon !== undefined &&
    manifest.icon !== 'document' &&
    manifest.icon !== 'checklist'
  )
    throw new Error('The application icon must be document or checklist.');
  return candidate as ExtensionPackage;
}
