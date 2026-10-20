export type WindowCommand =
  | 'save'
  | 'terminal:new-session'
  | 'terminal:copy'
  | 'terminal:paste'
  | 'terminal:paste-text'
  | 'terminal:clear'
  | 'terminal:interrupt'
  | 'terminal:details'
  | 'guest-terminal:reconnect'
  | 'guest-terminal:end'
  | 'codex:copy'
  | 'codex:paste'
  | 'codex:paste-text'
  | 'codex:clear'
  | 'codex:interrupt'
  | 'codex:reconnect'
  | 'codex:continue'
  | 'codex:open-running'
  | 'codex:details'
  | 'codex:rename'
  | 'codex:archive'
  | 'codex:unarchive'
  | 'codex:terminal'
  | 'codex:end';

export type WindowCommandHandler = () => Promise<boolean>;

type RegisteredCommand = { handler: WindowCommandHandler; enabled: boolean };

/** Each desktop instance owns a command registry, scoped to live window IDs. */
export function createWindowCommands() {
  const windows = new Map<string, Map<WindowCommand, RegisteredCommand>>();
  const listeners = new Set<() => void>();
  let version = 0;

  function notify() {
    version += 1;
    for (const listener of listeners) listener();
  }

  return {
    subscribe(listener: () => void) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    getSnapshot: () => version,
    canExecute(windowId: string, command: WindowCommand) {
      return windows.get(windowId)?.get(command)?.enabled ?? false;
    },
    register(
      windowId: string,
      command: WindowCommand,
      handler: WindowCommandHandler,
      enabled = true,
    ) {
      const handlers =
        windows.get(windowId) ?? new Map<WindowCommand, RegisteredCommand>();
      windows.set(windowId, handlers);
      const entry = { handler, enabled };
      handlers.set(command, entry);
      notify();
      return () => {
        if (handlers.get(command) !== entry) return;
        handlers.delete(command);
        if (!handlers.size) windows.delete(windowId);
        notify();
      };
    },
    async execute(windowId: string, command: WindowCommand): Promise<boolean> {
      const entry = windows.get(windowId)?.get(command);
      return entry?.enabled ? entry.handler() : false;
    },
  };
}

export type WindowCommands = ReturnType<typeof createWindowCommands>;
