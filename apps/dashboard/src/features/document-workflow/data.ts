import { filesystemApi } from '../../filesystem/api';
import { documentsAPI } from '../../filesystem/documents';
import type { Entry } from '../../filesystem/types';
import { APIError } from '../../shared/api/http';
import { projectsAPI } from '../project-files/api';
import { codexAPI } from '../codex/api';

async function part<T>(
  label: string,
  load: () => Promise<T>,
  fallback: T,
  missingSource = false,
) {
  try {
    return { value: await load(), error: '' };
  } catch (error) {
    return {
      value: fallback,
      error:
        missingSource && error instanceof APIError && error.status === 404
          ? ''
          : `${label}: ${error instanceof Error ? error.message : String(error)}`,
    };
  }
}

// A failed optional provider must not prevent access to saved document content.
export async function loadSavedVersions(entryId: string) {
  const [document, snapshots] = await Promise.all([
    part<{ entry: Entry | null; content?: string }>(
      'Saved document',
      () => filesystemApi.entry(entryId),
      { entry: null },
      true,
    ),
    part('Versions', () => documentsAPI.snapshots(entryId), { snapshots: [] }),
  ]);
  return {
    ...document.value,
    ...snapshots.value,
    warnings: [document.error, snapshots.error].filter(Boolean),
  };
}

export async function loadDocumentActions(entryId: string) {
  const [links, projects, conversations, origins] = await Promise.all([
    part('Document links', () => documentsAPI.links(entryId), { links: [] }),
    part('Host projects', () => projectsAPI.list(), { projects: [] }),
    part('Codex conversations', () => codexAPI.list(), { sessions: [] }),
    part('Import origins', () => documentsAPI.origins(entryId), {
      origins: [],
    }),
  ]);
  return {
    ...links.value,
    ...projects.value,
    ...conversations.value,
    ...origins.value,
    warnings: [links, projects, conversations, origins]
      .map((resource) => resource.error)
      .filter(Boolean),
  };
}
