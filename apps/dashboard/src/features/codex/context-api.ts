import { request } from '../../shared/api/http';

export interface ContextBatch {
  id: string;
  requestId: string;
  sessionId: string;
  runId: string;
  providerThreadId: string;
  status: 'pending' | 'accepted' | 'failed' | 'unknown' | 'cancelled';
  snapshots: {
    id: string;
    entryId: string;
    revision: number;
    name: string;
    sha256: string;
  }[];
  createdAt: string;
  updatedAt: string;
  providerTurnId?: string;
  error?: string;
}

export const contextStatusLabel: Record<ContextBatch['status'], string> = {
  pending: 'Queued for next message',
  accepted: 'Delivered',
  failed: 'Not delivered',
  unknown: 'Delivery unconfirmed',
  cancelled: 'Cancelled',
};

const endpoint = (sessionId: string, runId: string) =>
  `/host/codex/sessions/${sessionId}/runs/${runId}/context`;

export const contextAPI = {
  list: (sessionId: string, runId: string, signal?: AbortSignal) =>
    request<{ batches: ContextBatch[] }>(endpoint(sessionId, runId), {
      signal,
    }),
  attach: (
    sessionId: string,
    runId: string,
    requestId: string,
    snapshotIds: string[],
  ) =>
    request<{ batch: ContextBatch }>(endpoint(sessionId, runId), {
      method: 'POST',
      body: JSON.stringify({ requestId, snapshotIds }),
    }),
  cancel: (sessionId: string, runId: string, batchId: string) =>
    request<{ batch: ContextBatch }>(
      `${endpoint(sessionId, runId)}/${batchId}`,
      { method: 'DELETE' },
    ),
};
