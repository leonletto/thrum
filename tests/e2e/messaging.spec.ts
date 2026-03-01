import { test, expect } from '@playwright/test';
import { thrum, thrumJson } from './helpers/thrum-cli.js';

test.describe.serial('Messaging Tests', () => {
  test.beforeEach(async () => {
    // Ensure we have an active session for messaging
    try {
      thrum(['session', 'start']);
    } catch (err: any) {
      const msg = err.stderr?.toString() || err.message || '';
      if (!msg.toLowerCase().includes('already active') && !msg.toLowerCase().includes('already exists')) {
        throw err;
      }
    }
  });

  test.afterAll(async () => {
    try { thrum(['session', 'end']); } catch { /* session may already be ended */ }
  });

  test('SC-15: Send a broadcast message', async () => {
    // Act: send broadcast message (no --to flag) and verify via JSON
    const sendResult = thrumJson<{ message_id: string }>(['send', 'Hello everyone, coordinator is online']);

    // Assert: send succeeded and returned a message ID
    expect(sendResult.message_id).toBeTruthy();
    expect(sendResult.message_id).toMatch(/^msg_/);

    // Note: inbox excludes own messages (exclude_self=true), so we cannot
    // verify broadcast delivery by checking the sender's inbox.
  });

  test('SC-16: Send a direct message to a specific role', async () => {
    // Act: send direct message to implementer and verify via JSON
    const sendResult = thrumJson<{ message_id: string }>(['send', 'Please review the relay design', '--to', '@implementer']);
    expect(sendResult.message_id).toBeTruthy();
    expect(sendResult.message_id).toMatch(/^msg_/);

    // Verify the message exists and has correct content via message get
    const msgResult = thrumJson<{ message: { body: { content: string } } }>(['message', 'get', sendResult.message_id]);
    expect(msgResult.message.body.content).toContain('relay design');
  });

  test('SC-17: Send a message with mention', async () => {
    // Act: send message mentioning the implementer agent (which exists from global-setup)
    const sendResult = thrumJson<{ message_id: string }>(['send', 'Need input on relay architecture', '--mention', '@implementer']);
    expect(sendResult.message_id).toMatch(/^msg_/);

    // Verify message content via message get
    const msgResult = thrumJson<{ message: { body: { content: string } } }>(['message', 'get', sendResult.message_id]);
    expect(msgResult.message.body.content).toContain('relay architecture');
  });

  test.skip('SC-18: Send with priority levels (REMOVED FEATURE)', async () => {
    // --priority flag was removed (see J1 regression test)
    // Act: send messages with different priority levels and verify via JSON
    const low = thrumJson<{ message_id: string }>(['send', 'Low priority FYI', '--priority', 'low']);
    const normal = thrumJson<{ message_id: string }>(['send', 'Normal update', '--priority', 'normal']);
    const high = thrumJson<{ message_id: string }>(['send', 'URGENT: tests failing', '--priority', 'high']);

    // Assert: all messages created successfully
    expect(low.message_id).toMatch(/^msg_/);
    expect(normal.message_id).toMatch(/^msg_/);
    expect(high.message_id).toMatch(/^msg_/);
  });

  test('SC-19: Send with scope and refs', async () => {
    // Act: send message with scope and refs
    const sendResult = thrumJson<{ message_id: string }>(['send', 'Auth module updated', '--scope', 'module:auth', '--ref', 'commit:abc123']);
    expect(sendResult.message_id).toMatch(/^msg_/);

    // Verify message content via message get
    const msgResult = thrumJson<{ message: { body: { content: string } } }>(['message', 'get', sendResult.message_id]);
    expect(msgResult.message.body.content).toContain('Auth module');
  });

  test('SC-20: View inbox with unread indicators', async () => {
    // Arrange: send a message
    const sendResult = thrumJson<{ message_id: string }>(['send', 'Test unread message']);
    expect(sendResult.message_id).toMatch(/^msg_/);

    // Act: verify inbox command runs without error
    const inboxOutput = thrum(['inbox']);
    // Inbox may show "no messages" due to exclude_self, but should not error
    expect(inboxOutput.toLowerCase()).not.toContain('error');

    // Verify the message exists via message get
    const msgResult = thrumJson<{ message: { body: { content: string } } }>(['message', 'get', sendResult.message_id]);
    expect(msgResult.message.body.content).toContain('Test unread message');
  });

  test('SC-21: Get single message details', async () => {
    // Arrange: send a message and get its ID
    const sendResult = thrumJson<{ message_id: string }>(['send', 'Test message for details']);
    expect(sendResult.message_id).toMatch(/^msg_/);

    // Act: get message details via message get
    const msgResult = thrumJson<{ message: { message_id: string; body: { content: string } } }>(['message', 'get', sendResult.message_id]);
    expect(msgResult.message.body.content).toContain('Test message for details');
    expect(msgResult.message.message_id).toBe(sendResult.message_id);
  });

  test('SC-22: Edit a message', async () => {
    // Arrange: send a message and capture the message ID from JSON output
    const originalText = `Edit test original ${Date.now()}`;
    const sendResult = thrumJson<{ message_id: string }>(['send', originalText]);
    expect(sendResult.message_id).toBeTruthy();
    const msgId = sendResult.message_id;

    // Act: edit the message with new content
    const updatedText = `Edit test UPDATED ${Date.now()}`;
    const editOutput = thrum(['message', 'edit', msgId, updatedText]);
    expect(editOutput.toLowerCase()).toMatch(/edited|version/);

    // Assert: retrieve the message and verify updated content
    const getResult = thrumJson<{ message: { message_id: string; body: { content: string }; updated_at: string } }>(['message', 'get', msgId]);
    expect(getResult.message.body.content).toBe(updatedText);
    expect(getResult.message.updated_at).toBeTruthy();
  });

  test('SC-23: Delete a message', async () => {
    // Arrange: send a message and capture the message ID
    const deleteText = `Delete test ${Date.now()}`;
    const sendResult = thrumJson<{ message_id: string }>(['send', deleteText]);
    expect(sendResult.message_id).toBeTruthy();
    const msgId = sendResult.message_id;

    // Act: delete the message (requires --force flag)
    const deleteOutput = thrum(['message', 'delete', msgId, '--force']);
    expect(deleteOutput.toLowerCase()).toMatch(/deleted/);

    // Assert: retrieve the message and verify it is marked as deleted
    const getResult = thrumJson<{ message: { message_id: string; deleted: boolean } }>(['message', 'get', msgId]);
    expect(getResult.message.deleted).toBe(true);
  });

  test('SC-24: Reply to a message (auto-thread creation)', async () => {
    // Arrange: send a message and capture the message ID
    const originalText = `Reply test original ${Date.now()}`;
    const sendResult = thrumJson<{ message_id: string }>(['send', originalText]);
    expect(sendResult.message_id).toBeTruthy();
    const msgId = sendResult.message_id;

    // Act: reply to the message (auto-creates a thread if none exists)
    const replyText = `Reply to original ${Date.now()}`;
    const replyOutput = thrum(['reply', msgId, replyText]);
    expect(replyOutput.toLowerCase()).toMatch(/reply sent|thread/);

    // Assert: the original message still exists and reply was accepted
    const getResult = thrumJson<{ message: { message_id: string } }>(['message', 'get', msgId]);
    expect(getResult.message.message_id).toBe(msgId);
  });

  test.skip('SC-25: CLI broadcast command (MISSING FEATURE)', async () => {
    // TODO: Issue thrum-b0d4 - explicit --broadcast flag not implemented
    // Act: send with explicit broadcast flag
    thrum(['send', 'Important announcement to everyone', '--broadcast']);

    // Assert: message sent to all agents
    const inbox = thrum(['inbox']);
    expect(inbox.toLowerCase()).toContain('important announcement');
  });
});
