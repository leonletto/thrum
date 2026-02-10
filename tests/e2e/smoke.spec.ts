import { test, expect } from '@playwright/test';
import { thrum } from './helpers/thrum-cli.js';
import { waitForWebSocket } from './helpers/fixtures.js';

test('smoke: daemon is running and UI loads', async ({ page }) => {
  // Arrange: daemon already started by global-setup

  // Act: navigate to UI
  await page.goto('/');

  // Assert: page loads and shows connected status (health bar)
  await waitForWebSocket(page);
});

test('smoke: health bar shows connected', async ({ page }) => {
  await page.goto('/');

  // The health bar should show CONNECTED when WebSocket is up
  await expect(page.getByText('CONNECTED')).toBeVisible({ timeout: 15_000 });
});

test.fixme('smoke: CLI message sent while UI is open', async ({ page }) => {
  // FIXME: WebSocket push notifications don't trigger Live Feed re-render.
  // Same root cause as SC-51. Message sends successfully but doesn't appear in UI.
  // Arrange: navigate to UI first
  await page.goto('/');
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

  // Act: send a message via CLI
  const uniqueMsg = `Smoke test ${Date.now()}`;
  thrum(['send', uniqueMsg]);

  // Assert: message appears in the UI (real-time via WebSocket)
  await expect(page.getByText(uniqueMsg)).toBeVisible({ timeout: 10_000 });
});
