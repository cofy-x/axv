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
      budgetPath: fileURLToPath(
        new URL('../../scripts/dashboard-bundle-budget.json', import.meta.url),
      ),
      reportPath: process.env.AXV_BUNDLE_REPORT,
    }),
  ],
  build: {
    modulePreload: {
      // WebKit retains failed entry preloads and can block an explicit retry.
      // Keep dependency preloads; let import() fetch the dynamic entry itself.
      resolveDependencies: (filename, dependencies, { hostType }) =>
        hostType === 'js'
          ? dependencies.filter((dependency) => dependency !== filename)
          : dependencies,
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
