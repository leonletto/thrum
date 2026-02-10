import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ReactNode } from 'react';
import {
  useSubscriptionList,
  useSubscribe,
  useUnsubscribe,
} from '../useSubscription';
import { wsClient } from '../../api/client';
import type { SubscriptionListResponse } from '../../types/api';

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
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
}

describe('useSubscriptionList', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('fetches subscription list successfully', async () => {
    const mockResponse: SubscriptionListResponse = {
      subscriptions: [
        {
          subscription_id: 'sub-1',
          session_id: 'session-1',
          filter_type: 'scope',
          scope: { type: 'project', value: 'my-project' },
          created_at: '2024-02-05T12:00:00Z',
        },
        {
          subscription_id: 'sub-2',
          session_id: 'session-1',
          filter_type: 'mention',
          mention: 'agent:test',
          created_at: '2024-02-05T12:00:00Z',
        },
      ],
    };

    vi.mocked(wsClient.call).mockResolvedValue(mockResponse);

    const { result } = renderHook(() => useSubscriptionList(), {
      wrapper: createWrapper(),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(wsClient.call).toHaveBeenCalledWith('subscriptions.list');
    expect(result.current.data).toEqual(mockResponse);
    expect(result.current.data?.subscriptions).toHaveLength(2);
  });

  it('handles errors gracefully', async () => {
    const mockError = new Error('Failed to fetch subscriptions');
    vi.mocked(wsClient.call).mockRejectedValue(mockError);

    const { result } = renderHook(() => useSubscriptionList(), {
      wrapper: createWrapper(),
    });

    await waitFor(() => expect(result.current.isError).toBe(true));

    expect(result.current.error).toEqual(mockError);
  });

  it('returns empty subscriptions array when no data', async () => {
    const mockResponse: SubscriptionListResponse = {
      subscriptions: [],
    };

    vi.mocked(wsClient.call).mockResolvedValue(mockResponse);

    const { result } = renderHook(() => useSubscriptionList(), {
      wrapper: createWrapper(),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(result.current.data?.subscriptions).toEqual([]);
    expect(result.current.data?.subscriptions).toHaveLength(0);
  });
});

describe('useSubscribe', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('subscribes with scope filter successfully', async () => {
    const mockResponse = {
      subscription_id: 'sub-123',
      created_at: '2024-02-05T12:00:00Z',
    };

    vi.mocked(wsClient.call).mockResolvedValue(mockResponse);

    const { result } = renderHook(() => useSubscribe(), {
      wrapper: createWrapper(),
    });

    const subscribeRequest = {
      filter_type: 'scope' as const,
      scope: { type: 'project', value: 'my-project' },
    };

    await result.current.mutateAsync(subscribeRequest);

    expect(wsClient.call).toHaveBeenCalledWith('subscribe', subscribeRequest);
  });

  it('subscribes with mention filter successfully', async () => {
    const mockResponse = {
      subscription_id: 'sub-456',
      created_at: '2024-02-05T12:00:00Z',
    };

    vi.mocked(wsClient.call).mockResolvedValue(mockResponse);

    const { result } = renderHook(() => useSubscribe(), {
      wrapper: createWrapper(),
    });

    const subscribeRequest = {
      filter_type: 'mention' as const,
      mention: 'agent:test',
    };

    await result.current.mutateAsync(subscribeRequest);

    expect(wsClient.call).toHaveBeenCalledWith('subscribe', subscribeRequest);
  });

  it('subscribes with all filter successfully', async () => {
    const mockResponse = {
      subscription_id: 'sub-789',
      created_at: '2024-02-05T12:00:00Z',
    };

    vi.mocked(wsClient.call).mockResolvedValue(mockResponse);

    const { result } = renderHook(() => useSubscribe(), {
      wrapper: createWrapper(),
    });

    const subscribeRequest = {
      filter_type: 'all' as const,
    };

    await result.current.mutateAsync(subscribeRequest);

    expect(wsClient.call).toHaveBeenCalledWith('subscribe', subscribeRequest);
  });

  it('handles subscription errors', async () => {
    const mockError = new Error('Subscription failed');
    vi.mocked(wsClient.call).mockRejectedValue(mockError);

    const { result } = renderHook(() => useSubscribe(), {
      wrapper: createWrapper(),
    });

    await expect(
      result.current.mutateAsync({
        filter_type: 'all',
      })
    ).rejects.toThrow('Subscription failed');
  });
});

describe('useUnsubscribe', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('unsubscribes successfully', async () => {
    const mockResponse = {
      subscription_id: 'sub-123',
      deleted_at: '2024-02-05T12:00:00Z',
    };

    vi.mocked(wsClient.call).mockResolvedValue(mockResponse);

    const { result } = renderHook(() => useUnsubscribe(), {
      wrapper: createWrapper(),
    });

    const unsubscribeRequest = {
      subscription_id: 'sub-123',
    };

    await result.current.mutateAsync(unsubscribeRequest);

    expect(wsClient.call).toHaveBeenCalledWith('unsubscribe', unsubscribeRequest);
  });

  it('handles unsubscribe errors', async () => {
    const mockError = new Error('Unsubscribe failed');
    vi.mocked(wsClient.call).mockRejectedValue(mockError);

    const { result } = renderHook(() => useUnsubscribe(), {
      wrapper: createWrapper(),
    });

    await expect(
      result.current.mutateAsync({
        subscription_id: 'sub-123',
      })
    ).rejects.toThrow('Unsubscribe failed');
  });
});
