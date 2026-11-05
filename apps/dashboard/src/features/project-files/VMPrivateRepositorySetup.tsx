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

  const cleanRepository = repository && repositoryURLAllowed(repository);
  return (
    <details
      className="vm-private-repository-setup"
      ref={setup}
      onToggle={(event) => setExpanded(event.currentTarget.open)}
    >
      <summary>Private repository setup</summary>
      <p>
        Preconfigured guest credentials already support private clones. Set up
        access in the VM that will run Git.
      </p>
      {target ? (
        <>
          <p>
            VM: <strong>{target.machineName || target.machineId}</strong>. Guest
            user: <code>axv</code> (<code>/home/axv</code>) in AXV images.
            Custom guests must use their guest service account.
          </p>
          <details>
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
        </>
      ) : (
        <p>Choose a ready retained volume to select the authentication VM.</p>
      )}
      {cleanRepository ? (
        <p>
          Repository: <code>{repository}</code>
        </p>
      ) : (
        <p>
          Use a repository URL without credentials, query strings or fragments.
        </p>
      )}
      <Button
        disabled={!interactive || !target || busy}
        onClick={(event) => void authenticate(event.currentTarget)}
      >
        {busy ? 'Opening…' : 'Guest Terminal'}
      </Button>
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      <ol>
        <li>
          In Guest Terminal, use the guest service user without sudo. Existing
          Git credential helpers and SSH configurations remain supported.
        </li>
        <li>
          For GitHub HTTPS, install{' '}
          <a href="https://cli.github.com/" target="_blank" rel="noreferrer">
            GitHub CLI
          </a>{' '}
          inside the guest first; AXV images do not include it. A user-owned
          installation at <code>~/.local/bin/gh</code> does not need sudo. For
          that location, run:
          <pre>{`umask 077
mkdir -p "$HOME/.config/gh"
chmod 700 "$HOME/.config/gh"
"$HOME/.local/bin/gh" auth login --hostname github.com --git-protocol https --web
"$HOME/.local/bin/gh" auth setup-git --hostname github.com`}</pre>
          For another installation, substitute its absolute executable path. The
          helper must keep using that path for later tasks. Open the displayed
          GitHub device URL in your browser. Organization policies and
          repository permissions still apply. A missing private repository and
          denied access may look the same.
        </li>
        <li>
          Return here and clone into a new directory. Clone runs as a separate,
          noninteractive task. Persist the helper configuration; terminal-only
          environment exports and SSH agents are not inherited by later tasks.
          For SSH, verify the server fingerprint and configure known hosts and a
          key usable by the same guest user without a prompt.
        </li>
      </ol>
      <p>
        Without an OS keyring, GitHub CLI may store its token in a plaintext
        file. Check the path reported by GitHub CLI and restrict the directory
        and file to the guest user (permissions 700 and 600). Revoke the token
        in GitHub account settings when no longer needed. Read{' '}
        <a
          href="https://cli.github.com/manual/gh_auth_login"
          target="_blank"
          rel="noreferrer"
        >
          GitHub CLI authentication
        </a>{' '}
        for storage and account requirements.
      </p>
      <p>
        Credentials belong to this VM user and can be used by other programs
        running as that user. Keep credentials off the retained project volume.
        The volume preserves project files, while VM replacement may require
        setting up authentication again on its new root disk.
      </p>
      <p>
        Recover checks the original initialization receipt; it does not rerun a
        failed clone. Partial files remain. Inspect Tasks and the volume
        workspace before explicitly choosing another directory or cleaning up in
        the VM.
      </p>
    </details>
  );
}
