interface PasteTerminal {
  options: { disableStdin?: boolean };
  paste: (text: string) => void;
}

interface PasteTarget {
  terminal: PasteTerminal;
  isCurrent: () => boolean;
}

export interface TerminalPasteRequest {
  readonly sequence: number;
  readonly epoch: number;
  readonly target: PasteTarget;
  sent: boolean;
}

export type TerminalPasteResult =
  'sent' | 'empty' | 'unavailable' | 'rejected' | 'failed';

/** Pending clipboard input belongs to one uninterrupted terminal attachment. */
export class TerminalPasteRequests {
  private sequence = 0;
  private epoch = 0;
  private version = 0;
  private readonly listeners = new Set<() => void>();
  private input?: { received: boolean; accepted: boolean };

  readonly subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  readonly getSnapshot = () => this.version;

  private notify() {
    this.version++;
    for (const listener of this.listeners) listener();
  }

  begin(target: PasteTarget | null): TerminalPasteRequest | null {
    this.sequence++;
    this.notify();
    return target
      ? {
          sequence: this.sequence,
          epoch: this.epoch,
          target,
          sent: false,
        }
      : null;
  }

  readonly invalidate = () => {
    this.epoch++;
    this.notify();
  };

  isCurrent(request: TerminalPasteRequest) {
    return (
      request.sequence === this.sequence &&
      request.epoch === this.epoch &&
      request.target.isCurrent()
    );
  }

  /** xterm's paste() is synchronous but returns void; track its onData result. */
  readonly recordInput = (accepted: boolean) => {
    if (!this.input) return;
    this.input.received = true;
    this.input.accepted &&= accepted;
  };

  send(request: TerminalPasteRequest, text: string): TerminalPasteResult {
    if (!this.isCurrent(request)) return 'unavailable';
    if (request.sent) return 'sent';
    if (!text) return 'empty';
    const terminal = request.target.terminal;
    const disabled = terminal.options.disableStdin;
    const input = { received: false, accepted: true };
    this.input = input;
    // An explicit owner-bound command can send while ordinary keys are suspended.
    terminal.options.disableStdin = false;
    try {
      terminal.paste(text);
    } catch {
      return 'failed';
    } finally {
      terminal.options.disableStdin = this.isCurrent(request) ? disabled : true;
      this.input = undefined;
    }
    if (!this.isCurrent(request)) return 'failed';
    if (!input.received || !input.accepted) return 'rejected';
    request.sent = true;
    this.notify();
    return 'sent';
  }
}
