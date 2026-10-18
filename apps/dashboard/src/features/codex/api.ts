import { request } from '../../shared/api/http';

export interface CodexInfo {
  supported: boolean;
  installed: boolean;
  ready: boolean;
  codexPath?: string;
  codexVersion?: string;
  tmuxPath?: string;
  tmuxVersion?: string;
  reason?: string;
  guidance: string[];
  defaultProjectPath: string;
  configPath: string;
  network: {
    proxySource: 'environment' | 'configuration' | 'system' | 'direct';
    proxy?: string;
  };
}

export interface HostDirectories {
  path: string;
  parentPath: string | null;
  homePath: string;
  directories: { name: string; path: string }[];
  truncated: boolean;
}

export interface CodexSessionRecord {
  id: string;
  runId: string;
  target: 'host';
  projectPath: string;
  title: string;
  archivedAt?: string;
  metadataRevision: number;
  tmuxSession: string;
  tmuxSocket: string;
  attachCommand: string;
  status: 'starting' | 'running' | 'stopping' | 'ended' | 'lost' | 'failed';
  createdAt: string;
  endedAt?: string;
  reason?: string;
  connections: number;
  conversationId?: string;
  continuation: {
    state: 'available' | 'active' | 'unavailable';
    reason?: string;
    activeRunId?: string;
  };
}

export interface CodexProject {
  path: string;
  lastUsedAt: string;
}

export interface CodexMetadataPatch {
  revision: number;
  title?: string;
  archived?: boolean;
}

const root = '/host/codex';

export const codexAPI = {
  info: (signal?: AbortSignal) =>
    request<CodexInfo>(`${root}/info`, { signal }),
  directories: (path?: string, signal?: AbortSignal) =>
    request<HostDirectories>(
      `/host/directories${path ? `?path=${encodeURIComponent(path)}` : ''}`,
      { signal },
    ),
  list: (signal?: AbortSignal) =>
    request<{ sessions: CodexSessionRecord[] }>(`${root}/sessions`, { signal }),
  projects: (signal?: AbortSignal) =>
    request<{ projects: CodexProject[] }>(`${root}/projects`, { signal }),
  get: async (id: string, signal?: AbortSignal) =>
    (
      await request<{ session: CodexSessionRecord }>(
        `${root}/sessions/${encodeURIComponent(id)}`,
        { signal },
      )
    ).session,
  getRun: async (id: string, runId: string, signal?: AbortSignal) =>
    (
      await request<{ session: CodexSessionRecord }>(
        `${root}/sessions/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}`,
        { signal },
      )
    ).session,
  create: async (requestId: string, projectPath?: string) =>
    (
      await request<{ session: CodexSessionRecord }>(`${root}/sessions`, {
        method: 'POST',
        body: JSON.stringify({ requestId, projectPath }),
      })
    ).session,
  end: async (id: string, runId: string) =>
    (
      await request<{ session: CodexSessionRecord }>(
        `${root}/sessions/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}/end`,
        { method: 'POST' },
      )
    ).session,
  resume: async (id: string, requestId: string, expectedRunId: string) =>
    (
      await request<{ session: CodexSessionRecord }>(
        `${root}/sessions/${encodeURIComponent(id)}/resume`,
        { method: 'POST', body: JSON.stringify({ requestId, expectedRunId }) },
      )
    ).session,
  update: async (id: string, patch: CodexMetadataPatch) =>
    (
      await request<{ session: CodexSessionRecord }>(
        `${root}/sessions/${encodeURIComponent(id)}`,
        { method: 'PATCH', body: JSON.stringify(patch) },
      )
    ).session,
};

export function sessionIsLive(session: CodexSessionRecord) {
  return session.status === 'starting' || session.status === 'running';
}

export function sessionCanEnd(session: CodexSessionRecord) {
  return sessionIsLive(session) || session.status === 'stopping';
}

export function sessionCanContinue(session: CodexSessionRecord) {
  return !sessionCanEnd(session) && session.continuation.state === 'available';
}

export function otherActiveRun(session: CodexSessionRecord) {
  const active = session.continuation.activeRunId;
  return session.continuation.state === 'active' && active !== session.runId
    ? active
    : undefined;
}

export function projectName(path: string) {
  return path.split(/[\\/]/).filter(Boolean).at(-1) || path;
}

export function sessionTitle(session: CodexSessionRecord) {
  return session.title || projectName(session.projectPath);
}

export function sessionWindowTitle(session: CodexSessionRecord) {
  const title = `Codex · ${sessionTitle(session)}`;
  const final = { ended: 'Ended', lost: 'Lost', failed: 'Failed' };
  return session.status in final
    ? `${title} · ${final[session.status as keyof typeof final]} (${session.runId.slice(0, 8)})`
    : title;
}
