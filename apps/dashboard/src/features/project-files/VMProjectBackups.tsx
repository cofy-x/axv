import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Field, IconButton, Select } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { useResource } from '../../shared/api/useResource';
import { useDraftNamespace } from '../../shared/drafts/context';
import { machinesApi } from '../machines/api';
import { vmProjectsAPI } from './vm-api';
import {
  projectBackups,
  backupActionLabel,
  backupOperation,
  readBackupRecovery,
  restoredMachineSpec,
  submitBackupAction,
} from './vm-backup-api';
import type {
  BackupAction,
  BackupRecovery,
  ProjectBackup,
} from './vm-backup-api';
import type { Operation } from '../machines/api';
import './project-backups.css';
import { replaceVMRecoveryRecord } from './vm-workflow-recovery';

interface Props {
  projectId: string;
  hostId: string;
  active: boolean;
  interactive: boolean;
  dirty: boolean;
  desktop: AppWindowProps['desktop'];
  onNoticeChange: (notice: '' | 'recovery' | 'error' | 'working') => void;
}

export function VMProjectBackups({
  projectId,
  hostId,
  active,
  interactive,
  dirty,
  desktop,
  onNoticeChange,
}: Props) {
  const { databaseId, clientId } = useDraftNamespace();
  const key = JSON.stringify([
    'axv-project-backup-request',
    databaseId,
    clientId,
    hostId,
    projectId,
  ]);
  const [recovery] = useState(() => {
    let raw: string | null = null;
    try {
      raw = localStorage.getItem(key);
      return {
        pending:
          raw === null ? null : readBackupRecovery(raw, hostId, projectId),
        error: '',
        raw,
      };
    } catch {
      return {
        pending: null,
        raw,
        error:
          'Backup request recovery could not be read. Review the original operation before clearing this browser’s recovery record.',
      };
    }
  });
  const [error, setError] = useState(recovery.error);
  const [recoveryBlocked, setRecoveryBlocked] = useState(!!recovery.error);
  const [pending, setPending] = useState<BackupRecovery | null>(
    recovery.pending,
  );
  const [operation, setOperation] = useState<Operation | null>(null);
  const retainedRaw = useRef(recovery.raw);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const [refresh, setRefresh] = useState(0);
  const [name, setName] = useState('');
  const [backupId, setBackupId] = useState('');
  const [restoredId, setRestoredId] = useState('');
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState('');
  const busyRef = useRef(false);
  const interactiveRef = useRef(interactive);
  interactiveRef.current = interactive;
  const load = useCallback(async () => {
    if (!active) return null;
    const binding = { hostId };
    const [{ machines }, { volumes }, { projects }, { backups }, info] =
      await Promise.all([
        machinesApi.machines(undefined, binding),
        machinesApi.volumes(undefined, binding),
        vmProjectsAPI.list(hostId),
        projectBackups(hostId),
        machinesApi.info(undefined, binding),
      ]);
    return {
      machines,
      volumes: volumes as ProjectBackup[],
      projects,
      backups,
      info,
    };
  }, [hostId, active]);
  const inventory = useResource(
    `${hostId}:${projectId}:backups:${refresh}`,
    load,
  );
  const data = inventory.data;
  const project = data?.projects.find((p) => p.id === projectId);
  const volume = data?.volumes.find((v) => v.id === project?.volumeId);
  const machine = data?.machines.find((m) => m.id === volume?.machineId);
  const supportsCopy = data?.info.providers?.some(
    (provider) =>
      provider.provider === volume?.provider &&
      provider.available &&
      provider.volumeCopy,
  );
  const backups = data?.backups.filter((v) =>
    v.backup?.projects.some((p) => p.id === projectId),
  );
  const backup = backups?.find((v) => v.id === backupId);
  const restored = data?.volumes.filter((v) => v.restoredFrom === backupId);
  const target = restored?.find((v) => v.id === restoredId);
  const targetMachine = data?.machines.find((m) => m.id === target?.machineId);
  const template = data?.machines.find(
    (m) => m.id === backup?.backup?.sourceMachineId,
  );
  const disabled =
    !interactive || busy || !!pending || recoveryBlocked || !!inventory.error;
  const notice =
    recoveryBlocked || (pending && !pending.operationId && !busy)
      ? 'recovery'
      : error ||
          inventory.error ||
          backup?.reason ||
          target?.reason ||
          targetMachine?.reason ||
          targetMachine?.guestReason
        ? 'error'
        : pending || busy
          ? 'working'
          : '';
  useEffect(() => {
    // Suspended inventory may clear locally; keep its unread notice until shown.
    if (active || notice) onNoticeChange(notice);
  }, [active, notice, onNoticeChange]);

  const persist = useCallback(
    async (next: BackupRecovery | null, expected = retainedRaw.current) => {
      if (expected !== retainedRaw.current)
        throw new Error(
          'Backup recovery changed in another window. Reload to review its current request.',
        );
      const raw = next ? JSON.stringify(next) : null;
      await replaceVMRecoveryRecord(key, expected, raw);
      retainedRaw.current = raw;
      if (mounted.current) setPending(next);
      return raw;
    },
    [key],
  );

  useEffect(() => {
    if (!interactive || !active || !pending?.operationId) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const receipt = pending;
    const receiptRaw = retainedRaw.current;
    const poll = async () => {
      try {
        const current = await backupOperation(
          hostId,
          receipt.operationId!,
          AbortSignal.any([controller.signal, AbortSignal.timeout(15000)]),
        );
        if (controller.signal.aborted) return;
        if (
          current.id !== receipt.operationId ||
          current.requestId !== receipt.action.values.requestId
        )
          throw new Error('The host returned another operation identity.');
        setOperation(current);
        setError('');
        if (['succeeded', 'failed', 'cancelled'].includes(current.status)) {
          await persist(null, receiptRaw);
          if (controller.signal.aborted) return;
          setFeedback(
            `${backupActionLabel(receipt.action)} ${current.status}.`,
          );
          if (current.error) setError(current.error);
          setRefresh((value) => value + 1);
          if (current.volumeId && receipt.action.kind === 'backup')
            setBackupId(current.volumeId);
          if (receipt.action.kind === 'restore') {
            setBackupId(receipt.action.backupId);
            if (current.volumeId) setRestoredId(current.volumeId);
          }
          return;
        }
      } catch (error) {
        if (controller.signal.aborted) return;
        setError(error instanceof Error ? error.message : String(error));
      }
      timer = setTimeout(() => void poll(), 1500);
    };
    void poll();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [hostId, interactive, active, pending, persist]);

  async function submit(action: BackupAction) {
    if (
      !interactive ||
      busyRef.current ||
      action.hostId !== hostId ||
      action.projectId !== projectId
    )
      return;
    busyRef.current = true;
    setBusy(true);
    setError('');
    try {
      // Persist the complete captured payload before submission. A reload or
      // changed inventory must not turn an uncertain retry into another action.
      const submittedRaw = await persist({ version: 1, action });
      if (!mounted.current || !interactiveRef.current) return;
      const result = await submitBackupAction(action);
      if (!mounted.current) return;
      if (result.operation) {
        if (result.operation.requestId !== action.values.requestId)
          throw new Error(
            'The host returned an operation for another request. Recover the retained original request.',
          );
        await persist(
          { version: 1, action, operationId: result.operation.id },
          submittedRaw,
        );
        setOperation(result.operation);
      } else await persist(null, submittedRaw);
      if (!mounted.current) return;
      setFeedback(
        result.operation
          ? `${backupActionLabel(action)} accepted. Tracking the original operation.`
          : `${backupActionLabel(action)} completed.`,
      );
      setRefresh((v) => v + 1);
      if (result.volume?.restoredFrom) setRestoredId(result.volume.id);
      if (result.volume?.backup && !result.volume.restoredFrom)
        setBackupId(result.volume.id);
      if (result.project && mounted.current && interactiveRef.current)
        desktop.openApp(
          'project-files',
          {
            vmProjectId: result.project.id,
            hostId: result.project.hostId,
          },
          { title: `VM · ${result.project.name}` },
        );
    } catch (error) {
      if (mounted.current)
        setError(error instanceof Error ? error.message : String(error));
    } finally {
      busyRef.current = false;
      if (mounted.current) setBusy(false);
    }
  }

  function action(
    value: BackupAction extends infer A
      ? A extends BackupAction
        ? Omit<A, 'hostId' | 'projectId'>
        : never
      : never,
  ) {
    return submit({ ...value, hostId, projectId });
  }

  async function releaseRequest() {
    if (!interactive || busyRef.current) return;
    const reviewedRaw = retainedRaw.current;
    const answer = await desktop.dialog({
      title: 'Release backup request?',
      message:
        'First inspect the original operation and storage. It may already have succeeded. Releasing this recovery record allows a separate new request; it does not stop accepted work.',
      buttons: [
        { label: 'Cancel', value: 'cancel', cancel: true },
        { label: 'Release Request', value: 'release' },
      ],
    });
    if (
      answer?.button !== 'release' ||
      !mounted.current ||
      !interactiveRef.current
    )
      return;
    try {
      await persist(null, reviewedRaw);
      setRecoveryBlocked(false);
      setError('');
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    }
  }

  return (
    <section className="vm-project-backups" aria-label="Backup and restore">
      <h2 className="vm-project-workflow-title">Backup and restore</h2>
      <div className="vm-project-backup-name">
        <Field
          label="Name"
          value={name}
          disabled={disabled}
          onChange={(e) => setName(e.target.value)}
        />
        <IconButton
          icon="refresh"
          label="Refresh backups"
          disabled={!interactive || busy}
          onClick={() => setRefresh((v) => v + 1)}
        />
      </div>
      {feedback && <p role="status">{feedback}</p>}
      {Array.from(
        new Set(
          [error, inventory.error]
            .map((reason) => reason?.trim())
            .filter((reason): reason is string => !!reason),
        ),
      ).map((reason) => (
        <p role="alert" key={reason}>
          {reason}
        </p>
      ))}
      {(pending || recoveryBlocked) && (
        <section
          className="vm-project-backup-recovery"
          aria-label="Request recovery"
        >
          {pending && (
            <div role="status">
              <p>
                {pending.operationId
                  ? 'Tracking operation'
                  : 'Retained request'}
                : {backupActionLabel(pending.action)}
                {pending.operationId && (
                  <> · {operation?.status || 'checking'}</>
                )}
              </p>
              {!pending.operationId && (
                <p>Retry uses its original target and payload.</p>
              )}
              {operation?.phase === 'waiting-for-provider' &&
                pending.operationId && (
                  <p>Waiting for the previous disk copy to finish.</p>
                )}
              <details className="vm-project-backup-details">
                <summary>Request Details</summary>
                <p>Request ID: {pending.action.values.requestId}</p>
                {pending.operationId && (
                  <p>
                    Operation {pending.operationId}:{' '}
                    {operation?.status || 'checking'}
                  </p>
                )}
              </details>
            </div>
          )}
          <div className="vm-project-backup-controls">
            {pending && !pending.operationId && (
              <Button
                disabled={!interactive || busy}
                onClick={() => void submit(pending.action)}
              >
                Recover
              </Button>
            )}
            <Button
              disabled={!interactive || busy}
              onClick={() => void releaseRequest()}
            >
              Release
            </Button>
          </div>
          <p>Release removes browser recovery. Accepted work keeps running.</p>
        </section>
      )}
      <div className="vm-project-backup-flow">
        <section
          className="vm-project-backup-step"
          aria-label="Back up project"
        >
          <div className="vm-project-backup-step-heading">
            <h3>1. Back Up</h3>
            {machine && (
              <p>
                {machine.spec.name} · {machine.status}
              </p>
            )}
          </div>
          <p>Whole-volume copy. Save drafts, finish tasks and stop the VM.</p>
          <div className="vm-project-backup-controls">
            <Button
              disabled={
                disabled ||
                !supportsCopy ||
                !machine ||
                machine.status !== 'running' ||
                dirty
              }
              onClick={() =>
                machine &&
                void action({
                  kind: 'stop',
                  machineId: machine.id,
                  values: {
                    requestId: crypto.randomUUID(),
                    revision: machine.revision,
                    action: 'stop',
                  },
                })
              }
            >
              Stop VM
            </Button>
            <Button
              disabled={
                disabled ||
                dirty ||
                !supportsCopy ||
                !name.trim() ||
                !project ||
                !volume ||
                !machine ||
                machine.status !== 'stopped' ||
                machine.desired !== 'stopped'
              }
              onClick={() =>
                project &&
                volume &&
                machine &&
                void action({
                  kind: 'backup',
                  values: {
                    requestId: crypto.randomUUID(),
                    projectRevision: project.revision,
                    volumeRevision: volume.revision,
                    machineId: machine.id,
                    machineRevision: machine.revision,
                    name: name.trim(),
                  },
                })
              }
            >
              Back Up
            </Button>
          </div>
          {dirty && (
            <p>
              Save or explicitly discard the editor draft before stopping this
              VM.
            </p>
          )}
          {data && !supportsCopy && (
            <p>
              The source provider does not currently support offline volume
              copies.
            </p>
          )}
        </section>
        <section className="vm-project-backup-step" aria-label="Restore backup">
          <h3>2. Restore</h3>
          <Select
            label="Backup"
            value={backupId}
            disabled={disabled}
            onChange={(e) => {
              setBackupId(e.target.value);
              setRestoredId('');
            }}
          >
            <option value="">Choose a backup</option>
            {backups?.map((v) => (
              <option key={v.id} value={v.id}>
                {v.name} · {v.status} · {v.id.slice(0, 12)}
              </option>
            ))}
          </Select>
          {backup?.reason && <p role="alert">{backup.reason}</p>}
          <div className="vm-project-backup-controls">
            <Button
              disabled={
                disabled ||
                !name.trim() ||
                backup?.status !== 'ready' ||
                !data?.info.providers?.some(
                  (provider) =>
                    provider.provider === backup.provider &&
                    provider.available &&
                    provider.volumeCopy,
                )
              }
              onClick={() =>
                backup &&
                void action({
                  kind: 'restore',
                  backupId: backup.id,
                  values: {
                    requestId: crypto.randomUUID(),
                    revision: backup.revision,
                    name: name.trim(),
                  },
                })
              }
            >
              Restore
            </Button>
          </div>
          <p>Creates an independent volume. Backup remains immutable.</p>
        </section>
        <section
          className="vm-project-backup-step"
          aria-label="Open restored project"
        >
          <h3>3. Open Project</h3>
          <Select
            label="Restored volume"
            value={restoredId}
            disabled={disabled}
            onChange={(e) => setRestoredId(e.target.value)}
          >
            <option value="">Choose restored storage</option>
            {restored?.map((v) => (
              <option key={v.id} value={v.id}>
                {v.name} · {v.status} · {v.id.slice(0, 12)}
              </option>
            ))}
          </Select>
          {Array.from(
            new Set(
              [
                target?.reason,
                targetMachine?.reason,
                targetMachine?.guestReason,
              ]
                .map((reason) => reason?.trim())
                .filter((reason): reason is string => !!reason),
            ),
          ).map((reason) => (
            <p role="alert" key={reason}>
              {reason}
            </p>
          ))}
          {targetMachine && (
            <p>
              {targetMachine.spec.name} · {targetMachine.status}
            </p>
          )}
          <div className="vm-project-backup-controls">
            <Button
              disabled={
                disabled ||
                !name.trim() ||
                !template ||
                target?.status !== 'ready' ||
                !!target.machineId
              }
              onClick={() =>
                template &&
                target &&
                void action({
                  kind: 'create-vm',
                  backupId,
                  values: {
                    requestId: crypto.randomUUID(),
                    spec: restoredMachineSpec(
                      template.spec,
                      target,
                      `${name.trim()} VM`,
                    ),
                    start: true,
                  },
                })
              }
            >
              Create VM
            </Button>
            <Button
              disabled={
                disabled ||
                !target ||
                !targetMachine?.ready ||
                !targetMachine.guestCapabilities?.includes('project-restore') ||
                !name.trim()
              }
              onClick={() =>
                target &&
                targetMachine &&
                void action({
                  kind: 'bind',
                  backupId,
                  values: {
                    requestId: crypto.randomUUID(),
                    sourceProjectId: projectId,
                    volumeId: target.id,
                    volumeRevision: target.revision,
                    machineId: targetMachine.id,
                    name: name.trim(),
                  },
                })
              }
            >
              Open Project
            </Button>
          </div>
          <p>New verified project; original unchanged.</p>
          {!!backup && !template && (
            <p>
              Create a compatible VM with this restored volume in Virtual
              Machines, then refresh here.
            </p>
          )}
        </section>
      </div>
      <details className="vm-project-backup-details">
        <summary>Details</summary>
        <dl>
          <dt>Source project</dt>
          <dd>{projectId}</dd>
          {volume && (
            <>
              <dt>Source volume</dt>
              <dd>{volume.id}</dd>
            </>
          )}
          {machine && (
            <>
              <dt>Source VM</dt>
              <dd>{machine.id}</dd>
            </>
          )}
          {backup && (
            <>
              <dt>Backup</dt>
              <dd>{backup.id}</dd>
            </>
          )}
          {target && (
            <>
              <dt>Restored volume</dt>
              <dd>{target.id}</dd>
            </>
          )}
          {targetMachine && (
            <>
              <dt>Restored VM</dt>
              <dd>{targetMachine.id}</dd>
            </>
          )}
        </dl>
        {!!backup && <p>Copies preserve original Git/worktree locations.</p>}
      </details>
    </section>
  );
}
