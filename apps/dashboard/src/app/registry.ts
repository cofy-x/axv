import type { AppDefinition } from '@cofy-x/axv-nextstep';
import type { FilesystemData } from '../filesystem/context';
import type { WindowCommands } from '../shared/desktop/window-commands';
import { welcomeApp, aboutApp } from '../features/welcome/definition';
import { createFilesApp } from '../features/files/definition';
import { createNotesApp } from '../features/notes/definition';
import { projectFilesApp } from '../features/project-files/definition';
import {
  documentWorkflowApp,
  conversationContextApp,
  snapshotPreviewApp,
} from '../features/document-workflow/definition';
import { imageViewerApp } from '../features/image-viewer/definition';
import { trashApp } from '../features/trash/definition';
import { appearanceApp } from '../features/appearance/definition';
import { inspectorApp } from '../features/inspector/definition';
import { galleryApp } from '../features/gallery/definition';
import { createTerminalApp } from '../features/terminal/definition';
import { tailscaleApp } from '../features/tailscale';
import { cloudflareApp } from '../features/cloudflare';
import {
  notificationsApp,
  notificationWorkApp,
} from '../features/notifications/definition';
import { applicationsApp } from '../features/extensions/definition';
import {
  createCodexApp,
  codexEnvironmentApp,
} from '../features/codex/definition';

import {
  createMachinesApp,
  createMachineApp,
  machineInspectorApp,
  createGuestTerminalApp,
} from '../features/machines/definition';

export function createAppRegistry(
  data: FilesystemData,
  commands: WindowCommands,
): AppDefinition[] {
  return [
    welcomeApp,
    aboutApp,
    createFilesApp(data),
    createNotesApp(data, commands),
    projectFilesApp,
    documentWorkflowApp,
    conversationContextApp,
    snapshotPreviewApp,
    createTerminalApp(commands),
    createMachinesApp(commands),
    createMachineApp,
    machineInspectorApp,
    createGuestTerminalApp(commands),
    createCodexApp(commands),
    codexEnvironmentApp,
    tailscaleApp,
    cloudflareApp,
    notificationsApp,
    notificationWorkApp,
    applicationsApp,
    imageViewerApp,
    inspectorApp,
    trashApp,
    appearanceApp,
    galleryApp,
  ];
}
