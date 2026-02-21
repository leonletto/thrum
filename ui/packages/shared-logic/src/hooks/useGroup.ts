import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ensureConnected, wsClient } from '../api/client';
import type { GroupListResponse, GroupInfo } from '../types/api';

/**
 * Hook to fetch list of groups
 *
 * Polls every 60s since there's no WebSocket push event for group changes.
 */
export function useGroupList() {
  return useQuery({
    queryKey: ['groups', 'list'],
    queryFn: async () => {
      await ensureConnected();
      return wsClient.call<GroupListResponse>('group.list');
    },
    staleTime: 60000,
    refetchInterval: 60000,
  });
}

/**
 * Hook to fetch group details including members
 */
export function useGroupInfo(name: string) {
  return useQuery({
    queryKey: ['groups', 'info', name],
    queryFn: async () => {
      await ensureConnected();
      return wsClient.call<GroupInfo>('group.info', { name });
    },
    enabled: !!name,
    staleTime: 30000,
  });
}

/**
 * Hook to create a new group
 */
export function useGroupCreate() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (params: { name: string; description?: string }) => {
      await ensureConnected();
      return wsClient.call<{ group_id: string; name: string }>('group.create', params);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['groups', 'list'] });
    },
  });
}

/**
 * Hook to delete a group
 */
export function useGroupDelete() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (params: { name: string; delete_messages?: boolean }) => {
      await ensureConnected();
      return wsClient.call<{ name: string }>('group.delete', params);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['groups'] });
    },
  });
}

/**
 * Hook to add a member to a group
 */
export function useGroupMemberAdd() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (params: { group_name: string; member_type: 'agent' | 'role'; member_value: string }) => {
      await ensureConnected();
      return wsClient.call('group.member.add', params);
    },
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: ['groups', 'info', variables.group_name] });
      queryClient.invalidateQueries({ queryKey: ['groups', 'list'] });
    },
  });
}

/**
 * Hook to remove a member from a group
 */
export function useGroupMemberRemove() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (params: { group_name: string; member_type: 'agent' | 'role'; member_value: string }) => {
      await ensureConnected();
      return wsClient.call('group.member.remove', params);
    },
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: ['groups', 'info', variables.group_name] });
      queryClient.invalidateQueries({ queryKey: ['groups', 'list'] });
    },
  });
}
