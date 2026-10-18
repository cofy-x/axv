import type { AppDefinition } from '@cofy-x/axv-nextstep';
import type { FilesystemData } from '../../filesystem/context';
import { Files } from './Files';
import { createFilesMenu } from './menus';

export function createFilesApp(data: FilesystemData): AppDefinition {
  return {
    id: 'files',
    title: 'Files',
    icon: 'folder',
    component: Files,
    windowKey: (params) => params.folderId || 'documents',
    defaultSize: { width: 760, height: 420 },
    minSize: { width: 310, height: 260 },
    menu: (window, desktop) => createFilesMenu(data, window, desktop),
  };
}
