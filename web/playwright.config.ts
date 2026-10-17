import { defineConfig } from '@playwright/test'

// The pseudo-locale test of PLAN.md 7.4 runs every page at phone and
// desktop width, each against its own fresh server (see e2e/global-setup.ts).
export default defineConfig({
  testDir: 'e2e',
  globalSetup: './e2e/global-setup.ts',
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [['github'], ['list']] : 'list',
  use: { trace: 'retain-on-failure' },
  projects: [
    {
      name: 'phone',
      use: { baseURL: 'http://127.0.0.1:18090/', viewport: { width: 375, height: 812 }, deviceScaleFactor: 2, hasTouch: true },
    },
    {
      name: 'desktop',
      use: { baseURL: 'http://127.0.0.1:18091/', viewport: { width: 1280, height: 800 } },
    },
  ],
})
