import { createHash } from 'node:crypto';
import { expect } from '@playwright/test';
import type { Locator, Page } from '@playwright/test';
import type {
  VMProject,
  VMProjectBinding,
} from '../apps/dashboard/src/features/project-files/vm-api';

export async function openVMEditorFixture(
  page: Page,
  { touch = false, content = 'Original VM file\n' } = {},
) {
  const project: VMProject = {
    id: 'editor-project',
    hostId: 'editor-host',
    kind: 'vm',
    name: 'Editor VM project',
    volumeId: 'editor-volume',
    root: 'repo',
    rootIdentity: 'editor-root',
    revision: 1,
    createdAt: '2026-10-28T00:00:00Z',
  };
  const binding: VMProjectBinding = {
    hostId: project.hostId,
    projectId: project.id,
    projectRevision: project.revision,
    volumeId: project.volumeId,
    volumeRevision: 1,
    machineId: 'editor-machine',
    bootId: 'editor-boot',
    instanceId: 'editor-guest',
  };
  const filename = 'main.ts';
  const writes: unknown[] = [];
  let saved = content;
  const hash = () => createHash('sha256').update(saved).digest('hex');
  const metadata = () => ({
    path: filename,
    size: Buffer.byteLength(saved),
    modifiedAt: project.createdAt,
  });
  const prefix = '/api/compute';
  const projectPath = `${prefix}/projects/${project.id}`;
  await page.route('**/api/compute/**', (route) => {
    const { pathname } = new URL(route.request().url());
    switch (pathname) {
      case `${prefix}/info`:
        return route.fulfill({
          json: {
            configured: true,
            hostId: project.hostId,
            providers: [],
            images: [],
          },
        });
      case `${prefix}/machines`:
        return route.fulfill({ json: { machines: [] } });
      case `${prefix}/volumes`:
        return route.fulfill({ json: { volumes: [] } });
      case `${prefix}/projects`:
        return route.fulfill({ json: { projects: [project] } });
      case `${projectPath}/binding`:
        return route.fulfill({ json: { project, binding } });
      case `${projectPath}/files/list`:
        return route.fulfill({
          json: {
            project,
            binding,
            entries: [{ ...metadata(), name: filename, kind: 'file' }],
          },
        });
      case `${projectPath}/files/write`: {
        const value = route.request().postDataJSON();
        expect(value).toMatchObject({
          binding,
          path: filename,
          expectedSha256: hash(),
        });
        writes.push(value);
        saved = value.content;
        return route.fulfill({
          json: {
            ...metadata(),
            project,
            binding,
            content: saved,
            sha256: hash(),
          },
        });
      }
      case `${projectPath}/files/read`:
        return route.fulfill({
          json: {
            ...metadata(),
            project,
            binding,
            content: saved,
            sha256: hash(),
          },
        });
      default:
        return route.continue();
    }
  });
  const choose = (control: Locator) =>
    touch ? control.tap() : control.click();
  await choose(
    page.locator('.ns-dock [data-dock-app="project-files"] .ns-dock-tile'),
  );
  const launcher = page.locator('[data-window-id="project-files:projects"]');
  await choose(launcher.getByRole('tab', { name: 'VM', exact: true }));
  await choose(launcher.getByRole('button', { name: /^Editor VM project ·/ }));
  const window = page.locator(
    `[data-window-id="project-files:vm:${project.hostId}:${project.id}"]`,
  );
  await choose(window.getByRole('button', { name: filename, exact: true }));
  await choose(window.getByRole('button', { name: 'Edit', exact: true }));
  const editor = window.getByRole('textbox', {
    name: 'VM project file text',
    exact: true,
    includeHidden: true,
  });
  await expect(editor).toBeEditable();
  return { window, editor, saved: () => saved, writes, binding, filename };
}
