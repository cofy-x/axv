import { readFile } from 'node:fs/promises';
import ts from 'typescript';
import { expect, test } from '@playwright/test';
import type * as DraftStorageModule from '../apps/dashboard/src/shared/drafts/storage';
import type {
  DraftRecord,
  DraftRemovalGuard,
} from '../apps/dashboard/src/shared/drafts/storage';

// Exercise the actual storage module against each browser's IndexedDB event
// ordering without a mock transaction implementation or application timers.
test.beforeEach(async ({ page }) => {
  const source = await readFile(
    'apps/dashboard/src/shared/drafts/storage.ts',
    'utf8',
  );
  const { outputText } = ts.transpileModule(source, {
    compilerOptions: {
      target: ts.ScriptTarget.ES2022,
      module: ts.ModuleKind.ESNext,
    },
  });
  await page.route('**/__draft-storage-test', (route) =>
    route.fulfill({ contentType: 'text/html', body: '<!doctype html>' }),
  );
  await page.route('**/__draft-storage-module.js', (route) =>
    route.fulfill({ contentType: 'text/javascript', body: outputText }),
  );
  await page.goto('/__draft-storage-test');
});

for (const phase of ['opening', 'transaction', 'acknowledgement'] as const) {
  test(`guarded draft cleanup preserves recovery during ${phase}`, async ({
    page,
  }) => {
    const result = await page.evaluate(async (phase) => {
      const moduleURL = '/__draft-storage-module.js';
      const { draftStorage } = (await import(
        moduleURL
      )) as typeof DraftStorageModule;
      const drafts = draftStorage(
        { databaseId: 'original-database', clientId: 'original-client' },
        'original-editor',
      );
      const original = {
        entryId: 'same-document-id',
        name: 'Draft.txt',
        text: 'Keep this unsaved text',
        saved: 'Original server content',
        baseRevision: 7,
        updatedAt: 123,
      };
      await drafts.write(original);
      const saved = await drafts.read();
      let changed = false;
      let aborted = false;
      const listeners = new Set<() => void>();
      const guard: DraftRemovalGuard = {
        assertCurrent: () => {
          if (changed) throw new Error('database_changed');
        },
        subscribe: (listener) => {
          listeners.add(listener);
          return () => listeners.delete(listener);
        },
      };
      const invalidate = () => {
        changed = true;
        for (const listener of listeners) listener();
      };
      const originalDelete = IDBObjectStore.prototype.delete;
      if (phase === 'transaction')
        IDBObjectStore.prototype.delete = function (key) {
          const result = originalDelete.call(this, key);
          this.transaction.addEventListener('abort', () => {
            aborted = true;
          });
          invalidate();
          return result;
        };
      let error = '';
      let after: DraftRecord | undefined;
      let newer: DraftRecord | undefined;
      try {
        const removal = drafts.remove(guard);
        if (phase === 'opening') invalidate();
        if (phase === 'acknowledgement') {
          const receipt = (await removal)!;
          invalidate();
          await receipt.restore();
          after = await drafts.read();
          await drafts.write({ ...original, text: 'Newer typing' });
          await receipt.restore();
          newer = await drafts.read();
        } else {
          try {
            await removal;
          } catch (failure) {
            error = (failure as Error).message;
          }
          after = await drafts.read();
        }
      } finally {
        IDBObjectStore.prototype.delete = originalDelete;
      }
      return { saved, after, newer, error, aborted, listeners: listeners.size };
    }, phase);
    expect(result.after).toEqual(result.saved);
    expect(result.listeners).toBe(0);
    if (phase === 'acknowledgement')
      expect(result.newer).toEqual({ ...result.saved, text: 'Newer typing' });
    else expect(result.error).toBe('database_changed');
    if (phase === 'transaction') expect(result.aborted).toBe(true);
  });
}
