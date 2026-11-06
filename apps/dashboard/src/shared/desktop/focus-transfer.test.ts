import { describe, expect, it } from 'vitest';
import { canTransferWindowFocus } from './focus-transfer';

interface TestDocument {
  body: TestElement | null;
  activeElement: TestElement | null;
  defaultView: {
    getComputedStyle: (element: TestElement) => { visibility: string };
  };
}

/** Model the native gap between hiding a focused ancestor and its eventual blur. */
class TestElement {
  connected = true;
  frame = false;
  inert = false;
  hidden = false;
  disabled = false;
  display = true;
  visibility: 'visible' | 'hidden' | 'collapse' | undefined;

  constructor(
    readonly document: TestDocument,
    readonly parent: TestElement | null = null,
  ) {}

  get ownerDocument() {
    return this.document;
  }

  get isConnected(): boolean {
    return this.connected && (this.parent?.isConnected ?? true);
  }

  get computedVisibility(): string {
    return this.visibility ?? this.parent?.computedVisibility ?? 'visible';
  }

  get rendered(): boolean {
    return this.display && !this.hidden && (this.parent?.rendered ?? true);
  }

  getClientRects() {
    return this.rendered && this.isConnected ? [{}] : [];
  }

  matches(selector: string) {
    return selector === ':disabled' && this.disabled;
  }

  closest(selector: string): TestElement | null {
    const attributes = selector.split(',').map((part) => part.trim());
    if (
      (attributes.includes('[inert]') && this.inert) ||
      (attributes.includes('[hidden]') && this.hidden) ||
      (attributes.includes('[data-window-id]') && this.frame)
    )
      return this;
    return this.parent?.closest(selector) ?? null;
  }

  contains(other: TestElement | null): boolean {
    for (let element = other; element; element = element.parent)
      if (element === this) return true;
    return false;
  }

  focus() {
    this.document.activeElement = this;
  }

  asHTMLElement() {
    return this as unknown as HTMLElement;
  }
}

function fixture() {
  const document: TestDocument = {
    body: null,
    activeElement: null,
    defaultView: {
      getComputedStyle: (element) => ({
        visibility: element.computedVisibility,
      }),
    },
  };
  const body = new TestElement(document);
  document.body = body;
  document.activeElement = body;
  const frame = new TestElement(document, body);
  frame.frame = true;
  const target = new TestElement(document, frame);
  const source = new TestElement(document, frame);
  const otherFrame = new TestElement(document, body);
  otherFrame.frame = true;
  const outgoingControl = new TestElement(document, otherFrame);
  const transfer = () =>
    canTransferWindowFocus(target.asHTMLElement(), source.asHTMLElement());
  return {
    document,
    body,
    frame,
    target,
    source,
    otherFrame,
    outgoingControl,
    transfer,
  };
}

describe('passive window focus transfer', () => {
  it('claims the visible preview while an outgoing hidden window still owns native focus', () => {
    const f = fixture();
    f.outgoingControl.focus();
    expect(f.transfer()).toBe(false);
    f.otherFrame.inert = true;
    f.otherFrame.visibility = 'hidden';
    // Setting inert/visibility need not synchronously clear activeElement.
    expect(f.document.activeElement).toBe(f.outgoingControl);
    expect(f.transfer()).toBe(true);
    f.target.focus();
    expect(f.document.activeElement).toBe(f.target);
  });

  it.each(['inert', 'hidden', 'display', 'visibility', 'connected'] as const)(
    'permits transfer after the old ancestor becomes unavailable through %s',
    (state) => {
      const f = fixture();
      f.outgoingControl.focus();
      if (state === 'visibility') f.otherFrame.visibility = 'hidden';
      else if (state === 'display' || state === 'connected')
        f.otherFrame[state] = false;
      else f.otherFrame[state] = true;
      expect(f.document.activeElement).toBe(f.outgoingControl);
      expect(f.transfer()).toBe(true);
    },
  );

  it('retains the existing frame, body and source-pane transfer paths', () => {
    const f = fixture();
    expect(f.transfer()).toBe(true);
    f.frame.focus();
    expect(f.transfer()).toBe(true);
    const selectedFile = new TestElement(f.document, f.source);
    selectedFile.focus();
    expect(f.transfer()).toBe(true);
  });

  it('does not take focus from a live toolbar, another window, menu or dialog', () => {
    const f = fixture();
    const toolbarControl = new TestElement(f.document, f.frame);
    const menuControl = new TestElement(f.document, f.body);
    const dialogControl = new TestElement(f.document, f.body);
    for (const control of [
      toolbarControl,
      f.outgoingControl,
      menuControl,
      dialogControl,
    ]) {
      control.focus();
      expect(f.transfer()).toBe(false);
      expect(f.document.activeElement).toBe(control);
    }
  });

  it('does not accept a source pane owned by another visible window', () => {
    const f = fixture();
    f.outgoingControl.focus();
    expect(
      canTransferWindowFocus(
        f.target.asHTMLElement(),
        f.otherFrame.asHTMLElement(),
      ),
    ).toBe(false);
  });

  it.each(['inert', 'hidden', 'display', 'visibility', 'connected'] as const)(
    'never transfers into a target whose ancestor is unavailable through %s',
    (state) => {
      const f = fixture();
      if (state === 'visibility') f.frame.visibility = 'hidden';
      else if (state === 'display' || state === 'connected')
        f.frame[state] = false;
      else f.frame[state] = true;
      expect(f.transfer()).toBe(false);
    },
  );

  it('rejects a missing or disabled target and CSS visibility collapse', () => {
    const f = fixture();
    expect(canTransferWindowFocus(null)).toBe(false);
    f.target.disabled = true;
    expect(f.transfer()).toBe(false);
    f.target.disabled = false;
    f.target.visibility = 'collapse';
    expect(f.transfer()).toBe(false);
  });
});
