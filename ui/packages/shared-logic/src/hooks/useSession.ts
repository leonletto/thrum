import { useQuery } from '@tanstack/react-query';
import { ensureConnected, wsClient } from '../api/client';
import type { SessionListResponse } from '../types/api';

export interface UseSessionListOptions {
  agent_id?: string;
  active_only?: boolean;
}

export function useSessionList(options?: UseSessionListOptions) {
  return useQuery({
    queryKey: ['sessions', 'list', options],
    queryFn: async () => {
      await ensureConnected();
      return wsClient.call<SessionListResponse>('session.list', options as Record<string, unknown> | undefined);
    },
    staleTime: 30000,
  });
}
