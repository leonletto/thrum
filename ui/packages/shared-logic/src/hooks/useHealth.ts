import { useQuery } from '@tanstack/react-query';
import { ensureConnected, wsClient } from '../api/client';
import type { HealthResponse } from '../types/api';

/**
 * Hook to fetch daemon health status
 *
 * Example:
 * ```tsx
 * function HealthStatus() {
 *   const { data, isLoading, error } = useHealth();
 *
 *   if (isLoading) return <div>Checking health...</div>;
 *   if (error) return <div>Error: {error.message}</div>;
 *
 *   return (
 *     <div>
 *       <p>Status: {data?.status}</p>
 *       <p>Uptime: {data?.uptime_ms}ms</p>
 *       <p>Version: {data?.version}</p>
 *     </div>
 *   );
 * }
 * ```
 */
export function useHealth() {
  return useQuery<HealthResponse>({
    queryKey: ['health'],
    queryFn: async () => {
      await ensureConnected();
      return wsClient.call<HealthResponse>('health');
    },
    staleTime: 10000, // Consider data fresh for 10 seconds
    refetchInterval: 10000,
  });
}
