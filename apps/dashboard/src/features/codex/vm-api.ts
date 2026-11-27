import { request } from '../../shared/api/http';
import { codexAPI, isVMSession } from './api';
import type { CodexInfo, CodexMetadataPatch, CodexSessionRecord } from './api';

export interface VMTarget {
  hostId: string;
  machineId: string;
  machineName: string;
}

export interface GuestCodexInstallStatus {
  state: 'idle' | 'running' | 'succeeded' | 'failed';
  message?: string;
  updatedAt?: string;
}

export interface GuestCodexInfo extends CodexInfo {
  authMode: 'oauth' | 'api_key' | 'proxy' | 'none';
  hostProxyAvailable: boolean;
  fullAutoDefault: boolean;
  install: GuestCodexInstallStatus;
}

function readSignal(signal?: AbortSignal) {
  const timeout = AbortSignal.timeout(15_000);
  return signal ? AbortSignal.any([signal, timeout]) : timeout;
}

function basePath(machineId: string) {
  return `/compute/machines/${encodeURIComponent(machineId)}/guest/codex`;
}

function hostHeaders(hostId: string) {
  return { 'X-AXV-Host-ID': hostId };
}

export function tagVMSession(
  session: CodexSessionRecord,
  target: VMTarget,
): CodexSessionRecord {
  return {
    ...session,
    target: 'vm',
    hostId: target.hostId,
    machineId: target.machineId,
    machineName: target.machineName,
    fullAuto: true,
  };
}

export function sessionVMTarget(session: CodexSessionRecord): VMTarget | null {
  if (!isVMSession(session)) return null;
  return {
    hostId: session.hostId!,
    machineId: session.machineId!,
    machineName: session.machineName || session.machineId!,
  };
}

export const vmCodexAPI = {
  info: (target: VMTarget, signal?: AbortSignal) =>
    request<GuestCodexInfo>(`${basePath(target.machineId)}/info`, {
      headers: hostHeaders(target.hostId),
      signal: readSignal(signal),
    }),
  install: (target: VMTarget) =>
    request<GuestCodexInstallStatus>(`${basePath(target.machineId)}/install`, {
      method: 'POST',
      headers: hostHeaders(target.hostId),
    }),
  installStatus: (target: VMTarget, signal?: AbortSignal) =>
    request<GuestCodexInstallStatus>(`${basePath(target.machineId)}/install`, {
      headers: hostHeaders(target.hostId),
      signal: readSignal(signal),
    }),
  list: async (target: VMTarget, signal?: AbortSignal) => {
    const result = await request<{ sessions: CodexSessionRecord[] }>(
      `${basePath(target.machineId)}/sessions`,
      { headers: hostHeaders(target.hostId), signal: readSignal(signal) },
    );
    const list = Array.isArray(result.sessions) ? result.sessions : [];
    return {
      sessions: list.map((session) => tagVMSession(session, target)),
    };
  },
  get: async (target: VMTarget, id: string, signal?: AbortSignal) => {
    const result = await request<{ session: CodexSessionRecord }>(
      `${basePath(target.machineId)}/sessions/${encodeURIComponent(id)}`,
      { headers: hostHeaders(target.hostId), signal: readSignal(signal) },
    );
    return tagVMSession(result.session, target);
  },
  getRun: async (
    target: VMTarget,
    id: string,
    runId: string,
    signal?: AbortSignal,
  ) => {
    const result = await request<{ session: CodexSessionRecord }>(
      `${basePath(target.machineId)}/sessions/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}`,
      { headers: hostHeaders(target.hostId), signal: readSignal(signal) },
    );
    return tagVMSession(result.session, target);
  },
  create: async (target: VMTarget, requestId: string, projectPath?: string) => {
    const result = await request<{ session: CodexSessionRecord }>(
      `${basePath(target.machineId)}/sessions`,
      {
        method: 'POST',
        headers: hostHeaders(target.hostId),
        body: JSON.stringify({ requestId, projectPath }),
      },
    );
    return tagVMSession(result.session, target);
  },
  end: async (target: VMTarget, id: string, runId: string) => {
    const result = await request<{ session: CodexSessionRecord }>(
      `${basePath(target.machineId)}/sessions/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}/end`,
      { method: 'POST', headers: hostHeaders(target.hostId) },
    );
    return tagVMSession(result.session, target);
  },
  reviewActivity: async (
    target: VMTarget,
    id: string,
    runId: string,
    eventId: string,
  ) => {
    const result = await request<{ session: CodexSessionRecord }>(
      `${basePath(target.machineId)}/sessions/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}/activity/review`,
      {
        method: 'POST',
        headers: hostHeaders(target.hostId),
        body: JSON.stringify({ eventId }),
        signal: readSignal(),
      },
    );
    return tagVMSession(result.session, target);
  },
  resume: async (
    target: VMTarget,
    id: string,
    requestId: string,
    expectedRunId: string,
  ) => {
    const result = await request<{ session: CodexSessionRecord }>(
      `${basePath(target.machineId)}/sessions/${encodeURIComponent(id)}/resume`,
      {
        method: 'POST',
        headers: hostHeaders(target.hostId),
        body: JSON.stringify({ requestId, expectedRunId }),
      },
    );
    return tagVMSession(result.session, target);
  },
  update: async (target: VMTarget, id: string, patch: CodexMetadataPatch) => {
    const result = await request<{ session: CodexSessionRecord }>(
      `${basePath(target.machineId)}/sessions/${encodeURIComponent(id)}`,
      {
        method: 'PATCH',
        headers: hostHeaders(target.hostId),
        body: JSON.stringify(patch),
      },
    );
    return tagVMSession(result.session, target);
  },
  terminalURL: (
    target: VMTarget,
    sessionId: string,
    runId: string,
    baseURL: string,
    cols: number,
    rows: number,
  ) => {
    const url = new URL(
      `/api${basePath(target.machineId)}/sessions/${encodeURIComponent(sessionId)}/runs/${encodeURIComponent(runId)}/terminal`,
      baseURL,
    );
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
    url.searchParams.set('cols', String(cols));
    url.searchParams.set('rows', String(rows));
    url.searchParams.set('hostId', target.hostId);
    return url.href;
  },
};

export function codexTargetAPI(session: CodexSessionRecord) {
  const target = sessionVMTarget(session);
  if (!target) {
    return {
      get: (id: string, signal?: AbortSignal) => codexAPI.get(id, signal),
      getRun: (id: string, runId: string, signal?: AbortSignal) =>
        codexAPI.getRun(id, runId, signal),
      end: (id: string, runId: string) => codexAPI.end(id, runId),
      reviewActivity: (id: string, runId: string, eventId: string) =>
        codexAPI.reviewActivity(id, runId, eventId),
      resume: (
        id: string,
        requestId: string,
        expectedRunId: string,
        databaseId?: string,
      ) =>
        databaseId !== undefined
          ? codexAPI.resume(id, requestId, expectedRunId, databaseId)
          : codexAPI.resume(id, requestId, expectedRunId),
      update: (id: string, patch: CodexMetadataPatch) =>
        codexAPI.update(id, patch),
    };
  }
  return {
    get: (id: string, signal?: AbortSignal) =>
      vmCodexAPI.get(target, id, signal),
    getRun: (id: string, runId: string, signal?: AbortSignal) =>
      vmCodexAPI.getRun(target, id, runId, signal),
    end: (id: string, runId: string) => vmCodexAPI.end(target, id, runId),
    reviewActivity: (id: string, runId: string, eventId: string) =>
      vmCodexAPI.reviewActivity(target, id, runId, eventId),
    resume: (id: string, requestId: string, expectedRunId: string) =>
      vmCodexAPI.resume(target, id, requestId, expectedRunId),
    update: (id: string, patch: CodexMetadataPatch) =>
      vmCodexAPI.update(target, id, patch),
  };
}
