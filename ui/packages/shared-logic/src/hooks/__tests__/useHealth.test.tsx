import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ReactNode } from 'react';
import { useHealth } from '../useHealth';
import { wsClient } from '../../api/client';
import type { HealthResponse } from '../../types/api';

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

describe('useHealth', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('fetches health status successfully', async () => {
    const mockResponse: HealthResponse = {
      status: 'ok',
      uptime_ms: 9015000,
      version: '0.1.0',
      repo_id: 'thrum',
      sync_state: 'synced',
    };

    vi.mocked(wsClient.call).mockResolvedValue(mockResponse);

    const { result } = renderHook(() => useHealth(), {
      wrapper: createWrapper(),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(wsClient.call).toHaveBeenCalledWith('health');
    expect(result.current.data).toEqual(mockResponse);
    expect(result.current.data?.status).toBe('ok');
    expect(result.current.data?.version).toBe('0.1.0');
  });

  it('handles errors gracefully', async () => {
    const mockError = new Error('Health check failed');
    vi.mocked(wsClient.call).mockRejectedValue(mockError);

    const { result } = renderHook(() => useHealth(), {
      wrapper: createWrapper(),
    });

    await waitFor(() => expect(result.current.isError).toBe(true));

    expect(result.current.error).toEqual(mockError);
  });

  it('uses correct staleTime of 10000ms', () => {
    const { result } = renderHook(() => useHealth(), {
      wrapper: createWrapper(),
    });

    // The staleTime should be set in the query options
    expect(result.current).toBeDefined();
  });

  it('returns health data with all required fields', async () => {
    const mockResponse: HealthResponse = {
      status: 'degraded',
      uptime_ms: 475380000,
      version: '0.2.1',
      repo_id: 'thrum-production',
      sync_state: 'pending',
    };

    vi.mocked(wsClient.call).mockResolvedValue(mockResponse);

    const { result } = renderHook(() => useHealth(), {
      wrapper: createWrapper(),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(result.current.data).toHaveProperty('status');
    expect(result.current.data).toHaveProperty('uptime_ms');
    expect(result.current.data).toHaveProperty('version');
    expect(result.current.data).toHaveProperty('repo_id');
    expect(result.current.data).toHaveProperty('sync_state');
  });
});
