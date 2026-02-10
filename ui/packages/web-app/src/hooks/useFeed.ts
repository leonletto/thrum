import { useMessageList } from '@thrum/shared-logic';
import type { Message, MessageScope } from '@thrum/shared-logic';
import type { FeedItem } from '../types/feed';

/**
 * Transform a Message from the RPC API into a FeedItem for the UI
 */
function transformMessageToFeedItem(message: Message): FeedItem {
  // Extract recipient from scopes (find scope with type "to")
  const toScope = message.scopes?.find((s: MessageScope) => s.type === 'to');

  return {
    id: message.message_id,
    type: 'message',
    from: message.agent_id,
    to: toScope?.value,
    preview: message.body.content || message.body.structured || '',
    timestamp: message.created_at,
    metadata: {
      thread_id: message.thread_id,
      session_id: message.session_id,
      is_read: message.is_read,
      mentions: message.mentions,
      priority: message.priority,
    },
  };
}

/**
 * Hook to fetch live feed messages
 * Uses real RPC data from message.list
 */
export function useFeed() {
  const { data, isLoading, error } = useMessageList({
    page_size: 50,
    sort_by: 'created_at',
    sort_order: 'desc',
  });

  const feedItems = data?.messages.map(transformMessageToFeedItem) || [];

  return {
    data: feedItems,
    isLoading,
    error,
  };
}
