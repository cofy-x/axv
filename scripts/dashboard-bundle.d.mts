import type { Plugin } from 'vite';

export function dashboardBundlePlugin(options: {
  root: string;
  budgetPath?: string;
  reportPath?: string;
  /** Absolute or root-relative module paths required even when dynamically loaded. */
  requiredStartupModules?: string[];
}): Plugin;
