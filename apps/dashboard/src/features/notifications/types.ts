export const notificationKinds = [
  ['agent_waiting_approval', 'Agent waiting for approval'],
  ['agent_waiting_input', 'Agent waiting for input'],
  ['agent_succeeded', 'Agent succeeded'],
  ['agent_failed', 'Agent failed'],
  ['agent_interrupted', 'Agent interrupted'],
  ['task_succeeded', 'Task succeeded'],
  ['task_failed', 'Task failed'],
  ['task_cancelled', 'Task cancelled'],
  ['task_interrupted', 'Task interrupted'],
  ['task_unknown', 'Task outcome unknown'],
] as const;

export interface NotificationTarget {
  kind: 'codex' | 'task';
  storeId: string;
  sessionId?: string;
  runId?: string;
  threadId?: string;
  turnId?: string;
  eventId?: string;
  hostId?: string;
  projectId?: string;
  taskId?: string;
  attemptId?: string;
}

export interface NotificationEvent {
  id: string;
  createdAt: string;
  kind: string;
  state: string;
  status:
    | 'pending'
    | 'sending'
    | 'accepted'
    | 'uncertain'
    | 'failed'
    | 'disabled'
    | 'superseded';
  attempts: number;
  nextAttemptAt?: string;
  target: NotificationTarget;
  error?: string;
}

export interface NotificationConfig {
  enabled: boolean;
  tokenConfigured: boolean;
  applicationId: string;
  deviceId: string;
  returnOrigin: string;
  eventTypes: string[];
}

export interface NotificationSnapshot {
  storeId: string;
  revision: number;
  canConfigure: boolean;
  config: NotificationConfig;
  events: NotificationEvent[];
  intake: { gap: boolean; error?: string };
  nextSendAt?: string;
  error?: string;
}

export interface NotificationConfigWrite {
  storeId: string;
  revision: number;
  enabled: boolean;
  tokenAction: 'keep' | 'replace' | 'clear';
  token?: string;
  applicationId: string;
  deviceId: string;
  returnOrigin: string;
  eventTypes: string[];
}

export interface NotificationDetail {
  storeId: string;
  event: NotificationEvent;
  resolution: {
    available: boolean;
    current: boolean;
    state: string;
    target: NotificationTarget;
  };
}

export interface NotificationReference {
  storeId: string;
  notificationId: string;
}
