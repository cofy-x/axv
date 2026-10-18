import { describe, expect, it } from 'vitest';
import type { Project } from './api';
import { projectLabel } from './presentation';

describe('registered project labels', () => {
  const original: Project = {
    id: '12345678original',
    kind: 'host',
    name: 'Work',
    rootPath: '/work',
    createdAt: '2026-10-18T00:00:00Z',
  };
  it('keeps ordinary project labels concise', () => {
    expect(
      projectLabel(original, [
        original,
        { ...original, id: 'other', rootPath: '/other' },
      ]),
    ).toBe('Work');
  });
  it('distinguishes registrations of the same path even when their short IDs collide', () => {
    const replacement = {
      ...original,
      id: '12345678replacement',
      createdAt: '2026-10-18T00:01:00Z',
    };
    const projects = [original, replacement];
    const oldLabel = projectLabel(original, projects),
      newLabel = projectLabel(replacement, projects);
    expect(oldLabel).toContain('registered');
    expect(newLabel).toContain('registered');
    expect(oldLabel).not.toBe(newLabel);
    expect(oldLabel).toContain('12345678orig');
    expect(newLabel).toContain('12345678repl');
  });
});
