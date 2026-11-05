import type { AppDefinition } from '@cofy-x/axv-nextstep';
import { Welcome, About } from './Welcome';

export const welcomeApp: AppDefinition = {
  id: 'welcome',
  title: 'Welcome',
  icon: 'computer',
  defaultPinned: false,
  component: Welcome,
  defaultSize: { width: 550, height: 570 },
  minSize: { width: 330, height: 300 },
};

export const aboutApp: AppDefinition = {
  id: 'about',
  title: 'About AXV',
  icon: 'info',
  launchable: false,
  component: About,
  defaultSize: { width: 360, height: 400 },
  minSize: { width: 300, height: 310 },
};
