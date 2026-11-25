import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { deferWindow } from '../../shared/desktop/defer-window';
import { vmProjectVolumeScope } from './vm-project-volume';

const ProjectLauncher = deferWindow(() =>
  import('./ProjectLauncher').then((module) => module.ProjectLauncher),
);
const HostProjectFiles = deferWindow(() =>
  import('./HostProjectFiles').then((module) => module.HostProjectFiles),
);
const VMProjectFiles = deferWindow(() =>
  import('./VMProjectFiles').then((module) => module.VMProjectFiles),
);
const VMProjectTasks = deferWindow(() =>
  import('./VMProjectTasks').then((module) => module.VMProjectTasks),
);
export const VMProjectLauncherView = deferWindow(() =>
  import('./VMProjectLauncher').then((module) => module.VMProjectLauncher),
);

export function ProjectFiles(props: AppWindowProps) {
  const { params } = props.window;
  if (params.vmProjectVolumeId && !vmProjectVolumeScope(params))
    return (
      <div className="app-pad" role="alert">
        The original VM volume binding is missing. Open Project Files from the
        VM Inspector.
      </div>
    );
  return params.vmTasksProjectId ? (
    <VMProjectTasks {...props} />
  ) : params.vmProjectId ? (
    <VMProjectFiles {...props} />
  ) : params.projectId ? (
    <HostProjectFiles {...props} />
  ) : params.vmProjectVolumeId ? (
    <div className="project-launcher app-pad">
      <VMProjectLauncherView {...props} />
    </div>
  ) : (
    <ProjectLauncher {...props} />
  );
}
