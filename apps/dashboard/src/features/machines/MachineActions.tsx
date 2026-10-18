import { useRef } from 'react';
import { Button, IconButton } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { Machine } from './api';
import type { VMAction } from './lifecycle';

interface MachineActionsProps {
  desktop: DesktopAPI;
  machine?: Machine;
  reason: (action: VMAction) => string | undefined;
  terminalReason?: string;
  onAction: (action: VMAction) => void;
  onTerminal: () => void;
  onInspect?: () => void;
}

export function MachineActions(props: MachineActionsProps) {
  const {
    desktop,
    machine,
    reason,
    terminalReason,
    onAction,
    onTerminal,
    onInspect,
  } = props;
  const current = useRef(props);
  current.current = props;
  const primary =
    machine?.status === 'running' || machine?.desired === 'running'
      ? 'stop'
      : 'start';

  return (
    <div className="machines-action-buttons">
      <Button
        disabled={!!reason(primary)}
        title={reason(primary)}
        onClick={() => onAction(primary)}
      >
        {primary === 'start' ? 'Start' : 'Stop'}
      </Button>
      <Button
        disabled={!!terminalReason}
        title={terminalReason}
        onClick={onTerminal}
      >
        New Terminal
      </Button>
      {onInspect && (
        <Button disabled={!machine} onClick={onInspect}>
          Inspect
        </Button>
      )}
      <IconButton
        icon="more"
        label="More VM actions"
        disabled={!machine}
        aria-haspopup="menu"
        onClick={(event) => {
          if (!machine) return;
          const rect = event.currentTarget.getBoundingClientRect();
          const invoke = (action: VMAction) => {
            const latest = current.current;
            if (
              latest.machine?.hostId !== machine.hostId ||
              latest.machine.id !== machine.id ||
              latest.reason(action)
            )
              return;
            latest.onAction(action);
          };
          desktop.contextMenu(
            [
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
            { title: machine.spec.name },
          );
        }}
      />
    </div>
  );
}
