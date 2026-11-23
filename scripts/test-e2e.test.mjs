import { EventEmitter } from 'node:events';
import { expect, test, vi } from 'vitest';
import { createCodexFixture } from './codex-fixture.mjs';
import { exitAfterCleanup, launchServer, spawnChild, stop } from './lib.mjs';
import { reportRuntimeDiagnostics } from './test-e2e-diagnostics.mjs';

vi.mock('./test-temp.mjs', () => ({ initializeTestTemp: vi.fn() }));
vi.mock('./codex-fixture.mjs', () => ({ createCodexFixture: vi.fn() }));
vi.mock('./shell-fixture.mjs', () => ({ createShellEnvironment: () => ({}) }));
vi.mock('./test-e2e-diagnostics.mjs', () => ({
  reportRuntimeDiagnostics: vi.fn(),
}));
vi.mock('./lib.mjs', () => ({
  command: vi.fn(),
  exitAfterCleanup: vi.fn(),
  launchServer: vi.fn(),
  root: '/isolated-checkout',
  scratch: () => '/isolated-browser-run',
  spawnChild: vi.fn(),
  stop: vi.fn(),
}));

test('browser teardown preserves diagnostics and other cleanup after a child stop fails', async () => {
  vi.stubEnv('E2E_SKIP_BUILD', '1');
  const signals = ['SIGINT', 'SIGTERM'];
  const existingListeners = signals.map((signal) => process.listeners(signal));
  const log = vi.spyOn(console, 'error').mockImplementation(() => {});
  const browser = new EventEmitter();
  const server = new EventEmitter();
  const stopError = new Error('Controlled browser group stop failure');
  const diagnosticError = new Error('Controlled fixture diagnostic failure');
  const diagnostics = vi.fn().mockRejectedValue(diagnosticError);
  const verifyIsolation = vi.fn();
  const cleanup = vi.fn();
  vi.mocked(createCodexFixture).mockResolvedValue({
    env: {},
    diagnostics,
    verifyIsolation,
    cleanup,
  });
  vi.mocked(launchServer).mockResolvedValue({ child: server });
  vi.mocked(spawnChild).mockImplementation(() => {
    queueMicrotask(() => browser.emit('exit', 0));
    return browser;
  });
  vi.mocked(stop).mockRejectedValueOnce(stopError);

  try {
    await import('./test-e2e.mjs');

    expect(stop.mock.calls).toEqual([[browser], [server]]);
    expect(diagnostics).toHaveBeenCalledOnce();
    expect(verifyIsolation).toHaveBeenCalledOnce();
    expect(cleanup).toHaveBeenCalledOnce();
    expect(reportRuntimeDiagnostics).toHaveBeenCalledExactlyOnceWith(
      '/isolated-browser-run/axv-home',
    );
    expect(log).toHaveBeenCalledWith(
      expect.objectContaining({ errors: [stopError, diagnosticError] }),
    );
    expect(exitAfterCleanup).toHaveBeenCalledExactlyOnceWith(1);
  } finally {
    log.mockRestore();
    vi.unstubAllEnvs();
    for (const [index, signal] of signals.entries())
      for (const listener of process.listeners(signal))
        if (!existingListeners[index].includes(listener))
          process.removeListener(signal, listener);
  }
});
