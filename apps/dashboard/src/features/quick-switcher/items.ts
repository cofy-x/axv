import type { IconName } from '@cofy-x/axv-nextstep';

export type QuickSwitcherGroup =
  | 'Action needed'
  | 'Codex'
  | 'Virtual Machines'
  | 'Projects'
  | 'Applications'
  | 'Actions';

export interface QuickSwitcherItem {
  id: string;
  group: QuickSwitcherGroup;
  title: string;
  subtitle?: string;
  badge?: string;
  keywords?: string;
  icon: IconName;
  onSelect: () => void;
}

const GROUP_PRIORITY: Record<QuickSwitcherGroup, number> = {
  'Action needed': 0,
  Codex: 1,
  'Virtual Machines': 2,
  Projects: 3,
  Applications: 4,
  Actions: 5,
};

function scoreItem(item: QuickSwitcherItem, tokens: string[]): number {
  if (tokens.length === 0) return 100 - GROUP_PRIORITY[item.group] * 10;
  const title = item.title.toLowerCase();
  const haystack = [
    item.title,
    item.subtitle,
    item.badge,
    item.keywords,
    item.group,
  ]
    .filter(Boolean)
    .join(' ')
    .toLowerCase();

  let score = 0;
  for (const token of tokens) {
    const idx = haystack.indexOf(token);
    if (idx < 0) return -1;
    if (title === token) score += 60;
    else if (title.startsWith(token)) score += 40;
    else if (title.includes(token)) score += 25;
    else score += 10;
  }
  return score - GROUP_PRIORITY[item.group] * 2;
}

export function filterQuickSwitcherItems(
  items: QuickSwitcherItem[],
  query: string,
): QuickSwitcherItem[] {
  const tokens = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  return items
    .map((item, index) => ({ item, index, score: scoreItem(item, tokens) }))
    .filter((entry) => entry.score >= 0)
    .sort((a, b) => b.score - a.score || a.index - b.index)
    .map((entry) => entry.item);
}
