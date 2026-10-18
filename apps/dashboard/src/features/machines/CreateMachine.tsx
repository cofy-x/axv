import { useEffect, useRef, useState } from 'react';
import { Button, Checkbox, Field, Select } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { machinesApi } from './api';
import type { ComputeBinding, MachineSpec } from './api';
import {
  creationDefaults,
  integer,
  machineNameError,
  normalizeMachineName,
  resourceBusy,
} from './form-state';
import { OperationRequest, operationError } from './operation-requests';
import { useCompute } from './useCompute';
import { computeStore } from './compute-store';
import { RequestDetails } from './OperationFeedback';
import { openMachineInspector } from './desktop-icons';

export function CreateMachine({ desktop, window }: AppWindowProps) {
  const { setTitle } = desktop;
  const data = useCompute();
  const [name, setName] = useState('');
  const [binding, setBinding] = useState<ComputeBinding>();
  const [imageId, setImageId] = useState('');
  const initializedDefaults = useRef('');
  const initializedImage = useRef(false);
  const [kind, setKind] = useState<'development' | 'task'>('development');
  const [cpus, setCPUs] = useState('');
  const [memoryMiB, setMemory] = useState('');
  const [diskGiB, setDisk] = useState('');
  const [projectGiB, setProject] = useState('');
  const [volumeId, setVolume] = useState('');
  const [retainVolume, setRetain] = useState(true);
  const [ttl, setTTL] = useState('');
  const [start, setStart] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [requests] = useState(
    () =>
      new OperationRequest(
        (
          input: { spec: MachineSpec; start: boolean; binding: ComputeBinding },
          id,
        ) => machinesApi.create(input.spec, input.start, id, input.binding),
      ),
  );
  useEffect(() => setTitle(window.id, 'New VM'), [setTitle, window.id]);
  useEffect(() => {
    const defaults = creationDefaults(data.info);
    if (!defaults || !data.info?.hostId || data.error) return;
    if (
      initializedDefaults.current &&
      initializedDefaults.current !== data.info.hostId
    )
      return;
    if (!initializedDefaults.current) {
      initializedDefaults.current = data.info.hostId;
      setBinding({ hostId: data.info.hostId });
      setName(defaults.spec.name);
      setKind(defaults.spec.kind);
      setCPUs(String(defaults.spec.resources.cpus));
      setMemory(String(defaults.spec.resources.memoryMiB));
      setDisk(String(defaults.spec.resources.diskGiB));
      setProject(String(defaults.spec.projectGiB));
      setRetain(defaults.spec.retainVolume);
      setTTL(String(defaults.taskTtlSeconds / 60));
      setStart(defaults.start);
    }
    if (!initializedImage.current && defaults.spec.imageId) {
      initializedImage.current = true;
      setImageId(defaults.spec.imageId);
      setDisk((current) =>
        String(Math.max(Number(current), defaults.spec.resources.diskGiB)),
      );
    }
  }, [data.info, data.error]);

  const image = data.info?.images?.find((item) => item.id === imageId);
  const provider = data.info?.providers?.find(
    (item) => item.provider === image?.provider,
  );
  const volume = data.volumes.find((item) => item.id === volumeId);
  const availableVolumes = data.volumes.filter(
    (item) =>
      !item.machineId &&
      !item.pendingMachineId &&
      !item.pendingSizeGiB &&
      item.status === 'ready' &&
      item.provider === image?.provider &&
      (kind !== 'development' || item.retain) &&
      !resourceBusy(data.operations, item.id),
  );
  const selectedVolumeAvailable =
    !volumeId || availableVolumes.some((item) => item.id === volumeId);
  const selectedVolumeChanged =
    !!volume &&
    (volume.sizeGiB !== Number(projectGiB) || volume.retain !== retainVolume);
  const minimumRoot = Math.max(2, image?.minRootGiB || 2);
  const capacity = data.info?.capacity;
  const hostChanged = !!binding && binding.hostId !== data.info?.hostId;
  const unavailable =
    (hostChanged
      ? 'The VM host changed. This draft belongs to the original host. Reopen New VM to create on the current host.'
      : '') ||
    data.error ||
    (!data.info
      ? 'Checking the host…'
      : !data.info.configured
        ? data.info.reason || 'Configure the host before creating a VM.'
        : !data.info.defaults
          ? 'The host did not provide VM creation defaults. Update the host service.'
          : !image
            ? imageId
              ? 'The selected image is no longer available. Choose an image explicitly.'
              : 'Add a compatible image before creating a VM.'
            : !provider?.available
              ? provider?.reason ||
                'The selected image provider is unavailable.'
              : '');
  const validation =
    machineNameError(name, data.machines) ||
    (!integer(cpus, 1, capacity?.cpus)
      ? 'Enter a whole CPU count within the host capacity.'
      : !integer(memoryMiB, 128, capacity?.memoryMiB)
        ? 'Enter memory of at least 128 MiB within the host capacity.'
        : !integer(diskGiB, minimumRoot, capacity?.diskGiB)
          ? `Root disk must be at least ${minimumRoot} GiB and within the host capacity.`
          : !integer(projectGiB, 1, capacity?.diskGiB)
            ? 'Enter a project disk size within the host capacity.'
            : kind === 'task' && !integer(ttl, 1, 43200)
              ? 'Task leases must be 1–43,200 whole minutes.'
              : !selectedVolumeAvailable
                ? 'The selected volume is no longer available for attachment. Choose another volume.'
                : selectedVolumeChanged
                  ? 'The selected volume changed. Review and use its current settings before creating the VM.'
                  : '');
  const unresolved = !!requests.pending;
  const locked =
    busy || unresolved || hostChanged || !binding || !data.info?.defaults;

  async function submit(retry = false) {
    if (!binding || hostChanged || data.error) return;
    const latest = computeStore.getSnapshot();
    if (latest.error || latest.info?.hostId !== binding.hostId) return;
    if (!retry && (unavailable || validation || !image)) return;
    setBusy(true);
    setError('');
    try {
      const result = retry
        ? await requests.retry()
        : await requests.submit(
            {
              spec: {
                name: normalizeMachineName(name),
                imageId: image!.id,
                provider: image!.provider,
                kind,
                resources: {
                  cpus: Number(cpus),
                  memoryMiB: Number(memoryMiB),
                  diskGiB: Number(diskGiB),
                },
                projectGiB: Number(projectGiB),
                retainVolume,
                ...(volumeId ? { volumeId } : {}),
                ...(kind === 'task' ? { ttlSeconds: Number(ttl) * 60 } : {}),
              },
              start,
              binding,
            },
            `Create “${normalizeMachineName(name)}”`,
          );
      data.refresh();
      openMachineInspector(desktop, result.machine);
      await desktop.closeWindow(window.id);
    } catch (failure) {
      setError(operationError(failure));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="machines-app">
      <form
        className="app-pad machines-form machines-scroll"
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <fieldset disabled={locked}>
          <legend>Virtual Machine</legend>
          <Field
            label="Name"
            value={name}
            required
            maxLength={80}
            onChange={(event) => setName(event.target.value)}
          />
          <Select
            label="Image"
            value={imageId}
            onChange={(event) => {
              const next = data.info?.images?.find(
                (item) => item.id === event.target.value,
              );
              setImageId(event.target.value);
              setVolume('');
              if (next)
                setDisk((current) =>
                  String(Math.max(Number(current) || 0, next.minRootGiB || 2)),
                );
            }}
          >
            {!image && (
              <option value={imageId}>
                {imageId
                  ? 'Selected image unavailable'
                  : 'No configured images'}
              </option>
            )}
            {data.info?.images?.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name || item.id} · {item.provider} · {item.arch}
              </option>
            ))}
          </Select>
          <Select
            label="Workload"
            value={kind}
            onChange={(event) => {
              const next =
                event.target.value === 'task' ? 'task' : 'development';
              setKind(next);
              setRetain(
                next === 'development' ||
                  !!data.info?.defaults?.taskRetainVolume,
              );
              setVolume('');
            }}
          >
            <option value="development">Long-lived development</option>
            <option value="task">Temporary task</option>
          </Select>
          <p className="machines-form-help">
            Development VMs have no expiry and keep their project volume. Task
            VMs expire at the end of their lease.
          </p>
        </fieldset>
        <fieldset disabled={locked}>
          <legend>Resources</legend>
          <div className="machines-resource-fields">
            <Field
              label="CPUs"
              type="number"
              min={1}
              max={capacity?.cpus}
              value={cpus}
              required
              onChange={(event) => setCPUs(event.target.value)}
            />
            <Field
              label="Memory (MiB)"
              type="number"
              min={128}
              max={capacity?.memoryMiB}
              value={memoryMiB}
              required
              onChange={(event) => setMemory(event.target.value)}
            />
            <Field
              label="Root disk (GiB)"
              type="number"
              min={minimumRoot}
              max={capacity?.diskGiB}
              value={diskGiB}
              required
              onChange={(event) => setDisk(event.target.value)}
            />
          </div>
          <p className="machines-form-help">
            Minimum root disk: {minimumRoot} GiB. Resources share the host
            limits.
          </p>
        </fieldset>
        <fieldset disabled={locked}>
          <legend>Project storage</legend>
          <Select
            label="Volume"
            value={volumeId}
            onChange={(event) => {
              setVolume(event.target.value);
              const next = data.volumes.find(
                (item) => item.id === event.target.value,
              );
              if (next) {
                setProject(String(next.sizeGiB));
                setRetain(next.retain);
              }
            }}
          >
            <option value="">Create a new project volume</option>
            {volumeId && !selectedVolumeAvailable && (
              <option value={volumeId}>
                {volume?.name || volumeId} · unavailable
              </option>
            )}
            {availableVolumes.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name || item.id} · {item.sizeGiB} GiB
              </option>
            ))}
          </Select>
          <Field
            label="Project disk (GiB)"
            type="number"
            min={1}
            max={capacity?.diskGiB}
            disabled={!!volumeId}
            value={projectGiB}
            required
            onChange={(event) => setProject(event.target.value)}
          />
          <Checkbox
            label="Retain project volume when the VM is deleted"
            checked={retainVolume}
            disabled={kind === 'development' || !!volumeId}
            onChange={(event) => setRetain(event.target.checked)}
          />
          {selectedVolumeChanged && volume && (
            <Button
              onClick={() => {
                setProject(String(volume.sizeGiB));
                setRetain(volume.retain);
              }}
            >
              Use Current
            </Button>
          )}
          <p className="machines-form-help">
            Project files live on a separate volume. An existing volume must be
            detached, ready and compatible with this VM.
          </p>
        </fieldset>
        {kind === 'task' && (
          <fieldset disabled={locked}>
            <legend>Task lease</legend>
            <Field
              label="Expires after (minutes)"
              type="number"
              min={1}
              max={43200}
              required
              value={ttl}
              onChange={(event) => setTTL(event.target.value)}
            />
            <p className="machines-form-help">
              Expiry deletes the VM even if work is still running. Save task
              results before this deadline. Retained project volumes are kept.
            </p>
          </fieldset>
        )}
        <Checkbox
          label="Start after creation"
          checked={start}
          disabled={locked}
          onChange={(event) => setStart(event.target.checked)}
        />
        {(unavailable || validation) && (
          <p className="machines-warning">{unavailable || validation}</p>
        )}
        {error && (
          <p className="machines-error" role="alert">
            {error}
          </p>
        )}
        {requests.pending && !busy && (
          <>
            <p className="machines-warning">
              The response was interrupted. Retry the original request to find
              out whether this VM was created.
            </p>
            <RequestDetails
              requestId={requests.pending.requestId}
              hostId={requests.pending.input.binding.hostId}
            />
          </>
        )}
        <div className="machines-form-actions">
          <Button onClick={data.refresh}>Check Host</Button>
          {unresolved && !busy ? (
            <Button
              disabled={hostChanged || !!data.error}
              onClick={() => void submit(true)}
            >
              Retry Original Request
            </Button>
          ) : (
            <Button
              type="submit"
              disabled={locked || !!unavailable || !!validation}
            >
              {busy ? 'Creating…' : 'Create VM'}
            </Button>
          )}
        </div>
      </form>
    </div>
  );
}
