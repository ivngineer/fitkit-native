import path from 'node:path';
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(__dirname, './src') } },
  // The Go API (../api) on :8080 — the front-end will call /api/* through this proxy.
  server: { proxy: { '/api': 'http://localhost:8080', '/media': 'http://localhost:8080' } },
});
