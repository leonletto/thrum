import { test, expect } from '@playwright/test';
import { thrum, thrumJson, thrumIn, getTestRoot, getImplementerRoot } from './helpers/thrum-cli.js';
import { quickstartAgent, sendMessage, waitForWebSocket } from './helpers/fixtures.js';

/**
 * E2E tests for Multi-Agent Scenarios (SC-58 to SC-64)
 *
 * These tests validate realistic multi-agent coordination workflows
 * with cross-worktree delivery verification: messages are sent from
 * the coordinator worktree and verified in the implementer worktree's
 * inbox to confirm actual delivery, not just storage.
 */

/** Coordinator agent identity (sender). */
function coordEnv(): NodeJS.ProcessEnv {
  return { ...process.env, THRUM_NAME: 'e2e_coordinator', THRUM_ROLE: 'coordinator', THRUM_MODULE: 'all' };
}

/** Implementer agent identity (recipient). */
function implEnv(): NodeJS.ProcessEnv {
  return { ...process.env, THRUM_NAME: 'e2e_implementer', THRUM_ROLE: 'implementer', THRUM_MODULE: 'main' };
}

test.describe('Multi-Agent Scenarios', () => {
  test.beforeAll(() => {
    // Ensure both agents have active sessions
    try {
      thrumIn(getTestRoot(), ['quickstart', '--role', 'coordinator', '--module', 'all',
        '--name', 'e2e_coordinator', '--intent', 'Multi-agent tests'], 10_000, coordEnv());
    } catch { /* may already exist */ }
    try {
      thrumIn(getImplementerRoot(), ['quickstart', '--role', 'implementer', '--module', 'main',
        '--name', 'e2e_implementer', '--intent', 'Multi-agent tests'], 10_000, implEnv());
    } catch { /* may already exist */ }

    // Mark implementer inbox read to start clean
    try {
      thrumIn(getImplementerRoot(), ['message', 'read', '--all'], 10_000, implEnv());
    } catch { /* best effort */ }
  });

  test('SC-58: Two agents coordinate on a feature', async () => {
    // Mark implementer inbox read
    thrumIn(getImplementerRoot(), ['message', 'read', '--all'], 10_000, implEnv());

    // Act: coordinator sends task to implementer
    const sendResult = thrumIn(getTestRoot(), ['send', 'Please implement the WebSocket relay endpoint', '--to', '@e2e_implementer', '--json'], 10_000, coordEnv());
    const parsed = JSON.parse(sendResult);
    expect(parsed.message_id).toMatch(/^msg_/);

    // Assert: implementer receives the task in their inbox (cross-worktree delivery)
    const implInbox = thrumIn(getImplementerRoot(), ['inbox', '--unread', '--json'], 10_000, implEnv());
    const inbox = JSON.parse(implInbox);
    expect(Array.isArray(inbox.messages)).toBe(true);
    const hasTask = inbox.messages.some((msg: any) =>
      msg.body?.content?.includes('WebSocket relay endpoint')
    );
    expect(hasTask).toBe(true);
  });

  test('SC-59: Human supervises agents via browser', async ({ page }) => {
    // Send a message from coordinator for the browser to display
    thrumIn(getTestRoot(), ['send', 'Task assigned for browser test'], 10_000, coordEnv());

    // Act: open browser
    await page.goto('/');
    await waitForWebSocket(page);

    // Wait for header to load with identity (not "Unknown")
    const header = page.locator('header');
    await expect(header).not.toContainText('Unknown', { timeout: 5000 });

    // Assert: human identity is shown in header
    const headerText = await header.textContent();
    expect(headerText).not.toContain('Unknown');

    // Assert: page has content
    const pageContent = await page.textContent('body');
    expect(pageContent?.trim().length).toBeGreaterThan(0);
  });

  test.skip('SC-60: Agent impersonation (FEATURE MISSING)', async () => {
    // Missing feature: --as or --role flag on thrum reply
  });

  test.skip('SC-61: Broadcast (FEATURE MISSING)', async () => {
    // Missing feature: --broadcast flag (see thrum-b0d4)
  });

  test.skip('SC-62: File conflict detection (FEATURE MISSING)', async () => {
    // Requires multiple agents modifying the same file and `thrum who-has <file>`
  });

  test('SC-63: Session handoff between agents', async () => {
    // Mark implementer inbox read
    thrumIn(getImplementerRoot(), ['message', 'read', '--all'], 10_000, implEnv());

    // Act: coordinator sends handoff message to implementer
    const sendResult = thrumIn(getTestRoot(), ['send', 'Completed relay endpoint, passing to reviewer', '--to', '@e2e_implementer', '--json'], 10_000, coordEnv());
    const parsed = JSON.parse(sendResult);
    expect(parsed.message_id).toMatch(/^msg_/);

    // Assert: implementer receives handoff in their inbox (cross-worktree delivery)
    const implInbox = thrumIn(getImplementerRoot(), ['inbox', '--unread', '--json'], 10_000, implEnv());
    const inbox = JSON.parse(implInbox);
    const hasHandoff = inbox.messages.some((msg: any) =>
      msg.body?.content?.includes('passing to reviewer')
    );
    expect(hasHandoff).toBe(true);
  });

  test('SC-64: Multiple agents in parallel with sync', async () => {
    // Mark implementer inbox read
    thrumIn(getImplementerRoot(), ['message', 'read', '--all'], 10_000, implEnv());

    // Act: coordinator sends messages to implementer
    thrumIn(getTestRoot(), ['send', 'Sync test message 1', '--to', '@e2e_implementer'], 10_000, coordEnv());
    thrumIn(getTestRoot(), ['send', 'Sync test message 2', '--to', '@e2e_implementer'], 10_000, coordEnv());

    // Assert: implementer receives both messages in their inbox (cross-worktree delivery)
    const implInbox = thrumIn(getImplementerRoot(), ['inbox', '--unread', '--json'], 10_000, implEnv());
    const inbox = JSON.parse(implInbox);
    expect(inbox.messages.length).toBeGreaterThanOrEqual(2);

    const hasMsg1 = inbox.messages.some((msg: any) =>
      msg.body?.content?.includes('Sync test message 1')
    );
    const hasMsg2 = inbox.messages.some((msg: any) =>
      msg.body?.content?.includes('Sync test message 2')
    );
    expect(hasMsg1).toBe(true);
    expect(hasMsg2).toBe(true);
  });
});
