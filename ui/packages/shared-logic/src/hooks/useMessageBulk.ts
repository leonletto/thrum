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

// NOTE: The backend RPC `message.deleteByScope` exists but is currently only used
// by the CLI/backend directly. No UI hook wraps it yet; add one here when a UI
// flow requires scoped bulk deletion.
