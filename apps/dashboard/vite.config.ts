import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  resolve: { dedupe: ['react', 'react-dom'] },
  server: {
    host: '127.0.0.1',
    port: Number(process.env.WEB_PORT || 0),
    strictPort: true,
    headers: {
      'X-Frame-Options': 'DENY',
      'Content-Security-Policy': "frame-ancestors 'none'",
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
