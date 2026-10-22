import { useEffect, useRef, useState } from 'react';
import { Button, Field } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { APIError } from '../../shared/api/http';
import { useDraftNamespace } from '../../shared/drafts/context';
import { openVMProjectCodex } from '../codex/definition';
import { vmProjectsAPI } from './vm-api';
import type { VMProject } from './vm-api';
import {
  repositoryURLAllowed,
  vmInitializationAPI,
  validPendingInitialization,
  initializationDirectoryAllowed,
  initializationNameAllowed,
  verifyInitializationReceipt,
} from './vm-init-api';
import type { PendingInitialization } from './vm-init-api';
import { taskFinished, vmTasksAPI } from './vm-task-api';
import type { VMTaskResult } from './vm-task-api';
import { replaceVMRecoveryRecord } from './vm-workflow-recovery';
import './project-initialize.css';

const describe = (error: unknown) =>
  error instanceof Error ? error.message : String(error);

export function VMProjectInitialize({
  desktop,
  interactive,
  target,
  mode,
  onCreated,
}: {
  desktop: DesktopAPI;
  interactive: boolean;
  target: {
    hostId: string;
    volumeId: string;
    volumeRevision: number;
    machineId: string;
    label: string;
  } | null;
  mode: 'create' | 'clone';
  onCreated: () => void;
}) {
  const namespace = useDraftNamespace();
  const recoveryKey = JSON.stringify([
    'axv-vm-project-initialization',
    namespace.databaseId,
    namespace.clientId,
  ]);
  const [recovery] = useState(() => {
    let raw: string | null = null;
    try {
      raw = localStorage.getItem(recoveryKey);
      const value: unknown = raw === null ? null : JSON.parse(raw);
      if (raw !== null && !validPendingInitialization(value))
        throw new Error(
          'The saved project initialization could not be read. Its original browser record was preserved.',
        );
      return { value: value as PendingInitialization | null, error: '', raw };
    } catch (error) {
      return { value: null, error: describe(error), raw };
    }
  });
  const [pending, setPending] = useState(recovery.value);
  const retainedRaw = useRef(recovery.raw);
  const busyRef = useRef(false);
  const [recoveryError, setRecoveryError] = useState(recovery.error);
  const [directory, setDirectory] = useState(recovery.value?.directory || '');
  const [repository, setRepository] = useState(
    recovery.value?.repository || '',
  );
  const [name, setName] = useState(recovery.value?.name || '');
  const [result, setResult] = useState<VMTaskResult | null>(null);
  const [project, setProject] = useState<VMProject | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const mounted = useRef(true);
  const active = useRef(interactive);
  active.current = interactive;
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  async function persist(value: PendingInitialization) {
    const next = JSON.stringify(value);
    await replaceVMRecoveryRecord(recoveryKey, retainedRaw.current, next);
    retainedRaw.current = next;
    if (mounted.current) setPending(value);
  }

  async function submit(saved?: PendingInitialization) {
    if (busyRef.current || !active.current) return;
    busyRef.current = true;
    setBusy(true);
    setError('');
    try {
      if (saved && localStorage.getItem(recoveryKey) !== retainedRaw.current)
        throw new Error(
          'The original recovery record changed. Reopen VM Projects before recovering it.',
        );
      let operation = saved;
      if (!operation) {
        if (!target) throw new Error('Choose a ready retained volume.');
        if (!initializationDirectoryAllowed(directory))
          throw new Error(
            'Choose one new directory name inside the selected volume.',
          );
        if (!initializationNameAllowed(name, directory))
          throw new Error(
            'Choose a project name of 1–80 UTF-8 bytes without leading or trailing spaces or control characters.',
          );
        if (
          mode === 'clone' &&
          (!repository || !repositoryURLAllowed(repository))
        )
          throw new Error(
            'Use a credential-free HTTPS or SSH Git URL without query parameters or fragments. Configure authentication inside the VM.',
          );
        const health = await vmInitializationAPI.health(
          target.hostId,
          target.machineId,
        );
        if (
          !health.ready ||
          health.machineId !== target.machineId ||
          !health.bootId ||
          !health.instanceId
        )
          throw new Error(
            'The selected VM environment could not be captured. Refresh VM Projects before submitting.',
          );
        operation = {
          version: 1,
          bootId: health.bootId,
          instanceId: health.instanceId,
          hostId: target.hostId,
          volumeId: target.volumeId,
          volumeRevision: target.volumeRevision,
          machineId: target.machineId,
          requestId: crypto.randomUUID(),
          rootRequestId: crypto.randomUUID(),
          directory,
          repository: mode === 'clone' ? repository : '',
          name,
        };
        await persist(operation);
      }
      if (!mounted.current || !active.current) return;
      if (!operation.submission) {
        const { project: container } = await vmProjectsAPI.register(
          operation.hostId,
          {
            requestId: operation.rootRequestId,
            volumeId: operation.volumeId,
            volumeRevision: operation.volumeRevision,
            machineId: operation.machineId,
            root: '',
          },
        );
        if (
          container.hostId !== operation.hostId ||
          container.volumeId !== operation.volumeId ||
          container.root !== ''
        )
          throw new Error(
            'The volume workspace receipt does not match the original destination.',
          );
        if (mounted.current) onCreated();
        const { project: bound, binding } = await vmProjectsAPI.binding(
          container.id,
          operation.hostId,
        );
        if (
          bound.id !== container.id ||
          bound.rootIdentity !== container.rootIdentity ||
          binding.hostId !== operation.hostId ||
          binding.projectId !== container.id ||
          binding.projectRevision !== container.revision ||
          binding.volumeId !== operation.volumeId ||
          binding.volumeRevision !== operation.volumeRevision ||
          binding.machineId !== operation.machineId ||
          binding.bootId !== operation.bootId ||
          binding.instanceId !== operation.instanceId
        )
          throw new Error(
            'The selected volume environment changed. The initialization was not redirected.',
          );
        operation = {
          ...operation,
          submission: {
            requestId: operation.requestId,
            binding,
            directory: operation.directory,
            repository: operation.repository,
            name: operation.name,
          },
        };
        await persist(operation);
      }
      if (!mounted.current || !active.current) return;
      const submission = operation.submission!;
      let receipt: VMTaskResult;
      try {
        receipt = await vmTasksAPI.recover(
          submission.binding.projectId,
          submission.binding.hostId,
          submission.requestId,
        );
      } catch (error) {
        if (!(error instanceof APIError) || error.code !== 'not_found')
          throw error;
        if (!mounted.current || !active.current) return;
        receipt = await vmInitializationAPI.submit(submission);
      }
      verifyInitializationReceipt(receipt, submission);
      if (mounted.current)
        setResult((current) =>
          current?.attempt.id === receipt.attempt.id &&
          taskFinished(current.attempt.status)
            ? current
            : receipt,
        );
    } catch (error) {
      if (mounted.current) setError(describe(error));
    } finally {
      busyRef.current = false;
      if (mounted.current) setBusy(false);
    }
  }

  const pollInitialization = !!result && !taskFinished(result.attempt.status);
  useEffect(() => {
    if (!interactive || !pending?.submission || !pollInitialization) return;
    let active = true;
    const controller = new AbortController();
    const submission = pending.submission;
    let timer: ReturnType<typeof setTimeout>;

    async function refresh() {
      try {
        const next = verifyInitializationReceipt(
          await vmTasksAPI.recover(
            submission.binding.projectId,
            submission.binding.hostId,
            submission.requestId,
            controller.signal,
          ),
          submission,
        );
        if (active) {
          setResult((current) =>
            current?.attempt.id === next.attempt.id &&
            taskFinished(current.attempt.status)
              ? current
              : next,
          );
          setError('');
        }
      } catch (error) {
        if (active) setError(describe(error));
      }
      if (active) timer = setTimeout(() => void refresh(), 1500);
    }

    timer = setTimeout(() => void refresh(), 1500);
    return () => {
      active = false;
      controller.abort();
      clearTimeout(timer);
    };
  }, [interactive, pending, pollInitialization]);

  async function finish() {
    if (!pending?.submission || busyRef.current || !active.current) return;
    busyRef.current = true;
    setBusy(true);
    setError('');
    try {
      if (!result)
        throw new Error('Recover the original initialization receipt first.');
      verifyInitializationReceipt(result, pending.submission);
      const { project } = await vmInitializationAPI.finish(pending.submission);
      const root = result.attempt.createdRoot;
      if (
        !root ||
        project.hostId !== pending.hostId ||
        project.volumeId !== pending.volumeId ||
        project.root !== root.root ||
        project.rootIdentity !== root.rootIdentity
      )
        throw new Error(
          'The completed project does not match the original created directory. Its recovery record was preserved.',
        );
      if (mounted.current) {
        setProject(project);
        onCreated();
        if (active.current)
          desktop.openApp(
            'project-files',
            { vmProjectId: project.id, hostId: project.hostId },
            { title: `VM · ${project.name}` },
          );
      }
    } catch (error) {
      if (mounted.current) setError(describe(error));
    } finally {
      busyRef.current = false;
      if (mounted.current) setBusy(false);
    }
  }

  async function dismiss() {
    if (busyRef.current || !active.current) return;
    const answer = await desktop.dialog({
      title: 'Dismiss recovery',
      message:
        'This removes only this browser recovery prompt. Accepted tasks and existing files remain. Review the volume workspace Tasks before submitting another initialization.',
      buttons: [
        { label: 'Cancel', value: 'cancel', cancel: true },
        { label: 'Dismiss', value: 'dismiss' },
      ],
    });
    if (answer?.button !== 'dismiss') return;
    try {
      const raw = localStorage.getItem(recoveryKey);
      if (!active.current || raw !== retainedRaw.current)
        throw new Error(
          'The original recovery record changed. Reopen VM Projects before dismissing it.',
        );
      await replaceVMRecoveryRecord(recoveryKey, retainedRaw.current, null);
      retainedRaw.current = null;
      setPending(null);
      setRecoveryError('');
      setResult(null);
      setProject(null);
      setError('');
    } catch (error) {
      setError(describe(error));
    }
  }

  const locked = !!pending || !!recoveryError || busy || !interactive;
  return (
    <section
      className="vm-project-initialize"
      aria-label="Initialize VM project"
    >
      <p className="vm-project-initialize-target">
        {pending
          ? `Original VM: ${pending.machineId} · Volume: ${pending.volumeId} · Directory: ${pending.directory}`
          : target
            ? target.label
            : 'Choose a ready retained volume above.'}
      </p>
      {!pending && !recoveryError && (
        <form
          className="vm-project-initialize-form"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          {mode === 'clone' && (
            <div className="vm-project-initialize-repository">
              <Field
                label="Repository URL"
                value={repository}
                placeholder="https://github.com/owner/repository.git"
                disabled={locked}
                onChange={(event) => setRepository(event.target.value)}
              />
              <p className="vm-project-initialize-guidance">
                HTTPS or SSH, without inline credentials. Configure Git
                authentication and SSH host trust in the VM first.
              </p>
            </div>
          )}
          <div className="vm-project-initialize-fields">
            <Field
              label="New directory"
              value={directory}
              placeholder="my-project"
              disabled={locked}
              onChange={(event) => setDirectory(event.target.value)}
            />
            <Field
              label="New name"
              value={name}
              placeholder="Optional"
              disabled={locked}
              onChange={(event) => setName(event.target.value)}
            />
          </div>
          <div className="vm-project-initialize-submit">
            <Button type="submit" primary disabled={locked || !target}>
              {busy ? 'Submitting…' : mode === 'clone' ? 'Clone' : 'Create'}
            </Button>
            <p className="vm-project-initialize-guidance">
              Existing directories are never overwritten.
            </p>
          </div>
        </form>
      )}
      {[...new Set([error, recoveryError].filter(Boolean))].map((reason) => (
        <p role="alert" key={reason}>
          {reason}
        </p>
      ))}
      {pending && (
        <div className="app-actions">
          <Button
            disabled={busy || !interactive}
            onClick={() => void submit(pending)}
          >
            {busy ? 'Checking…' : 'Recover'}
          </Button>
          {pending.submission && (
            <Button
              disabled={!interactive}
              onClick={() =>
                desktop.openApp(
                  'project-files',
                  {
                    vmTasksProjectId: pending.submission!.binding.projectId,
                    hostId: pending.hostId,
                  },
                  { title: 'Volume workspace Tasks' },
                )
              }
            >
              Tasks
            </Button>
          )}
        </div>
      )}
      {result && (
        <>
          <p role="status">
            Initialization: {result.attempt.status} ·{' '}
            {result.attempt.id.slice(0, 8)}
          </p>
          {result.attempt.reason && <p role="alert">{result.attempt.reason}</p>}
          {result.attempt.syncError && (
            <p role="alert">{result.attempt.syncError}</p>
          )}
          {result.attempt.status === 'failed' && (
            <p>
              The destination and any partial clone were preserved. Inspect
              Tasks and the volume workspace before choosing another directory.
            </p>
          )}
          {result.attempt.status === 'succeeded' && !project && (
            <Button
              primary
              disabled={busy || !interactive}
              onClick={() => void finish()}
            >
              Open Project
            </Button>
          )}
        </>
      )}
      {project && (
        <div className="app-actions">
          <Button
            primary
            disabled={!interactive}
            onClick={() =>
              openVMProjectCodex(
                desktop,
                project.hostId,
                project.id,
                project.name,
              )
            }
          >
            Codex
          </Button>
          <Button
            disabled={!interactive}
            onClick={() =>
              desktop.openApp(
                'project-files',
                { vmProjectId: project.id, hostId: project.hostId },
                { title: `VM · ${project.name}` },
              )
            }
          >
            Files
          </Button>
        </div>
      )}
      {(pending || recoveryError) && (
        <Button disabled={busy || !interactive} onClick={() => void dismiss()}>
          Dismiss
        </Button>
      )}
    </section>
  );
}
