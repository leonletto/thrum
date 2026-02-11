import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ReactNode } from 'react';
import { useAgentContext } from '../useAgentContext';
import { wsClient } from '../../api/client';
import type { AgentContextListResponse } from '../../types/api';

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
  // Return both the wrapper and queryClient for testing
  return {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
    queryClient,
  };
}

describe('useAgentContext', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('fetches agent context successfully', async () => {
    const mockResponse: AgentContextListResponse = {
      contexts: [
        {
          session_id: 'session-1',
          agent_id: 'agent:test',
          branch: 'feature/test',
          worktree_path: '/path/to/worktree',
          unmerged_commits: [
            { hash: 'abc123', subject: 'Test commit' },
          ],
          uncommitted_files: ['file1.ts', 'file2.ts'],
          changed_files: ['file1.ts'],
          git_updated_at: '2024-02-05T12:00:00Z',
          current_task: 'Implementing feature X',
          task_updated_at: '2024-02-05T12:00:00Z',
          intent: 'Build new feature',
          intent_updated_at: '2024-02-05T12:00:00Z',
        },
      ],
    };

    vi.mocked(wsClient.call).mockResolvedValue(mockResponse);

    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useAgentContext(), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(wsClient.call).toHaveBeenCalledWith('agent.listContext', undefined);
    expect(result.current.data).toEqual(mockResponse.contexts);

    const data = result.current.data;
    expect(data).toHaveLength(1);
    expect(data?.[0]?.agent_id).toBe('agent:test');
  });

  it('fetches context for specific agent when agentId provided', async () => {
    const mockResponse: AgentContextListResponse = {
      contexts: [
        {
          session_id: 'session-2',
          agent_id: 'agent:specific',
          branch: 'main',
          worktree_path: '/path/to/main',
          unmerged_commits: [],
          uncommitted_files: [],
          changed_files: [],
          git_updated_at: '2024-02-05T12:00:00Z',
          current_task: 'Idle',
          task_updated_at: '2024-02-05T12:00:00Z',
          intent: 'None',
          intent_updated_at: '2024-02-05T12:00:00Z',
        },
      ],
    };

    vi.mocked(wsClient.call).mockResolvedValue(mockResponse);

    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useAgentContext({ agentId: 'agent:specific' }), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(wsClient.call).toHaveBeenCalledWith('agent.listContext', {
      agent_id: 'agent:specific',
    });

    const data = result.current.data;
    expect(data?.[0]?.agent_id).toBe('agent:specific');
  });

  it('handles errors gracefully', async () => {
    const mockError = new Error('Failed to fetch context');
    vi.mocked(wsClient.call).mockRejectedValue(mockError);

    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useAgentContext(), { wrapper });

    await waitFor(() => expect(result.current.isError).toBe(true));

    expect(result.current.error).toEqual(mockError);
  });

  it('uses correct staleTime of 5000ms', async () => {
    const mockResponse: AgentContextListResponse = { contexts: [] };
    vi.mocked(wsClient.call).mockResolvedValue(mockResponse);

    const { wrapper, queryClient } = createWrapper();
    renderHook(() => useAgentContext(), { wrapper });

    await waitFor(() => {
      const queryState = queryClient.getQueryState(['agent', 'context', undefined]);
      expect(queryState).toBeDefined();
    });

    // Verify the query has staleTime of 5000ms
    const queryCache = queryClient.getQueryCache();
    const query = queryCache.find({ queryKey: ['agent', 'context', undefined] });
    expect(query?.options.staleTime).toBe(5000);
  });

  it('returns empty contexts array when no data', async () => {
    const mockResponse: AgentContextListResponse = {
      contexts: [],
    };

    vi.mocked(wsClient.call).mockResolvedValue(mockResponse);

    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useAgentContext(), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    const data = result.current.data;
    expect(data).toEqual([]);
    expect(data).toHaveLength(0);
  });
});
