import { useEffect, useReducer, useRef } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ensureConnected, wsClient } from '../api/client';
import type {
  Message,
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

/**
 * Hook that wraps useMessageList with pagination state management.
 *
 * Returns accumulated messages across all loaded pages together with
 * `hasMore`, `loadMore`, and `isLoadingMore` helpers so that callers
 * can render an infinite-scroll / "Load More" UI without managing the
 * per-page TanStack Query calls themselves.
 *
 * When `baseRequest` changes (e.g. the filter or scope changes) the
 * accumulated message list is reset and fetching restarts from page 1.
 *
 * Example:
 * ```tsx
 * const {
 *   messages,
 *   total,
 *   isLoading,
 *   hasMore,
 *   loadMore,
 *   isLoadingMore,
 * } = useMessageListPaged({ for_agent: 'alice', page_size: 50, sort_order: 'desc' });
 * ```
 */
interface PaginationState {
  page: number;
  allMessages: Message[];
  total: number | undefined;
}

type PaginationAction =
  | { type: 'reset' }
  | { type: 'loadMore' }
  | { type: 'receivedPage'; page: number; messages: Message[]; total: number };

function paginationReducer(state: PaginationState, action: PaginationAction): PaginationState {
  switch (action.type) {
    case 'reset':
      return { page: 1, allMessages: [], total: undefined };
    case 'loadMore':
      return { ...state, page: state.page + 1 };
    case 'receivedPage': {
      if (action.page === 1) {
        return { ...state, allMessages: action.messages, total: action.total };
      }
      const existingIds = new Set(state.allMessages.map(m => m.message_id));
      const newOnes = action.messages.filter(m => !existingIds.has(m.message_id));
      return {
        ...state,
        allMessages: [...state.allMessages, ...newOnes],
        total: action.total,
      };
    }
  }
}

const INITIAL_PAGINATION_STATE: PaginationState = {
  page: 1,
  allMessages: [],
  total: undefined,
};

export function useMessageListPaged(baseRequest?: Omit<MessageListRequest, 'page'>) {
  const PAGE_SIZE = baseRequest?.page_size ?? 50;

  const [state, dispatch] = useReducer(paginationReducer, INITIAL_PAGINATION_STATE);

  // Tracks the stable JSON representation of baseRequest so we can detect
  // filter changes without needing deep-equality logic in useEffect.
  const prevRequestKeyRef = useRef<string>('');
  const requestKey = JSON.stringify(baseRequest);

  // Reset accumulated state atomically whenever the base request changes.
  useEffect(() => {
    if (prevRequestKeyRef.current !== requestKey) {
      prevRequestKeyRef.current = requestKey;
      dispatch({ type: 'reset' });
    }
  }, [requestKey]);

  const currentRequest: MessageListRequest = {
    ...baseRequest,
    page: state.page,
  };

  const { data, isLoading, isFetching } = useMessageList(currentRequest);

  // Append newly fetched page to the accumulated list.
  useEffect(() => {
    if (!data) return;
    dispatch({
      type: 'receivedPage',
      page: state.page,
      messages: data.messages,
      total: data.total,
    });
  // state.page and data are the correct deps; we intentionally omit requestKey
  // because the reset above already clears state before the new page-1
  // fetch resolves.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, state.page]);

  const hasMore =
    state.total !== undefined
      ? state.allMessages.length < state.total
      : data !== undefined && data.messages.length === PAGE_SIZE;

  const isLoadingMore = isFetching && state.page > 1;

  const loadMore = () => {
    if (!isFetching && hasMore) {
      dispatch({ type: 'loadMore' });
    }
  };

  return {
    messages: state.allMessages,
    total: state.total,
    isLoading: isLoading && state.page === 1,
    hasMore,
    loadMore,
    isLoadingMore,
  };
}
