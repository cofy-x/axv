import type { AppDefinition } from '@cofy-x/axv-nextstep';
import { ApplicationsManager } from './ApplicationsManager';
import './styles.css';

export const applicationsApp: AppDefinition = {
  id: 'applications',
  title: 'Application Manager',
  icon: 'applications',
  defaultPinned: false,
  defaultDesktop: true,
  windowKey: () => 'main',
  component: ApplicationsManager,
  defaultSize: { width: 660, height: 560 },
  minSize: { width: 320, height: 300 },
};
