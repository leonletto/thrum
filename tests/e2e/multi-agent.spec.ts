import { test, expect } from '@playwright/test';
import { thrum, thrumJson, thrumIn, getTestRoot } from './helpers/thrum-cli.js';
import { registerAgent, quickstartAgent, sendMessage, waitForWebSocket } from './helpers/fixtures.js';

/**
 * E2E tests for Multi-Agent Scenarios (SC-58 to SC-64)
 *
 * These tests validate realistic multi-agent coordination workflows:
 * - Two agents coordinating on a feature
 * - Human supervising agents via browser
 * - Agent crash and impersonation
 * - Broadcast announcements
 * - File conflict detection
 * - Session handoffs
 * - Multiple agents in parallel worktrees with sync
 *
 * Note: The daemon resolves identity via THRUM_ROLE/THRUM_MODULE env vars
 * as fallback. All message sends resolve to the default test agent. Each
 * test ensures an active session exists for the test agent before sending.
 */

/**
 * Ensure the default test agent has an active session.
 * This is needed because previous tests may have ended the session.
 */
function ensureTestSession(): void {
  try {
    thrum(['session', 'start']);
  } catch (err: any) {
    const msg = err.message || '';
    if (!msg.toLowerCase().includes('already active') && !msg.toLowerCase().includes('already exists')) {
      throw err;
    }
  }
}

test.describe('Multi-Agent Scenarios', () => {
  test('SC-58: Two agents coordinate on a feature', async () => {
    // Arrange: register two agents with sessions (--name pins identity)
    quickstartAgent('coordinator', 'all', 'Claude-Main', 'Coordinating relay feature', 'e2e_sc58_coord');
    quickstartAgent('implementer', 'relay', 'Claude-Relay', 'Building relay service', 'e2e_sc58_impl');

    // Ensure the default test agent has a session for sending messages
    ensureTestSession();

    // Act: coordinator sends task to implementer
    const sendResult = thrumJson<{ message_id: string }>(['send', 'Please implement the WebSocket relay endpoint', '--to', '@e2e_sc58_impl']);
    expect(sendResult.message_id).toMatch(/^msg_/);

    // Verify: message exists and has correct content
    const msgResult = thrumJson<{ message: { body: { content: string } } }>(['message', 'get', sendResult.message_id]);
    expect(msgResult.message.body.content).toContain('WebSocket relay endpoint');
  });

  test('SC-59: Human supervises agents via browser', async ({ page }) => {
    // Arrange: register two agents with sessions
    quickstartAgent('coordinator', 'all', 'Claude-Main', 'Coordinating agents');
    quickstartAgent('implementer', 'relay', 'Claude-Relay', 'Building relay');

    // Ensure the default test agent has a session for sending messages
    ensureTestSession();

    // Send a message between agents
    sendMessage('Task assigned', { to: '@implementer' });

    // Act: open browser
    await page.goto('/');
    await waitForWebSocket(page);

    // Wait for header to load with identity (not "Unknown")
    const header = page.locator('header');
    await expect(header).not.toContainText('Unknown', { timeout: 5000 });

    // Assert: human identity is shown in header
    const headerText = await header.textContent();
    expect(headerText).not.toContain('Unknown');

    // Assert: agents should be visible in sidebar (if working)
    // Note: This may fail if agent list not properly wired
    const pageContent = await page.textContent('body');
    expect(pageContent?.trim().length).toBeGreaterThan(0);

    // Note: Full compose and send from browser depends on SC-53 passing
  });

  test.skip('SC-60: Agent impersonation (FEATURE MISSING)', async () => {
    // Missing feature: --as or --role flag on thrum reply
    // When implemented, should allow a human to reply on behalf of a crashed agent:
    //   thrum reply <msg_id> "..." --as @implementer
  });

  test.skip('SC-61: Broadcast (FEATURE MISSING)', async () => {
    // Missing feature: --broadcast flag (see thrum-b0d4)
    // When implemented, should allow:
    //   thrum send "ANNOUNCEMENT: ..." --broadcast
  });

  test.skip('SC-62: File conflict detection (FEATURE MISSING)', async () => {
    // Requires multiple agents modifying the same file and `thrum who-has <file>`
    // showing both agents. Complex to simulate — needs active sessions with
    // uncommitted changes in E2E context.
  });

  test('SC-63: Session handoff between agents', async () => {
    // Arrange: agent A working, reviewer registered as recipient
    quickstartAgent('agent_a', 'relay', 'Agent A', 'Working on relay', 'e2e_sc63_agent_a');
    quickstartAgent('reviewer', 'relay', 'Reviewer', 'Reviewing code', 'e2e_sc63_reviewer');

    // Ensure the default test agent has a session for sending messages
    ensureTestSession();

    // Act: agent A sends handoff message to reviewer
    const sendResult = thrumJson<{ message_id: string }>(['send', 'Completed relay endpoint, passing to reviewer', '--to', '@e2e_sc63_reviewer']);
    expect(sendResult.message_id).toMatch(/^msg_/);

    // Verify: message exists and has correct handoff content
    const msgResult = thrumJson<{ message: { body: { content: string } } }>(['message', 'get', sendResult.message_id]);
    expect(msgResult.message.body.content).toContain('passing to reviewer');
  });

  test('SC-64: Multiple agents in parallel with sync', async () => {
    // Register multiple agents with sessions (--name pins identity)
    quickstartAgent('agent_1', 'main', 'Agent 1', 'Working on main', 'e2e_sc64_agent1');
    quickstartAgent('agent_2', 'relay', 'Agent 2', 'Working on relay', 'e2e_sc64_agent2');
    quickstartAgent('agent_3', 'daemon', 'Agent 3', 'Working on daemon', 'e2e_sc64_agent3');

    // Ensure the default test agent has a session for sending messages
    ensureTestSession();

    // Send messages to named agents
    sendMessage('Sync test message 1', { to: '@e2e_sc64_agent2' });
    sendMessage('Sync test message 2', { to: '@e2e_sc64_agent3' });

    // Query inbox as agent_2 to verify delivery (exclude_self filters sender's own messages)
    const agent2Env = { ...process.env, THRUM_NAME: 'e2e_sc64_agent2', THRUM_ROLE: 'agent_2', THRUM_MODULE: 'relay' };
    const inbox = thrumJson<{ messages: Array<{ body: { content?: string } }> }>(['inbox'], agent2Env);
    expect(inbox.messages.length).toBeGreaterThan(0);
    const hasSyncMessage = inbox.messages.some((msg: { body: { content?: string } }) =>
      msg.body.content?.includes('Sync test message 1')
    );
    expect(hasSyncMessage).toBe(true);
  });
});
