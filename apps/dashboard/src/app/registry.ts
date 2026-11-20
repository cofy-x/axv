import type { AppDefinition } from '@cofy-x/axv-nextstep';
import type { WindowCommands } from '../shared/desktop/window-commands';
import { welcomeApp, aboutApp } from '../features/welcome/definition';
import { projectFilesApp } from '../features/project-files/definition';
import { appearanceApp } from '../features/appearance/definition';
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

export function createAppRegistry(commands: WindowCommands): AppDefinition[] {
  return [
    welcomeApp,
    aboutApp,
    projectFilesApp,
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
    appearanceApp,
    galleryApp,
  ];
}
