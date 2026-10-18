import { describe, expect, it } from 'vitest';
import { RequestIdentity } from './request-identity';

describe('mutation request identity', () => {
  it('recovers an uncertain request until its payload changes or it completes', () => {
    const requests = new RequestIdentity();
    const payload = { projectId: 'p', snapshotId: 's', path: 'brief.md' };
    const first = requests.for(payload);
    expect(requests.for({ ...payload })).toBe(first);
    expect(requests.for({ ...payload, path: 'another.md' })).not.toBe(first);
    expect(requests.for(payload)).toBe(first);
    const changed = requests.for({ ...payload, path: 'another.md' });
    requests.complete();
    expect(requests.for({ ...payload, path: 'another.md' })).not.toBe(changed);
    expect(requests.for(payload)).toBe(first);
  });

  it('retains uncertain payload identities after a panel is reopened', () => {
    const values = new Map<string, string>();
    const storage = {
      getItem: (key: string) => values.get(key) || null,
      setItem: (key: string, value: string) => {
        values.set(key, value);
      },
    };
    const first = new RequestIdentity('database:document', storage);
    const id = first.for({ path: 'brief.md' });
    first.for({ path: 'another.md' });
    const reopened = new RequestIdentity('database:document', storage);
    expect(reopened.for({ path: 'brief.md' })).toBe(id);
    expect(reopened.complete()).toBe(true);
    expect(reopened.for({ path: 'brief.md' })).not.toBe(id);
  });

  it('refuses dispatch when its retry identity cannot be persisted', () => {
    const requests = new RequestIdentity('database:document', {
      getItem: () => null,
      setItem: () => {
        throw new Error('Storage unavailable');
      },
    });
    expect(() => requests.for({ path: 'brief.md' })).toThrow(
      'Storage unavailable',
    );
  });

  it('does not let a late acknowledgment clear a new request from a reopened view', () => {
    const values = new Map<string, string>();
    const storage = {
      getItem: (key: string) => values.get(key) || null,
      setItem: (key: string, value: string) => {
        values.set(key, value);
      },
    };
    const oldView = new RequestIdentity('shared', storage);
    const reopened = new RequestIdentity('shared', storage);
    const first = oldView.for({ path: 'brief.md' });
    expect(reopened.for({ path: 'brief.md' })).toBe(first);
    oldView.complete(first);
    const next = oldView.for({ path: 'brief.md' });
    expect(next).not.toBe(first);
    expect(reopened.complete(first)).toBe(true);
    expect(
      new RequestIdentity('shared', storage).for({ path: 'brief.md' }),
    ).toBe(next);
  });

  it('acknowledges the dispatched operation when another payload is now selected', () => {
    const requests = new RequestIdentity();
    const first = requests.for({ path: 'a.md' });
    const second = requests.for({ path: 'b.md' });
    requests.complete(first);
    expect(requests.for({ path: 'b.md' })).toBe(second);
    expect(requests.for({ path: 'a.md' })).not.toBe(first);
  });
});
