import { useQuery } from '@tanstack/react-query';
import { ensureConnected, wsClient } from '../api/client';
import type { AgentContext, AgentContextListResponse } from '../types/api';

export interface UseAgentContextOptions {
  agentId?: string;
}

/**
 * Hook to fetch agent context information
 *
 * Example:
 * ```tsx
 * function AgentContextView({ agentId }: { agentId?: string }) {
 *   const { data, isLoading, error } = useAgentContext({ agentId });
 *
 *   if (isLoading) return <div>Loading context...</div>;
 *   if (error) return <div>Error: {error.message}</div>;
 *
 *   return (
 *     <div>
 *       {data?.map(context => (
 *         <div key={context.session_id}>
 *           <h3>{context.agent_id}</h3>
 *           <p>Branch: {context.branch}</p>
 *           <p>Task: {context.current_task}</p>
 *         </div>
 *       ))}
 *     </div>
 *   );
 * }
 * ```
 */
export function useAgentContext(options?: UseAgentContextOptions) {
  return useQuery<AgentContext[]>({
    queryKey: ['agent', 'context', options?.agentId],
    queryFn: async () => {
      await ensureConnected();
      const response = await wsClient.call<AgentContextListResponse>(
        'agent.listContext',
        options?.agentId ? { agent_id: options.agentId } : undefined
      );
      return response.contexts;
    },
    staleTime: 5000, // Consider data fresh for 5 seconds
  });
}
