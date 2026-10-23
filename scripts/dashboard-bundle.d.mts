import type { Plugin } from 'vite';

export function dashboardBundlePlugin(options: {
  root: string;
  budgetPath?: string;
  reportPath?: string;
}): Plugin;
