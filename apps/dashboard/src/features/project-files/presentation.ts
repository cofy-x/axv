import type { Project } from './api';

/** Re-registering a replaced directory creates a distinct, deliberate target. */
export function projectLabel(project: Project, projects: readonly Project[]) {
  const samePath = projects.filter(
    (item) => item.rootPath === project.rootPath,
  );
  if (samePath.length < 2) return project.name;
  let length = 8;
  while (
    length < project.id.length &&
    samePath.some(
      (item) =>
        item.id !== project.id &&
        item.id.slice(0, length) === project.id.slice(0, length),
    )
  )
    length += 4;
  const timestamp = new Date(project.createdAt);
  const registered = Number.isNaN(timestamp.getTime())
    ? project.createdAt
    : timestamp.toLocaleString();
  return `${project.name} · registered ${registered} · ${project.id.slice(0, length)}`;
}
