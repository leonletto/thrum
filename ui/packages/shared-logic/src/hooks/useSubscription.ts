import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ensureConnected, wsClient } from '../api/client';
import type { SubscriptionListResponse, MessageScope } from '../types/api';

/**
 * Hook to fetch list of subscriptions
 *
 * Example:
 * ```tsx
 * function SubscriptionList() {
 *   const { data, isLoading, error } = useSubscriptionList();
 *
 *   if (isLoading) return <div>Loading subscriptions...</div>;
 *   if (error) return <div>Error: {error.message}</div>;
 *
 *   return (
 *     <ul>
 *       {data?.subscriptions.map(sub => (
 *         <li key={sub.subscription_id}>
 *           {sub.filter_type}: {sub.scope?.value || sub.mention || 'all'}
 *         </li>
 *       ))}
 *     </ul>
 *   );
 * }
 * ```
 */
export function useSubscriptionList() {
  return useQuery<SubscriptionListResponse>({
    queryKey: ['subscriptions'],
    queryFn: async () => {
      await ensureConnected();
      return wsClient.call<SubscriptionListResponse>('subscriptions.list');
    },
    staleTime: 5000, // Consider data fresh for 5 seconds
  });
}

export interface SubscribeRequest {
  filter_type: 'scope' | 'mention' | 'all';
  scope?: MessageScope;
  mention?: string;
}

export interface SubscribeResponse {
  subscription_id: string;
  created_at: string;
}

/**
 * Hook to subscribe to messages
 *
 * Example:
 * ```tsx
 * function SubscribeButton() {
 *   const subscribe = useSubscribe();
 *
 *   const handleSubscribe = async () => {
 *     try {
 *       await subscribe.mutateAsync({
 *         filter_type: 'scope',
 *         scope: { type: 'project', value: 'my-project' },
 *       });
 *     } catch (error) {
 *       console.error('Failed to subscribe:', error);
 *     }
 *   };
 *
 *   return (
 *     <button onClick={handleSubscribe} disabled={subscribe.isPending}>
 *       Subscribe to Project
 *     </button>
 *   );
 * }
 * ```
 */
export function useSubscribe() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (request: SubscribeRequest) => {
      await ensureConnected();
      return wsClient.call<SubscribeResponse>('subscribe', request as unknown as Record<string, unknown>);
    },
    onSuccess: () => {
      // Invalidate subscriptions list to refetch with new subscription
      queryClient.invalidateQueries({ queryKey: ['subscriptions'] });
    },
  });
}

export interface UnsubscribeRequest {
  subscription_id: string;
}

export interface UnsubscribeResponse {
  subscription_id: string;
  deleted_at: string;
}

/**
 * Hook to unsubscribe from messages
 *
 * Example:
 * ```tsx
 * function UnsubscribeButton({ subscriptionId }: { subscriptionId: string }) {
 *   const unsubscribe = useUnsubscribe();
 *
 *   const handleUnsubscribe = async () => {
 *     try {
 *       await unsubscribe.mutateAsync({ subscription_id: subscriptionId });
 *     } catch (error) {
 *       console.error('Failed to unsubscribe:', error);
 *     }
 *   };
 *
 *   return (
 *     <button onClick={handleUnsubscribe} disabled={unsubscribe.isPending}>
 *       Unsubscribe
 *     </button>
 *   );
 * }
 * ```
 */
export function useUnsubscribe() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (request: UnsubscribeRequest) => {
      await ensureConnected();
      return wsClient.call<UnsubscribeResponse>('unsubscribe', request as unknown as Record<string, unknown>);
    },
    onSuccess: () => {
      // Invalidate subscriptions list to refetch after unsubscribe
      queryClient.invalidateQueries({ queryKey: ['subscriptions'] });
    },
  });
}
