import type { AppDefinition } from '@cofy-x/axv-nextstep';
import { ImageViewer } from './ImageViewer';

export const imageViewerApp: AppDefinition = {
  id: 'image',
  title: 'Picture Viewer',
  icon: 'image',
  launchable: false,
  component: ImageViewer,
  windowKey: (params) => params.entryId,
  defaultSize: { width: 480, height: 360 },
  minSize: { width: 260, height: 200 },
};
