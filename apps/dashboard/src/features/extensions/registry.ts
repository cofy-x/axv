import type { AppDefinition } from '@cofy-x/axv-nextstep';

/** Composition rejects ambiguous identity before passing definitions to the desktop. */
export function composeApplicationDefinitions(...groups: AppDefinition[][]) {
  const apps: AppDefinition[] = [];
  const ids = new Set<string>();
  for (const group of groups) {
    for (const app of group) {
      if (ids.has(app.id))
        throw new Error(`Duplicate application ID: ${app.id}`);
      ids.add(app.id);
      apps.push(app);
    }
  }
  return apps;
}
