// Reusing a key after an uncertain response lets the server recover the original
// operation. Changing the payload always starts a separate operation.
export class RequestIdentity {
  private requestId = '';
  private identities: Record<string, string> = {};

  constructor(
    private readonly scope?: string,
    private readonly storage?: Pick<Storage, 'getItem' | 'setItem'>,
  ) {}

  private persistence() {
    if (!this.scope) return undefined;
    return this.storage || globalThis.sessionStorage;
  }

  private read() {
    const raw = this.persistence()?.getItem(`axv-requests:${this.scope}`);
    if (!raw) return;
    const value: unknown = JSON.parse(raw);
    if (
      !value ||
      typeof value !== 'object' ||
      Array.isArray(value) ||
      Object.values(value).some((id) => typeof id !== 'string')
    )
      throw new Error(
        'The saved transfer request identities could not be read. Keep the original receipt before clearing browser session data.',
      );
    this.identities = value as Record<string, string>;
  }

  private write() {
    this.persistence()?.setItem(
      `axv-requests:${this.scope}`,
      JSON.stringify(this.identities),
    );
  }

  for(payload: unknown) {
    const next = JSON.stringify(payload);
    this.read();
    this.identities[next] ||= crypto.randomUUID();
    this.write();
    this.requestId = this.identities[next];
    return this.requestId;
  }

  complete(requestId = this.requestId) {
    try {
      this.read();
      // An older view's late acknowledgment must not clear a newer operation
      // that was explicitly started with the same payload after completion.
      for (const [payload, id] of Object.entries(this.identities)) {
        if (id === requestId) delete this.identities[payload];
      }
      this.write();
      return true;
    } catch {
      // Keeping an acknowledged key is safe: a later retry recovers the same
      // receipt. The caller reports that this browser could not clear the key.
      return false;
    }
  }
}
