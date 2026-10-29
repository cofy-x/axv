import type { AppDefinition } from '@cofy-x/axv-nextstep';
import { deferWindow } from '../../shared/desktop/defer-window';

const Inspector = deferWindow(() =>
  import('./Inspector').then((module) => module.Inspector),
);

export const inspectorApp: AppDefinition = {
  id: 'inspector',
  title: 'Inspector',
  icon: 'info',
  kind: 'panel',
  initialPlacement: 'beside-active',
  defaultPinned: false,
  windowKey: () => 'main',
  component: Inspector,
  defaultSize: { width: 350, height: 510 },
  minSize: { width: 300, height: 260 },
};
