import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ensureConnected, wsClient } from '../api/client';
import type { ThreadGetResponse, ThreadListResponse, MessageScope } from '../types/api';

/**
 * Hook to fetch list of threads
 *
 * Example:
 * ```tsx
 * function ThreadList() {
 *   const { data, isLoading } = useThreadList({ page_size: 20 });
 *
 *   if (isLoading) return <div>Loading...</div>;
 *
 *   return (
 *     <ul>
 *       {data?.threads.map(thread => (
 *         <li key={thread.thread_id}>{thread.title}</li>
 *       ))}
 *     </ul>
 *   );
 * }
 * ```
 */
export function useThreadList(params?: { page_size?: number; page?: number; scope?: MessageScope }) {
  return useQuery({
    queryKey: ['threads', 'list', params],
    queryFn: async () => {
      await ensureConnected();
      return wsClient.call<ThreadListResponse>('thread.list', params);
    },
    staleTime: 5000,
  });
}

/**
 * Hook to fetch a single thread with messages
 *
 * Example:
 * ```tsx
 * function ThreadView({ threadId }: { threadId: string }) {
 *   const { data, isLoading } = useThread(threadId, { page_size: 50 });
 *
 *   if (isLoading) return <div>Loading...</div>;
 *
 *   return (
 *     <div>
 *       <h2>{data?.title}</h2>
 *       {data?.messages.map(msg => (
 *         <div key={msg.message_id}>{msg.body.content}</div>
 *       ))}
 *     </div>
 *   );
 * }
 * ```
 */
export function useThread(
  threadId: string,
  options?: { page_size?: number; page?: number; enabled?: boolean }
) {
  const { enabled = true, ...params } = options ?? {};
  return useQuery({
    queryKey: ['threads', 'detail', threadId, params],
    queryFn: async () => {
      await ensureConnected();
      return wsClient.call<ThreadGetResponse>('thread.get', {
        thread_id: threadId,
        ...params,
      });
    },
    enabled: !!threadId && enabled,
    staleTime: 1000,
  });
}

/**
 * Hook to create a new thread
 *
 * Example:
 * ```tsx
 * function CreateThread() {
 *   const createThread = useCreateThread();
 *
 *   const handleCreate = async (title: string) => {
 *     await createThread.mutateAsync({ title });
 *   };
 *
 *   return <button onClick={() => handleCreate('New Thread')}>Create</button>;
 * }
 * ```
 */
export function useCreateThread() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (params: { title: string }) => {
      await ensureConnected();
      return wsClient.call<{
        thread_id: string;
        title: string;
        created_at: string;
        created_by: string;
      }>('thread.create', params);
    },
    onSuccess: () => {
      // Invalidate thread lists
      queryClient.invalidateQueries({ queryKey: ['threads', 'list'] });
    },
  });
}
