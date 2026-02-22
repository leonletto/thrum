import { useMutation } from '@tanstack/react-query';
import { ensureConnected, wsClient } from '../api/client';

/**
 * Hook to archive messages for an agent or group
 */
export function useMessageArchive() {
  return useMutation({
    mutationFn: async (params: { archive_type: 'agent' | 'group'; identifier: string }) => {
      await ensureConnected();
      return wsClient.call<{ archived_count: number; archive_path: string }>(
        'message.archive',
        params
      );
    },
  });
}

/**
 * Hook to delete all messages for a given agent
 */
export function useMessageDeleteByAgent() {
  return useMutation({
    mutationFn: async (params: { agent_id: string }) => {
      await ensureConnected();
      return wsClient.call<{ deleted_count: number }>('message.deleteByAgent', params);
    },
  });
}
