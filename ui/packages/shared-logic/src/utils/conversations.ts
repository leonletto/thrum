import type { Message } from '../types/api';

export interface Conversation {
  rootMessage: Message;
  replies: Message[];
  lastActivity: string;
}

/**
 * Groups a flat array of messages into conversations.
 *
 * Phase 1: Messages that share a thread_id are grouped together.
 *   - The root is the message in the group with no reply_to (or the earliest if all have reply_to).
 *   - This is reliable O(1) field comparison — no chain walking needed.
 *
 * Phase 2: Messages without thread_id fall back to reply_to chain-walking.
 *   - This handles old messages that pre-date auto-threading.
 *
 * Conversations are sorted by most recent activity (descending).
 * Replies within a conversation are sorted chronologically (ascending).
 */
export function groupByConversation(messages: Message[]): Conversation[] {
  // Build lookup map
  const messageMap = new Map<string, Message>();
  for (const msg of messages) {
    messageMap.set(msg.message_id, msg);
  }

  // Phase 1: Group by thread_id where available
  const threadGroups = new Map<string, Message[]>();
  const ungrouped: Message[] = [];

  for (const msg of messages) {
    if (msg.thread_id) {
      const group = threadGroups.get(msg.thread_id) ?? [];
      group.push(msg);
      threadGroups.set(msg.thread_id, group);
    } else {
      ungrouped.push(msg);
    }
  }

  const conversations: Conversation[] = [];

  // Build conversations from thread groups
  for (const [, msgs] of threadGroups) {
    if (msgs.length === 0) continue;
    // Find the root: prefer message with no reply_to, else earliest by created_at
    const sorted = [...msgs].sort((a, b) => a.created_at.localeCompare(b.created_at));
    const rootMessage = sorted.find(m => !m.reply_to) ?? sorted[0]!;
    const replies = sorted
      .filter(m => m.message_id !== rootMessage.message_id)
      .sort((a, b) => a.created_at.localeCompare(b.created_at));

    const allMsgs: Message[] = [rootMessage, ...replies];
    const lastActivity = allMsgs.reduce(
      (latest: string, m: Message) => (m.created_at > latest ? m.created_at : latest),
      rootMessage.created_at
    );

    conversations.push({ rootMessage, replies, lastActivity });
  }

  // Phase 2: For ungrouped messages, use reply_to chain-walking (backward compat)
  if (ungrouped.length > 0) {
    // Build lookup for ungrouped messages only
    const ungroupedMap = new Map<string, Message>();
    for (const msg of ungrouped) {
      ungroupedMap.set(msg.message_id, msg);
    }

    function findRoot(msg: Message): string {
      let current = msg;
      const visited = new Set<string>();
      while (
        current.reply_to &&
        ungroupedMap.has(current.reply_to) &&
        !visited.has(current.reply_to)
      ) {
        visited.add(current.message_id);
        current = ungroupedMap.get(current.reply_to)!;
      }
      return current.message_id;
    }

    const groups = new Map<string, Message[]>();
    for (const msg of ungrouped) {
      const rootId = findRoot(msg);
      if (!groups.has(rootId)) {
        groups.set(rootId, []);
      }
      groups.get(rootId)!.push(msg);
    }

    for (const [rootId, msgs] of groups) {
      const rootMessage = ungroupedMap.get(rootId)!;
      const replies = msgs
        .filter(m => m.message_id !== rootId)
        .sort((a, b) => a.created_at.localeCompare(b.created_at));

      const allMsgs = [rootMessage, ...replies];
      const lastActivity = allMsgs.reduce(
        (latest, m) => (m.created_at > latest ? m.created_at : latest),
        rootMessage.created_at
      );

      conversations.push({ rootMessage, replies, lastActivity });
    }
  }

  // Sort conversations by most recent activity (descending)
  conversations.sort((a, b) => b.lastActivity.localeCompare(a.lastActivity));

  return conversations;
}
