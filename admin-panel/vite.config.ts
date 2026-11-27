import path from 'node:path';
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// Served by the Fitkit server at /admin/ (FITKIT_ADMIN_WEB_DIR).
export default defineConfig({
  base: '/admin/',
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(__dirname, './src') } },
  server: { port: 5174, proxy: { '/admin/api': 'http://localhost:8080' } },
});
