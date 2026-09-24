import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  base: '/',
  plugins: [react()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    // Never inline assets as data: URIs; the server's CSP allows fonts from 'self' only.
    assetsInlineLimit: 0,
  },
});
