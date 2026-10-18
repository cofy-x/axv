import { beforeEach, describe, expect, it, vi } from 'vitest';
import { APIError } from '../../shared/api/http';
import { loadDocumentActions, loadSavedVersions } from './data';

const reads = vi.hoisted(() => ({
  entry: vi.fn(),
  links: vi.fn(),
  snapshots: vi.fn(),
  projects: vi.fn(),
  conversations: vi.fn(),
  origins: vi.fn(),
}));
vi.mock('../../filesystem/api', () => ({
  filesystemApi: { entry: reads.entry },
}));
vi.mock('../../filesystem/documents', () => ({
  documentsAPI: {
    links: reads.links,
    snapshots: reads.snapshots,
    origins: reads.origins,
  },
}));
vi.mock('../project-files/api', () => ({
  projectsAPI: { list: reads.projects },
}));
vi.mock('../codex/api', () => ({ codexAPI: { list: reads.conversations } }));

beforeEach(() => {
  vi.resetAllMocks();
  reads.entry.mockResolvedValue({
    entry: { id: 'document', revision: 2, editable: true },
  });
  reads.links.mockResolvedValue({ links: [] });
  reads.snapshots.mockResolvedValue({
    snapshots: [{ id: 'snapshot', entryId: 'document', revision: 1 }],
  });
  reads.projects.mockResolvedValue({ projects: [{ id: 'project' }] });
  reads.conversations.mockResolvedValue({ sessions: [] });
  reads.origins.mockResolvedValue({ origins: [] });
});

describe('saved document resources', () => {
  it('preserves document and snapshot actions when an auxiliary agent service fails', async () => {
    reads.conversations.mockRejectedValue(new Error('Agent unavailable'));
    const [data, versions] = await Promise.all([
      loadDocumentActions('document'),
      loadSavedVersions('document'),
    ]);
    expect(versions.entry?.revision).toBe(2);
    expect(versions.snapshots[0].id).toBe('snapshot');
    expect(data.projects[0].id).toBe('project');
    expect(data.sessions).toEqual([]);
    expect(data.warnings).toEqual(['Codex conversations: Agent unavailable']);
  });

  it('retains immutable snapshots after source deletion or a source read failure', async () => {
    reads.entry.mockRejectedValue(new APIError('Gone', 404, 'not_found'));
    const deleted = await loadSavedVersions('document');
    expect(deleted.entry).toBeNull();
    expect(deleted.snapshots).toHaveLength(1);
    expect(deleted.warnings).toEqual([]);
    reads.entry.mockRejectedValue(new Error('Read failed'));
    const failed = await loadSavedVersions('document');
    expect(failed.entry).toBeNull();
    expect(failed.snapshots).toHaveLength(1);
    expect(failed.warnings).toEqual(['Saved document: Read failed']);
  });
});
