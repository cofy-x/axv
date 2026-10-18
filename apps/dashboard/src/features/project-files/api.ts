import { APIError, request } from '../../shared/api/http';
import type { Entry } from '../../filesystem/types';

export interface Project {
  id: string;
  kind: 'host';
  name: string;
  rootPath: string;
  createdAt: string;
}

export interface ProjectFile {
  name: string;
  path: string;
  kind: 'file' | 'folder' | 'symlink';
  size: number;
  modifiedAt: string;
}

export interface ProjectContent {
  projectId: string;
  path: string;
  name: string;
  content: string;
  sha256: string;
  size: number;
}

export interface HostDirectories {
  path: string;
  parentPath: string | null;
  homePath: string;
  directories: { name: string; path: string }[];
  truncated: boolean;
}

export const projectsAPI = {
  directories: (path?: string, signal?: AbortSignal) =>
    request<HostDirectories>(
      `/host/directories${path ? `?path=${encodeURIComponent(path)}` : ''}`,
      { signal },
    ),
  list: () => request<{ projects: Project[] }>('/projects'),
  register: (rootPath: string) =>
    request<{ project: Project }>('/projects', {
      method: 'POST',
      body: JSON.stringify({ rootPath }),
    }),
  files: (id: string, path: string, cursor = '') =>
    request<{
      project: Project;
      entries: ProjectFile[];
      nextCursor?: string;
      truncated?: boolean;
    }>(`/projects/${id}/files?${new URLSearchParams({ path, cursor })}`),
  read: (id: string, path: string) =>
    request<ProjectContent>(
      `/projects/${id}/file?${new URLSearchParams({ path })}`,
    ),
  download: async (id: string, path: string) => {
    const response = await fetch(
      `/api/projects/${id}/download?${new URLSearchParams({ path })}`,
    );
    if (!response.ok) {
      const error = await response
        .json()
        .catch(() => ({ error: response.statusText, code: 'download_failed' }));
      throw new APIError(error.error, response.status, error.code);
    }
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = path.split('/').at(-1) || 'download';
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  },
  import: (
    id: string,
    values: {
      requestId: string;
      path: string;
      sha256: string;
      parentId?: string;
    },
  ) =>
    request<{ entry: Entry }>(`/projects/${id}/import`, {
      method: 'POST',
      body: JSON.stringify(values),
    }),
  export: (
    id: string,
    values: {
      requestId: string;
      snapshotId: string;
      path: string;
    },
  ) =>
    request<{ path: string; sha256: string }>(`/projects/${id}/export`, {
      method: 'POST',
      body: JSON.stringify(values),
    }),
};
