import type { AppDefinition, DesktopAPI } from '@cofy-x/axv-nextstep';
import { deferWindow } from '../../shared/desktop/defer-window';
import type { CloudflarePublishTarget } from './types';

const CloudflarePanel = deferWindow(() =>
  import('./CloudflarePanel').then((module) => module.CloudflarePanel),
);

export const cloudflareApp: AppDefinition = {
  id: 'cloudflare',
  title: 'Cloudflare',
  icon: 'cloud-tunnel',
  kind: 'panel',
  launchable: true,
  defaultPinned: false,
  defaultDesktop: true,
  windowKey: () => 'main',
  component: CloudflarePanel,
  defaultSize: { width: 620, height: 560 },
  minSize: { width: 300, height: 290 },
};

export function openCloudflarePublishing(
  desktop: DesktopAPI,
  target: CloudflarePublishTarget,
) {
  const id = desktop.openApp('cloudflare');
  desktop.updateWindowState(id, (current) => ({
    ...current,
    cloudflareTab: 'publishing',
    cloudflarePublishRequest: {
      id: crypto.randomUUID(),
      binding: { ...target.binding },
      port: target.port,
    },
  }));
  return id;
}
