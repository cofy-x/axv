import type { PreparationCheck } from '../apps/dashboard/src/features/machines/api';

export function preparationCheckFixture(
  overrides: Partial<PreparationCheck> = {},
): PreparationCheck {
  return {
    cacheHome: '/test/shared-cache',
    officialImageAvailable: false,
    platform: 'darwin',
    arch: 'arm64',
    configured: true,
    configurationSource: 'AXV profile',
    automaticSetupSupported: true,
    requiredLimaVersion: '2.2.1',
    instanceId: 'preparation-test-instance',
    defaults: {
      storageRoot: '/test/axv/compute',
      limaHome: '/test/axv/lima',
      cpus: 4,
      memoryMiB: 4096,
      diskGiB: 80,
      machines: 4,
    },
    checks: [
      {
        id: 'platform',
        label: 'Operating system',
        status: 'ready',
        detail: 'macOS ARM64 supports the pinned private Lima runtime.',
      },
    ],
    ...overrides,
  };
}
