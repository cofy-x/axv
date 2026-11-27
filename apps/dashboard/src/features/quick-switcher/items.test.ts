import { describe, expect, it } from 'vitest';
import { filterQuickSwitcherItems } from './items';
import type { QuickSwitcherItem } from './items';

const noop = () => {};

const sample: QuickSwitcherItem[] = [
  {
    id: 'app:terminal',
    group: 'Applications',
    title: 'Terminal',
    subtitle: 'Open host shell',
    icon: 'terminal',
    onSelect: noop,
  },
  {
    id: 'vm:dev-box',
    group: 'Virtual Machines',
    title: 'dev-box',
    subtitle: 'Running · 2 CPU · 2048 MiB',
    badge: 'VM',
    icon: 'virtual-machine',
    onSelect: noop,
  },
  {
    id: 'codex:alert',
    group: 'Action needed',
    title: 'Fix auth bug',
    subtitle: 'VM · dev-box · /projects/api',
    badge: 'Approval',
    icon: 'companion',
    onSelect: noop,
  },
];

describe('filterQuickSwitcherItems', () => {
  it('orders Action needed ahead of VMs and Applications when query is empty', () => {
    const result = filterQuickSwitcherItems(sample, '');
    expect(result.map((item) => item.id)).toEqual([
      'codex:alert',
      'vm:dev-box',
      'app:terminal',
    ]);
  });

  it('matches across multi-word tokens across title, subtitle, and badge', () => {
    const result = filterQuickSwitcherItems(sample, 'dev api');
    expect(result.map((item) => item.id)).toEqual(['codex:alert']);
  });

  it('returns empty array when no item matches all tokens', () => {
    expect(filterQuickSwitcherItems(sample, 'nonexistent')).toEqual([]);
  });
});
