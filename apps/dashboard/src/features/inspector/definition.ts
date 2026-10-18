import type { AppDefinition } from '@cofy-x/axv-nextstep';
import { Inspector } from './Inspector';

export const inspectorApp: AppDefinition = {
  id: 'inspector',
  title: 'Inspector',
  icon: 'info',
  kind: 'panel',
  windowKey: () => 'main',
  component: Inspector,
  defaultSize: { width: 350, height: 510 },
  minSize: { width: 300, height: 260 },
};
