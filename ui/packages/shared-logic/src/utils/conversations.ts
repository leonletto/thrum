import type { Message } from '../types/api';

export interface Conversation {
  rootMessage: Message;
  replies: Message[];
  lastActivity: string;
}

/**
 * Groups a flat array of messages into conversations using the reply_to field.
 *
 * - Messages with no reply_to become conversation roots
 * - Messages with reply_to are grouped under their root (via reply_to chain)
 * - Conversations are sorted by most recent activity (descending)
 * - Replies within a conversation are sorted chronologically (ascending)
 */
export function groupByConversation(messages: Message[]): Conversation[] {
  // Build lookup map
  const messageMap = new Map<string, Message>();
  for (const msg of messages) {
    messageMap.set(msg.message_id, msg);
  }

  // Find root for each message by following reply_to chain
  function findRoot(msg: Message): string {
    let current = msg;
    const visited = new Set<string>();
    while (current.reply_to && messageMap.has(current.reply_to) && !visited.has(current.reply_to)) {
      visited.add(current.message_id);
      current = messageMap.get(current.reply_to)!;
    }
    return current.message_id;
  }

  // Group messages by root
  const groups = new Map<string, Message[]>();
  for (const msg of messages) {
    const rootId = findRoot(msg);
    if (!groups.has(rootId)) {
      groups.set(rootId, []);
    }
    groups.get(rootId)!.push(msg);
  }

  // Build conversation objects
  const conversations: Conversation[] = [];
  for (const [rootId, msgs] of groups) {
    const rootMessage = messageMap.get(rootId)!;
    const replies = msgs
      .filter(m => m.message_id !== rootId)
      .sort((a, b) => a.created_at.localeCompare(b.created_at));

    const allMsgs = [rootMessage, ...replies];
    const lastActivity = allMsgs.reduce((latest, m) =>
      m.created_at > latest ? m.created_at : latest,
      allMsgs[0].created_at
    );

    conversations.push({ rootMessage, replies, lastActivity });
  }

  // Sort conversations by most recent activity (descending)
  conversations.sort((a, b) => b.lastActivity.localeCompare(a.lastActivity));

  return conversations;
}
