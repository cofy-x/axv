import { describe, expect, it, vi } from 'vitest';
import { openProjectFile, projectFileLocation } from './navigation';

describe('project file navigation', () => {
  it('locates a nested file while retaining its original relative path', () => {
    expect(projectFileLocation('reports/review notes.md')).toEqual({
      folder: 'reports',
      file: 'reports/review notes.md',
      cursor: '',
      reading: true,
    });
    expect(projectFileLocation()).toEqual({
      folder: '',
      file: '',
      cursor: '',
      reading: false,
    });
  });

  it('opens the exact project and locates the file in an already open browser', () => {
    const desktop = {
      openApp: vi.fn().mockReturnValue('project-files:original-project'),
      updateWindowState: vi.fn(),
    };
    expect(
      openProjectFile(desktop, 'original-project', 'docs/export.md', 'Work'),
    ).toBe('project-files:original-project');
    expect(desktop.openApp).toHaveBeenCalledWith(
      'project-files',
      { projectId: 'original-project', file: 'docs/export.md' },
      { title: 'Work' },
    );
    expect(desktop.updateWindowState).toHaveBeenCalledWith(
      'project-files:original-project',
      {
        folder: 'docs',
        file: 'docs/export.md',
        cursor: '',
        reading: true,
      },
    );
  });
});
