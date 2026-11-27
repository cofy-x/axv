import { Button, IconButton } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { VMProject, VMProjectBinding } from './vm-api';

export interface VMProjectToolbarProps {
  desktop: DesktopAPI;
  projectId: string;
  hostId: string;
  project?: VMProject;
  binding: VMProjectBinding | null;
  section: string;
  folder: string;
  windowInteractive?: boolean;
  interactive: boolean;
  unavailable: boolean;
  saving: boolean;
  composing: boolean;
  openingTerminal: boolean;
  openingCodex: boolean;
  onOpenTerminal: () => void;
  onOpenCodex: () => void;
  onOpenMoreMenu: (rect: DOMRect) => void;
  onNavigateUp: () => void;
  onRefresh: () => void;
}

export function VMProjectToolbar({
  desktop,
  projectId,
  hostId,
  project,
  binding,
  section,
  folder,
  windowInteractive,
  interactive,
  unavailable,
  saving,
  composing,
  openingTerminal,
  openingCodex,
  onOpenTerminal,
  onOpenCodex,
  onOpenMoreMenu,
  onNavigateUp,
  onRefresh,
}: VMProjectToolbarProps) {
  return (
    <div className="app-toolbar project-browser-toolbar">
      <div
        className="project-browser-actions"
        role="group"
        aria-label="VM project actions"
      >
        <Button
          primary
          disabled={!windowInteractive || !binding || openingTerminal}
          onClick={onOpenTerminal}
        >
          {openingTerminal ? 'Opening Terminal…' : 'Terminal'}
        </Button>
        <Button
          disabled={!windowInteractive || !binding || openingCodex}
          onClick={onOpenCodex}
        >
          {openingCodex ? 'Opening Codex…' : 'Codex'}
        </Button>
        <Button
          disabled={!windowInteractive}
          onClick={() =>
            desktop.openApp(
              'project-files',
              { hostId, vmTasksProjectId: projectId },
              { title: `Tasks · ${project?.name || 'VM project'}` },
            )
          }
        >
          Tasks
        </Button>
        {binding && (
          <Button
            disabled={!windowInteractive}
            onClick={() =>
              desktop.openApp(
                'machine-inspector',
                { hostId: binding.hostId, machineId: binding.machineId },
                {
                  title: `${project?.name || binding.machineId} — Inspector`,
                },
              )
            }
          >
            VM Inspector
          </Button>
        )}
        <IconButton
          icon="more"
          label="More VM project actions"
          aria-haspopup="menu"
          disabled={!windowInteractive}
          onClick={(event) => {
            onOpenMoreMenu(event.currentTarget.getBoundingClientRect());
          }}
        />
      </div>
      <div
        className="project-browser-navigation"
        role="group"
        aria-label="Project navigation"
        hidden={section !== 'files'}
        inert={!interactive}
      >
        <IconButton
          icon="up"
          label="Up"
          disabled={!folder || saving || composing || !interactive}
          onClick={onNavigateUp}
        />
        <IconButton
          icon="refresh"
          label="Refresh directory"
          disabled={unavailable || !interactive}
          onClick={onRefresh}
        />
      </div>
    </div>
  );
}
