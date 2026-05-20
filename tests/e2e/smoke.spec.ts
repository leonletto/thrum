import { test, expect } from '@playwright/test';
import { thrum, getWebUIUrl } from './helpers/thrum-cli.js';
import { waitForWebSocket } from './helpers/fixtures.js';

test('smoke: daemon is running and UI loads', async ({ page }) => {
  // Arrange: daemon already started by global-setup

  // Act: navigate to UI
  await page.goto(getWebUIUrl());

  // Assert: page loads and shows connected status (health bar)
  await waitForWebSocket(page);
});

test('smoke: health bar shows connected', async ({ page }) => {
  await page.goto(getWebUIUrl());

  // The health bar should show CONNECTED when WebSocket is up
  await expect(page.getByText('CONNECTED')).toBeVisible({ timeout: 15_000 });
});

test('smoke: CLI message sent while UI is open', async ({ page }) => {
  // Arrange: navigate to UI first
  await page.goto(getWebUIUrl());
  await waitForWebSocket(page);

  // Ensure we have an active session (in case other tests ended it)
  try {
    thrum(['session', 'start']);
  } catch (err: any) {
    const msg = err.stderr?.toString() || err.message || '';
    if (!msg.toLowerCase().includes('already active') && !msg.toLowerCase().includes('already exists')) {
      throw err;
    }
  }

  // Act: send a message via CLI (thrum-t698: explicit --broadcast — smoke
  // test verifies the message appears in the UI feed, not a specific recipient)
  const uniqueMsg = `Smoke test ${Date.now()}`;
  thrum(['send', uniqueMsg, '--broadcast']);

  // Assert: message appears in the UI (real-time via WebSocket)
  await expect(page.getByText(uniqueMsg)).toBeVisible({ timeout: 10_000 });
});
