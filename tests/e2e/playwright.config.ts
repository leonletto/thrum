/**
 * Local config for running `npx playwright test` from tests/e2e/.
 *
 * Playwright only looks for a config in CWD — it does NOT walk up directories.
 * Without this file, running from tests/e2e/ silently uses default settings:
 * no globalSetup (so no daemon, no port file), multiple workers instead of 1,
 * and all browser tests fail on the fallback port 9999.
 *
 * This config mirrors the root playwright.config.ts with paths adjusted for
 * the tests/e2e/ working directory.
 */
import { defineConfig } from '@playwright/test';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';

const REPO_ROOT = path.resolve(__dirname, '../..');
const WS_PORT_FILE = path.join(REPO_ROOT, 'node_modules', '.e2e-ws-port');

function getBaseURL(): string {
  if (existsSync(WS_PORT_FILE)) {
    const port = readFileSync(WS_PORT_FILE, 'utf-8').trim();
    if (port) return `http://localhost:${port}`;
  }
  return 'http://localhost:9999';
}

export default defineConfig({
  testDir: '.',
  globalSetup: './global-setup.ts',
  globalTeardown: './global-teardown.ts',
  timeout: 30_000,
  retries: process.env.CI ? 2 : 0,
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? 'list' : [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: getBaseURL(),
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'chromium',
      use: { browserName: 'chromium' },
    },
  ],
});
