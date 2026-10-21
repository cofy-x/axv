import manifest from '../../../../../examples/extensions/checklist/manifest.json';
import template from '../../../../../examples/extensions/checklist/index.html?raw';
import sdk from '../../../../../examples/extensions/sdk.js?raw';
import type { ExtensionPackage } from './types';

export const checklistPackage: ExtensionPackage = {
  manifest: {
    ...manifest,
    schemaVersion: 1,
    apiVersion: 1,
    icon: 'checklist',
    capabilities: ['storage'],
  },
  html: template.replace('/* AXV_EXTENSION_SDK */', () => sdk),
};
