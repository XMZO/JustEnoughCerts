import { fileURLToPath } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

const repo = fileURLToPath(new URL('..', import.meta.url))

export default defineConfig({
  // Relative asset URLs: the server puts a <base href> into index.html, so
  // the same build works at the root and under any sub-path.
  base: './',
  plugins: [vue(), tailwindcss()],
  server: {
    // The locale files and the design tokens live outside web/.
    fs: { allow: [repo] },
    proxy: { '/api': 'http://127.0.0.1:8080' },
  },
  build: {
    target: 'es2023',
    assetsInlineLimit: 0,
    reportCompressedSize: false,
  },
  test: {
    environment: 'happy-dom',
    include: ['src/**/*.test.ts'], // e2e/ is Playwright's
  },
})
