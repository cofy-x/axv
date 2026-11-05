import type { AppDefinition } from '@cofy-x/axv-nextstep';
import { Trash } from './Trash';

export const trashApp: AppDefinition = {
  id: 'trash',
  title: 'Recycler',
  icon: 'trash',
  defaultPinned: false,
  component: Trash,
  defaultSize: { width: 430, height: 300 },
  minSize: { width: 300, height: 220 },
};
