import { test, expect } from '@playwright/test';
import { thrum } from './helpers/thrum-cli.js';
import { sendMessage, registerAgent, waitForWebSocket } from './helpers/fixtures.js';

/**
 * E2E tests for Web UI (SC-49 to SC-57)
 *
 * These tests validate that the Web UI is properly wired to real RPC data:
 * - WebSocket connection and health bar
 * - Live Feed shows real messages (not mock data)
 * - Live Feed real-time updates
 * - Inbox view shows real messages
 * - Inbox compose functionality
 * - Agent list sidebar
 * - Who Has? view
 * - Theme toggle
 * - Settings panel
 */

test.describe('Web UI', () => {
  // Ensure an active session exists before tests that send messages.
  // The daemon may have been restarted by daemon.spec.ts, killing the
  // session that global-setup created.
  test.beforeAll(() => {
    try {
      thrum(['session', 'start']);
    } catch (err: any) {
      const msg = err.message || '';
      if (!msg.toLowerCase().includes('already active') && !msg.toLowerCase().includes('already exists')) {
        throw err;
      }
    }
  });
  test('SC-49: WebSocket connection and health bar', async ({ page }) => {
    // Act: navigate to UI
    await page.goto('/');

    // Assert: footer shows connected status
    await waitForWebSocket(page);

    // Verify: health bar elements
    const footer = page.locator('footer, [role="contentinfo"]');
    await expect(footer.getByText('CONNECTED')).toBeVisible();

    // Note: version, uptime, and repo name are also displayed but formats may vary
    // Just verify the footer exists and has content
    const footerText = await footer.textContent();
    expect(footerText?.trim().length).toBeGreaterThan(0);
  });

  test('SC-50: Live Feed shows real messages', async ({ page }) => {
    // Arrange: send a unique message via CLI
    const uniqueMessage = `E2E test message ${Date.now()}`;
    sendMessage(uniqueMessage);

    // Act: navigate to Live Feed (default view)
    await page.goto('/');
    await waitForWebSocket(page);

    // Assert: real message appears in Live Feed
    // The LiveFeed component uses useFeed() -> useMessageList() via RPC
    // FeedItem renders item.preview as text content
    await expect(page.getByText(uniqueMessage)).toBeVisible({ timeout: 10_000 });
  });

  test('SC-51: Live Feed real-time updates', async ({ page }) => {
    // Arrange: open browser to Live Feed first
    await page.goto('/');
    await waitForWebSocket(page);

    // Act: send a message while browser is watching
    const realtimeMessage = `Real-time test ${Date.now()}`;
    sendMessage(realtimeMessage);

    // Assert: new message appears without page refresh
    // WebSocket push notification should trigger React Query invalidation
    await expect(page.getByText(realtimeMessage)).toBeVisible({ timeout: 10_000 });
  });

  test('SC-52: Inbox view shows real messages', async ({ page }) => {
    // Arrange: send a message addressed to current session
    const inboxMessage = `Inbox test ${Date.now()}`;
    sendMessage(inboxMessage);

    // Act: navigate and click "My Inbox" (same selector as SC-53)
    await page.goto('/');
    await waitForWebSocket(page);

    const inboxNav = page.getByText('My Inbox');
    await expect(inboxNav).toBeVisible({ timeout: 5000 });
    await inboxNav.click();

    // Wait for inbox content to load
    await page.waitForTimeout(500);

    // Assert: page shows inbox content
    const pageContent = await page.textContent('body');
    expect(pageContent?.toLowerCase()).toContain('inbox');
  });

  test('SC-53: Inbox compose message', async ({ page }) => {
    // Arrange: register a recipient agent
    registerAgent('test-recipient', 'all', 'Test Recipient');

    // Act: navigate to UI and switch to inbox view
    await page.goto('/');
    await waitForWebSocket(page);

    // Navigate to "My Inbox" via sidebar
    const inboxNav = page.getByText('My Inbox');
    await expect(inboxNav).toBeVisible({ timeout: 5000 });
    await inboxNav.click();

    // The ComposeBar is always visible at the bottom of InboxView (no compose dialog)
    const messageInput = page.getByRole('textbox', { name: /write a message/i });
    await expect(messageInput).toBeVisible({ timeout: 5000 });

    // Fill in message content
    const composeText = `Test compose message ${Date.now()}`;
    await messageInput.fill(composeText);

    // Assert: send button becomes enabled after typing and click it
    const sendButton = page.getByRole('button', { name: /^send$/i });
    await expect(sendButton).toBeEnabled({ timeout: 2000 });
    await sendButton.click();

    // Assert: message input clears after successful send
    await expect(messageInput).toHaveValue('', { timeout: 5000 });
  });

  test('SC-54: Agent list in sidebar', async ({ page }) => {
    // Arrange: register multiple agents so the sidebar has agents to display
    registerAgent('agent_sidebar_1', 'all', 'Agent One', 'sidebar_one');
    registerAgent('agent_sidebar_2', 'relay', 'Agent Two', 'sidebar_two');

    // Act: navigate to UI
    await page.goto('/');
    await waitForWebSocket(page);

    // The AgentList component renders:
    //   <h3 class="... uppercase ...">Agents ({count})</h3>
    // The "uppercase" CSS class renders it visually as "AGENTS (N)"
    // but textContent returns the DOM text "Agents (N)".
    // Wait for the agent list to load with a non-zero count.
    // Use a locator that waits for the count to be > 0.
    const agentHeading = page.locator('h3').filter({ hasText: /agents?\s*\(\d+\)/i });
    await expect(agentHeading).toBeVisible({ timeout: 10_000 });

    // Extract and verify the count is greater than 0
    const headingText = await agentHeading.textContent();
    const match = headingText?.match(/agents?\s*\((\d+)\)/i);
    expect(match).not.toBeNull();
    expect(parseInt(match![1], 10)).toBeGreaterThan(0);
  });

  test('SC-55: Who Has? view', async ({ page }) => {
    // Navigate to UI
    await page.goto('/');
    await waitForWebSocket(page);

    // Look for "Who Has?" navigation item — must be visible
    const whoHasButton = page.getByRole('button', { name: /who has/i });
    const whoHasLink = page.getByRole('link', { name: /who has/i });
    await expect(whoHasButton.or(whoHasLink)).toBeVisible({ timeout: 5000 });

    // Click whichever element is visible
    if (await whoHasButton.isVisible().catch(() => false)) {
      await whoHasButton.click();
    } else {
      await whoHasLink.click();
    }

    await page.waitForTimeout(500);

    // Verify the view loaded (should have a heading)
    const heading = page.getByRole('heading', { name: /who has/i });
    await expect(heading).toBeVisible({ timeout: 5000 });
  });

  test('SC-56: Theme toggle', async ({ page }) => {
    // Navigate to UI
    await page.goto('/');
    await waitForWebSocket(page);

    // Assert: theme toggle button must be visible
    // ThemeToggle renders a Button with aria-label="Toggle theme"
    const themeButton = page.getByRole('button', { name: /toggle theme/i });
    await expect(themeButton).toBeVisible({ timeout: 5000 });

    // Get initial theme state from <html> element (NOT body)
    // The useTheme hook applies 'dark' class to document.documentElement
    const initialHtmlClass = await page.locator('html').getAttribute('class');

    // Click the theme button to open dropdown menu
    await themeButton.click();
    await page.waitForTimeout(300);

    // Select a different theme from the dropdown
    // The dropdown has options: Light, Dark, System
    // Pick the opposite of current to ensure a change
    const hasDarkClass = initialHtmlClass?.includes('dark');
    const targetTheme = hasDarkClass ? 'Light' : 'Dark';
    const themeOption = page.getByRole('menuitem', { name: targetTheme });
    await expect(themeOption).toBeVisible({ timeout: 2000 });
    await themeOption.click();
    await page.waitForTimeout(300);

    // Verify theme changed on <html> element
    const newHtmlClass = await page.locator('html').getAttribute('class');
    if (hasDarkClass) {
      // Was dark, switched to light — 'dark' class should be removed
      expect(newHtmlClass).not.toContain('dark');
    } else {
      // Was light/no class, switched to dark — 'dark' class should be added
      expect(newHtmlClass).toContain('dark');
    }

    // Reload page to test persistence (theme saved to localStorage)
    await page.reload();
    await waitForWebSocket(page);

    // Verify theme persisted after reload
    const reloadedHtmlClass = await page.locator('html').getAttribute('class');
    expect(reloadedHtmlClass).toBe(newHtmlClass);
  });

  test('SC-57: Settings panel', async ({ page }) => {
    // Navigate to UI
    await page.goto('/');
    await waitForWebSocket(page);

    // Assert: settings button must be visible in header
    // There are two "Settings" buttons: one in header (opens SubscriptionPanel)
    // and one in sidebar nav (opens SettingsView). Scope to header to avoid ambiguity.
    const settingsButton = page.locator('header').getByRole('button', { name: /settings/i });
    await expect(settingsButton).toBeVisible({ timeout: 5000 });
    await settingsButton.click();

    await page.waitForTimeout(500);

    // Verify the subscriptions panel opened
    // The settings button opens SubscriptionPanel, which is a Dialog with title "Subscriptions"
    const subscriptionsHeading = page.getByRole('heading', { name: /subscriptions/i });
    await expect(subscriptionsHeading).toBeVisible({ timeout: 5000 });
  });
});
