import { useRef } from 'react';
import { Button, IconButton } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { Machine } from './api';
import type { VMAction } from './lifecycle';

export function MachineExpiry({ expiresAt }: { expiresAt?: string }) {
  if (!expiresAt) return null;
  const deadline = new Date(expiresAt);
  return (
    <p className="machines-vm-expiry">
      {deadline.getTime() <= Date.now() ? 'Expired' : 'Expires'}{' '}
      <time dateTime={expiresAt}>
        {deadline.toLocaleString(undefined, {
          dateStyle: 'short',
          timeStyle: 'short',
        })}
      </time>
    </p>
  );
}

interface MachineActionsProps {
  desktop: DesktopAPI;
  windowId: string;
  machine?: Machine;
  reason: (action: VMAction) => string | undefined;
  transition?: string;
  terminalReason?: string;
  terminalLabel?: string;
  terminalPending?: boolean;
  onAction: (action: VMAction) => void;
  onTerminal: () => void;
  onResume?: () => void;
  onCopyLimaShell?: () => void;
  limaShellPending?: boolean;
  onInspect?: () => void;
  onProjectFiles?: () => void;
}

export function MachineActions(props: MachineActionsProps) {
  const {
    desktop,
    windowId,
    machine,
    reason,
    transition,
    terminalReason,
    terminalLabel = 'Open Terminal',
    terminalPending,
    onAction,
    onTerminal,
    onResume,
    onCopyLimaShell,
    limaShellPending,
    onInspect,
    onProjectFiles,
  } = props;
  const current = useRef(props);
  current.current = props;
  if (!machine) return null;
  const primary =
    (machine.status !== 'stopped' && machine.desired === 'running') ||
    machine.status === 'running' ||
    machine.status === 'unknown'
      ? 'stop'
      : 'start';
  const terminalAvailable = !transition && (!terminalReason || terminalPending);

  return (
    <div className="machines-action-buttons">
      {terminalAvailable && onResume && (
        <Button primary onClick={onResume}>
          Resume Terminal
        </Button>
      )}
      {terminalAvailable && (
        <Button
          primary={!onResume}
          disabled={terminalPending}
          onClick={onTerminal}
        >
          {terminalPending
            ? 'Opening Terminal…'
            : onResume
              ? 'New Terminal'
              : terminalLabel}
        </Button>
      )}
      {!terminalAvailable && !transition && onCopyLimaShell && (
        <Button disabled={limaShellPending} onClick={onCopyLimaShell}>
          {limaShellPending ? 'Copying…' : 'Copy Lima Shell'}
        </Button>
      )}
      {!transition && (
        <Button
          primary={
            !terminalAvailable &&
            (primary === 'start' || machine.status === 'unknown')
          }
          disabled={!!reason(primary)}
          title={reason(primary)}
          onClick={() => onAction(primary)}
        >
          {primary === 'start' ? 'Start VM' : 'Stop'}
        </Button>
      )}
      {onInspect && <Button onClick={onInspect}>Inspect</Button>}
      {onProjectFiles && (
        <Button onClick={onProjectFiles}>Project Files</Button>
      )}
      {!transition && (
        <IconButton
          icon="more"
          label="More VM actions"
          aria-haspopup="menu"
          onClick={(event) => {
            if (!machine) return;
            const rect = event.currentTarget.getBoundingClientRect();
            const invoke = (action: VMAction) => {
              const latest = current.current;
              if (
                latest.machine?.hostId !== machine.hostId ||
                latest.machine.id !== machine.id ||
                latest.transition ||
                latest.reason(action)
              )
                return;
              latest.onAction(action);
            };
            desktop.contextMenu(
              [
                ...(primary === 'start' && !reason('stop')
                  ? [{ label: 'Stop', action: () => invoke('stop') }]
                  : []),
                {
                  label: 'Restart',
                  disabled: !!reason('restart'),
                  action: () => invoke('restart'),
                },
                {
                  label: 'Force Stop',
                  disabled: !!reason('force-stop'),
                  action: () => invoke('force-stop'),
                },
                { separator: true },
                {
                  label: 'Delete',
                  disabled: !!reason('delete'),
                  action: () => invoke('delete'),
                },
              ],
              { x: rect.left, y: rect.bottom },
              { title: machine.spec.name, ownerWindowId: windowId },
            );
          }}
        />
      )}
    </div>
  );
}
