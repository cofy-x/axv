import { request } from '../shared/api/http';
import type { Entry } from './types';

export const filesystemApi = {
  entries: () => request<{ entries: Entry[] }>('/entries'),
  entry: (id: string) =>
    request<{ entry: Entry; content?: string }>(
      `/entries/${encodeURIComponent(id)}`,
    ),
  create: (
    parentId: string,
    name: string,
    kind: Entry['kind'],
    content?: string,
  ) =>
    request<{ entry: Entry }>('/entries', {
      method: 'POST',
      body: JSON.stringify({ parentId, name, kind, content }),
    }),
  patch: (
    id: string,
    values: {
      name?: string;
      parentId?: string;
      content?: string;
      revision: number;
    },
  ) =>
    request<{ entry: Entry }>(`/entries/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      body: JSON.stringify(values),
    }),
  action: (
    id: string,
    action: 'duplicate' | 'trash' | 'restore',
    parentId?: string,
  ) =>
    request<{ entry: Entry }>(`/entries/${encodeURIComponent(id)}/${action}`, {
      method: 'POST',
      body: JSON.stringify(parentId ? { parentId } : {}),
    }),
  remove: (id: string) =>
    request<{ ok: boolean }>(`/entries/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    }),
  emptyTrash: () => request<{ ok: boolean }>('/trash', { method: 'DELETE' }),
  import: async (files: File[], parentId: string) => {
    if (!files.length || files.length > 10)
      throw new Error('Choose between one and ten files per import.');
    if (files.some((file) => file.size > 10 * 1024 * 1024))
      throw new Error('Each file must be no larger than 10 MiB.');
    const body = new FormData();
    body.append('parentId', parentId);
    for (const file of files) body.append('files', file);
    const result = await request<{ entries: Entry[] }>('/import', {
      method: 'POST',
      body,
    });
    return result.entries;
  },
  contentURL: (id: string, download = false) =>
    `/api/entries/${encodeURIComponent(id)}/content?${download ? 'download=1' : 'preview=1'}`,
};
