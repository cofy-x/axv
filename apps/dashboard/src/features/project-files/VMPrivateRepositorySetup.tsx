import { useEffect, useRef, useState } from 'react';
import { Button } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { canTransferWindowFocus } from '../../shared/desktop/focus-transfer';
import {
  guestTerminalRequests,
  openGuestTerminal,
} from '../machines/open-terminal';
import { repositoryURLAllowed, vmInitializationAPI } from './vm-init-api';

export interface PrivateRepositoryTarget {
  hostId: string;
  machineId: string;
  machineName?: string;
  volumeId: string;
  volumeRevision: number;
  bootId?: string;
  instanceId?: string;
}

const GH_SETUP_COMMANDS = `umask 077
mkdir -p "$HOME/.config/gh"
chmod 700 "$HOME/.config/gh"
"$HOME/.local/bin/gh" auth login --hostname github.com --git-protocol https --web
"$HOME/.local/bin/gh" auth setup-git --hostname github.com`;

export function VMPrivateRepositorySetup({
  desktop,
  interactive,
  repository,
  target,
}: {
  desktop: DesktopAPI;
  interactive: boolean;
  repository: string;
  target: PrivateRepositoryTarget | null;
}) {
  const [expanded, setExpanded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const mounted = useRef(true);
  const busyRef = useRef(false);
  const generation = useRef(0);
  const setup = useRef<HTMLDetailsElement>(null);
  const identity = target
    ? JSON.stringify([
        target.hostId,
        target.machineId,
        target.volumeId,
        target.volumeRevision,
        target.bootId,
        target.instanceId,
      ])
    : '';
  useEffect(() => {
    generation.current += 1;
  }, [interactive, expanded, identity]);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      generation.current += 1;
    };
  }, []);

  async function authenticate(trigger: HTMLButtonElement) {
    if (!interactive || !expanded || !target || busyRef.current) return;
    const original = { ...target };
    const originalGeneration = generation.current;
    const current = () =>
      mounted.current && generation.current === originalGeneration;
    busyRef.current = true;
    setBusy(true);
    setError('');
    setNotice('');
    try {
      const health = await vmInitializationAPI.health(
        original.hostId,
        original.machineId,
      );
      if (
        !health.ready ||
        health.machineId !== original.machineId ||
        !health.bootId ||
        !health.instanceId ||
        (original.bootId && health.bootId !== original.bootId) ||
        (original.instanceId && health.instanceId !== original.instanceId)
      )
        throw new Error(
          'The original VM environment is unavailable or changed. Authentication was not redirected. Review the VM before setting up access.',
        );
      if (!current()) return;
      const session = await guestTerminalRequests.execute(
        original.machineId,
        original.hostId,
      );
      const confirmed = await vmInitializationAPI.health(
        original.hostId,
        original.machineId,
      );
      if (
        session.bootId !== health.bootId ||
        !confirmed.ready ||
        confirmed.machineId !== original.machineId ||
        confirmed.bootId !== health.bootId ||
        confirmed.instanceId !== health.instanceId
      )
        throw new Error(
          'The VM or guest service restarted while opening Guest Terminal. Review its current environment before setting up access.',
        );
      if (mounted.current)
        setNotice(
          `Guest Terminal is ready for VM ${original.machineName || original.machineId} (${original.machineId}). Reopen that terminal from the VM Inspector if needed.`,
        );
      if (current() && canTransferWindowFocus(setup.current, trigger))
        openGuestTerminal(
          desktop,
          {
            id: original.machineId,
            hostId: original.hostId,
            spec: { name: original.machineName || original.machineId },
          },
          session,
        );
    } catch (error) {
      if (mounted.current)
        setError(
          `VM ${original.machineName || original.machineId} (${original.machineId}): ${error instanceof Error ? error.message : String(error)}`,
        );
    } finally {
      busyRef.current = false;
      if (mounted.current) setBusy(false);
    }
  }

  async function copyCommands() {
    if (!navigator.clipboard?.writeText) return;
    try {
      await navigator.clipboard.writeText(GH_SETUP_COMMANDS);
      if (mounted.current) {
        setCopied(true);
        setTimeout(() => {
          if (mounted.current) setCopied(false);
        }, 2000);
      }
    } catch {
      // Clipboard may be unavailable outside secure contexts.
    }
  }

  const cleanRepository = repository && repositoryURLAllowed(repository);
  return (
    <details
      className="vm-private-repository-setup"
      ref={setup}
      onToggle={(event) => setExpanded(event.currentTarget.open)}
    >
      <summary className="ns-disclosure">Private repository setup</summary>
      <div className="vm-private-setup-body">
        <div className="vm-private-setup-header">
          <div className="vm-private-setup-meta">
            {target ? (
              <p>
                VM: <strong>{target.machineName || target.machineId}</strong> ·
                Guest user: <code>axv (/home/axv)</code>
              </p>
            ) : (
              <p>
                Choose a ready retained volume to select the authentication VM.
              </p>
            )}
            {cleanRepository ? (
              <p className="vm-private-setup-repo">
                Repository: <code>{repository}</code>
              </p>
            ) : (
              <p className="vm-private-setup-hint">
                Use a repository URL without credentials, query strings or
                fragments.
              </p>
            )}
          </div>
          <Button
            disabled={!interactive || !target || busy}
            onClick={(event) => void authenticate(event.currentTarget)}
          >
            {busy ? 'Opening…' : 'Guest Terminal'}
          </Button>
        </div>
        {error && (
          <p className="project-error" role="alert">
            {error}
          </p>
        )}
        {notice && (
          <p className="project-feedback" role="status">
            {notice}
          </p>
        )}
        {target && (
          <details className="vm-private-setup-target">
            <summary>Target details</summary>
            <dl>
              <dt>VM</dt>
              <dd>
                <code>{target.machineId}</code>
              </dd>
              <dt>Host</dt>
              <dd>
                <code>{target.hostId}</code>
              </dd>
              {target.bootId && (
                <>
                  <dt>Original boot</dt>
                  <dd>
                    <code>{target.bootId}</code>
                  </dd>
                </>
              )}
            </dl>
          </details>
        )}
        <ol className="vm-private-setup-steps">
          <li>
            Open <strong>Guest Terminal</strong> as the guest service user
            (without sudo). Existing Git credential helpers and SSH keys work
            directly.
          </li>
          <li>
            For GitHub HTTPS, install{' '}
            <a href="https://cli.github.com/" target="_blank" rel="noreferrer">
              GitHub CLI
            </a>{' '}
            (e.g. <code>~/.local/bin/gh</code>) inside the guest and run:
            <div className="vm-private-setup-code">
              <pre>{GH_SETUP_COMMANDS}</pre>
              <Button
                type="button"
                className="vm-private-setup-copy"
                disabled={!interactive}
                onClick={() => void copyCommands()}
              >
                {copied ? 'Copied' : 'Copy'}
              </Button>
            </div>
            <span className="vm-private-setup-step-note">
              Open the device URL in your browser (use your installed{' '}
              <code>gh</code> path if different). See{' '}
              <a
                href="https://cli.github.com/manual/gh_auth_login"
                target="_blank"
                rel="noreferrer"
              >
                GitHub CLI auth docs
              </a>
              .
            </span>
          </li>
          <li>
            Return here and clone into a new directory. Persist credential
            helper or SSH config (with known hosts) — terminal-only environment
            exports and SSH agents are not inherited by clone tasks.
          </li>
        </ol>
        <div className="vm-private-setup-notes">
          <p>
            Keep credentials off the retained project volume (store them on the
            VM root disk under the guest user with <code>0700</code>/
            <code>0600</code> permissions). VM replacement requires setting up
            authentication again.
          </p>
          <p>
            Recover checks the original initialization receipt; it does not
            rerun a failed clone and partial files remain until cleaned up.
          </p>
        </div>
      </div>
    </details>
  );
}
