import { Button, IconButton, PixelIcon } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { useCodexAvailability } from '../codex/CodexContext';

export function Welcome({ desktop, window }: AppWindowProps) {
  const { info } = useCodexAvailability();

  return (
    <div className="welcome-app">
      <div className="welcome-banner">
        <img src="/favicon.svg" alt="" width={64} height={64} />
        <div>
          <strong>Welcome to AXV</strong>
          <span>Your personal cloud for VMs and coding agents.</span>
        </div>
      </div>
      <div className="welcome-copy">
        <div className="welcome-links">
          {[
            {
              app: 'machines',
              icon: 'virtual-machines' as const,
              name: 'Virtual Machines',
              text: 'Create and manage VMs.',
            },
            ...(info?.installed
              ? [
                  {
                    app: 'codex',
                    icon: 'companion' as const,
                    name: 'Codex',
                    text: 'Start or resume coding.',
                  },
                ]
              : []),
            {
              app: 'terminal',
              icon: 'terminal' as const,
              name: 'Terminal',
              text: 'Open a host shell.',
            },
            {
              app: 'files',
              icon: 'folder' as const,
              name: 'Documents',
              text: 'Write notes in AXV.',
              params: { folderId: 'documents' },
            },
            {
              app: 'project-files',
              icon: 'project' as const,
              name: 'Project Files',
              text: 'Browse host files or edit VM projects.',
            },
          ].map((link) => (
            <button
              key={link.app}
              className="welcome-link"
              onClick={() =>
                link.app === 'terminal'
                  ? desktop.launchApp(link.app)
                  : desktop.openApp(link.app, link.params)
              }
            >
              <PixelIcon name={link.icon} />
              <span>
                <strong>{link.name}</strong>
                <small>{link.text}</small>
              </span>
            </button>
          ))}
        </div>
      </div>
      <div className="app-toolbar welcome-footer">
        <IconButton
          icon="more"
          label="More welcome actions"
          aria-haspopup="menu"
          onClick={(event) => {
            const rect = event.currentTarget.getBoundingClientRect();
            desktop.contextMenu(
              [
                {
                  label: 'Preferences',
                  action: () => {
                    desktop.openApp('appearance');
                  },
                },
                {
                  label: 'About AXV',
                  action: () => {
                    desktop.openApp('about');
                  },
                },
              ],
              { x: rect.left, y: rect.bottom },
              { title: 'Welcome' },
            );
          }}
        />
        <Button primary onClick={() => void desktop.closeWindow(window.id)}>
          Show Desktop
        </Button>
      </div>
    </div>
  );
}

export function About() {
  return (
    <div className="about-app app-pad">
      <img
        className="brand-mark"
        src="/favicon.svg"
        alt=""
        width={64}
        height={64}
      />
      <h1>AXV</h1>
      <p>Your personal cloud for VMs and coding agents.</p>
      <hr />
      <dl className="info-list">
        <dt>Name</dt>
        <dd>Agents × Virtualization</dd>
        <dt>By</dt>
        <dd>cofy-x</dd>
      </dl>
      <p className="fine-print">
        Documents and desktop layout stay on the AXV host. VM project files stay
        on their retained volumes.
      </p>
    </div>
  );
}
