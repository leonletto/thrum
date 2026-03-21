import { useCallback } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ensureConnected, wsClient } from '../api/client';
import { useWebSocketEvent } from './useWebSocket';
import type { Message, MessageListResponse } from '../types/api';

/**
 * A conversation message with direction metadata.
 */
export interface ConversationMessage {
  id: string;
  threadId?: string;
  replyTo?: string;
  content: string;
  author: { agentId: string; display?: string };
  direction: 'sent' | 'received';
  timestamp: string;
  isRead?: boolean;
  structured?: Record<string, unknown>;
  mentions?: string[];
  raw: Message;
}

interface UseConversationOptions {
  /** The current agent viewing the conversation */
  agentId: string;
  /** The other agent in the conversation */
  withAgentId: string;
  /** Max messages per direction (default 50) */
  limit?: number;
}

/**
 * Fetches the full bidirectional conversation between two agents.
 * Merges received and sent messages, deduplicates, and sorts chronologically.
 */
export function useConversation({ agentId, withAgentId, limit = 50 }: UseConversationOptions) {
  const queryClient = useQueryClient();

  // Invalidate on new messages
  const invalidate = useCallback(() => {
    queryClient.invalidateQueries({ queryKey: ['conversation'] });
  }, [queryClient]);

  useWebSocketEvent('notification.message', invalidate);
  useWebSocketEvent('notification.thread.updated', invalidate);

  return useQuery({
    queryKey: ['conversation', agentId, withAgentId, limit],
    queryFn: async (): Promise<ConversationMessage[]> => {
      await ensureConnected();

      // Fetch messages received by agentId from withAgentId
      const received = await wsClient.call<MessageListResponse>('message.list', {
        for_agent: agentId,
        author_id: withAgentId,
        page_size: limit,
        sort_order: 'desc',
      });

      // Fetch messages sent by agentId (all, then filter client-side to withAgentId)
      const sent = await wsClient.call<MessageListResponse>('message.list', {
        author_id: agentId,
        page_size: limit,
        sort_order: 'desc',
      });

      // Filter sent messages to those mentioning or scoped to withAgentId
      const sentToPartner = sent.messages.filter(
        (m) =>
          m.mentions?.includes(withAgentId) ||
          m.mentions?.includes(`@${withAgentId}`) ||
          m.scopes?.some((s) => s.value === withAgentId)
      );

      return mergeAndSort(received.messages, sentToPartner);
    },
    staleTime: 1000,
    refetchInterval: 5000,
    enabled: !!agentId && !!withAgentId,
  });
}

/**
 * Returns a list of unique agents the current user has conversations with,
 * sorted by most recent message.
 */
export interface ConversationEntry {
  agentId: string;
  lastMessage: {
    content: string;
    timestamp: string;
    direction: 'sent' | 'received';
  };
  unreadCount: number;
}

export function useConversationList(currentAgentId: string) {
  const queryClient = useQueryClient();

  const invalidate = useCallback(() => {
    queryClient.invalidateQueries({ queryKey: ['conversation-list'] });
  }, [queryClient]);

  useWebSocketEvent('notification.message', invalidate);

  return useQuery({
    queryKey: ['conversation-list', currentAgentId],
    queryFn: async (): Promise<ConversationEntry[]> => {
      await ensureConnected();

      // Get all inbox messages
      const inbox = await wsClient.call<MessageListResponse>('message.list', {
        for_agent: currentAgentId,
        page_size: 100,
        sort_order: 'desc',
      });

      // Group by sender, find latest message and unread count
      const byAgent = new Map<
        string,
        { lastMessage: Message; unreadCount: number }
      >();

      for (const msg of inbox.messages) {
        const senderId = msg.agent_id || msg.authored_by;
        if (!senderId) continue;

        const existing = byAgent.get(senderId);
        if (!existing) {
          byAgent.set(senderId, {
            lastMessage: msg,
            unreadCount: msg.is_read === false ? 1 : 0,
          });
        } else {
          if (msg.is_read === false) {
            existing.unreadCount++;
          }
        }
      }

      // Convert to entries sorted by most recent
      const entries: ConversationEntry[] = [];
      for (const [agentId, data] of byAgent) {
        entries.push({
          agentId,
          lastMessage: {
            content:
              data.lastMessage.body.content?.slice(0, 80) || '(no content)',
            timestamp: data.lastMessage.created_at,
            direction: 'received',
          },
          unreadCount: data.unreadCount,
        });
      }

      entries.sort(
        (a, b) =>
          new Date(b.lastMessage.timestamp).getTime() -
          new Date(a.lastMessage.timestamp).getTime()
      );

      return entries;
    },
    staleTime: 1000,
    refetchInterval: 5000,
    enabled: !!currentAgentId,
  });
}

/** Merge received and sent messages, deduplicate, sort chronologically. */
function mergeAndSort(
  received: Message[],
  sent: Message[]
): ConversationMessage[] {
  const seen = new Set<string>();
  const result: ConversationMessage[] = [];

  const toConversationMessage = (
    msg: Message,
    direction: 'sent' | 'received'
  ): ConversationMessage => ({
    id: msg.message_id,
    threadId: msg.thread_id,
    replyTo: msg.reply_to,
    content: msg.body.content || '',
    author: {
      agentId: msg.agent_id || msg.authored_by || 'unknown',
      display: msg.authored_by,
    },
    direction,
    timestamp: msg.created_at,
    isRead: msg.is_read,
    structured: msg.body.structured as Record<string, unknown> | undefined,
    mentions: msg.mentions,
    raw: msg,
  });

  for (const msg of received) {
    if (!seen.has(msg.message_id)) {
      seen.add(msg.message_id);
      result.push(toConversationMessage(msg, 'received'));
    }
  }

  for (const msg of sent) {
    if (!seen.has(msg.message_id)) {
      seen.add(msg.message_id);
      result.push(toConversationMessage(msg, 'sent'));
    }
  }

  // Sort chronologically (oldest first for chat view)
  result.sort(
    (a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime()
  );

  return result;
}
