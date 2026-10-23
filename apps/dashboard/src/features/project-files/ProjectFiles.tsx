import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { deferWindow } from '../../shared/desktop/defer-window';

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

export function ProjectFiles(props: AppWindowProps) {
  return props.window.params.vmTasksProjectId ? (
    <VMProjectTasks {...props} />
  ) : props.window.params.vmProjectId ? (
    <VMProjectFiles {...props} />
  ) : props.window.params.projectId ? (
    <HostProjectFiles {...props} />
  ) : (
    <ProjectLauncher {...props} />
  );
}
