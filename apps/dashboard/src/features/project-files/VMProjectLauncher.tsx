import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Button, Field, IconButton, Select } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { useResource } from '../../shared/api/useResource';
import { RequestIdentity } from '../../shared/api/request-identity';
import { desktopDatabase } from '../../shared/api/desktop';
import { usePageVisibility } from '../../shared/desktop/usePageVisibility';
import { useDraftNamespace } from '../../shared/drafts/context';
import { machinesApi } from '../machines/api';
import { vmProjectsAPI } from './vm-api';
import type { VMProject } from './vm-api';
import { VMProjectInitialize } from './VMProjectInitialize';
import {
  canOpenVMProjectVolume,
  sameVMProjectVolume,
  vmProjectVolumeScope,
} from './vm-project-volume';

function shortResourceId(id: string): string {
  const stripped = id.replace(/^(?:project|vol)-/, '');
  return (stripped || id).slice(0, 8);
}

export function VMProjectLauncher({
  desktop,
  window,
  visible,
  interactive,
}: AppWindowProps) {
  const scope = vmProjectVolumeScope(window.params);
  const scopedHostId = scope?.hostId;
  const pageVisible = usePageVisibility();
  const active =
    pageVisible && visible !== false && desktop.activeWindowId === window.id;
  const canInteract = !!interactive;
  const [refresh, setRefresh] = useState(0);
  const [selection, setSelection] = useState(scope);
  const [root, setRoot] = useState('');
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [removingId, setRemovingId] = useState('');
  const [error, setError] = useState('');
  const { databaseId, clientId } = useDraftNamespace();
  const [mode, setMode] = useState<'open' | 'create' | 'clone'>(() => {
    if (scope) return 'open';
    try {
      return localStorage.getItem(
        JSON.stringify(['axv-vm-project-initialization', databaseId, clientId]),
      )
        ? 'create'
        : 'open';
    } catch {
      return 'create';
    }
  });
  const requests = useRef(new RequestIdentity(`${databaseId}:vm-projects`));
  const mounted = useRef(false);
  const latestDesktop = useRef(desktop);
  latestDesktop.current = desktop;
  const canOpen = useRef(active && canInteract);
  canOpen.current = active && canInteract;
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const load = useCallback(
    async (signal: AbortSignal) => {
      const info = await machinesApi.info(
        signal,
        scopedHostId ? { hostId: scopedHostId } : undefined,
      );
      if (!info.configured || !info.hostId)
        throw new Error(info.reason || 'Start a VM host to open VM projects.');
      if (scopedHostId && info.hostId !== scopedHostId)
        throw new Error(
          'Reconnect to the original VM host to open these project files.',
        );
      const binding = { hostId: info.hostId };
      const [{ machines }, { volumes }, { projects }] = await Promise.all([
        machinesApi.machines(signal, binding),
        machinesApi.volumes(signal, binding),
        vmProjectsAPI.list(info.hostId, signal),
      ]);
      return { hostId: info.hostId, machines, volumes, projects };
    },
    [scopedHostId],
  );
  // Each return to this view rechecks eligibility while the form owns its draft.
  const request = useMemo(
    () => (active ? { refresh } : null),
    [active, refresh],
  );
  const inventory = useResource(request, load, desktopDatabase.assertActive, {
    keepPreviousData: true,
  });
  const data = inventory.data;
  const verified =
    active && canInteract && !inventory.pending && !inventory.error;
  const eligible = data?.volumes.flatMap((volume) => {
    const machine = data.machines.find(
      (item) => item.id === volume.machineId && item.hostId === data.hostId,
    );
    if (!machine || !canOpenVMProjectVolume(machine, volume)) return [];
    if (
      scope &&
      (volume.id !== scope.volumeId || machine.id !== scope.machineId)
    )
      return [];
    if (
      mode !== 'open' &&
      (!machine.guestCapabilities?.includes('project-tasks') ||
        !machine.guestCapabilities.includes('project-initialization'))
    )
      return [];
    return [{ volume, machine }];
  });
  const selected =
    selection && data?.hostId === selection.hostId
      ? eligible?.find(
          ({ volume, machine }) =>
            volume.id === selection.volumeId &&
            machine.id === selection.machineId,
        )
      : undefined;
  const projects = data?.projects.filter(
    (item) =>
      item.hostId === data.hostId &&
      (!scope || item.volumeId === scope.volumeId),
  );

  function open(project: VMProject) {
    const desktop = latestDesktop.current;
    const existing =
      scope &&
      desktop.windows.find(
        ({ appId, params }) =>
          appId === 'project-files' &&
          params.hostId === project.hostId &&
          params.vmProjectId === project.id,
      );
    if (
      scope &&
      existing &&
      !sameVMProjectVolume(scope, vmProjectVolumeScope(existing.params))
    ) {
      void desktop
        .dialog({
          title: 'Project already open',
          message:
            'To open the project for this VM, close its existing Project Files window first.',
          buttons: [
            { label: 'Cancel', value: 'cancel', cancel: true },
            { label: 'Show Window', value: 'show', default: true },
          ],
        })
        .then((answer) => {
          if (mounted.current && answer?.button === 'show')
            desktop.focusWindow(existing.id);
        });
      return;
    }
    desktop.openApp(
      'project-files',
      {
        vmProjectId: project.id,
        hostId: project.hostId,
        ...(scope && {
          machineId: scope.machineId,
          vmProjectVolumeId: scope.volumeId,
        }),
      },
      { title: `VM · ${project.name}` },
    );
  }

  async function register(overrideRoot?: string) {
    if (!data || !selected || busy || !verified) return;
    const { volume, machine } = selected;
    const targetRoot = overrideRoot !== undefined ? overrideRoot : root;
    const targetName = overrideRoot !== undefined ? '' : name.trim();
    setBusy(true);
    setError('');
    try {
      const values = {
        volumeId: volume.id,
        volumeRevision: volume.revision,
        machineId: machine.id,
        root: targetRoot,
        ...(targetName ? { name: targetName } : {}),
      };
      const requestId = requests.current.for({
        hostId: data.hostId,
        ...values,
      });
      const { project } = await vmProjectsAPI.register(data.hostId, {
        ...values,
        requestId,
      });
      if (project.hostId !== data.hostId || project.volumeId !== volume.id)
        throw new Error(
          'The project receipt does not match the original volume.',
        );
      if (!mounted.current) return;
      if (!requests.current.complete(requestId))
        setError('Project saved. Its retry identity could not be cleared.');
      setRefresh((value) => value + 1);
      if (canOpen.current) open(project);
    } catch (error) {
      if (mounted.current)
        setError(error instanceof Error ? error.message : String(error));
    } finally {
      if (mounted.current) setBusy(false);
    }
  }

  async function removeProject(project: VMProject) {
    if (!data || busy || removingId || !verified) return;
    const answer = await desktop.dialog({
      title: 'Remove VM project?',
      message: `Remove project registration “${project.name}” from AXV? Files on the project volume remain on disk; detached volumes can then be deleted in Virtual Machines → Volumes.`,
      buttons: [
        { label: 'Cancel', value: 'cancel', default: true, cancel: true },
        { label: 'Remove', value: 'remove' },
      ],
    });
    if (answer?.button !== 'remove') return;
    setRemovingId(project.id);
    setError('');
    try {
      await vmProjectsAPI.delete(
        project.id,
        project.hostId,
        crypto.randomUUID(),
        project.revision,
      );
      if (mounted.current) setRefresh((value) => value + 1);
    } catch (error) {
      if (mounted.current)
        setError(error instanceof Error ? error.message : String(error));
    } finally {
      if (mounted.current) setRemovingId('');
    }
  }

  return (
    <section className="vm-project-launcher" aria-label="VM projects">
      <div className="project-launcher-card">
        <div className="project-launcher-heading">
          <h2>Projects</h2>
          <IconButton
            icon="refresh"
            label="Refresh VM projects"
            disabled={busy || !active || !canInteract || !!inventory.pending}
            onClick={() => setRefresh((value) => value + 1)}
          />
        </div>
        {!data && !inventory.error && <p role="status">Loading VM projects…</p>}
        {inventory.error && inventory.error !== error && (
          <p className="project-error" role="alert">
            {inventory.error}
          </p>
        )}
        {projects &&
          (projects.length > 0 ? (
            <ul
              className="project-list"
              role="list"
              aria-label="Registered VM projects"
            >
              {projects.map((project) => {
                const projectVolume = data?.volumes.find(
                  (item) => item.id === project.volumeId,
                );
                return (
                  <li key={project.id} className="project-list-row">
                    <Button
                      className="project-list-open"
                      disabled={!verified || (!!scope && !selected)}
                      onClick={() => open(project)}
                    >
                      <strong>
                        {project.root === ''
                          ? `Volume workspace · ${project.name}`
                          : project.name}{' '}
                        <span className="project-list-id">
                          · {shortResourceId(project.id)}
                        </span>
                      </strong>
                      <small>
                        {projectVolume
                          ? `${projectVolume.name} · ${shortResourceId(project.volumeId)}`
                          : `Volume ${shortResourceId(project.volumeId)}`}{' '}
                        / {project.root || 'Volume root'}
                      </small>
                    </Button>
                    <Button
                      className="project-list-remove"
                      disabled={!verified || busy || !!removingId}
                      aria-label={`Remove ${project.name}`}
                      onClick={() => void removeProject(project)}
                    >
                      {removingId === project.id ? 'Removing…' : 'Remove'}
                    </Button>
                  </li>
                );
              })}
            </ul>
          ) : (
            <p className="project-list-empty">No VM projects yet.</p>
          ))}
        {data &&
          !inventory.pending &&
          !inventory.error &&
          eligible?.length === 0 && (
            <p className="project-launcher-notice">
              {scope
                ? 'The original VM project volume is unavailable.'
                : `No ready retained volumes are attached to a running VM with project file access${mode !== 'open' ? ' and persistent task support' : ''}.`}
            </p>
          )}
        {(inventory.error ||
          (!inventory.pending && eligible?.length === 0)) && (
          <div className="project-launcher-fallback">
            <Button
              disabled={!canInteract}
              onClick={() => desktop.openApp('machines')}
            >
              Open Virtual Machines
            </Button>
          </div>
        )}
        {scope && selected && projects && projects.length === 0 && (
          <div className="project-register-actions project-workspace-quick">
            <Button
              primary
              disabled={busy || !verified}
              onClick={() => void register('')}
            >
              {busy ? 'Opening…' : 'Open Volume Workspace'}
            </Button>
          </div>
        )}
      </div>
      <div className="project-launcher-card" hidden={!data}>
        <form
          className="project-register-form"
          aria-label="VM project setup"
          aria-busy={inventory.pending || undefined}
          onSubmit={(event) => {
            event.preventDefault();
            if (mode === 'open') void register();
          }}
        >
          <div className="project-setup-header">
            <h3>Add or open a project</h3>
            {scope && selected && (
              <span className="project-volume-badge">
                Volume:{' '}
                <strong className="project-register-volume">
                  {selected.volume.name}
                </strong>
              </span>
            )}
          </div>
          <div
            className={`project-register-fields${scope ? ' is-scoped' : ''}`}
          >
            <Select
              label="Project action"
              value={mode}
              disabled={busy || !canInteract}
              onChange={(event) =>
                setMode(event.target.value as 'open' | 'create' | 'clone')
              }
            >
              <option value="open">Open Folder</option>
              <option value="create">Create Project</option>
              <option value="clone">Clone Repository</option>
            </Select>
            {!scope && (
              <Select
                label="Project volume"
                value={selected?.volume.id || ''}
                disabled={busy || !canInteract}
                onChange={(event) => {
                  const choice = eligible?.find(
                    ({ volume }) => volume.id === event.target.value,
                  );
                  setSelection(
                    choice
                      ? {
                          hostId: choice.machine.hostId,
                          machineId: choice.machine.id,
                          volumeId: choice.volume.id,
                        }
                      : undefined,
                  );
                }}
              >
                <option value="">Choose a ready volume</option>
                {eligible?.map(({ volume, machine }) => (
                  <option key={volume.id} value={volume.id}>
                    {volume.name} · {machine.spec.name} ·{' '}
                    {shortResourceId(volume.id)}
                  </option>
                ))}
              </Select>
            )}
            {mode === 'open' && (
              <>
                <Field
                  label="Directory"
                  value={root}
                  placeholder="Blank for volume root"
                  disabled={busy || !canInteract}
                  onChange={(event) => setRoot(event.target.value)}
                />
                <Field
                  label="Project name"
                  value={name}
                  placeholder="Optional"
                  disabled={busy || !canInteract}
                  onChange={(event) => setName(event.target.value)}
                />
              </>
            )}
          </div>
          {mode === 'open' && (
            <div className="project-register-actions">
              <Button
                type="submit"
                disabled={!selected || busy || !verified}
                primary={!(scope && projects && projects.length === 0)}
              >
                {busy ? 'Opening…' : 'Open'}
              </Button>
            </div>
          )}
        </form>
        <VMProjectInitialize
          desktop={desktop}
          interactive={active && canInteract}
          inventoryAvailable={!!data}
          mode={mode}
          target={
            selected && verified
              ? {
                  hostId: selected.machine.hostId,
                  volumeId: selected.volume.id,
                  volumeRevision: selected.volume.revision,
                  machineId: selected.machine.id,
                  machineName: selected.machine.spec.name,
                  label: `${selected.volume.name} · ${selected.machine.spec.name}`,
                }
              : null
          }
          onCreated={() => setRefresh((value) => value + 1)}
        />
      </div>
      {error && (
        <p className="project-error" role="alert">
          {error}
        </p>
      )}
    </section>
  );
}
