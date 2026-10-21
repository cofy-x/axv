import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Field, IconButton, Select } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { useResource } from '../../shared/api/useResource';
import { RequestIdentity } from '../../shared/api/request-identity';
import { useDraftNamespace } from '../../shared/drafts/context';
import { machinesApi } from '../machines/api';
import { vmProjectsAPI } from './vm-api';
import type { VMProject } from './vm-api';

export function VMProjectLauncher({ desktop, interactive }: AppWindowProps) {
  const [refresh, setRefresh] = useState(0);
  const [volumeId, setVolumeId] = useState('');
  const [root, setRoot] = useState('');
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const { databaseId } = useDraftNamespace();
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
          machine.guestCapabilities?.includes('project-files'),
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
        <h2>Open Folder</h2>
        <IconButton
          icon="refresh"
          label="Refresh VMs"
          disabled={busy || !interactive}
          onClick={() => setRefresh((value) => value + 1)}
        />
      </div>
      <p className="project-launcher-guidance">
        Open an existing folder on a running VM’s retained volume.
      </p>
      <form
        className="project-register-form"
        onSubmit={(event) => {
          event.preventDefault();
          void register();
        }}
      >
        <div className="project-register-fields">
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
          <Field
            label="Directory in volume"
            value={root}
            placeholder="repo (empty for volume root)"
            disabled={busy || !interactive}
            onChange={(event) => setRoot(event.target.value)}
          />
          <div className="project-register-name">
            <Field
              label="Project name (optional)"
              value={name}
              disabled={busy || !interactive}
              onChange={(event) => setName(event.target.value)}
            />
          </div>
        </div>
        <div className="project-register-actions">
          <Button
            type="submit"
            disabled={!volume || busy || !interactive}
            primary
          >
            {busy ? 'Opening…' : 'Open'}
          </Button>
        </div>
      </form>
      {!data && !inventory.error && <p role="status">Loading VM projects…</p>}
      {(error || inventory.error) && (
        <p role="alert">{error || inventory.error}</p>
      )}
      {data && eligible?.length === 0 && (
        <p>
          No ready retained volumes are attached to a running VM with project
          file access. Manage VMs and volumes in Virtual Machines.
        </p>
      )}
      {!!data?.projects.length && (
        <h2 className="project-list-heading">Projects</h2>
      )}
      <div className="project-list" aria-label="Registered VM projects">
        {data?.projects.map((project) => {
          const projectVolume = data.volumes.find(
            (item) => item.id === project.volumeId,
          );
          return (
            <Button
              key={project.id}
              disabled={!interactive}
              onClick={() => open(project)}
            >
              <strong>
                {project.name} · {project.id.slice(0, 8)}
              </strong>
              <small>
                {projectVolume
                  ? `${projectVolume.name} · ${project.volumeId.slice(0, 8)}`
                  : `Volume ${project.volumeId.slice(0, 8)}`}{' '}
                / {project.root || 'Volume root'}
              </small>
            </Button>
          );
        })}
      </div>
    </section>
  );
}
