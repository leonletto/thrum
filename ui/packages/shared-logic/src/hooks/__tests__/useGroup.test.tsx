import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ReactNode } from 'react';
import {
  useGroupList,
  useGroupInfo,
  useGroupCreate,
  useGroupDelete,
  useGroupMemberAdd,
  useGroupMemberRemove,
} from '../useGroup';
import { wsClient } from '../../api/client';
import type { GroupListResponse, GroupInfo } from '../../types/api';

vi.mock('../../api/client', () => ({
  wsClient: {
    call: vi.fn(),
    isConnected: true,
  },
  ensureConnected: vi.fn(),
}));

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  return {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
    queryClient,
  };
}

describe('useGroupList', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('fetches group list successfully', async () => {
    const mockResponse: GroupListResponse = {
      groups: [
        { group_id: 'g1', name: 'everyone', member_count: 5, created_at: '2026-01-01T00:00:00Z' },
        { group_id: 'g2', name: 'backend', description: 'Backend team', member_count: 2, created_at: '2026-01-02T00:00:00Z' },
      ],
    };

    vi.mocked(wsClient.call).mockResolvedValue(mockResponse);

    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useGroupList(), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(wsClient.call).toHaveBeenCalledWith('group.list');
    expect(result.current.data?.groups).toHaveLength(2);
    expect(result.current.data?.groups[0].name).toBe('everyone');
  });

  it('handles errors', async () => {
    vi.mocked(wsClient.call).mockRejectedValue(new Error('Network error'));

    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useGroupList(), { wrapper });

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error?.message).toBe('Network error');
  });
});

describe('useGroupInfo', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('fetches group info with members', async () => {
    const mockResponse: GroupInfo = {
      group_id: 'g1',
      name: 'backend',
      created_at: '2026-01-01T00:00:00Z',
      created_by: 'user:leon',
      members: [
        { member_type: 'agent', member_value: 'impl_1', added_at: '2026-01-01T00:00:00Z' },
        { member_type: 'role', member_value: 'implementer', added_at: '2026-01-01T00:00:00Z', added_by: 'user:leon' },
      ],
    };

    vi.mocked(wsClient.call).mockResolvedValue(mockResponse);

    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useGroupInfo('backend'), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(wsClient.call).toHaveBeenCalledWith('group.info', { name: 'backend' });
    expect(result.current.data?.members).toHaveLength(2);
  });

  it('is disabled when name is empty', () => {
    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useGroupInfo(''), { wrapper });

    expect(result.current.fetchStatus).toBe('idle');
  });
});

describe('useGroupCreate', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('creates a group and invalidates list', async () => {
    vi.mocked(wsClient.call).mockResolvedValue({ group_id: 'g3', name: 'reviewers' });

    const { wrapper, queryClient } = createWrapper();
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries');

    const { result } = renderHook(() => useGroupCreate(), { wrapper });

    await act(async () => {
      await result.current.mutateAsync({ name: 'reviewers', description: 'Code reviewers' });
    });

    expect(wsClient.call).toHaveBeenCalledWith('group.create', { name: 'reviewers', description: 'Code reviewers' });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['groups', 'list'] });
  });
});

describe('useGroupDelete', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('deletes a group and invalidates cache', async () => {
    vi.mocked(wsClient.call).mockResolvedValue({ name: 'old-group' });

    const { wrapper, queryClient } = createWrapper();
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries');

    const { result } = renderHook(() => useGroupDelete(), { wrapper });

    await act(async () => {
      await result.current.mutateAsync({ name: 'old-group' });
    });

    expect(wsClient.call).toHaveBeenCalledWith('group.delete', { name: 'old-group' });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['groups'] });
  });
});

describe('useGroupMemberAdd', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('adds member and invalidates group info', async () => {
    vi.mocked(wsClient.call).mockResolvedValue({});

    const { wrapper, queryClient } = createWrapper();
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries');

    const { result } = renderHook(() => useGroupMemberAdd(), { wrapper });

    await act(async () => {
      await result.current.mutateAsync({
        group_name: 'backend',
        member_type: 'agent',
        member_value: 'impl_2',
      });
    });

    expect(wsClient.call).toHaveBeenCalledWith('group.member.add', {
      group_name: 'backend',
      member_type: 'agent',
      member_value: 'impl_2',
    });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['groups', 'info', 'backend'] });
  });
});

describe('useGroupMemberRemove', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('removes member and invalidates group info', async () => {
    vi.mocked(wsClient.call).mockResolvedValue({});

    const { wrapper, queryClient } = createWrapper();
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries');

    const { result } = renderHook(() => useGroupMemberRemove(), { wrapper });

    await act(async () => {
      await result.current.mutateAsync({
        group_name: 'backend',
        member_type: 'agent',
        member_value: 'impl_2',
      });
    });

    expect(wsClient.call).toHaveBeenCalledWith('group.member.remove', {
      group_name: 'backend',
      member_type: 'agent',
      member_value: 'impl_2',
    });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['groups', 'info', 'backend'] });
  });
});
