import { describe, expect, it, vi } from 'vitest';
import {
  clientIdentityKey,
  createClientIdentity as createIdentity,
} from './client-identity';
import type {
  ClientIdentityWitness,
  IdentityWitnessStore,
} from './client-identity-witness';

function memoryWitness(initial?: ClientIdentityWitness): IdentityWitnessStore {
  let value = initial;
  return {
    claim: vi.fn(async (createId) => {
      value ??= { id: createId(), state: 'pending' };
      return { ...value };
    }),
    confirm: vi.fn(async (id) => {
      if (value?.id !== id) throw new Error('Witness changed');
      value = { id, state: 'confirmed' };
    }),
  };
}

function createClientIdentity(
  environment: Parameters<typeof createIdentity>[0],
) {
  return createIdentity({
    witness: memoryWitness(),
    observe: async () => {},
    ...environment,
  });
}

function storage(initial?: string) {
  const values = new Map<string, string>();
  if (initial !== undefined) values.set(clientIdentityKey, initial);
  return {
    getItem: vi.fn((key: string) => values.get(key) ?? null),
    setItem: vi.fn((key: string, value: string) => {
      values.set(key, value);
    }),
  };
}

function locks() {
  let tail: Promise<unknown> = Promise.resolve();
  return vi.fn((work: () => Promise<string>) => {
    const result = tail.then(work);
    tail = result.catch(() => {});
    return result;
  });
}

describe('persistent browser identity', () => {
  it('serializes first startup in separate pages sharing browser storage', async () => {
    const shared = storage();
    const locked = locks();
    const witness = memoryWitness();
    const first = createClientIdentity({
      storage: () => shared,
      locked,
      witness,
      randomId: () => 'first-browser',
    });
    const second = createClientIdentity({
      storage: () => shared,
      locked,
      witness,
      randomId: () => 'second-browser',
    });
    expect(await Promise.all([first(), second()])).toEqual([
      'first-browser',
      'first-browser',
    ]);
    expect(shared.setItem).toHaveBeenCalledExactlyOnceWith(
      clientIdentityKey,
      'first-browser',
    );
  });

  it('shares startup work and retains the captured identity after storage changes', async () => {
    const saved = storage();
    const locked = locks();
    const initialize = createClientIdentity({
      storage: () => saved,
      locked,
      randomId: () => 'captured-browser',
    });
    const first = initialize();
    expect(initialize()).toBe(first);
    await expect(first).resolves.toBe('captured-browser');
    saved.setItem(clientIdentityKey, 'another-browser');
    await expect(initialize()).resolves.toBe('captured-browser');
    expect(locked).toHaveBeenCalledTimes(1);
  });

  it('reuses an existing identifier without locks or a storage write', async () => {
    const saved = storage('existing-non-uuid');
    saved.setItem.mockImplementation(() => {
      throw new Error('writes denied');
    });
    const locked = vi.fn(async () => {
      throw new Error('locks unavailable');
    });
    const randomId = vi.fn(() => 'unused');
    const witness = memoryWitness();
    const initialize = createClientIdentity({
      storage: () => saved,
      locked,
      randomId,
      witness,
    });
    await expect(initialize()).resolves.toBe('existing-non-uuid');
    expect(locked).not.toHaveBeenCalled();
    expect(saved.setItem).not.toHaveBeenCalled();
    expect(randomId).not.toHaveBeenCalled();
    expect(witness.claim).not.toHaveBeenCalled();
  });

  it('reports unreadable storage, then permits an explicit retry', async () => {
    const saved = storage('original-browser');
    const access = vi
      .fn(() => saved)
      .mockImplementationOnce(() => {
        throw new Error('storage access denied');
      });
    const randomId = vi.fn(() => 'unused');
    const initialize = createClientIdentity({
      storage: access,
      locked: locks(),
      randomId,
    });
    await expect(initialize()).rejects.toThrow('cannot read this browser');
    await expect(initialize()).resolves.toBe('original-browser');
    expect(randomId).not.toHaveBeenCalled();
    expect(saved.setItem).not.toHaveBeenCalled();
  });

  it('preserves an identity that appears while waiting for the lock', async () => {
    const saved = storage();
    const witness = memoryWitness();
    const initialize = createClientIdentity({
      storage: () => saved,
      locked: async (work) => {
        saved.setItem(clientIdentityKey, 'newly-visible-legacy-id');
        return work();
      },
      witness,
      randomId: () => 'unused',
    });
    await expect(initialize()).resolves.toBe('newly-visible-legacy-id');
    expect(witness.claim).not.toHaveBeenCalled();
    expect(saved.setItem).toHaveBeenCalledExactlyOnceWith(
      clientIdentityKey,
      'newly-visible-legacy-id',
    );
  });

  it('does not return an ephemeral identity when persistence fails', async () => {
    const saved = storage();
    saved.setItem.mockImplementationOnce(() => {
      throw new Error('storage is full');
    });
    const randomId = vi.fn(() => 'browser');
    const initialize = createClientIdentity({
      storage: () => saved,
      locked: locks(),
      randomId,
    });
    await expect(initialize()).rejects.toThrow('could not save');
    expect(saved.getItem(clientIdentityKey)).toBeNull();
    await expect(initialize()).resolves.toBe('browser');
    expect(randomId).toHaveBeenCalledTimes(1);
  });

  it('requires successful cross-page coordination before creating an identity', async () => {
    const saved = storage();
    const randomId = vi.fn(() => 'unused');
    const initialize = createClientIdentity({
      storage: () => saved,
      locked: async () => {
        throw new Error('Web Locks unavailable');
      },
      randomId,
    });
    await expect(initialize()).rejects.toThrow('Web Locks unavailable');
    expect(randomId).not.toHaveBeenCalled();
    expect(saved.setItem).not.toHaveBeenCalled();
  });

  it('rejects storage writes that cannot be read back', async () => {
    const saved = storage();
    saved.setItem.mockImplementation(() => {});
    const initialize = createClientIdentity({
      storage: () => saved,
      locked: locks(),
      randomId: () => 'not-retained',
    });
    await expect(initialize()).rejects.toThrow('changed or is missing');
  });

  it('does not publish an identity before the election transaction commits', async () => {
    const saved = storage();
    const witness = memoryWitness();
    const claim = vi
      .fn(witness.claim)
      .mockRejectedValueOnce(new Error('Transaction aborted'));
    const initialize = createClientIdentity({
      storage: () => saved,
      locked: locks(),
      witness: { ...witness, claim },
      randomId: () => 'committed-id',
    });
    await expect(initialize()).rejects.toThrow('Transaction aborted');
    expect(saved.setItem).not.toHaveBeenCalled();
    await expect(initialize()).resolves.toBe('committed-id');
  });

  it.each([null, 'unrelated-browser'])(
    'does not restore or overwrite a confirmed identity when localStorage becomes %s',
    async (current) => {
      const saved = storage(current ?? undefined);
      saved.getItem.mockReturnValueOnce(null).mockReturnValueOnce(null);
      const witness = memoryWitness({ id: 'confirmed-id', state: 'confirmed' });
      const initialize = createClientIdentity({
        storage: () => saved,
        locked: locks(),
        witness,
        randomId: () => 'unused',
      });
      await expect(initialize()).rejects.toThrow('changed or is missing');
      expect(saved.setItem).not.toHaveBeenCalled();
      expect(saved.getItem(clientIdentityKey)).toBe(current);
      expect(witness.confirm).not.toHaveBeenCalled();
    },
  );

  it('observes a confirmed identity becoming visible without writing it', async () => {
    const saved = storage('confirmed-id');
    saved.getItem
      .mockReturnValueOnce(null)
      .mockReturnValueOnce(null)
      .mockReturnValueOnce(null);
    const observe = vi.fn(async () => {});
    const initialize = createClientIdentity({
      storage: () => saved,
      locked: locks(),
      witness: memoryWitness({ id: 'confirmed-id', state: 'confirmed' }),
      observe,
      randomId: () => 'unused',
    });
    await expect(initialize()).resolves.toBe('confirmed-id');
    expect(observe).toHaveBeenCalledWith('confirmed-id');
    expect(saved.setItem).not.toHaveBeenCalled();
  });

  it.each(['', 'x'.repeat(129), '界'.repeat(43)])(
    'preserves an invalid stored identity without replacing it (%j)',
    async (invalid) => {
      const saved = storage(invalid);
      const initialize = createClientIdentity({
        storage: () => saved,
        locked: locks(),
        randomId: () => 'replacement',
      });
      await expect(initialize()).rejects.toThrow('identity is invalid');
      expect(saved.getItem(clientIdentityKey)).toBe(invalid);
      expect(saved.setItem).not.toHaveBeenCalled();
    },
  );

  it('assigns independent identities to separate browser storage containers', async () => {
    const firstStorage = storage();
    const first = createClientIdentity({
      storage: () => firstStorage,
      locked: locks(),
      randomId: () => 'one',
    });
    const secondStorage = storage();
    const second = createClientIdentity({
      storage: () => secondStorage,
      locked: locks(),
      randomId: () => 'two',
    });
    await expect(first()).resolves.toBe('one');
    await expect(second()).resolves.toBe('two');
  });
});
