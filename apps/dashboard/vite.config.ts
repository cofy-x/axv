import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { dashboardBundlePlugin } from '../../scripts/dashboard-bundle.mjs';
import { fileURLToPath } from 'node:url';

const repositoryRoot = fileURLToPath(new URL('../../', import.meta.url));

export default defineConfig({
  plugins: [
    react(),
    dashboardBundlePlugin({
      root: repositoryRoot.replace(/\/$/, ''),
      requiredStartupModules: [
        fileURLToPath(new URL('./src/main.tsx', import.meta.url)),
      ],
      budgetPath: fileURLToPath(
        new URL('../../scripts/dashboard-bundle-budget.json', import.meta.url),
      ),
      reportPath: process.env.AXV_BUNDLE_REPORT,
    }),
  ],
  build: {
    rolldownOptions: {
      output: { entryFileNames: 'assets/startup-[name]-[hash].js' },
    },
    modulePreload: {
      // WebKit retains failed module preloads across explicit startup reloads.
      // Identify the loader by its output prefix; Vite retains CSS separately.
      resolveDependencies: (filename, dependencies, { hostType, hostId }) =>
        hostType !== 'js'
          ? dependencies
          : hostId.startsWith('assets/startup-')
            ? dependencies.filter((dependency) => dependency.endsWith('.css'))
            : dependencies.filter((dependency) => dependency !== filename),
    },
  },
  resolve: { dedupe: ['react', 'react-dom'] },
  server: {
    host: '127.0.0.1',
    port: Number(process.env.WEB_PORT || 0),
    strictPort: true,
    headers: {
      'X-Frame-Options': 'DENY',
      'Content-Security-Policy': "frame-ancestors 'none'; frame-src 'self'",
    },
    proxy: {
      '/api': {
        target:
          process.env.API_TARGET ||
          `http://127.0.0.1:${process.env.PORT || 3001}`,
        ws: true,
      },
    },
  },
});
