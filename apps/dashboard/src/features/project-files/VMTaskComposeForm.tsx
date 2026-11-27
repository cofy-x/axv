import type { RefObject } from 'react';
import { Button, Field, TextArea } from '@cofy-x/axv-nextstep';
import type { VMProjectBinding } from './vm-api';

export interface VMTaskComposeFormProps {
  composerRef: RefObject<HTMLFormElement | null>;
  executionOptionsRef: RefObject<HTMLDetailsElement | null>;
  composing: boolean;
  interactive: boolean;
  busy: boolean;
  hasPending: boolean;
  hasRecoveryError: boolean;
  submitting: boolean;
  binding: VMProjectBinding | null;
  hostId: string;
  projectId: string;
  command: string;
  directory: string;
  timeout: number;
  cpus: number;
  memoryMiB: number;
  maxProcesses: number;
  logKiB: number;
  customOptions: boolean;
  onFocusCapture: () => void;
  onSubmit: () => void;
  onCommandChange: (value: string) => void;
  onDirectoryChange: (value: string) => void;
  onTimeoutChange: (value: number) => void;
  onCPUsChange: (value: number) => void;
  onMemoryMiBChange: (value: number) => void;
  onMaxProcessesChange: (value: number) => void;
  onLogKiBChange: (value: number) => void;
}

export function VMTaskComposeForm({
  composerRef,
  executionOptionsRef,
  composing,
  interactive,
  busy,
  hasPending,
  hasRecoveryError,
  submitting,
  binding,
  hostId,
  projectId,
  command,
  directory,
  timeout,
  cpus,
  memoryMiB,
  maxProcesses,
  logKiB,
  customOptions,
  onFocusCapture,
  onSubmit,
  onCommandChange,
  onDirectoryChange,
  onTimeoutChange,
  onCPUsChange,
  onMemoryMiBChange,
  onMaxProcessesChange,
  onLogKiBChange,
}: VMTaskComposeFormProps) {
  const fieldsDisabled = !interactive || busy || hasPending || hasRecoveryError;

  return (
    <form
      className="vm-task-form"
      aria-label="New task"
      noValidate
      hidden={!composing}
      ref={composerRef}
      onFocusCapture={onFocusCapture}
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit();
      }}
    >
      <p className="vm-task-target" title={binding?.machineId}>
        <strong>
          {binding ? `VM · ${binding.machineId}` : 'No VM available'}
        </strong>
      </p>
      <TextArea
        label="Shell command"
        value={command}
        rows={2}
        spellCheck={false}
        disabled={fieldsDisabled}
        onChange={(event) => onCommandChange(event.target.value)}
      />
      <details className="vm-task-options" ref={executionOptionsRef}>
        <summary>
          <span>Execution options</span>
          {customOptions ? ' · Customized' : ''}
        </summary>
        <div className="vm-task-fields">
          <Field
            label="Directory in project"
            value={directory}
            disabled={fieldsDisabled}
            onChange={(event) => onDirectoryChange(event.target.value)}
            placeholder="Project root"
          />
          <Field
            label="Timeout (seconds)"
            type="number"
            min={1}
            max={86400}
            value={timeout}
            disabled={fieldsDisabled}
            onChange={(event) => onTimeoutChange(Number(event.target.value))}
          />
        </div>
        <div className="vm-task-limit-fields">
          <Field
            label="CPU limit"
            type="number"
            min={1}
            max={128}
            value={cpus}
            disabled={fieldsDisabled}
            onChange={(event) => onCPUsChange(Number(event.target.value))}
          />
          <Field
            label="Memory limit (MiB)"
            type="number"
            min={64}
            max={1048576}
            value={memoryMiB}
            disabled={fieldsDisabled}
            onChange={(event) => onMemoryMiBChange(Number(event.target.value))}
          />
          <Field
            label="Process limit"
            type="number"
            min={2}
            max={256}
            value={maxProcesses}
            disabled={fieldsDisabled}
            onChange={(event) =>
              onMaxProcessesChange(Number(event.target.value))
            }
          />
          <Field
            label="Log limit (KiB)"
            type="number"
            min={1}
            max={8192}
            value={logKiB}
            disabled={fieldsDisabled}
            onChange={(event) => onLogKiBChange(Number(event.target.value))}
          />
        </div>
        <details>
          <summary>Execution environment</summary>
          <dl className="info-list">
            <dt>Host / project</dt>
            <dd>
              <code>
                {hostId} / {projectId}
              </code>
            </dd>
            <dt>VM / volume</dt>
            <dd>
              <code>
                {binding
                  ? `${binding.machineId} / ${binding.volumeId}`
                  : 'Unavailable'}
              </code>
            </dd>
            <dt>Boot / guest</dt>
            <dd>
              <code>
                {binding
                  ? `${binding.bootId} / ${binding.instanceId}`
                  : 'Unavailable'}
              </code>
            </dd>
            <dt>New task limits</dt>
            <dd>
              Unsupported guest enforcement rejects the task. Reaching the log
              limit stops that attempt.
            </dd>
            <dt>Additional controls</dt>
            <dd>Per-task disk, I/O and network controls are unavailable.</dd>
          </dl>
        </details>
      </details>
      <div className="vm-task-compose-actions">
        <Button
          type="submit"
          primary
          aria-label={submitting ? 'Submitting…' : 'Run in VM'}
          disabled={fieldsDisabled || !binding || !command.trim()}
        >
          {submitting ? 'Submitting…' : 'Run'}
        </Button>
      </div>
    </form>
  );
}
