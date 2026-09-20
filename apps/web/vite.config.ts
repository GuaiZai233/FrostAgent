import { defineConfig } from 'vite';
import { fileURLToPath } from 'node:url';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  plugins: [tailwindcss()],
  root: '.',
  build: {
    outDir: '../../internal/frontend/dist',
    emptyOutDir: true,
    target: 'esnext',
  },
  server: {
    port: 4200,
    proxy: {
      '/instances/': { target: 'http://127.0.0.1:8080', changeOrigin: false, ws: true },
      '/api/': { target: 'http://127.0.0.1:8080', changeOrigin: true },
      '/frostagent.v1.': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
    },
  },
  resolve: {
    alias: {
      '@frostagent/proto': fileURLToPath(new URL('../../libs/frostagent-proto/src', import.meta.url)),
    },
  },
});
