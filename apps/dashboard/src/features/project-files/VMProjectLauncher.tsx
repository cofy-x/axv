import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Field, IconButton, Select } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { useResource } from '../../shared/api/useResource';
import { RequestIdentity } from '../../shared/api/request-identity';
import { useDraftNamespace } from '../../shared/drafts/context';
import { machinesApi } from '../machines/api';
import { vmProjectsAPI } from './vm-api';
import type { VMProject } from './vm-api';
import { VMProjectInitialize } from './VMProjectInitialize';

export function VMProjectLauncher({ desktop, interactive }: AppWindowProps) {
  const [refresh, setRefresh] = useState(0);
  const [volumeId, setVolumeId] = useState('');
  const [root, setRoot] = useState('');
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const { databaseId, clientId } = useDraftNamespace();
  const [mode, setMode] = useState<'open' | 'create' | 'clone'>(() => {
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
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const load = useCallback(async () => {
    const info = await machinesApi.info();
    if (!info.configured || !info.hostId)
      throw new Error(info.reason || 'Start a VM host to open VM projects.');
    const binding = { hostId: info.hostId };
    const [{ machines }, { volumes }, { projects }] = await Promise.all([
      machinesApi.machines(undefined, binding),
      machinesApi.volumes(undefined, binding),
      vmProjectsAPI.list(info.hostId),
    ]);
    return { hostId: info.hostId, machines, volumes, projects };
  }, []);
  const inventory = useResource(`vm-projects:${refresh}`, load);
  const data = inventory.data;
  const eligible = data?.volumes.filter(
    (volume) =>
      volume.retain &&
      volume.status === 'ready' &&
      data.machines.some(
        (machine) =>
          machine.id === volume.machineId &&
          machine.hostId === data.hostId &&
          machine.status === 'running' &&
          machine.ready &&
          machine.guestCapabilities?.includes('project-files') &&
          (mode === 'open' ||
            (machine.guestCapabilities?.includes('project-tasks') &&
              machine.guestCapabilities?.includes('project-initialization'))),
      ),
  );
  const volume = eligible?.find((item) => item.id === volumeId);

  function open(project: VMProject) {
    desktop.openApp(
      'project-files',
      { vmProjectId: project.id, hostId: project.hostId },
      { title: `VM · ${project.name}` },
    );
  }

  async function register() {
    if (!data || !volume || !volume.machineId || busy || !interactive) return;
    setBusy(true);
    setError('');
    try {
      const values = {
        volumeId: volume.id,
        volumeRevision: volume.revision,
        machineId: volume.machineId,
        root,
        ...(name.trim() ? { name: name.trim() } : {}),
      };
      const requestId = requests.current.for({
        hostId: data.hostId,
        ...values,
      });
      const { project } = await vmProjectsAPI.register(data.hostId, {
        ...values,
        requestId,
      });
      if (!mounted.current) return;
      if (!requests.current.complete(requestId))
        setError('Project saved. Its retry identity could not be cleared.');
      setRefresh((value) => value + 1);
      open(project);
    } catch (error) {
      if (mounted.current)
        setError(error instanceof Error ? error.message : String(error));
    } finally {
      if (mounted.current) setBusy(false);
    }
  }

  return (
    <section className="vm-project-launcher" aria-label="VM projects">
      <div className="project-launcher-heading">
        <h2>Projects</h2>
        <IconButton
          icon="refresh"
          label="Refresh VM projects"
          disabled={busy || !interactive}
          onClick={() => setRefresh((value) => value + 1)}
        />
      </div>
      {!data && !inventory.error && <p role="status">Loading VM projects…</p>}
      {inventory.error && inventory.error !== error && (
        <p role="alert">{inventory.error}</p>
      )}
      {data &&
        (data.projects.length > 0 ? (
          <ul
            className="project-list"
            role="list"
            aria-label="Registered VM projects"
          >
            {data.projects.map((project) => {
              const projectVolume = data.volumes.find(
                (item) => item.id === project.volumeId,
              );
              return (
                <li key={project.id}>
                  <Button disabled={!interactive} onClick={() => open(project)}>
                    <strong>
                      {project.root === ''
                        ? `Volume workspace · ${project.name}`
                        : project.name}{' '}
                      · {project.id.slice(0, 8)}
                    </strong>
                    <small>
                      {projectVolume
                        ? `${projectVolume.name} · ${project.volumeId.slice(0, 8)}`
                        : `Volume ${project.volumeId.slice(0, 8)}`}{' '}
                      / {project.root || 'Volume root'}
                    </small>
                  </Button>
                </li>
              );
            })}
          </ul>
        ) : (
          <p className="project-list-empty">No VM projects yet.</p>
        ))}
      <form
        className="project-register-form"
        aria-label="VM project setup"
        onSubmit={(event) => {
          event.preventDefault();
          if (mode === 'open') void register();
        }}
      >
        <div className="project-register-fields">
          <Select
            label="Project action"
            value={mode}
            disabled={busy || !interactive}
            onChange={(event) =>
              setMode(event.target.value as 'open' | 'create' | 'clone')
            }
          >
            <option value="open">Open Folder</option>
            <option value="create">Create Project</option>
            <option value="clone">Clone Repository</option>
          </Select>
          <Select
            label="Project volume"
            value={volumeId}
            disabled={busy || !interactive}
            onChange={(event) => setVolumeId(event.target.value)}
          >
            <option value="">Choose a ready volume</option>
            {eligible?.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name} ·{' '}
                {
                  data?.machines.find(
                    (machine) => machine.id === item.machineId,
                  )?.spec.name
                }{' '}
                · {item.id.slice(0, 8)}
              </option>
            ))}
          </Select>
          {mode === 'open' && (
            <>
              <Field
                label="Directory"
                value={root}
                placeholder="Blank for volume root"
                disabled={busy || !interactive}
                onChange={(event) => setRoot(event.target.value)}
              />
              <Field
                label="Project name"
                value={name}
                placeholder="Optional"
                disabled={busy || !interactive}
                onChange={(event) => setName(event.target.value)}
              />
            </>
          )}
        </div>
        {mode === 'open' && (
          <div className="project-register-actions">
            <Button
              type="submit"
              disabled={!volume || busy || !interactive}
              primary
            >
              {busy ? 'Opening…' : 'Open'}
            </Button>
          </div>
        )}
      </form>
      <div hidden={mode === 'open'} inert={mode === 'open' || !interactive}>
        <VMProjectInitialize
          desktop={desktop}
          interactive={!!interactive && mode !== 'open'}
          mode={mode === 'clone' ? 'clone' : 'create'}
          target={
            data && volume?.machineId
              ? {
                  hostId: data.hostId,
                  volumeId: volume.id,
                  volumeRevision: volume.revision,
                  machineId: volume.machineId,
                  machineName:
                    data.machines.find(
                      (machine) => machine.id === volume.machineId,
                    )?.spec.name || volume.machineId,
                  label: `${volume.name} · ${data.machines.find((machine) => machine.id === volume.machineId)?.spec.name || volume.machineId}`,
                }
              : null
          }
          onCreated={() => setRefresh((value) => value + 1)}
        />
      </div>
      {error && <p role="alert">{error}</p>}
      {data && eligible?.length === 0 && (
        <p>
          No ready retained volumes are attached to a running VM with project
          file access{mode !== 'open' ? ' and persistent task support' : ''}.
          Manage VMs and volumes in Virtual Machines.
        </p>
      )}
    </section>
  );
}
