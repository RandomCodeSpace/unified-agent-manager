import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  base: '/',
  plugins: [react(), tailwindcss()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    // Never inline assets as data: URIs; the server's CSP allows fonts from 'self' only.
    assetsInlineLimit: 0,
  },
});
