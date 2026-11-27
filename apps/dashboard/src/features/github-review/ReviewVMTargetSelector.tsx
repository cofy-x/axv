import { useEffect, useState } from 'react';
import { Select } from '@cofy-x/axv-nextstep';
import { machinesApi } from '../machines/api';
import type { Machine } from '../machines/types';
import { vmProjectsAPI } from '../project-files/vm-api';
import type { VMProject } from '../project-files/vm-api';
import type { RepositoryReviewEnvironment, ReviewVMTarget } from './api';

export function ReviewVMTargetSelector({
  active,
  disabled,
  environment,
  target,
  onChange,
}: {
  active: boolean;
  disabled: boolean;
  environment: RepositoryReviewEnvironment;
  target?: ReviewVMTarget;
  onChange: (next?: ReviewVMTarget) => void;
}) {
  const [machines, setMachines] = useState<Machine[]>([]);
  const [projectsByHost, setProjectsByHost] = useState<
    Record<string, VMProject[]>
  >({});
  const [loaded, setLoaded] = useState(false);
  const [selectedMachineId, setSelectedMachineId] = useState(
    target?.machineId ?? '',
  );

  useEffect(() => {
    setSelectedMachineId(target?.machineId ?? '');
  }, [target?.machineId]);

  useEffect(() => {
    if (!active || environment.source === 'env' || loaded) return;
    const controller = new AbortController();
    void machinesApi
      .machines(controller.signal)
      .then(async ({ machines: list }) => {
        if (controller.signal.aborted) return;
        const safeMachines = Array.isArray(list) ? list : [];
        setMachines(safeMachines);
        const hostIds = Array.from(
          new Set(
            safeMachines.map((machine) => machine.hostId).filter(Boolean),
          ),
        );
        const entries = await Promise.all(
          hostIds.map((hostId) =>
            vmProjectsAPI
              .list(hostId, controller.signal)
              .then((res) => [hostId, res.projects ?? []] as const)
              .catch(() => [hostId, [] as VMProject[]] as const),
          ),
        );
        if (!controller.signal.aborted) {
          setProjectsByHost(Object.fromEntries(entries));
          setLoaded(true);
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) setLoaded(true);
      });
    return () => controller.abort();
  }, [active, environment.source, loaded]);

  if (environment.source === 'env') {
    return (
      <p className="github-review-detail">
        Review VM bound via workstation environment:{' '}
        <strong>{environment.name}</strong>.
      </p>
    );
  }

  const effectiveMachineId = selectedMachineId || target?.machineId || '';
  const selectedMachine = machines.find((m) => m.id === effectiveMachineId);
  const hostProjects = selectedMachine
    ? (projectsByHost[selectedMachine.hostId] ?? []).filter(
        (project) =>
          !selectedMachine.spec.volumeId ||
          project.volumeId === selectedMachine.spec.volumeId,
      )
    : [];

  if (!loaded && machines.length === 0) return null;
  if (loaded && machines.length === 0 && !target) return null;

  function chooseMachine(machineId: string) {
    setSelectedMachineId(machineId);
    if (!machineId) {
      onChange(undefined);
      return;
    }
    const machine = machines.find((item) => item.id === machineId);
    if (!machine) return;
    const matchingProjects = (projectsByHost[machine.hostId] ?? []).filter(
      (project) =>
        !machine.spec.volumeId || project.volumeId === machine.spec.volumeId,
    );
    if (matchingProjects.length === 1) {
      const project = matchingProjects[0];
      onChange({
        name: `${machine.spec.name || machine.id} · ${project.name}`.slice(
          0,
          80,
        ),
        hostId: machine.hostId,
        machineId: machine.id,
        projectId: project.id,
      });
    } else if (target?.machineId !== machineId) {
      onChange(undefined);
    }
  }

  function chooseProject(projectId: string) {
    if (!selectedMachine || !projectId) {
      onChange(undefined);
      return;
    }
    const project = hostProjects.find((item) => item.id === projectId);
    if (!project) return;
    onChange({
      name: `${selectedMachine.spec.name || selectedMachine.id} · ${project.name}`.slice(
        0,
        80,
      ),
      hostId: selectedMachine.hostId,
      machineId: selectedMachine.id,
      projectId: project.id,
    });
  }

  return (
    <div
      className="github-review-vm-binding"
      data-testid="github-review-vm-binding"
    >
      <Select
        label="Review VM"
        value={effectiveMachineId}
        disabled={disabled}
        onChange={(event) => chooseMachine(event.target.value)}
      >
        <option value="">
          {environment.source === 'default' && environment.configured
            ? `Use default (${environment.name})`
            : 'None selected'}
        </option>
        {machines.map((machine) => (
          <option key={machine.id} value={machine.id}>
            {machine.spec.name || machine.id} ({machine.status})
          </option>
        ))}
        {target && !machines.some((m) => m.id === target.machineId) && (
          <option value={target.machineId}>{target.name} (saved)</option>
        )}
      </Select>
      {selectedMachine && (
        <Select
          label="Review VM project"
          value={
            target?.machineId === selectedMachine.id ? target.projectId : ''
          }
          disabled={disabled}
          onChange={(event) => chooseProject(event.target.value)}
        >
          <option value="">
            {hostProjects.length
              ? 'Select a VM project…'
              : 'No VM projects on this VM'}
          </option>
          {hostProjects.map((project) => (
            <option key={project.id} value={project.id}>
              {project.name} ({project.root})
            </option>
          ))}
        </Select>
      )}
    </div>
  );
}
