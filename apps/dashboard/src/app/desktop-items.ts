import type { DesktopAPI, DesktopIcon } from '@cofy-x/axv-nextstep';
import { openTerminalWindow } from '../features/terminal/definition';

export function createDesktopIcons(
  getDesktop: () => DesktopAPI | null,
): DesktopIcon[] {
  return [
    {
      id: 'system:terminal',
      label: 'Terminal',
      icon: 'terminal',
      appId: 'terminal',
      applicationLauncher: true,
      onOpen: () => {
        const desktop = getDesktop();
        if (desktop) openTerminalWindow(desktop);
      },
    },
    {
      id: 'system:project-files',
      label: 'Project Files',
      icon: 'project',
      appId: 'project-files',
      applicationLauncher: true,
    },
    {
      id: 'system:remote-access',
      label: 'Tailscale',
      icon: 'mesh-network',
      appId: 'remote-access',
      applicationLauncher: true,
    },
    {
      id: 'system:appearance',
      label: 'Preferences',
      icon: 'appearance',
      appId: 'appearance',
      applicationLauncher: true,
    },
    {
      id: 'system:codex',
      label: 'Codex',
      icon: 'companion',
      appId: 'codex',
      applicationLauncher: true,
    },
    {
      id: 'system:machines',
      label: 'Virtual Machines',
      icon: 'virtual-machines',
      appId: 'machines',
      applicationLauncher: true,
    },
    {
      id: 'system:gallery',
      label: 'Component Gallery',
      icon: 'gallery',
      appId: 'gallery',
      applicationLauncher: true,
    },
  ];
}
