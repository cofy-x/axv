import type { DesktopAPI, IconName } from '@cofy-x/axv-nextstep';
import { filesystemApi } from './api';
import type { Entry } from './types';

export function entryIcon(entry: Entry): IconName {
  return entry.kind === 'folder'
    ? 'folder'
    : entry.previewable
      ? 'image'
      : 'document';
}

export function openEntry(entry: Entry, desktop: DesktopAPI) {
  if (entry.kind === 'folder')
    desktop.openApp('files', { folderId: entry.id }, { title: entry.name });
  else if (entry.editable)
    desktop.openApp('notes', { entryId: entry.id }, { title: entry.name });
  else if (entry.previewable)
    desktop.openApp('image', { entryId: entry.id }, { title: entry.name });
  else downloadEntry(entry);
}

export function downloadEntry(entry: Entry) {
  const anchor = document.createElement('a');
  anchor.href = filesystemApi.contentURL(entry.id, true);
  anchor.download = entry.name;
  anchor.click();
}
