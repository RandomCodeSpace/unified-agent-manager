// Component tests (packages/ui/tests/dom): the real React tree in happy-dom, driven through the in-browser
// mock of the service. The pure-logic suite stays on `node --test` (npm test).
import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'happy-dom',
    include: ['packages/ui/tests/dom/**/*.test.tsx'],
    setupFiles: ['packages/ui/tests/dom/setup.ts'],
    // Each test drives whole flows through the mock, which answers after 60 ms per request.
    testTimeout: 20000,
    coverage: {
      provider: 'v8',
      include: ['src/**/*.{ts,tsx}', 'packages/ui/src/**/*.{ts,tsx}'],
      exclude: ['packages/ui/src/mock/**'],
      reportsDirectory: 'coverage/dom',
      // Paths relative to the repository root, where the Sonar scan runs.
      reporter: ['text-summary', ['lcovonly', { projectRoot: '..' }]],
    },
  },
});
