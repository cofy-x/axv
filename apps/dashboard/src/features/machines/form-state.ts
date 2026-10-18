import type { HostInfo, Machine, MachineSpec, Operation } from './api';

export function integer(value: string, minimum: number, maximum = Infinity) {
  const result = Number(value);
  return value.trim() &&
    Number.isSafeInteger(result) &&
    result >= minimum &&
    result <= maximum
    ? result
    : undefined;
}

export function nameError(value: string) {
  return normalizedNameError(value.trim());
}

function normalizedNameError(name: string) {
  return !name ||
    new TextEncoder().encode(name).length > 80 ||
    /[\r\n\0]/.test(name)
    ? 'Enter a name of 1–80 UTF-8 bytes without line breaks.'
    : '';
}

/** Match Go strings.TrimSpace without changing case or Unicode normalization. */
export function normalizeMachineName(value: string) {
  return value.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, '');
}

export function machineNameError(
  value: string,
  machines: Machine[] = [],
  ownId?: string,
) {
  const name = normalizeMachineName(value);
  const invalid = normalizedNameError(name);
  if (invalid) return invalid;
  if (/^vm-[0-9a-f]{32}$/.test(name))
    return 'Choose a name that does not use the reserved VM ID format.';
  if (
    machines.some(
      (machine) =>
        machine.id !== ownId &&
        machine.status !== 'deleted' &&
        (machine.spec.name === name || machine.pendingSpec?.name === name),
    )
  )
    return 'Another VM already uses or reserves this name. Choose a different name.';
  return '';
}

export function creationDefaults(info?: HostInfo) {
  if (!info?.defaults) return undefined;
  const { spec } = info.defaults;
  const image = info.images?.find((item) => item.id === spec.imageId);
  return {
    ...info.defaults,
    spec: {
      ...spec,
      resources: {
        ...spec.resources,
        diskGiB: Math.max(spec.resources.diskGiB, image?.minRootGiB || 2),
      },
    },
  };
}

export function resourceBusy(
  operations: Operation[],
  ...ids: (string | undefined)[]
) {
  return operations.some(
    (operation) =>
      ['queued', 'running'].includes(operation.status) &&
      ids.some(
        (id) =>
          !!id &&
          (operation.machineId === id ||
            operation.volumeId === id ||
            operation.resourceId === id ||
            operation.locks?.includes(id)),
      ),
  );
}

export interface ConfigurationDraft {
  name: string;
  cpus: string;
  memoryMiB: string;
  diskGiB: string;
  kind: MachineSpec['kind'];
  ttlMinutes: string;
  retainVolume: boolean;
}

export function configurationDraft(spec: MachineSpec): ConfigurationDraft {
  return {
    name: spec.name,
    cpus: String(spec.resources.cpus),
    memoryMiB: String(spec.resources.memoryMiB),
    diskGiB: String(spec.resources.diskGiB),
    kind: spec.kind,
    ttlMinutes: String((spec.ttlSeconds || 3600) / 60),
    retainVolume: spec.retainVolume,
  };
}

export function configurationSpec(
  base: MachineSpec,
  draft: ConfigurationDraft,
): MachineSpec {
  return {
    ...base,
    name: normalizeMachineName(draft.name),
    resources: {
      cpus: Number(draft.cpus),
      memoryMiB: Number(draft.memoryMiB),
      diskGiB: Number(draft.diskGiB),
    },
    kind: draft.kind,
    ttlSeconds: draft.kind === 'task' ? Number(draft.ttlMinutes) * 60 : 0,
    retainVolume: draft.kind === 'development' || draft.retainVolume,
  };
}

export function equalDraft(a: ConfigurationDraft, b: ConfigurationDraft) {
  return JSON.stringify(a) === JSON.stringify(b);
}

export interface ConfigurationSubmission {
  revision: number;
  spec: MachineSpec;
  draft: ConfigurationDraft;
  operationId: string;
}

/** Only our exact completed write can advance a dirty form's revision. */
export function reconcileConfiguration(
  base: Machine,
  current: Machine,
  draft: ConfigurationDraft,
  submission: ConfigurationSubmission | undefined,
  operations: Operation[],
) {
  if (base.revision === current.revision) return undefined;
  const operation = operations.find(
    (item) => item.id === submission?.operationId,
  );
  const ownCompletion =
    submission &&
    operation?.status === 'succeeded' &&
    !current.pendingSpec &&
    current.revision === submission.revision + 2 &&
    equalDraft(
      configurationDraft(current.spec),
      configurationDraft(submission.spec),
    );
  if (ownCompletion)
    return {
      base: current,
      draft: equalDraft(draft, submission.draft)
        ? configurationDraft(current.spec)
        : draft,
    };
  if (
    !submission &&
    equalDraft(draft, configurationDraft(base.pendingSpec || base.spec))
  )
    return {
      base: current,
      draft: configurationDraft(current.pendingSpec || current.spec),
    };
  return undefined;
}
