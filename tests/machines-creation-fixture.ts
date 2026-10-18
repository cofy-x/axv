import type { CreationDefaults } from '../apps/dashboard/src/features/machines/api';

export function creationDefaultsFixture(
  imageId: string,
  provider = 'lima',
): CreationDefaults {
  return {
    spec: {
      name: 'my-vm',
      provider,
      imageId,
      kind: 'development',
      resources: { cpus: 2, memoryMiB: 1024, diskGiB: 8 },
      projectGiB: 4,
      retainVolume: true,
    },
    start: true,
    taskTtlSeconds: 3600,
    taskRetainVolume: false,
  };
}
