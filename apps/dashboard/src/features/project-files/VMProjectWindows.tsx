import { useCallback, useEffect, useRef } from 'react';
import type { RefObject } from 'react';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { deferWindow } from '../../shared/desktop/defer-window';
import { useDraftNamespace } from '../../shared/drafts/context';

type FileGuard = () => {
  busy: boolean;
  dirty: boolean;
  keep: () => boolean;
  discard: () => boolean;
};

export interface VMProjectTaskProps extends AppWindowProps {
  recoveryKey: string;
}

export interface VMProjectFileProps extends VMProjectTaskProps {
  registerFileGuard: (guard: FileGuard) => () => void;
}

const VMProjectFiles = deferWindow(() =>
  import('./VMProjectFiles').then((module) => module.VMProjectFiles),
);
const VMProjectTasks = deferWindow(() =>
  import('./VMProjectTasks').then((module) => module.VMProjectTasks),
);

function readRecovery(key: string) {
  try {
    return sessionStorage.getItem(key);
  } catch {
    return undefined;
  }
}

function useRecoveryUnloadGuard(
  key: string,
  guard?: RefObject<FileGuard | undefined>,
) {
  useEffect(() => {
    const protectPage = (event: BeforeUnloadEvent) => {
      const file = guard?.current?.();
      if (file ? file.busy || file.dirty : readRecovery(key) !== null) {
        event.preventDefault();
        event.returnValue = '';
      }
    };
    globalThis.addEventListener('beforeunload', protectPage);
    return () => globalThis.removeEventListener('beforeunload', protectPage);
  }, [guard, key]);
}

export function VMProjectTaskWindow(props: AppWindowProps) {
  const { databaseId, clientId } = useDraftNamespace();
  const recoveryKey = JSON.stringify([
    'axv-vm-task-submission',
    databaseId,
    clientId,
    props.window.params.hostId || '',
    props.window.params.vmTasksProjectId,
  ]);
  useRecoveryUnloadGuard(recoveryKey);
  return <VMProjectTasks {...props} recoveryKey={recoveryKey} />;
}

export function VMProjectFileWindow(props: AppWindowProps) {
  const { window, desktop } = props;
  const { beforeClose, dialog } = desktop;
  const { databaseId, clientId } = useDraftNamespace();
  const recoveryKey = JSON.stringify([
    'axv-vm-project-draft',
    databaseId,
    clientId,
    window.params.hostId || '',
    window.params.vmProjectId,
  ]);
  const fileGuard = useRef<FileGuard | undefined>(undefined);
  useRecoveryUnloadGuard(recoveryKey, fileGuard);
  const registerFileGuard = useCallback((guard: FileGuard) => {
    fileGuard.current = guard;
    return () => {
      if (fileGuard.current === guard) fileGuard.current = undefined;
    };
  }, []);

  useEffect(() => {
    return beforeClose(window.id, async () => {
      const file = fileGuard.current?.();
      if (file?.busy) return false;
      const recovery = file ? null : readRecovery(recoveryKey);
      if (!file?.dirty && recovery === null) return true;
      const answer = await dialog({
        title: 'Close VM project?',
        message:
          'Unsaved changes remain in this tab’s recovery storage. Discard removes that recovery copy.',
        buttons: [
          { label: 'Cancel', value: 'cancel', cancel: true },
          { label: 'Keep Draft', value: 'keep', default: true },
          { label: 'Discard', value: 'discard' },
        ],
      });
      const current = fileGuard.current?.();
      if (current?.busy) return false;
      if (answer?.button === 'keep') return current?.keep() ?? true;
      if (answer?.button !== 'discard') return false;
      if (current) return current.discard();
      if (recovery === undefined || readRecovery(recoveryKey) !== recovery)
        return false;
      try {
        sessionStorage.removeItem(recoveryKey);
        return true;
      } catch {
        await dialog({
          title: 'Could not discard draft',
          message: 'The recovery copy remains associated with this project.',
          buttons: [{ label: 'OK', value: 'ok', default: true }],
        });
        return false;
      }
    });
  }, [beforeClose, dialog, recoveryKey, window.id]);

  return (
    <VMProjectFiles
      {...props}
      recoveryKey={recoveryKey}
      registerFileGuard={registerFileGuard}
    />
  );
}
