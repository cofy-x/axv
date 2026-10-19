import type { AppDefinition } from '@cofy-x/axv-nextstep';
import { Appearance } from './Appearance';

export const appearanceApp: AppDefinition = {
  id: 'appearance',
  title: 'Preferences',
  icon: 'appearance',
  component: Appearance,
  defaultSize: { width: 440, height: 500 },
  minSize: { width: 320, height: 320 },
};
