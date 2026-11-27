import { describe, expect, test } from 'vitest';
import { classifyChanges } from './ci-changes.mjs';

describe('classifyChanges', () => {
  test('skips heavy runtime and browser checks for docs-only PRs', () => {
    const result = classifyChanges([
      'README.md',
      'docs/startup.md',
      'docs/development.md',
    ]);
    expect(result.runtime).toBe(false);
    expect(result.macosNative).toBe(false);
    expect(result.security).toBe(false);
    expect(result.e2e).toBe(false);
    expect(result.guestScripts).toBe(false);
    expect(result.browserMatrix.include).toHaveLength(0);
  });

  test('skips runtime and browser checks for guest image and release workflow PRs while checking guest shell scripts', () => {
    const result = classifyChanges([
      '.github/workflows/guest-images.yml',
      '.github/workflows/release.yml',
      'images/guest/build-lima.sh',
      'images/guest/README.md',
    ]);
    expect(result.runtime).toBe(false);
    expect(result.macosNative).toBe(false);
    expect(result.security).toBe(false);
    expect(result.e2e).toBe(false);
    expect(result.guestScripts).toBe(true);
    expect(result.browserMatrix.include).toHaveLength(0);
  });

  test('runs Linux verification and macOS native without browser E2E for CLI-only Go PRs', () => {
    const result = classifyChanges([
      'apps/server/internal/cli/cli.go',
      'apps/server/internal/daemon/host.go',
      'apps/server/internal/compute/hostinstall/install.go',
    ]);
    expect(result.runtime).toBe(true);
    expect(result.macosNative).toBe(true);
    expect(result.security).toBe(true);
    expect(result.e2e).toBe(false);
    expect(result.browserMatrix.include).toHaveLength(0);
  });

  test('runs Linux verification without macOS native or browser E2E for installer script PRs', () => {
    const result = classifyChanges([
      'scripts/install.sh',
      'scripts/install-sh.test.mjs',
    ]);
    expect(result.runtime).toBe(true);
    expect(result.macosNative).toBe(false);
    expect(result.security).toBe(true);
    expect(result.e2e).toBe(false);
  });

  test('runs Chromium 3-shard E2E without macOS native for dashboard-only PRs', () => {
    const result = classifyChanges([
      'apps/dashboard/src/features/machines/MachineActions.tsx',
      'packages/nextstep/src/Button.tsx',
    ]);
    expect(result.runtime).toBe(true);
    expect(result.macosNative).toBe(false);
    expect(result.security).toBe(true);
    expect(result.e2e).toBe(true);
    expect(result.fullE2E).toBe(false);
    expect(result.browserMatrix.include).toEqual([
      { project: 'chromium', browser: 'chromium', shard: 1 },
      { project: 'chromium', browser: 'chromium', shard: 2 },
      { project: 'chromium', browser: 'chromium', shard: 3 },
    ]);
  });

  test('runs macOS native and Chromium E2E for server API or embedded asset PRs', () => {
    const result = classifyChanges([
      'apps/server/internal/server/routes.go',
      'apps/server/internal/storage/migrations/0001_initial.sql',
    ]);
    expect(result.runtime).toBe(true);
    expect(result.macosNative).toBe(true);
    expect(result.security).toBe(true);
    expect(result.e2e).toBe(true);
    expect(result.browserMatrix.include).toHaveLength(3);
  });

  test('runs full 18-shard browser matrix on push to main and scheduled runs', () => {
    for (const eventName of ['push', 'schedule', 'workflow_dispatch']) {
      const result = classifyChanges([], { eventName });
      expect(result.runtime).toBe(true);
      expect(result.macosNative).toBe(true);
      expect(result.security).toBe(true);
      expect(result.e2e).toBe(true);
      expect(result.fullE2E).toBe(true);
      expect(result.browserMatrix.include).toHaveLength(18);
    }
  });

  test('fails safe to full PR checks when changed file list is empty on pull_request', () => {
    const result = classifyChanges([], { eventName: 'pull_request' });
    expect(result.runtime).toBe(true);
    expect(result.macosNative).toBe(true);
    expect(result.security).toBe(true);
    expect(result.e2e).toBe(true);
    expect(result.fullE2E).toBe(false);
    expect(result.browserMatrix.include).toHaveLength(3);
  });
});
