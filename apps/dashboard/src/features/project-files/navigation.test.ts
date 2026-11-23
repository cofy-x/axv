import { describe, expect, it } from 'vitest';
import { projectFileLocation } from './navigation';

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
});
