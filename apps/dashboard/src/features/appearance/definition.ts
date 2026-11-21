import type { AppDefinition } from '@cofy-x/axv-nextstep';
import { Appearance } from './Appearance';

export { openRemoteAccessPreferences } from './navigation';

export const appearanceApp: AppDefinition = {
  id: 'appearance',
  title: 'Preferences',
  icon: 'appearance',
  defaultPinned: false,
  component: Appearance,
  defaultSize: { width: 440, height: 450 },
  minSize: { width: 320, height: 320 },
};
