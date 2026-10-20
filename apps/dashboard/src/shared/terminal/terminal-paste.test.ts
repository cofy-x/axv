import { describe, expect, it, vi } from 'vitest';
import { TerminalPasteRequests } from './terminal-paste';

function harness() {
  const requests = new TerminalPasteRequests();
  let current = true;
  let accepted = true;
  const terminal = {
    options: { disableStdin: true },
    paste: vi.fn((_text: string) => {
      expect(terminal.options.disableStdin).toBe(false);
      requests.recordInput(accepted);
    }),
  };
  const target = { terminal, isCurrent: () => current };
  return {
    requests,
    terminal,
    target,
    request: () => requests.begin(target)!,
    replace: () => {
      current = false;
    },
    reject: () => {
      accepted = false;
    },
    accept: () => {
      accepted = true;
    },
  };
}

describe('terminal paste requests', () => {
  it('passes text unchanged through xterm and never adds Enter or sends twice', () => {
    const { requests, request, terminal } = harness();
    const captured = request();
    const text = '你好 ✓🙂\r\nsecond line\n';
    expect(requests.send(captured, text)).toBe('sent');
    expect(terminal.paste).toHaveBeenCalledExactlyOnceWith(text);
    expect(terminal.options.disableStdin).toBe(true);
    expect(captured.sent).toBe(true);
    expect(requests.send(captured, text)).toBe('sent');
    expect(terminal.paste).toHaveBeenCalledOnce();
  });

  it('invalidates older clipboard work when a new paste begins or its owner disappears', () => {
    const { requests, request, terminal, replace } = harness();
    const old = request();
    const newest = request();
    expect(requests.send(old, 'old text')).toBe('unavailable');
    replace();
    expect(requests.send(newest, 'replacement text')).toBe('unavailable');
    expect(terminal.paste).not.toHaveBeenCalled();
  });

  it('rejects a request across disconnect and reconnect with the same controller', () => {
    const { requests, request, terminal } = harness();
    const captured = request();
    requests.invalidate();
    expect(requests.send(captured, 'delayed clipboard text')).toBe(
      'unavailable',
    );
    expect(terminal.paste).not.toHaveBeenCalled();
    expect(requests.send(request(), 'explicit new paste')).toBe('sent');
  });

  it('rejects an empty paste without sending input', () => {
    const { requests, request, terminal } = harness();
    expect(requests.send(request(), '')).toBe('empty');
    expect(terminal.paste).not.toHaveBeenCalled();
  });

  it('keeps a rejected submission retryable only through another explicit send', () => {
    const { requests, request, terminal, reject, accept } = harness();
    const captured = request();
    reject();
    expect(requests.send(captured, 'draft')).toBe('rejected');
    expect(captured.sent).toBe(false);
    expect(terminal.paste).toHaveBeenCalledOnce();
    expect(terminal.options.disableStdin).toBe(true);
    accept();
    expect(requests.send(captured, 'edited draft')).toBe('sent');
    expect(terminal.paste).toHaveBeenLastCalledWith('edited draft');
  });

  it('requires an accepted onData event instead of assuming paste() means sent', () => {
    const { requests, request, terminal } = harness();
    terminal.paste.mockImplementation(() => {});
    expect(requests.send(request(), 'text')).toBe('rejected');
    expect(terminal.options.disableStdin).toBe(true);
  });

  it('does not hide a rejected part of a paste behind an accepted input event', () => {
    const { requests, request, terminal } = harness();
    terminal.paste.mockImplementation(() => {
      requests.recordInput(false);
      requests.recordInput(true);
    });
    expect(requests.send(request(), 'text')).toBe('rejected');
  });

  it('reports paste exceptions separately and restores suspended stdin', () => {
    const { requests, request, terminal } = harness();
    terminal.paste.mockImplementation(() => {
      throw new Error('paste failed');
    });
    expect(requests.send(request(), 'text')).toBe('failed');
    expect(terminal.options.disableStdin).toBe(true);
  });

  it('does not report success or re-enable stdin after a synchronous disconnect', () => {
    const { requests, request, terminal } = harness();
    terminal.options.disableStdin = false;
    terminal.paste.mockImplementation(() => {
      // A queue flush may fail and publish disconnected before sendText returns.
      requests.invalidate();
      terminal.options.disableStdin = true;
      requests.recordInput(true);
    });
    const captured = request();
    expect(requests.send(captured, 'text')).toBe('failed');
    expect(captured.sent).toBe(false);
    expect(terminal.options.disableStdin).toBe(true);
  });

  it('notifies an open editor when its request becomes stale or input is accepted', () => {
    const { requests, request } = harness();
    const captured = request();
    const listener = vi.fn();
    const unsubscribe = requests.subscribe(listener);
    const initial = requests.getSnapshot();
    expect(requests.send(captured, 'text')).toBe('sent');
    expect(listener).toHaveBeenCalledOnce();
    expect(requests.getSnapshot()).toBeGreaterThan(initial);
    requests.invalidate();
    expect(listener).toHaveBeenCalledTimes(2);
    unsubscribe();
    requests.invalidate();
    expect(listener).toHaveBeenCalledTimes(2);
  });
});
