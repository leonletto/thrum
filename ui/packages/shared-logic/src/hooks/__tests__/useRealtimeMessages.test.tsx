import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ReactNode } from 'react';
import { useRealtimeMessages } from '../useRealtimeMessages';
import { wsClient } from '../../api/client';

vi.mock('../../api/client', () => ({
  wsClient: {
    call: vi.fn(),
    isConnected: true,
    on: vi.fn(() => vi.fn()),
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

describe('useRealtimeMessages', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('subscribes to notification.message event', () => {
    const { wrapper } = createWrapper();
    renderHook(() => useRealtimeMessages(), { wrapper });

    expect(wsClient.on).toHaveBeenCalledWith('notification.message', expect.any(Function));
  });

  it('subscribes to notification.thread.updated event', () => {
    const { wrapper } = createWrapper();
    renderHook(() => useRealtimeMessages(), { wrapper });

    expect(wsClient.on).toHaveBeenCalledWith('notification.thread.updated', expect.any(Function));
  });

  it('invalidates message and thread queries on notification.message', () => {
    const { wrapper, queryClient } = createWrapper();
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries');

    renderHook(() => useRealtimeMessages(), { wrapper });

    // Find the callback registered for notification.message
    const messageCall = vi.mocked(wsClient.on).mock.calls.find(
      (call) => call[0] === 'notification.message'
    );
    expect(messageCall).toBeDefined();

    // Trigger the callback
    const callback = messageCall![1] as () => void;
    callback();

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['messages'] });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['threads'] });
  });

  it('invalidates queries on notification.thread.updated', () => {
    const { wrapper, queryClient } = createWrapper();
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries');

    renderHook(() => useRealtimeMessages(), { wrapper });

    // Find the callback registered for notification.thread.updated
    const threadCall = vi.mocked(wsClient.on).mock.calls.find(
      (call) => call[0] === 'notification.thread.updated'
    );
    expect(threadCall).toBeDefined();

    const callback = threadCall![1] as () => void;
    callback();

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['messages'] });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['threads'] });
  });

  it('cleans up subscriptions on unmount', () => {
    const unsubscribe = vi.fn();
    vi.mocked(wsClient.on).mockReturnValue(unsubscribe);

    const { wrapper } = createWrapper();
    const { unmount } = renderHook(() => useRealtimeMessages(), { wrapper });

    unmount();

    // useWebSocketEvent should call unsubscribe on cleanup
    expect(unsubscribe).toHaveBeenCalled();
  });
});
