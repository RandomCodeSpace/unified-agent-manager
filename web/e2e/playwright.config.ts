// The browser regression pack: Chromium drives the built uam (UAM_E2E_BIN)
// running the real Copilot CLI against aimock. See docs/testing.md.
import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  testMatch: '*.spec.ts',
  outputDir: 'test-results',
  // CI retries a failed case once so a flake does not drop a pull request from the
  // merge queue; the report still marks a case that passed on retry as flaky.
  // Locally a flaky case fails.
  retries: process.env.CI ? 1 : 0,
  timeout: 120_000,
  expect: { timeout: 20_000 },
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  // Each worker runs its own uam, Copilot CLI and aimock.
  workers: process.env.CI ? 3 : undefined,
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
  use: {
    ...devices['Desktop Chrome'],
    viewport: { width: 1280, height: 900 },
    actionTimeout: 15_000,
    navigationTimeout: 30_000,
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium' }],
});
