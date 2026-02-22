import { useCallback } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useWebSocketEvent } from './useWebSocket';

/**
 * Hook to subscribe to real-time WebSocket events and invalidate query caches.
 *
 * Should be mounted once at the DashboardPage level. On any notification.message
 * or notification.thread.updated event, invalidates all message list queries
 * and unread count queries so TanStack Query auto-refetches.
 *
 * Strategy: invalidate-and-refetch (not optimistic insert).
 * Local daemon roundtrip is sub-millisecond.
 */
export function useRealtimeMessages() {
  const queryClient = useQueryClient();

  const invalidateAll = useCallback(() => {
    queryClient.invalidateQueries({ queryKey: ['messages'] });
    queryClient.invalidateQueries({ queryKey: ['threads'] });
  }, [queryClient]);

  // New/edited message notification
  useWebSocketEvent('notification.message', invalidateAll);

  // Thread stats changed (unread counts, etc.)
  useWebSocketEvent('notification.thread.updated', invalidateAll);
}
