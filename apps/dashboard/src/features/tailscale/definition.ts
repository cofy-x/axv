import type { AppDefinition } from '@cofy-x/axv-nextstep';
import { TailscalePanel } from './TailscalePanel';

export const tailscaleApp: AppDefinition = {
  id: 'remote-access',
  title: 'Tailscale',
  icon: 'mesh-network',
  kind: 'panel',
  launchable: true,
  defaultPinned: false,
  windowKey: () => 'main',
  component: TailscalePanel,
  defaultSize: { width: 420, height: 470 },
  minSize: { width: 300, height: 290 },
};
