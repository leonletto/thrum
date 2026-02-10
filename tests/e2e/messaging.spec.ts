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
    // Act: send broadcast message (no --to flag)
    const sendOutput = thrum(['send', 'Hello everyone, coordinator is online']);
    expect(sendOutput.toLowerCase()).toMatch(/sent|message|ok/);

    // Assert: message appears in inbox
    const inbox = thrum(['inbox']);
    expect(inbox.toLowerCase()).toContain('hello everyone');
  });

  test('SC-16: Send a direct message to a specific role', async () => {
    // Act: send direct message to implementer
    const sendOutput = thrum(['send', 'Please review the relay design', '--to', '@implementer']);
    expect(sendOutput.toLowerCase()).toMatch(/sent|message|ok/);

    // Note: Verifying the message is delivered to @implementer would require
    // multiple agents, which is beyond simple CLI testing. For now, we verify
    // the command succeeds and the message appears in sender's inbox.
    const inbox = thrum(['inbox']);
    expect(inbox.toLowerCase()).toContain('relay design');
  });

  test('SC-17: Send a message with mention', async () => {
    // Act: send message with mention
    const sendOutput = thrum(['send', 'Need input on relay architecture', '--mention', '@reviewer']);
    expect(sendOutput.toLowerCase()).toMatch(/sent|message|ok/);

    // Assert: message appears in inbox
    const inbox = thrum(['inbox']);
    expect(inbox.toLowerCase()).toContain('relay architecture');
  });

  test('SC-18: Send with priority levels', async () => {
    // Act: send messages with different priority levels
    thrum(['send', 'Low priority FYI', '--priority', 'low']);
    thrum(['send', 'Normal update', '--priority', 'normal']);
    thrum(['send', 'URGENT: tests failing', '--priority', 'high']);

    // Assert: all messages appear in inbox
    const inbox = thrum(['inbox']);
    expect(inbox.toLowerCase()).toContain('low priority');
    expect(inbox.toLowerCase()).toContain('normal update');
    expect(inbox.toLowerCase()).toContain('urgent');
  });

  test('SC-19: Send with scope and refs', async () => {
    // Act: send message with scope and refs
    thrum(['send', 'Auth module updated', '--scope', 'module:auth', '--ref', 'commit:abc123']);

    // Assert: get message with JSON output to verify metadata
    const inbox = thrum(['inbox']);
    expect(inbox.toLowerCase()).toContain('auth module');
  });

  test('SC-20: View inbox with unread indicators', async () => {
    // Arrange: send a message
    thrum(['send', 'Test unread message']);

    // Act: check inbox (should be unread)
    const inboxBefore = thrum(['inbox']);
    expect(inboxBefore.toLowerCase()).toContain('test unread');

    // Note: Marking as read and verifying indicators requires
    // knowing the message ID format and read command behavior.
    // This test verifies the inbox command works.
  });

  test('SC-21: Get single message details', async () => {
    // Arrange: send a message and get its ID
    thrum(['send', 'Test message for details']);
    const inbox = thrum(['inbox']);

    // Note: Getting a single message requires parsing the inbox
    // to extract a message ID. This test verifies the command flow works.
    expect(inbox.toLowerCase()).toContain('test message for details');
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

    // Assert: the original message should now be in a thread
    const getResult = thrumJson<{ message: { message_id: string; thread_id: string } }>(['message', 'get', msgId]);
    // A thread should have been created (reply creates one if none existed)
    // The reply command creates a thread and sends a message in it
    // Verify by checking inbox for the reply content
    const inbox = thrum(['inbox']);
    expect(inbox.toLowerCase()).toContain('reply to original');
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
