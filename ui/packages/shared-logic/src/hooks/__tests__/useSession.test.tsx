import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ReactNode } from 'react';
import { useSessionList } from '../useSession';
import { wsClient } from '../../api/client';
import type { SessionListResponse } from '../../types/api';

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

describe('useSessionList', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('fetches session list successfully', async () => {
    const mockResponse: SessionListResponse = {
      sessions: [
        {
          session_id: 'session-1',
          agent_id: 'agent-abc',
          started_at: '2024-01-01T00:00:00Z',
          active: true,
        },
        {
          session_id: 'session-2',
          agent_id: 'agent-xyz',
          started_at: '2024-01-02T00:00:00Z',
          ended_at: '2024-01-02T01:00:00Z',
          active: false,
        },
      ],
    };

    vi.mocked(wsClient.call).mockResolvedValue(mockResponse);

    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useSessionList(), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(wsClient.call).toHaveBeenCalledWith('session.list', undefined);
    expect(result.current.data).toEqual(mockResponse);
    expect(result.current.data?.sessions).toHaveLength(2);
  });

  it('handles errors gracefully', async () => {
    const mockError = new Error('Failed to fetch sessions');
    vi.mocked(wsClient.call).mockRejectedValue(mockError);

    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useSessionList(), { wrapper });

    await waitFor(() => expect(result.current.isError).toBe(true));

    expect(result.current.error).toEqual(mockError);
  });

  it('fetches sessions filtered by agent_id', async () => {
    const mockResponse: SessionListResponse = {
      sessions: [
        {
          session_id: 'session-1',
          agent_id: 'agent-abc',
          started_at: '2024-01-01T00:00:00Z',
          active: true,
        },
      ],
    };

    vi.mocked(wsClient.call).mockResolvedValue(mockResponse);

    const { wrapper } = createWrapper();
    const { result } = renderHook(
      () => useSessionList({ agent_id: 'agent-abc' }),
      { wrapper }
    );

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(wsClient.call).toHaveBeenCalledWith('session.list', { agent_id: 'agent-abc' });
    expect(result.current.data?.sessions[0].agent_id).toBe('agent-abc');
  });

  it('fetches sessions filtered by active_only', async () => {
    const mockResponse: SessionListResponse = {
      sessions: [
        {
          session_id: 'session-1',
          agent_id: 'agent-abc',
          started_at: '2024-01-01T00:00:00Z',
          active: true,
        },
      ],
    };

    vi.mocked(wsClient.call).mockResolvedValue(mockResponse);

    const { wrapper } = createWrapper();
    const { result } = renderHook(
      () => useSessionList({ active_only: true }),
      { wrapper }
    );

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(wsClient.call).toHaveBeenCalledWith('session.list', { active_only: true });
    expect(result.current.data?.sessions.every(s => s.active)).toBe(true);
  });
});
