import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ensureConnected, wsClient } from '../api/client';
import type { AgentListResponse } from '../types/api';

export interface UseAgentListOptions {
  role?: string;
  module?: string;
}

/**
 * Hook to fetch list of registered agents
 *
 * Example:
 * ```tsx
 * function AgentList() {
 *   const { data, isLoading, error } = useAgentList();
 *
 *   if (isLoading) return <div>Loading...</div>;
 *   if (error) return <div>Error: {error.message}</div>;
 *
 *   return (
 *     <ul>
 *       {data?.agents.map(agent => (
 *         <li key={agent.agent_id}>{agent.display || agent.agent_id}</li>
 *       ))}
 *     </ul>
 *   );
 * }
 * ```
 */
export function useAgentList(options?: UseAgentListOptions) {
  return useQuery({
    queryKey: ['agents', options],
    queryFn: async () => {
      await ensureConnected();
      return wsClient.call<AgentListResponse>(
        'agent.list',
        options as Record<string, unknown> | undefined
      );
    },
    staleTime: 5000, // Consider data fresh for 5 seconds
  });
}

export interface UseAgentDeleteOptions {
  onSuccess?: () => void;
  onError?: (error: Error) => void;
}

/**
 * Hook to delete an agent
 *
 * Example:
 * ```tsx
 * function AgentDetail({ agentId }) {
 *   const deleteAgent = useAgentDelete({
 *     onSuccess: () => console.log('Agent deleted'),
 *     onError: (error) => console.error('Delete failed:', error),
 *   });
 *
 *   return (
 *     <button
 *       onClick={() => deleteAgent.mutate(agentId)}
 *       disabled={deleteAgent.isPending}
 *     >
 *       Delete Agent
 *     </button>
 *   );
 * }
 * ```
 */
export function useAgentDelete(options?: UseAgentDeleteOptions) {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (agentName: string) => {
      await ensureConnected();
      return wsClient.call('agent.delete', { name: agentName });
    },
    onSuccess: () => {
      // Invalidate agent list query to refetch
      queryClient.invalidateQueries({ queryKey: ['agents'] });
      options?.onSuccess?.();
    },
    onError: (error: Error) => {
      options?.onError?.(error);
    },
  });
}
