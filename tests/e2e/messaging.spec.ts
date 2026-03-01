/**
 * Messaging Tests — E1 to E9 + extras
 *
 * Tests verify actual message delivery between agents across worktrees.
 * Send from coordinator (getTestRoot), verify delivery in implementer's
 * inbox (getImplementerRoot). This confirms the full messaging pipeline.
 */
import { test, expect } from '@playwright/test';
import { thrum, thrumIn, thrumJson, getTestRoot, getImplementerRoot } from './helpers/thrum-cli.js';

/** Coordinator agent identity (sender). */
function coordEnv(): NodeJS.ProcessEnv {
  return { ...process.env, THRUM_NAME: 'e2e_coordinator', THRUM_ROLE: 'coordinator', THRUM_MODULE: 'all' };
}

/** Implementer agent identity (recipient). */
function implEnv(): NodeJS.ProcessEnv {
  return { ...process.env, THRUM_NAME: 'e2e_implementer', THRUM_ROLE: 'implementer', THRUM_MODULE: 'main' };
}

test.describe.serial('Messaging Tests', () => {
  test.beforeAll(async () => {
    // Ensure both agents have active sessions
    try {
      thrumIn(getTestRoot(), ['quickstart', '--role', 'coordinator', '--module', 'all',
        '--name', 'e2e_coordinator', '--intent', 'Messaging tests'], 10_000, coordEnv());
    } catch { /* may already exist */ }
    try {
      thrumIn(getImplementerRoot(), ['quickstart', '--role', 'implementer', '--module', 'main',
        '--name', 'e2e_implementer', '--intent', 'Messaging tests'], 10_000, implEnv());
    } catch { /* may already exist */ }

    // Mark all read in implementer inbox to start clean
    try {
      thrumIn(getImplementerRoot(), ['message', 'read', '--all'], 10_000, implEnv());
    } catch { /* best effort */ }
  });

  test('E1: Send a broadcast and verify delivery to another agent', async () => {
    // Act: coordinator sends broadcast
    const sendResult = thrumIn(getTestRoot(), ['send', 'Hello everyone, coordinator is online', '--json'], 10_000, coordEnv());
    const parsed = JSON.parse(sendResult);
    expect(parsed.message_id).toMatch(/^msg_/);

    // Assert: implementer receives broadcast in their inbox
    const implInbox = thrumIn(getImplementerRoot(), ['inbox', '--unread', '--json'], 10_000, implEnv());
    const inbox = JSON.parse(implInbox);
    expect(Array.isArray(inbox.messages)).toBe(true);
    const hasBroadcast = inbox.messages.some((msg: any) =>
      msg.body?.content?.includes('coordinator is online')
    );
    expect(hasBroadcast).toBe(true);
  });

  test('E2: Send a direct message and verify delivery', async () => {
    // Mark implementer inbox read
    thrumIn(getImplementerRoot(), ['message', 'read', '--all'], 10_000, implEnv());

    // Act: coordinator sends DM to implementer
    const sendResult = thrumIn(getTestRoot(), ['send', 'Please review the relay design', '--to', '@e2e_implementer', '--json'], 10_000, coordEnv());
    const parsed = JSON.parse(sendResult);
    expect(parsed.message_id).toMatch(/^msg_/);

    // Assert: implementer receives the DM
    const implInbox = thrumIn(getImplementerRoot(), ['inbox', '--unread', '--json'], 10_000, implEnv());
    const inbox = JSON.parse(implInbox);
    const hasDM = inbox.messages.some((msg: any) =>
      msg.body?.content?.includes('relay design')
    );
    expect(hasDM).toBe(true);
  });

  test('E3: Send a message with mention and verify delivery', async () => {
    thrumIn(getImplementerRoot(), ['message', 'read', '--all'], 10_000, implEnv());

    // Act: coordinator sends message mentioning implementer
    const sendResult = thrumIn(getTestRoot(), ['send', 'Need input on relay architecture', '--mention', '@e2e_implementer', '--json'], 10_000, coordEnv());
    const parsed = JSON.parse(sendResult);
    expect(parsed.message_id).toMatch(/^msg_/);

    // Assert: implementer receives the mention
    const implInbox = thrumIn(getImplementerRoot(), ['inbox', '--unread', '--json'], 10_000, implEnv());
    const inbox = JSON.parse(implInbox);
    const hasMention = inbox.messages.some((msg: any) =>
      msg.body?.content?.includes('relay architecture')
    );
    expect(hasMention).toBe(true);
  });

  test('E4: Send with scope and refs', async () => {
    // Act: send message with scope and refs
    const sendResult = thrumIn(getTestRoot(), ['send', 'Auth module updated', '--scope', 'module:auth', '--ref', 'commit:abc123', '--json'], 10_000, coordEnv());
    const parsed = JSON.parse(sendResult);
    expect(parsed.message_id).toMatch(/^msg_/);

    // Verify message content and metadata via message get
    const msgResult = thrumIn(getTestRoot(), ['message', 'get', parsed.message_id, '--json'], 10_000, coordEnv());
    const msg = JSON.parse(msgResult);
    expect(msg.message.body.content).toContain('Auth module');
  });

  test('E5: Get single message details', async () => {
    // Arrange: send a message
    const sendResult = thrumIn(getTestRoot(), ['send', 'Test message for details', '--json'], 10_000, coordEnv());
    const parsed = JSON.parse(sendResult);
    expect(parsed.message_id).toMatch(/^msg_/);

    // Act: get message details
    const msgResult = thrumIn(getTestRoot(), ['message', 'get', parsed.message_id, '--json'], 10_000, coordEnv());
    const msg = JSON.parse(msgResult);
    expect(msg.message.body.content).toContain('Test message for details');
    expect(msg.message.message_id).toBe(parsed.message_id);
  });

  test('E6: Unknown recipient fails with hard error', async () => {
    // Act: send to non-existent agent
    let error = '';
    try {
      thrumIn(getTestRoot(), ['send', 'Should fail', '--to', '@does-not-exist'], 10_000, coordEnv());
    } catch (err: any) {
      error = err.message || '';
    }

    // Assert: clear error about unknown recipient
    expect(error.toLowerCase()).toContain('unknown');
  });

  test('E7: Edit a message', async () => {
    // Arrange: send a message
    const originalText = `Edit test original ${Date.now()}`;
    const sendResult = thrumIn(getTestRoot(), ['send', originalText, '--json'], 10_000, coordEnv());
    const parsed = JSON.parse(sendResult);
    const msgId = parsed.message_id;

    // Act: edit the message
    const updatedText = `Edit test UPDATED ${Date.now()}`;
    const editOutput = thrumIn(getTestRoot(), ['message', 'edit', msgId, updatedText], 10_000, coordEnv());
    expect(editOutput.toLowerCase()).toMatch(/edited|version/);

    // Assert: updated content
    const getResult = thrumIn(getTestRoot(), ['message', 'get', msgId, '--json'], 10_000, coordEnv());
    const msg = JSON.parse(getResult);
    expect(msg.message.body.content).toBe(updatedText);
    expect(msg.message.updated_at).toBeTruthy();
  });

  test('E8: Delete a message', async () => {
    // Arrange: send a message
    const deleteText = `Delete test ${Date.now()}`;
    const sendResult = thrumIn(getTestRoot(), ['send', deleteText, '--json'], 10_000, coordEnv());
    const parsed = JSON.parse(sendResult);
    const msgId = parsed.message_id;

    // Act: delete
    const deleteOutput = thrumIn(getTestRoot(), ['message', 'delete', msgId, '--force'], 10_000, coordEnv());
    expect(deleteOutput.toLowerCase()).toMatch(/deleted/);

    // Assert: marked deleted
    const getResult = thrumIn(getTestRoot(), ['message', 'get', msgId, '--json'], 10_000, coordEnv());
    const msg = JSON.parse(getResult);
    expect(msg.message.deleted).toBe(true);
  });

  test('E9: Reply to a message (auto-thread creation)', async () => {
    // Arrange: send a message
    const originalText = `Reply test original ${Date.now()}`;
    const sendResult = thrumIn(getTestRoot(), ['send', originalText, '--json'], 10_000, coordEnv());
    const parsed = JSON.parse(sendResult);
    const msgId = parsed.message_id;

    // Act: reply
    const replyText = `Reply to original ${Date.now()}`;
    const replyOutput = thrumIn(getTestRoot(), ['reply', msgId, replyText], 10_000, coordEnv());
    expect(replyOutput.toLowerCase()).toMatch(/reply sent|thread/);

    // Assert: original message still exists
    const getResult = thrumIn(getTestRoot(), ['message', 'get', msgId, '--json'], 10_000, coordEnv());
    const msg = JSON.parse(getResult);
    expect(msg.message.message_id).toBe(msgId);
  });

  test('E-extra: Inbox unread indicators from recipient perspective', async () => {
    // Mark all read first
    thrumIn(getImplementerRoot(), ['message', 'read', '--all'], 10_000, implEnv());

    // Send a message from coordinator to implementer
    thrumIn(getTestRoot(), ['send', `Unread test ${Date.now()}`, '--to', '@e2e_implementer'], 10_000, coordEnv());

    // Assert: implementer has unread messages
    const implInbox = thrumIn(getImplementerRoot(), ['inbox', '--unread', '--json'], 10_000, implEnv());
    const inbox = JSON.parse(implInbox);
    expect(inbox.messages.length).toBeGreaterThan(0);
  });

  test('SC-25: CLI broadcast via --broadcast flag', async () => {
    // Mark implementer inbox read
    thrumIn(getImplementerRoot(), ['message', 'read', '--all'], 10_000, implEnv());

    // Act: coordinator sends broadcast using --broadcast flag
    const sendResult = thrumIn(getTestRoot(), ['send', `Broadcast flag test ${Date.now()}`, '--broadcast', '--json'], 10_000, coordEnv());
    const parsed = JSON.parse(sendResult);
    expect(parsed.message_id).toMatch(/^msg_/);

    // Assert: implementer receives the broadcast
    const implInbox = thrumIn(getImplementerRoot(), ['inbox', '--unread', '--json'], 10_000, implEnv());
    const inbox = JSON.parse(implInbox);
    const hasBroadcast = inbox.messages.some((msg: any) =>
      msg.body?.content?.includes('Broadcast flag test')
    );
    expect(hasBroadcast).toBe(true);
  });
});
