import { requestDesktop as request } from '../shared/api/desktop';

export interface DocumentSnapshotInfo {
  id: string;
  databaseId: string;
  entryId: string;
  revision: number;
  name: string;
  sha256: string;
  createdAt: string;
}

export interface DocumentSnapshot extends DocumentSnapshotInfo {
  content: string;
}

export interface DocumentLink {
  id: string;
  entryId: string;
  kind: 'project' | 'machine' | 'conversation';
  projectId?: string;
  hostId?: string;
  machineId?: string;
  conversationId?: string;
}

export type DocumentTarget = Omit<DocumentLink, 'id' | 'entryId'>;

export interface DocumentOrigin {
  projectId: string;
  path: string;
  sha256: string;
  createdAt: string;
}

export const documentsAPI = {
  origins: (entryId: string) =>
    request<{ origins: DocumentOrigin[] }>(`/documents/${entryId}/origins`),
  getSnapshot: (id: string) =>
    request<{ snapshot: DocumentSnapshot }>(`/document-snapshots/${id}`),
  links: (entryId: string) =>
    request<{ links: DocumentLink[] }>(`/documents/${entryId}/links`),
  related: (target: Record<string, string>) =>
    request<{ links: DocumentLink[] }>(
      `/document-links?${new URLSearchParams(target)}`,
    ),
  link: (entryId: string, target: DocumentTarget) =>
    request<{ link: DocumentLink }>(`/documents/${entryId}/links`, {
      method: 'POST',
      body: JSON.stringify(target),
    }),
  unlink: (id: string) =>
    request<{ ok: boolean }>(`/document-links/${id}`, { method: 'DELETE' }),
  snapshots: (entryId: string) =>
    request<{ snapshots: DocumentSnapshotInfo[] }>(
      `/documents/${entryId}/snapshots`,
    ),
  snapshot: (entryId: string, revision: number) =>
    request<{ snapshot: DocumentSnapshot }>(`/documents/${entryId}/snapshots`, {
      method: 'POST',
      body: JSON.stringify({ revision }),
    }),
};
