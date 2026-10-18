export interface TestTempConfiguration {
  root: string;
  directory: string;
  cleanup(): void;
}

export function configureTestTemp(
  env?: NodeJS.ProcessEnv,
  cwd?: string,
): TestTempConfiguration;
export function initializeTestTemp(options?: {
  isolatedCache?: boolean;
}): TestTempConfiguration;
export let testTempRoot: string | undefined;
