import type { DesktopAPI } from '@cofy-x/axv-nextstep';

export function projectFileLocation(file = '') {
  return {
    folder: file.split('/').slice(0, -1).join('/'),
    file,
    cursor: '',
    reading: !!file,
  };
}

/** Locate the result even when the project's browser window is already open. */
export function openProjectFile(
  desktop: Pick<DesktopAPI, 'openApp' | 'updateWindowState'>,
  projectId: string,
  file: string,
  title?: string,
) {
  const id = desktop.openApp('project-files', { projectId, file }, { title });
  desktop.updateWindowState(id, projectFileLocation(file));
  return id;
}
