import { describe, it, expect } from 'vitest';
import { groupByConversation } from '../conversations';
import type { Message } from '../../types/api';

function makeMessage(overrides: Partial<Message> & { message_id: string; created_at: string }): Message {
  return {
    body: { format: 'text', content: 'test' },
    ...overrides,
  };
}

describe('groupByConversation', () => {
  it('returns empty array for empty input', () => {
    expect(groupByConversation([])).toEqual([]);
  });

  it('single message with no reply_to becomes one conversation with no replies', () => {
    const msg = makeMessage({ message_id: 'a', created_at: '2024-01-01T10:00:00Z' });
    const result = groupByConversation([msg]);

    expect(result).toHaveLength(1);
    expect(result[0].rootMessage).toEqual(msg);
    expect(result[0].replies).toEqual([]);
    expect(result[0].lastActivity).toBe('2024-01-01T10:00:00Z');
  });

  it('two messages where one replies to the other form a single conversation', () => {
    const root = makeMessage({ message_id: 'a', created_at: '2024-01-01T10:00:00Z' });
    const reply = makeMessage({ message_id: 'b', created_at: '2024-01-01T11:00:00Z', reply_to: 'a' });

    const result = groupByConversation([root, reply]);

    expect(result).toHaveLength(1);
    expect(result[0].rootMessage).toEqual(root);
    expect(result[0].replies).toHaveLength(1);
    expect(result[0].replies[0]).toEqual(reply);
    expect(result[0].lastActivity).toBe('2024-01-01T11:00:00Z');
  });

  it('messages with different roots create separate conversations', () => {
    const root1 = makeMessage({ message_id: 'a', created_at: '2024-01-01T10:00:00Z' });
    const root2 = makeMessage({ message_id: 'b', created_at: '2024-01-01T09:00:00Z' });
    const replyToA = makeMessage({ message_id: 'c', created_at: '2024-01-01T10:30:00Z', reply_to: 'a' });

    const result = groupByConversation([root1, root2, replyToA]);

    expect(result).toHaveLength(2);
    // Sorted by most recent activity descending; conversation A has lastActivity 10:30, B has 09:00
    expect(result[0].rootMessage.message_id).toBe('a');
    expect(result[1].rootMessage.message_id).toBe('b');
  });

  it('deep reply chain (A -> B -> C) groups all under A', () => {
    const a = makeMessage({ message_id: 'a', created_at: '2024-01-01T10:00:00Z' });
    const b = makeMessage({ message_id: 'b', created_at: '2024-01-01T10:10:00Z', reply_to: 'a' });
    const c = makeMessage({ message_id: 'c', created_at: '2024-01-01T10:20:00Z', reply_to: 'b' });

    const result = groupByConversation([a, b, c]);

    expect(result).toHaveLength(1);
    expect(result[0].rootMessage.message_id).toBe('a');
    expect(result[0].replies).toHaveLength(2);
    const replyIds = result[0].replies.map(r => r.message_id);
    expect(replyIds).toContain('b');
    expect(replyIds).toContain('c');
  });

  it('reply to a missing message makes the replying message its own root', () => {
    const orphan = makeMessage({ message_id: 'b', created_at: '2024-01-01T10:00:00Z', reply_to: 'nonexistent' });

    const result = groupByConversation([orphan]);

    expect(result).toHaveLength(1);
    expect(result[0].rootMessage.message_id).toBe('b');
    expect(result[0].replies).toEqual([]);
  });

  it('conversations are sorted by most recent activity descending', () => {
    const oldRoot = makeMessage({ message_id: 'old', created_at: '2024-01-01T08:00:00Z' });
    const recentRoot = makeMessage({ message_id: 'recent', created_at: '2024-01-01T12:00:00Z' });
    const middleRoot = makeMessage({ message_id: 'mid', created_at: '2024-01-01T10:00:00Z' });

    const result = groupByConversation([oldRoot, recentRoot, middleRoot]);

    expect(result).toHaveLength(3);
    expect(result[0].rootMessage.message_id).toBe('recent');
    expect(result[1].rootMessage.message_id).toBe('mid');
    expect(result[2].rootMessage.message_id).toBe('old');
  });

  it('replies within a conversation are sorted chronologically ascending', () => {
    const root = makeMessage({ message_id: 'root', created_at: '2024-01-01T10:00:00Z' });
    const reply1 = makeMessage({ message_id: 'r1', created_at: '2024-01-01T12:00:00Z', reply_to: 'root' });
    const reply2 = makeMessage({ message_id: 'r2', created_at: '2024-01-01T11:00:00Z', reply_to: 'root' });
    const reply3 = makeMessage({ message_id: 'r3', created_at: '2024-01-01T13:00:00Z', reply_to: 'root' });

    const result = groupByConversation([root, reply1, reply2, reply3]);

    expect(result).toHaveLength(1);
    const replyIds = result[0].replies.map(r => r.message_id);
    expect(replyIds).toEqual(['r2', 'r1', 'r3']);
  });
});
