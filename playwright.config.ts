import { defineConfig } from '@playwright/test';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';

/**
 * Read the daemon's WebSocket port written by global-setup.
 * Global-setup writes the port to a file before tests run.
 * On first run (or if file is stale), falls back to reading from daemon status.
 */
const WS_PORT_FILE = path.join(__dirname, 'node_modules', '.e2e-ws-port');
function getBaseURL(): string {
  if (existsSync(WS_PORT_FILE)) {
    const port = readFileSync(WS_PORT_FILE, 'utf-8').trim();
    if (port) return `http://localhost:${port}`;
  }
  return 'http://localhost:9999';
}

export default defineConfig({
  testDir: './tests/e2e',
  globalSetup: './tests/e2e/global-setup.ts',
  globalTeardown: './tests/e2e/global-teardown.ts',
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
