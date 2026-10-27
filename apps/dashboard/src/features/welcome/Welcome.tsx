import { Button, IconButton, PixelIcon } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';

export function Welcome({ desktop, window }: AppWindowProps) {
  return (
    <div className="welcome-app">
      <div className="welcome-banner">
        <img src="/favicon.svg" alt="" width={48} height={48} />
        <div>
          <strong>Welcome to AXV</strong>
          <span>Your personal cloud for VMs and coding agents.</span>
        </div>
      </div>
      <div className="welcome-copy">
        <h2>Start working</h2>
        <div className="welcome-links" aria-label="Work applications">
          {[
            {
              app: 'codex',
              icon: 'companion' as const,
              name: 'Codex',
              text: 'Resume coding and review sessions that need attention.',
            },
            {
              app: 'machines',
              icon: 'virtual-machines' as const,
              name: 'Virtual Machines',
              text: 'Create and manage your development machines.',
            },
            {
              app: 'project-files',
              icon: 'project' as const,
              name: 'Project Files',
              text: 'Browse projects, edit files and preview your work.',
            },
          ].map((link) => (
            <button
              key={link.app}
              className="welcome-link"
              onClick={() => desktop.openApp(link.app)}
            >
              <PixelIcon name={link.icon} size={40} />
              <span>
                <strong>{link.name}</strong>
                <small>{link.text}</small>
              </span>
            </button>
          ))}
        </div>
        <div className="welcome-tools" aria-label="Desktop tools">
          <Button
            onClick={() => desktop.openApp('files', { folderId: 'documents' })}
          >
            Documents
          </Button>
          <Button onClick={() => desktop.launchApp('terminal')}>
            Terminal
          </Button>
          <Button onClick={() => desktop.openApp('applications')}>
            Applications
          </Button>
        </div>
        <details className="welcome-remote">
          <summary>Connect from another device</summary>
          <p>
            Set up private access with Tailscale, or browser access with
            Cloudflare. Configure access on the AXV host before connecting.
          </p>
          <div className="welcome-tools">
            <Button onClick={() => desktop.openApp('remote-access')}>
              Tailscale
            </Button>
            <Button onClick={() => desktop.openApp('cloudflare')}>
              Cloudflare
            </Button>
          </div>
        </details>
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
                  label: 'Developer Tools',
                  children: [
                    {
                      label: 'Component Gallery',
                      action: () => {
                        desktop.openApp('gallery');
                      },
                    },
                  ],
                },
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
        on retained volumes. Host Codex can use the AXV Skill to edit a selected
        VM project and run its tasks. Codex activity identifies working
        sessions, pending requests and turn outcomes. Project Files can clone
        repositories, open development previews and restore independent volume
        copies. Private remote access uses Tailscale; optional Cloudflare Access
        supports an externally managed tunnel. Application Manager adds
        restricted extensions with their own saved data.
      </p>
    </div>
  );
}
