import { describe, expect, it } from 'vitest';
import { contextRunKey, readyContextRun, selectedContextRun } from './state';
import { conversationLabel } from './state';
import type { CodexSessionRecord } from '../codex/api';

describe('document context destination', () => {
  const original = {
    id: 'conversation',
    runId: 'original-run',
    status: 'running',
    conversationId: 'provider-thread',
  };

  it('never redirects a previously selected conversation to its continuation', () => {
    const selection = contextRunKey(original);
    expect(selectedContextRun([original], selection)).toBe(original);
    const continued = { ...original, runId: 'continued-run' };
    expect(selectedContextRun([continued], selection)).toBeUndefined();
    expect(selectedContextRun([continued], contextRunKey(continued))).toBe(
      continued,
    );
  });

  it('offers only initialized running destinations', () => {
    expect(readyContextRun(original)).toBe(true);
    for (const status of ['starting', 'stopping', 'ended', 'lost', 'failed']) {
      const unavailable = { ...original, status };
      expect(readyContextRun(unavailable)).toBe(false);
      expect(
        selectedContextRun([unavailable], contextRunKey(original)),
      ).toBeUndefined();
    }
    expect(readyContextRun({ ...original, conversationId: '' })).toBe(false);
  });

  it('distinguishes conversations with the same visible title and project', () => {
    const first = {
      ...original,
      id: '12345678original',
      title: '',
      projectPath: '/code/app',
    } as CodexSessionRecord;
    const second = { ...first, id: '12345678independent' };
    expect(conversationLabel(first, [first])).toBe('app');
    const firstLabel = conversationLabel(first, [first, second]);
    const secondLabel = conversationLabel(second, [first, second]);
    expect(firstLabel).toContain('/code/app');
    expect(firstLabel).toContain('12345678orig');
    expect(secondLabel).toContain('12345678inde');
    expect(firstLabel).not.toBe(secondLabel);
  });
});
