import { desktopDatabase } from '../../shared/api/desktop';
import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Field, IconButton, Tabs } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { useDesktopResource as useResource } from '../../shared/api/useDesktopResource';
import { projectsAPI } from './api';
import { projectLabel } from './presentation';
import { HostFolderChooser } from './HostFolderChooser';
import { VMProjectLauncherView } from './ProjectFiles';

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
  const projects = result.data?.projects;
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
      const { project } = await projectsAPI.register(root);
      desktopDatabase.assertUnchanged();
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
            <h2>Projects</h2>
            <IconButton
              icon="refresh"
              label="Refresh host projects"
              disabled={!hostInteractive || busy}
              onClick={() => setRefresh((value) => value + 1)}
            />
          </div>
          {result.error && result.error !== error && (
            <p role="alert">{result.error}</p>
          )}
          {!projects && !result.error && <p role="status">Loading projects…</p>}
          {projects &&
            (projects.length > 0 ? (
              <ul
                className="project-list"
                role="list"
                aria-label="Registered host projects"
              >
                {projects.map((project) => {
                  const label = projectLabel(project, projects);
                  return (
                    <li key={project.id}>
                      <Button
                        disabled={!hostInteractive}
                        onClick={() =>
                          desktop.openApp(
                            'project-files',
                            { projectId: project.id },
                            { title: label },
                          )
                        }
                      >
                        <strong>{label}</strong>
                        <small>{project.rootPath}</small>
                      </Button>
                    </li>
                  );
                })}
              </ul>
            ) : (
              <p className="project-list-empty">No host projects yet.</p>
            ))}
          <form
            className="project-register-form"
            aria-label="Open host folder"
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
                  setPickerPath(root);
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
          {error && <p role="alert">{error}</p>}
        </section>
        <div
          className="project-launcher-pane"
          hidden={location !== 'vm'}
          inert={!interactive || location !== 'vm'}
        >
          <VMProjectLauncherView
            {...props}
            visible={props.visible !== false && location === 'vm'}
            interactive={interactive && location === 'vm'}
          />
        </div>
      </Tabs>
    </div>
  );
}
