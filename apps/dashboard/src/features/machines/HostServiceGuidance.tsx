import { useState } from 'react';
import { Button, IconButton } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { canTransferWindowFocus } from '../../shared/desktop/focus-transfer';
import { openTerminalWindow } from '../terminal/definition';
import type { HostUnitState } from './api';
import { linuxVMSetupGuide } from './presentation';

const setupGuide = (
  <a href={linuxVMSetupGuide} target="_blank" rel="noreferrer">
    Linux VM setup guide
  </a>
);
const startCommand = 'sudo systemctl start axv-host.service';
const statusCommand = 'systemctl status axv-host.service --no-pager';
const guidance = {
  inactive: {
    title: 'Start the VM service',
    instruction:
      'The service is installed and stopped. Run this command as an administrator.',
    command: startCommand,
  },
  failed: {
    title: 'The VM service failed to start',
    instruction: 'Run this command to see why the service failed.',
    command: statusCommand,
  },
  active: {
    title: 'Check the VM service socket',
    instruction:
      'Check the running service. If it uses another socket, choose Edit connection in Virtual Machines.',
    command: statusCommand,
  },
  unknown: {
    title: 'Check the VM service',
    instruction:
      'On a systemd host, run this command to check the service. If it is missing, follow the setup guide.',
    command: statusCommand,
  },
};

function quoteShellArgument(value: string) {
  // Protect quote, backslash and history expansion across host shells.
  return "'" + value.replace(/['\\!]/g, "'\\$&'") + "'";
}

export function hostInstallerCommand(
  executablePath: string | undefined,
  command: 'install-service' | 'install-image',
  hostId?: string,
) {
  if (
    !executablePath?.startsWith('/') ||
    /[\p{Cc}\p{Cf}\u2028\u2029]/u.test(executablePath) ||
    (hostId !== undefined && !/^host-[0-9a-f]{32}$/.test(hostId))
  )
    return;
  const target = hostId ? ` --host-id ${hostId}` : '';
  return `sudo ${quoteShellArgument(executablePath)} host ${command}${target}`;
}

function installationStep(arch: string, executablePath?: string) {
  const title = 'Install the VM service';
  if (arch !== 'amd64')
    return {
      title,
      instruction:
        'The built-in installer supports Linux amd64. Ask your host administrator to configure a compatible Firecracker service for this host.',
    };
  const command = hostInstallerCommand(executablePath, 'install-service');
  if (!command)
    return {
      title,
      instruction:
        'AXV could not identify a usable path to its running executable. Choose Machines → Refresh to check again.',
    };
  return {
    title,
    instruction:
      'The command installs Firecracker and the default Guest image, then starts axv-host.service.',
    command,
  };
}

export function getHostServiceGuidance(
  state: HostUnitState | undefined,
  arch: string,
  executablePath?: string,
) {
  return state === 'not_installed'
    ? installationStep(arch, executablePath)
    : guidance[state ?? 'unknown'];
}

export function HostCommand({
  command,
  desktop,
}: {
  command: string;
  desktop: DesktopAPI;
}) {
  const [feedback, setFeedback] = useState<'copying' | 'copied' | 'blocked'>();

  async function copy(openFrom?: HTMLButtonElement) {
    if (feedback === 'copying') return;
    setFeedback('copying');
    try {
      await navigator.clipboard.writeText(command);
    } catch {
      setFeedback('blocked');
      return;
    }
    setFeedback('copied');
    if (openFrom && canTransferWindowFocus(openFrom, openFrom))
      openTerminalWindow(desktop);
  }

  return (
    <>
      <div className="machines-command-line">
        <code className="machines-command">{command}</code>
        <IconButton
          icon="copy"
          label="Copy command"
          aria-disabled={feedback === 'copying'}
          onClick={() => void copy()}
        />
      </div>
      {feedback === 'blocked' && (
        <p role="alert">
          Couldn’t copy to the clipboard. Select and copy the command, then open
          the terminal.
        </p>
      )}
      {feedback === 'copied' && (
        <p role="status">
          Command copied. Paste and run it in the host terminal.
        </p>
      )}
      <div className="machines-host-actions">
        {feedback === 'blocked' ? (
          <Button primary onClick={() => openTerminalWindow(desktop)}>
            Open Host Terminal
          </Button>
        ) : (
          <Button
            primary
            aria-disabled={feedback === 'copying'}
            onClick={(event) => void copy(event.currentTarget)}
          >
            Copy &amp; Open Terminal
          </Button>
        )}
        {setupGuide}
      </div>
    </>
  );
}

export function HostServiceGuidance({
  instruction,
  command,
  desktop,
}: {
  instruction: string;
  command?: string;
  desktop: DesktopAPI;
}) {
  return (
    <div className="machines-host-guidance">
      <p>{instruction}</p>
      {command ? (
        <HostCommand key={command} command={command} desktop={desktop} />
      ) : (
        setupGuide
      )}
    </div>
  );
}
