import type { AppDefinition } from '@cofy-x/axv-nextstep';
import { ProjectFiles } from './ProjectFiles';
import './project-files.css';

export const projectFilesApp: AppDefinition = {
  id: 'project-files',
  title: 'Project Files',
  icon: 'project',
  component: ProjectFiles,
  windowKey: (params) =>
    params.vmTasksProjectId
      ? `vm-tasks:${params.hostId}:${params.vmTasksProjectId}`
      : params.vmProjectId
        ? `vm:${params.hostId}:${params.vmProjectId}`
        : params.projectId || 'projects',
  defaultSize: { width: 800, height: 540 },
  minSize: { width: 310, height: 300 },
};
