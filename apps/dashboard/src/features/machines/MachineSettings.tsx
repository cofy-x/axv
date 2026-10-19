import { useEffect, useState } from 'react';
import { Button, Checkbox, Field, Select } from '@cofy-x/axv-nextstep';
import { machinesApi } from './api';
import type { HostInfo, Machine, MachineSpec, Operation } from './api';
import {
  configurationDraft,
  configurationSpec,
  equalDraft,
  integer,
  machineNameError,
  reconcileConfiguration,
} from './form-state';
import type { ConfigurationDraft, ConfigurationSubmission } from './form-state';
import { OperationFeedback, RequestDetails } from './OperationFeedback';
import { OperationRequest, operationError } from './operation-requests';

export function MachineSettings({
  machine,
  machines,
  info,
  active,
  operations,
  unavailable = false,
  onSaved,
}: {
  machine: Machine;
  machines: Machine[];
  info?: HostInfo;
  active: boolean;
  operations: Operation[];
  unavailable?: boolean;
  onSaved: () => void;
}) {
  const [base, setBase] = useState(machine);
  const [draft, setDraft] = useState(() =>
    configurationDraft(machine.pendingSpec || machine.spec),
  );
  const [renewLease, setRenewLease] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [submission, setSubmission] = useState<ConfigurationSubmission>();
  const [accepted, setAccepted] = useState<{
    operation: Operation;
    label: string;
  }>();
  const [requests] = useState(
    () =>
      new OperationRequest(
        (
          input: {
            base: Machine;
            spec: MachineSpec;
            draft: ConfigurationDraft;
            renewLease: boolean;
          },
          id,
        ) => machinesApi.update(input.base, input.spec, input.renewLease, id),
      ),
  );
  const provider = info?.providers?.find(
    (item) => item.provider === machine.spec.provider,
  );
  const stopped = machine.status === 'stopped' && machine.desired === 'stopped';
  const resourceDisabled = !stopped || !provider?.configuration;
  const locked = busy || !!requests.pending;
  const dirty =
    !equalDraft(draft, configurationDraft(base.pendingSpec || base.spec)) ||
    renewLease;
  const minRoot = Math.max(
    2,
    machine.spec.resources.diskGiB,
    machine.pendingSpec?.resources.diskGiB || 0,
    info?.images?.find((item) => item.id === machine.spec.imageId)
      ?.minRootGiB || 0,
  );
  const validation =
    machineNameError(draft.name, machines, machine.id) ||
    (!integer(draft.cpus, 1, info?.capacity?.cpus)
      ? 'Enter a whole CPU count within the host capacity.'
      : !integer(draft.memoryMiB, 128, info?.capacity?.memoryMiB)
        ? 'Enter memory of at least 128 MiB within the host capacity.'
        : !integer(draft.diskGiB, minRoot, info?.capacity?.diskGiB)
          ? `Root disk must be at least ${minRoot} GiB and within the host capacity.`
          : draft.kind === 'task' && !integer(draft.ttlMinutes, 1, 43200)
            ? 'Task leases must be 1–43,200 whole minutes.'
            : '');
  const disabledReason = unavailable
    ? 'Reconnect to the host before applying changes.'
    : !provider?.available
      ? provider?.reason || 'The VM provider is unavailable.'
      : active
        ? 'Wait for the current VM or volume operation to finish before applying changes. You can keep editing this draft.'
        : validation;

  useEffect(() => {
    if (locked || (renewLease && !submission)) return;
    const next = reconcileConfiguration(
      base,
      machine,
      draft,
      submission,
      operations,
    );
    if (next) {
      setBase(next.base);
      setDraft(next.draft);
      setSubmission(undefined);
    }
  }, [base, machine, draft, submission, operations, locked, renewLease]);

  function change<Key extends keyof ConfigurationDraft>(
    key: Key,
    value: ConfigurationDraft[Key],
  ) {
    setDraft((current) => ({ ...current, [key]: value }));
  }

  function reload() {
    setBase(machine);
    setDraft(configurationDraft(machine.pendingSpec || machine.spec));
    setRenewLease(false);
    setError('');
    setSubmission(undefined);
  }

  async function save(retry = false) {
    if (unavailable) return;
    if (!retry && (disabledReason || (!dirty && !machine.pendingSpec))) return;
    setBusy(true);
    setError('');
    const input = retry
      ? requests.pending!.input
      : {
          base,
          spec: configurationSpec(base.pendingSpec || base.spec, draft),
          draft,
          renewLease,
        };
    try {
      const result = retry
        ? await requests.retry()
        : await requests.submit(input, `Configure “${input.spec.name}”`);
      setSubmission({
        revision: input.base.revision,
        spec: input.spec,
        draft: input.draft,
        operationId: result.operation.id,
      });
      if (
        result.machine.revision === input.base.revision + 1 ||
        (result.machine.revision === input.base.revision + 2 &&
          result.operation.status === 'succeeded')
      )
        setBase(result.machine);
      if (
        result.operation.status === 'succeeded' &&
        result.machine.revision === input.base.revision + 2
      ) {
        setSubmission(undefined);
        setDraft((current) =>
          equalDraft(current, input.draft)
            ? configurationDraft(result.machine.spec)
            : current,
        );
      }
      setAccepted({
        operation: result.operation,
        label: `Configure “${input.spec.name}”`,
      });
      setRenewLease(false);
      onSaved();
    } catch (failure) {
      setError(operationError(failure));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form
      className="machines-form"
      onSubmit={(event) => {
        event.preventDefault();
        void save();
      }}
    >
      <fieldset disabled={locked}>
        <legend>Configuration</legend>
        <Field
          label="VM name"
          value={draft.name}
          required
          maxLength={80}
          onChange={(event) => change('name', event.target.value)}
        />
        <div className="machines-resource-fields">
          <Field
            label="CPUs"
            type="number"
            min={1}
            max={info?.capacity?.cpus}
            value={draft.cpus}
            disabled={resourceDisabled}
            required
            onChange={(event) => change('cpus', event.target.value)}
          />
          <Field
            label="Memory (MiB)"
            type="number"
            min={128}
            max={info?.capacity?.memoryMiB}
            value={draft.memoryMiB}
            disabled={resourceDisabled}
            required
            onChange={(event) => change('memoryMiB', event.target.value)}
          />
          <Field
            label="Root disk (GiB)"
            type="number"
            min={minRoot}
            max={info?.capacity?.diskGiB}
            value={draft.diskGiB}
            disabled={resourceDisabled}
            required
            onChange={(event) => change('diskGiB', event.target.value)}
          />
        </div>
        <p className="machines-form-help">
          Stop the VM to change resources; provider support is required. Root
          disk: at least {minRoot} GiB, grow only. Host limits apply.
        </p>
        <Select
          label="VM policy"
          value={draft.kind}
          onChange={(event) => {
            const kind = event.target.value as MachineSpec['kind'];
            setDraft((current) => ({
              ...current,
              kind,
              retainVolume: kind === 'development' || current.retainVolume,
            }));
            if (kind === 'development') setRenewLease(false);
          }}
        >
          <option value="development">Development · no expiry</option>
          <option value="task">Task · lease</option>
        </Select>
        {draft.kind === 'task' && (
          <>
            <Field
              label="Task lease (minutes)"
              type="number"
              min={1}
              max={43200}
              value={draft.ttlMinutes}
              required
              onChange={(event) => change('ttlMinutes', event.target.value)}
            />
            <Checkbox
              label="Renew the lease from now"
              checked={renewLease}
              onChange={(event) => setRenewLease(event.target.checked)}
            />
            <Checkbox
              label="Keep project volume after VM deletion"
              checked={draft.retainVolume}
              onChange={(event) => change('retainVolume', event.target.checked)}
            />
            <p className="machines-form-help">
              Expiry can delete a VM during active work. Changing the lease
              duration starts a new lease from now.
            </p>
          </>
        )}
      </fieldset>
      {machine.pendingSpec && (
        <p className="machines-warning">
          Configuration is pending. Failed changes must be retried before the VM
          can start.
        </p>
      )}
      {machine.revision !== base.revision && (
        <p className="machines-warning">
          The VM changed since this draft was loaded. Your edits were preserved.
          Reload discards them; applying this draft checks its original
          revision.
        </p>
      )}
      {disabledReason && <p className="machines-warning">{disabledReason}</p>}
      {error && (
        <p className="machines-error" role="alert">
          {error}
        </p>
      )}
      <OperationFeedback accepted={accepted} operations={operations} />
      {requests.pending && !busy && (
        <>
          <p className="machines-warning">
            The response was interrupted. Resolve the original request before
            making another change.
          </p>
          <RequestDetails
            requestId={requests.pending.requestId}
            hostId={requests.pending.input.base.hostId}
          />
        </>
      )}
      <div className="machines-form-actions">
        {requests.pending && !busy ? (
          <Button disabled={unavailable} onClick={() => void save(true)}>
            Retry Original Request
          </Button>
        ) : (
          <Button
            type="submit"
            disabled={
              locked || !!disabledReason || (!dirty && !machine.pendingSpec)
            }
          >
            {busy ? 'Applying…' : 'Apply'}
          </Button>
        )}
        <Button disabled={locked || active} onClick={reload}>
          Reload
        </Button>
      </div>
    </form>
  );
}
