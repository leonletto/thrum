import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ensureConnected, wsClient } from '../api/client';
import type {
  MessageListRequest,
  MessageListResponse,
  SendMessageRequest,
  SendMessageResponse,
} from '../types/api';

/**
 * Hook to fetch messages (inbox)
 *
 * Example:
 * ```tsx
 * function Inbox() {
 *   const { data, isLoading, error } = useMessageList({
 *     page_size: 20,
 *     sort_order: 'desc',
 *   });
 *
 *   if (isLoading) return <div>Loading messages...</div>;
 *   if (error) return <div>Error: {error.message}</div>;
 *
 *   return (
 *     <div>
 *       {data?.messages.map(msg => (
 *         <div key={msg.message_id}>{msg.body.content}</div>
 *       ))}
 *       <div>Page {data?.page} of {data?.total_pages}</div>
 *     </div>
 *   );
 * }
 * ```
 */
export function useMessageList(request?: MessageListRequest) {
  return useQuery({
    queryKey: ['messages', 'list', request],
    queryFn: async () => {
      await ensureConnected();
      return wsClient.call<MessageListResponse>('message.list', request);
    },
    staleTime: 1000, // Consider data fresh for 1 second
  });
}

/**
 * Alias for useMessageList - represents the inbox view
 */
export function useInbox(request?: MessageListRequest) {
  return useMessageList(request);
}

/**
 * Hook to get a single message by ID
 *
 * Example:
 * ```tsx
 * function MessageDetail({ messageId }: { messageId: string }) {
 *   const { data, isLoading } = useMessage(messageId);
 *
 *   if (isLoading) return <div>Loading...</div>;
 *
 *   return <div>{data?.body.content}</div>;
 * }
 * ```
 */
export function useMessage(messageId: string) {
  return useQuery({
    queryKey: ['messages', 'detail', messageId],
    queryFn: async () => {
      await ensureConnected();
      return wsClient.call<MessageListResponse['messages'][number]>(
        'message.get',
        { message_id: messageId }
      );
    },
    enabled: !!messageId,
  });
}

/**
 * Hook to send a message
 *
 * Example:
 * ```tsx
 * function MessageComposer() {
 *   const sendMessage = useSendMessage();
 *
 *   const handleSend = async (content: string) => {
 *     try {
 *       await sendMessage.mutateAsync({ content });
 *     } catch (error) {
 *       console.error('Failed to send:', error);
 *     }
 *   };
 *
 *   return (
 *     <form onSubmit={(e) => {
 *       e.preventDefault();
 *       handleSend(e.currentTarget.message.value);
 *     }}>
 *       <input name="message" placeholder="Type a message..." />
 *       <button type="submit" disabled={sendMessage.isPending}>Send</button>
 *     </form>
 *   );
 * }
 * ```
 */
export function useSendMessage() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (request: SendMessageRequest) => {
      await ensureConnected();
      return wsClient.call<SendMessageResponse>('message.send', request);
    },
    onSuccess: () => {
      // Invalidate message lists to refetch with new message
      queryClient.invalidateQueries({ queryKey: ['messages', 'list'] });
      queryClient.invalidateQueries({ queryKey: ['threads'] });
    },
  });
}

/**
 * Hook to edit a message
 */
export function useEditMessage() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (params: { message_id: string; content: string; structured?: string }) => {
      await ensureConnected();
      return wsClient.call<{ message_id: string; updated_at: string }>(
        'message.edit',
        params
      );
    },
    onSuccess: (data) => {
      // Invalidate the specific message and lists
      queryClient.invalidateQueries({ queryKey: ['messages', 'detail', data.message_id] });
      queryClient.invalidateQueries({ queryKey: ['messages', 'list'] });
    },
  });
}

/**
 * Hook to delete a message
 */
export function useDeleteMessage() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (params: { message_id: string; reason?: string }) => {
      await ensureConnected();
      return wsClient.call<{ message_id: string; deleted_at: string }>(
        'message.delete',
        params
      );
    },
    onSuccess: (data) => {
      // Invalidate the specific message and lists
      queryClient.invalidateQueries({ queryKey: ['messages', 'detail', data.message_id] });
      queryClient.invalidateQueries({ queryKey: ['messages', 'list'] });
    },
  });
}
