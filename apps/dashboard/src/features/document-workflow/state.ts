import { sessionTitle } from '../codex/api';
import type { CodexSessionRecord } from '../codex/api';

interface ContextRun {
  id: string;
  runId: string;
  status: string;
  conversationId?: string;
}

export function contextRunKey(run: Pick<ContextRun, 'id' | 'runId'>) {
  return JSON.stringify([run.id, run.runId]);
}

export function readyContextRun(run: ContextRun) {
  return run.status === 'running' && !!run.conversationId;
}

export function selectedContextRun<T extends ContextRun>(
  runs: readonly T[],
  key: string,
) {
  return runs.find((run) => contextRunKey(run) === key && readyContextRun(run));
}

export function conversationLabel(
  session: CodexSessionRecord,
  sessions: readonly CodexSessionRecord[],
) {
  const title = sessionTitle(session);
  const duplicates = sessions.filter((item) => sessionTitle(item) === title);
  if (duplicates.length < 2) return title;
  let length = 8;
  while (
    length < session.id.length &&
    duplicates.some(
      (item) =>
        item.id !== session.id &&
        item.id.slice(0, length) === session.id.slice(0, length),
    )
  )
    length += 4;
  return `${title} · ${session.projectPath} · ${session.id.slice(0, length)}`;
}
