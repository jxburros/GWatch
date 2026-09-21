// Playwright configuration for the browser tests in tests/e2e/: an axe scan
// of every page in both themes and a walk through the Help page's Keyboard
// contract. They run against web/ served statically by tests/e2e/serve.mjs
// with the in-browser mock backend switched on (?mock=1), so no Go service is
// involved. `npm test` stays the quick jsdom suite; this is `npm run test:e2e`
// (or `make web-e2e`), and needs Chromium once: `npx playwright install
// --with-deps chromium`.

import { defineConfig, devices } from '@playwright/test';

const port = Number(process.env.PORT) || 4173;

export default defineConfig({
  testDir: 'tests/e2e',
  testMatch: /.*\.spec\.mjs$/,
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['github']] : 'list',
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    ...devices['Desktop Chrome'],
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    command: 'node tests/e2e/serve.mjs',
    port,
    reuseExistingServer: !process.env.CI,
    env: { PORT: String(port) },
  },
});
