import type { VMProject, VMProjectBinding } from './vm-api';

export interface VMProjectLocationDetailsProps {
  section: 'files' | 'previews' | 'backups' | 'github';
  folder: string;
  id: string;
  hostId: string;
  project?: VMProject;
  binding: VMProjectBinding | null;
}

export function VMProjectLocationDetails({
  section,
  folder,
  id,
  hostId,
  project,
  binding,
}: VMProjectLocationDetailsProps) {
  return (
    <details className="project-location">
      {section === 'files' ? (
        <summary title={folder || 'Project root'}>
          <strong>VM · {folder || 'Project root'}</strong> · Details
        </summary>
      ) : (
        <summary>Project details</summary>
      )}
      <dl className="info-list">
        {project && (
          <>
            <dt>Project name</dt>
            <dd>{project.name}</dd>
          </>
        )}
        <dt>Project</dt>
        <dd>
          <code>{id}</code>
        </dd>
        <dt>Host</dt>
        <dd>
          <code>{hostId}</code>
        </dd>
        <dt>Volume</dt>
        <dd>
          <code>{binding?.volumeId || project?.volumeId || 'Unavailable'}</code>
        </dd>
        <dt>Directory in volume</dt>
        <dd>
          <code>{project?.root || 'Volume root'}</code>
        </dd>
        {section === 'files' && folder && (
          <>
            <dt>Current folder</dt>
            <dd>
              <code>{folder}</code>
            </dd>
          </>
        )}
        <dt>VM</dt>
        <dd>
          <code>{binding?.machineId || 'Unavailable'}</code>
        </dd>
        <dt>Boot / instance</dt>
        <dd>
          <code>
            {binding
              ? `${binding.bootId} / ${binding.instanceId}`
              : 'Unavailable'}
          </code>
        </dd>
      </dl>
    </details>
  );
}
