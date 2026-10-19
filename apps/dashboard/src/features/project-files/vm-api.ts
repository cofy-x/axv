import { request } from '../../shared/api/http';
import type { ProjectFile } from './api';

export interface VMProject {
  id: string;
  hostId: string;
  kind: 'vm';
  name: string;
  volumeId: string;
  root: string;
  rootIdentity: string;
  revision: number;
  createdAt: string;
}

export interface VMProjectBinding {
  hostId: string;
  projectId: string;
  projectRevision: number;
  volumeId: string;
  volumeRevision: number;
  machineId: string;
  bootId: string;
  instanceId: string;
}

export interface VMProjectContent {
  project: VMProject;
  binding: VMProjectBinding;
  path: string;
  content: string;
  sha256: string;
  size: number;
  modifiedAt: string;
}

const base = '/compute/projects';
const headers = (hostId: string) => ({ 'X-AXV-Host-ID': hostId });
const projectPath = (id: string) => `${base}/${encodeURIComponent(id)}`;

function operation<T>(
  id: string,
  binding: VMProjectBinding,
  action: string,
  values: object,
) {
  return request<T>(`${projectPath(id)}/files/${action}`, {
    method: 'POST',
    headers: headers(binding.hostId),
    body: JSON.stringify({ binding, ...values }),
    signal: AbortSignal.timeout(30000),
  });
}

export const vmProjectsAPI = {
  list: (hostId: string) =>
    request<{ projects: VMProject[] }>(base, { headers: headers(hostId) }),
  register: (
    hostId: string,
    values: {
      requestId: string;
      volumeId: string;
      volumeRevision: number;
      machineId: string;
      root: string;
      name?: string;
    },
  ) =>
    request<{ project: VMProject }>(base, {
      method: 'POST',
      headers: headers(hostId),
      body: JSON.stringify(values),
      signal: AbortSignal.timeout(30000),
    }),
  binding: (id: string, hostId: string) =>
    request<{ project: VMProject; binding: VMProjectBinding }>(
      `${projectPath(id)}/binding`,
      { headers: headers(hostId), signal: AbortSignal.timeout(30000) },
    ),
  files: (id: string, binding: VMProjectBinding, path: string, cursor = '') =>
    operation<{
      project: VMProject;
      binding: VMProjectBinding;
      entries: ProjectFile[];
      nextCursor?: string;
      truncated?: boolean;
    }>(id, binding, 'list', { path, cursor }),
  read: (id: string, binding: VMProjectBinding, path: string) =>
    operation<VMProjectContent>(id, binding, 'read', { path }),
  write: (
    id: string,
    binding: VMProjectBinding,
    path: string,
    content: string,
    expectedSha256: string,
  ) =>
    operation<VMProjectContent>(id, binding, 'write', {
      path,
      content,
      expectedSha256,
    }),
  mkdir: (id: string, binding: VMProjectBinding, path: string) =>
    operation<ProjectFile>(id, binding, 'mkdir', { path }),
};
