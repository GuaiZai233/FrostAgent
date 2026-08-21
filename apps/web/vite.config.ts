import { defineConfig } from 'vite';
import path from 'node:path';

export default defineConfig({
  root: '.',
  build: {
    outDir: '../../internal/frontend/dist',
    emptyOutDir: true,
    target: 'esnext',
  },
  server: {
    port: 4200,
    proxy: {
      '/frostagent.v1.': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
    },
  },
  resolve: {
    alias: {
      '@frostagent/proto': path.resolve(__dirname, '../../libs/frostagent-proto/src'),
    },
  },
});
