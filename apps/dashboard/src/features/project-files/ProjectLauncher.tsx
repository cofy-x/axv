import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Button,
  Field,
  IconButton,
  PixelIcon,
  Tabs,
} from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { useResource } from '../../shared/api/useResource';
import { projectsAPI } from './api';
import { projectLabel } from './presentation';
import { HostFolderChooser } from './HostFolderChooser';
import { VMProjectLauncher } from './VMProjectLauncher';

export function ProjectLauncher(props: AppWindowProps) {
  const { desktop, interactive } = props;
  const [location, setLocation] = useState<'host' | 'vm'>('host');
  const [root, setRoot] = useState('');
  const [choosingFolder, setChoosingFolder] = useState(false);
  const [pickerPath, setPickerPath] = useState('');
  const [refresh, setRefresh] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const load = useCallback(() => projectsAPI.list(), []);
  const result = useResource(`projects:${refresh}`, load);
  const launcher = useRef<HTMLDivElement>(null);
  const hostPanel = useRef<HTMLElement>(null);
  const mounted = useRef(false);
  const hostInteractive = interactive && location === 'host';

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  function closeFolderChooser() {
    setChoosingFolder(false);
    requestAnimationFrame(() => {
      if (hostPanel.current?.hidden || hostPanel.current?.inert) return;
      launcher.current
        ?.querySelector<HTMLButtonElement>('[data-folder-picker-toggle]')
        ?.focus();
    });
  }

  async function register() {
    if (busy || !hostInteractive) return;
    setBusy(true);
    setError('');
    try {
      const { project } = await projectsAPI.register(root.trim());
      if (!mounted.current) return;
      const projects = [
        ...(result.data?.projects || []).filter(
          (item) => item.id !== project.id,
        ),
        project,
      ];
      setRefresh((value) => value + 1);
      desktop.openApp(
        'project-files',
        { projectId: project.id },
        { title: projectLabel(project, projects) },
      );
    } catch (error) {
      if (mounted.current)
        setError(error instanceof Error ? error.message : String(error));
    } finally {
      if (mounted.current) setBusy(false);
    }
  }

  return (
    <div className="project-launcher app-pad" ref={launcher}>
      <div className="panel-heading project-launcher-identity">
        <PixelIcon name="project" size={32} />
        <div>
          <strong>Project Files</strong>
          <p>Browse project folders on the host or a VM.</p>
        </div>
      </div>
      <div className="project-launcher-tabs">
        <Tabs
          label="Project location"
          value={location}
          onChange={(value) => setLocation(value === 'vm' ? 'vm' : 'host')}
          tabs={[
            { id: 'host', label: 'Host', disabled: !interactive },
            { id: 'vm', label: 'VM', disabled: !interactive },
          ]}
        >
          <section
            className="project-launcher-pane"
            aria-label="Host projects"
            ref={hostPanel}
            hidden={location !== 'host'}
            inert={!hostInteractive}
          >
            <div className="project-launcher-heading">
              <h2>Open Folder</h2>
              <IconButton
                icon="refresh"
                label="Refresh"
                disabled={!hostInteractive || busy}
                onClick={() => setRefresh((value) => value + 1)}
              />
            </div>
            <form
              className="project-register-form"
              onSubmit={(event) => {
                event.preventDefault();
                void register();
              }}
            >
              <Field
                label="Host project directory"
                value={root}
                placeholder="/absolute/path/to/project"
                disabled={choosingFolder || busy || !hostInteractive}
                onChange={(event) => setRoot(event.target.value)}
              />
              <div className="project-register-actions">
                <Button
                  data-folder-picker-toggle
                  aria-expanded={choosingFolder}
                  disabled={busy || choosingFolder || !hostInteractive}
                  onClick={() => {
                    setPickerPath(root.trim());
                    setChoosingFolder(true);
                  }}
                >
                  Choose Folder
                </Button>
                <Button
                  type="submit"
                  primary
                  disabled={
                    !root.trim() || busy || choosingFolder || !hostInteractive
                  }
                >
                  {busy ? 'Opening…' : 'Open'}
                </Button>
              </div>
            </form>
            {choosingFolder && (
              <HostFolderChooser
                initialPath={pickerPath}
                onCancel={closeFolderChooser}
                onChoose={(path) => {
                  setRoot(path);
                  closeFolderChooser();
                }}
              />
            )}
            {(error || result.error) && (
              <p role="alert">{error || result.error}</p>
            )}
            {!result.data && !result.error && (
              <p role="status">Loading projects…</p>
            )}
            {result.data?.projects.length === 0 && (
              <div className="app-empty-state project-launcher-empty">
                <PixelIcon name="folder" size={32} />
                <div>
                  <strong>No host projects yet.</strong>
                  <p>Open a folder above to keep it in your project list.</p>
                </div>
              </div>
            )}
            {!!result.data?.projects.length && (
              <h2 className="project-list-heading">Projects</h2>
            )}
            <div className="project-list" aria-label="Registered host projects">
              {result.data?.projects.map((project) => (
                <Button
                  key={project.id}
                  disabled={!hostInteractive}
                  onClick={() =>
                    desktop.openApp(
                      'project-files',
                      { projectId: project.id },
                      {
                        title: projectLabel(
                          project,
                          result.data?.projects || [],
                        ),
                      },
                    )
                  }
                >
                  <strong>
                    {projectLabel(project, result.data?.projects || [])}
                  </strong>
                  <small>{project.rootPath}</small>
                </Button>
              ))}
            </div>
          </section>
          <div
            className="project-launcher-pane"
            hidden={location !== 'vm'}
            inert={!interactive || location !== 'vm'}
          >
            <VMProjectLauncher
              {...props}
              interactive={interactive && location === 'vm'}
            />
          </div>
        </Tabs>
      </div>
    </div>
  );
}
