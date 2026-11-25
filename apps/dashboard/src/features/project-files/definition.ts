import type { AppDefinition } from '@cofy-x/axv-nextstep';
import { ProjectFiles } from './ProjectFiles';
import { vmProjectVolumeScope } from './vm-project-volume';
import './project-files.css';

export const projectFilesApp: AppDefinition = {
  id: 'project-files',
  title: 'Project Files',
  icon: 'project',
  component: ProjectFiles,
  windowKey: (params) => {
    const scope = vmProjectVolumeScope(params);
    if (scope)
      return `vm-volume:${JSON.stringify([scope.hostId, scope.machineId, scope.volumeId])}`;
    return params.vmTasksProjectId
      ? `vm-tasks:${params.hostId}:${params.vmTasksProjectId}`
      : params.vmProjectId
        ? `vm:${params.hostId}:${params.vmProjectId}`
        : params.projectId || 'projects';
  },
  defaultSize: { width: 800, height: 540 },
  minSize: { width: 310, height: 300 },
};
