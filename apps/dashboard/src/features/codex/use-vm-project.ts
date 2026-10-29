import { desktopDatabase } from '../../shared/api/desktop';
import { useCallback, useRef, useState } from 'react';
import { useDraftNamespace } from '../../shared/drafts/context';
import { useResource } from '../../shared/api/useResource';
import { vmProjectsAPI } from '../project-files/vm-api';
import { codexAPI } from './api';
import {
  launchRejectedBeforeStart,
  VMProjectRequests,
} from './vm-project-requests';

export function useVMProject(hostId: string, projectId: string) {
  const currentNamespace = useDraftNamespace();
  // A mounted manager keeps the store that owns its request receipts.
  const [{ databaseId, clientId }] = useState(() => ({ ...currentNamespace }));
  const [refresh, setRefresh] = useState(0);
  const requests = useRef(
    new VMProjectRequests(
      JSON.stringify([
        'axv-codex-vm-start',
        databaseId,
        clientId,
        hostId,
        projectId,
      ]),
      hostId,
      projectId,
    ),
  );
  const readPending = () => {
    try {
      const request = projectId ? requests.current.read() : undefined;
      if (request?.expectedRunId)
        throw new Error(
          'The saved Codex start request contains an unexpected run. Preserve its recovery data before clearing it.',
        );
      return {
        request,
        error: '',
      };
    } catch (error) {
      return {
        request: undefined,
        error: error instanceof Error ? error.message : String(error),
      };
    }
  };
  const [pending, setPending] = useState(readPending);
  const load = useCallback(
    () =>
      projectId
        ? vmProjectsAPI.binding(projectId, hostId)
        : Promise.resolve(null),
    [hostId, projectId],
  );
  const context = useResource(`${hostId}:${projectId}:${refresh}`, load);

  const create = async () => {
    const current = readPending();
    if (current.error) {
      setPending(current);
      throw new Error(current.error);
    }
    if (pending.request?.requestId !== current.request?.requestId) {
      setPending(current);
      throw new Error(
        'The Codex start request changed in another window. Review the session list and the current request before starting or resolving a conversation.',
      );
    }
    const saved = current.request;
    if (!saved && !context.data)
      throw new Error('Connect to the VM project before starting Codex.');
    const attempt = saved || requests.current.capture(context.data!.binding);
    setPending({ request: attempt, error: '' });
    try {
      const session = await codexAPI.create(
        attempt.requestId,
        undefined,
        {
          binding: attempt.binding,
        },
        databaseId,
      );
      desktopDatabase.assertUnchanged();
      requests.current.complete(attempt.requestId);
      return { session, recovered: saved !== undefined };
    } catch (error) {
      if (launchRejectedBeforeStart(error))
        requests.current.complete(attempt.requestId);
      throw error;
    } finally {
      setPending(readPending());
    }
  };

  return {
    ...context,
    pending: pending.request,
    recoveryError: pending.error,
    create,
    reconnect: () => {
      const current = readPending();
      setPending(current);
      if (current.request || current.error) return;
      setRefresh((value) => value + 1);
    },
  };
}
