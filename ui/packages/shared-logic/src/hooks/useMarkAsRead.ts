import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ensureConnected, wsClient } from '../api/client';
import type { MarkAsReadRequest, MarkAsReadResponse } from '../types/api';

/**
 * Hook to mark messages as read
 *
 * Example:
 * ```tsx
 * function ThreadView({ threadId }: { threadId: string }) {
 *   const { data } = useThread(threadId);
 *   const markAsRead = useMarkAsRead();
 *
 *   useEffect(() => {
 *     if (!data?.messages) return;
 *
 *     const unreadIds = data.messages
 *       .filter(m => !m.is_read)
 *       .map(m => m.message_id);
 *
 *     if (unreadIds.length > 0) {
 *       const timer = setTimeout(() => {
 *         markAsRead.mutate(unreadIds);
 *       }, 500); // Debounce
 *
 *       return () => clearTimeout(timer);
 *     }
 *   }, [data?.messages, markAsRead]);
 *
 *   return <div>...</div>;
 * }
 * ```
 */
export function useMarkAsRead() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (messageIds: string[]) => {
      await ensureConnected();
      const request: MarkAsReadRequest = { message_ids: messageIds };
      return wsClient.call<MarkAsReadResponse>('message.markRead', request);
    },
    onSuccess: (_data, messageIds) => {
      // Invalidate thread queries to refresh unread counts
      queryClient.invalidateQueries({ queryKey: ['threads'] });

      // Optimistically update message is_read status in cache
      // This updates any thread details that have these messages
      queryClient.setQueriesData(
        { queryKey: ['threads', 'detail'] },
        (old: unknown) => {
          if (!old || typeof old !== 'object' || !('messages' in old)) {
            return old;
          }
          const threadData = old as { messages?: Array<{ message_id: string }> };
          if (!threadData.messages) return old;
          return {
            ...threadData,
            messages: threadData.messages.map((msg) =>
              messageIds.includes(msg.message_id)
                ? { ...msg, is_read: true }
                : msg
            ),
          };
        }
      );
    },
  });
}
